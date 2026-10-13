package litclient

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// HistoryEntry is one immutable history entry of a record, as lit-server
// records it: the revisions it went between, when, the request and its intent
// (whose Actor says who made it), every record the transaction changed, the
// fields it changed (JSON values; the body excluded) and, when the body
// changed, one hunk with the whole body before and after.
type HistoryEntry struct {
	SchemaVersion  int                        `json:"schema_version"`
	OwnerID        string                     `json:"owner_id"`
	BeforeRevision int64                      `json:"before_revision"`
	Revision       int64                      `json:"revision"`
	Timestamp      string                     `json:"timestamp"`
	RequestHash    string                     `json:"request_hash"`
	Intent         protocol.Intent            `json:"intent"`
	Results        []protocol.ChangedObject   `json:"results"`
	Differences    map[string]FieldDifference `json:"differences"`
	BodyHunks      []BodyHunk                 `json:"body_hunks"`
}

// FieldDifference is a field's JSON value before and after an entry; a field
// that did not exist before is null.
type FieldDifference struct {
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}

// BodyHunk is the whole body (a project's description) before and after an
// entry.
type BodyHunk struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

// HistoryQuery pages a history. Limit bounds one page (zero: the service's
// default page); Cursor continues from a previous page's NextCursor and is
// bound to the same Limit. All returns every entry as one bounded read and
// conflicts with Limit and Cursor.
type HistoryQuery struct {
	Limit  int
	Cursor string
	All    bool
}

// HistoryPage is one page of a history, oldest first. A non-nil NextCursor
// continues it.
type HistoryPage struct {
	Items      []HistoryEntry `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}

// History reads one page of the immutable history of record id (a UUID) of
// type typ ("issues" or "comments"): GET /v1/<typ>/<id>/history, as lit
// issues history and lit comments history send it once they resolved the
// owner. It is a read and journals nothing. Invalid arguments are refused
// before anything is sent; entries for another owner are an invalid
// response. ctx bounds this read; nil keeps the session's context.
func (s *Session) History(ctx context.Context, typ, id string, q HistoryQuery) (HistoryPage, error) {
	switch {
	case typ != "issues" && typ != "comments":
		return HistoryPage{}, protocol.E(400, "invalid_arguments", "history is read for issues or comments")
	case !protocol.ValidUUID(id):
		return HistoryPage{}, protocol.E(400, "invalid_arguments", "history requires the owner's UUID")
	case q.All && (q.Limit != 0 || q.Cursor != ""):
		return HistoryPage{}, protocol.E(400, "invalid_arguments", "all conflicts with pagination")
	case q.Limit < 0:
		return HistoryPage{}, protocol.E(400, "invalid_arguments", "limit must be positive")
	}
	values := url.Values{}
	if q.Limit > 0 {
		values.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Cursor != "" {
		values.Set("cursor", q.Cursor)
	}
	if q.All {
		values.Set("all", "true")
	}
	path := "/v1/" + typ + "/" + id + "/history"
	if len(values) > 0 {
		path += "?" + values.Encode()
	}
	defer s.bind(ctx)()
	var page HistoryPage
	if e := s.Call("GET", path, nil, nil, &page, false); e != nil {
		return HistoryPage{}, e
	}
	if page.Items == nil {
		return HistoryPage{}, protocol.E(500, "invalid_response", "history omitted items")
	}
	for _, e := range page.Items {
		if e.SchemaVersion != 1 || e.OwnerID != id {
			return HistoryPage{}, protocol.E(500, "invalid_response", "history returned an entry of another record")
		}
	}
	return page, nil
}
