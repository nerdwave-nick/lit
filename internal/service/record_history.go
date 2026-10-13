package service

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

func mustJSON(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}

// recordHistory reads a record's whole history, oldest first.
func (s *Server) recordHistory(typ, id string) ([]ProjectHistory, error) {
	if !protocol.ResourceType(typ) || !protocol.ValidUUID(id) {
		return nil, invalid("invalid history owner")
	}
	return s.q().History(s.ctx(), typ, id, 0, 0)
}

// historyCursor continues a history page behind the entry named by Last, the
// timestamp and request hash of the previous page's last entry. Binding ties it
// to the owner and page size.
type historyCursor struct {
	Binding string `json:"binding"`
	Last    string `json:"last"`
}

// serveHistory answers one keyset page of a record's history, oldest first,
// in the page envelope. The cursor format is unchanged from the file store;
// storage pages by revision, which orders history as timestamps do. The
// entries are written from their stored JSON (storage.AppendHistory), which
// encodes them as the decoded entries would be.
func (s *Server) serveHistory(w http.ResponseWriter, typ, id, binding string, values url.Values) error {
	rows, next, e := s.historyPage(typ, id, binding, values)
	if e != nil {
		return e
	}
	b := append(make([]byte, 0, 64), `{"items":`...)
	b = storage.AppendHistory(b, id, rows)
	b = append(b, `,"next_cursor":`...)
	b = append(b, mustJSON(next)...)
	b = append(b, `,"server_time":`...)
	b = append(b, mustJSON(s.responseTime())...)
	b = append(b, "}\n"...)
	w.Header().Set("Content-Type", "application/json")
	writeBody(w, b)
	return nil
}

// historyPage reads one keyset page of a record's history and the cursor of
// the next page, nil after the last one.
func (s *Server) historyPage(typ, id, binding string, values url.Values) ([]storage.HistoryRow, *string, error) {
	for key := range values {
		if key != "limit" && key != "cursor" && key != "all" {
			return nil, nil, invalid("unsupported history query")
		}
	}
	q, e := recordURLQuery("projects", values)
	if e != nil {
		return nil, nil, e
	}
	if q.All && (q.Limit != 0 || q.Cursor != "" || q.Fields["limit"] || q.Fields["cursor"]) {
		return nil, nil, invalid("all conflicts with pagination")
	}
	if q.Fields["limit"] && q.Limit == 0 {
		return nil, nil, invalid("limit must be positive")
	}
	if q.Limit == 0 {
		q.Limit = s.Config.Limits.DefaultPage
	}
	if q.Limit < 1 || q.Limit > s.Config.Limits.MaxPage {
		return nil, nil, invalid("invalid limit")
	}
	binding += ":" + strconv.Itoa(q.Limit)
	var after int64
	if q.Cursor != "" {
		var cursor historyCursor
		b, e := base64.RawURLEncoding.DecodeString(q.Cursor)
		if e != nil || protocol.Decode(b, &cursor) != nil || cursor.Binding != binding {
			return nil, nil, protocol.E(400, "invalid_cursor", "invalid history cursor")
		}
		if after, e = s.historyPosition(typ, id, cursor.Last); e != nil {
			return nil, nil, e
		}
	}
	limit := q.Limit + 1
	if q.All {
		limit = s.Config.Limits.ExpandedRecords + 1
	}
	rows, e := s.q().HistoryRows(s.ctx(), typ, id, after, limit)
	if e != nil {
		return nil, nil, e
	}
	if q.All && len(rows) > s.Config.Limits.ExpandedRecords {
		return nil, nil, protocol.E(413, "limit_exceeded", "history snapshot exceeds item limit")
	}
	if q.All || len(rows) <= q.Limit {
		return rows, nil, nil
	}
	rows = rows[:q.Limit]
	last := rows[len(rows)-1]
	b, _ := json.Marshal(historyCursor{binding, last.Timestamp + "-" + last.RequestHash})
	value := base64.RawURLEncoding.EncodeToString(b)
	return rows, &value, nil
}

// historyPosition finds the revision of the entry a cursor names by its
// timestamp and request hash.
func (s *Server) historyPosition(typ, id, last string) (int64, error) {
	bad := protocol.E(400, "invalid_cursor", "invalid history cursor")
	cut := strings.LastIndexByte(last, '-')
	if cut < 0 {
		return 0, bad
	}
	rev, stamp, found, e := s.q().HistoryRevision(s.ctx(), typ, id, last[cut+1:])
	if e != nil {
		return 0, e
	}
	if !found || stamp != last[:cut] {
		return 0, bad
	}
	return rev, nil
}

// historyEntry answers <owner>/history/<hash> with the owner's revision that
// the request committed, or <owner>/requests/<hash> with whether the request
// changed the owner (revision is the owner's current one).
func (s *Server) historyEntry(w http.ResponseWriter, typ, id, hash string, requests bool, revision int64) error {
	h, found, e := s.q().HistoryByRequest(s.ctx(), typ, id, hash)
	if e != nil {
		return e
	}
	switch {
	case found && requests:
		s.write(w, 200, map[string]any{"outcome": "recorded", "request_hash": h.RequestHash, "results": h.Results})
	case found:
		s.write(w, 200, h)
	case requests:
		s.write(w, 200, map[string]any{"outcome": "not_recorded_here", "revision": revision})
	default:
		return protocol.E(404, "not_found", "history entry not found")
	}
	return nil
}
