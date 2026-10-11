package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/textdiff"
)

// FieldDifference is one non-body field's JSON value before and after a change.
type FieldDifference struct {
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}

// BodyHunk is a body change as the whole text before and after it.
type BodyHunk struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

// bodyField names a record type's body field (the project description).
func bodyField(typ string) string {
	if typ == Projects {
		return "description"
	}
	return "body"
}

func bodyOf(r any) string {
	switch p := r.(type) {
	case protocol.Project:
		return p.Description
	case protocol.Issue:
		return p.Body
	case protocol.Comment:
		return p.Body
	case protocol.Milestone:
		return p.Body
	}
	return ""
}

// writeHistory stores one history row: the changed own fields (computed
// membership lists are not fields) with their JSON values before and after,
// and the body change as a forward unified diff from the previous body, or
// NULL when the body is unchanged. The diff must reproduce the new body.
func (t *WriteTx) writeHistory(ctx context.Context, hash string, c change) error {
	before, oldBody := map[string]json.RawMessage{}, ""
	if c.before != nil {
		before, oldBody = ownFields(c.before), bodyOf(c.before)
	}
	after, newBody := ownFields(c.after), bodyOf(c.after)
	delete(after, bodyField(c.typ))
	diffs := map[string]FieldDifference{}
	own := oldBody != newBody
	for k, v := range after {
		old := before[k]
		if old == nil {
			old = json.RawMessage("null")
		}
		if !bytes.Equal(old, v) {
			diffs[k] = FieldDifference{Before: old, After: v}
			own = own || k != "revision" && k != "updated_at"
		}
	}
	if !own {
		return invalidWrite("%s %s changes none of its own fields", c.typ, c.id)
	}
	var bodyDiff any // NULL when the body is unchanged
	if oldBody != newBody {
		d := textdiff.Diff(oldBody, newBody)
		if got, e := textdiff.Apply(d, oldBody); e != nil || got != newBody {
			return invalidWrite("the body diff of %s %s does not reproduce its body: %v", c.typ, c.id, e)
		}
		bodyDiff = strings.Join(d, "\n")
	}
	differences, _ := json.Marshal(diffs)
	_, _, rev, _, _, _ := identity(c.after)
	_, e := t.exec(ctx, "INSERT INTO history (owner_type, owner_id, revision, request_hash, differences, body_diff) VALUES (?, ?, ?, ?, ?, ?)",
		c.typ, c.id, rev, hash, string(differences), bodyDiff)
	return e
}

// HistoryEntry is one revision of a record as the history API returns it:
// its request's intent, time and results, the changed fields, and the body
// change as the whole text before and after (empty when unchanged).
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

// History reads the revisions of one record after afterRevision, oldest
// first, at most limit of them (0 for all): a keyset page by revision. Body
// hunks are rebuilt by replaying the stored diffs forward from the empty body
// of the record's creation, in one pass that includes the revisions before the
// page. A record without history, or a page past its newest revision, gives an
// empty list. Missing revisions or a diff that does not apply are errors.
func (q queries) History(ctx context.Context, typ, id string, afterRevision int64, limit int) ([]HistoryEntry, error) {
	if _, e := recordColumns(typ); e != nil {
		return nil, e
	}
	body, expect := "", int64(1)
	replay := func(rev int64, diff *string) error {
		if rev != expect {
			return fmt.Errorf("history of %s %s lacks revision %d", typ, id, expect)
		}
		expect++
		if diff == nil {
			return nil
		}
		next, e := textdiff.Apply(strings.Split(*diff, "\n"), body)
		if e != nil {
			return fmt.Errorf("history of %s %s revision %d: %w", typ, id, rev, e)
		}
		body = next
		return nil
	}
	e := q.each(ctx, "SELECT revision, body_diff FROM history WHERE owner_type = ? AND owner_id = ? AND revision <= ? ORDER BY revision", []any{typ, id, afterRevision}, func(s rowScanner) error {
		var rev int64
		var diff *string
		if e := s.Scan(&rev, &diff); e != nil {
			return e
		}
		return replay(rev, diff)
	})
	if e != nil {
		return nil, e
	}
	if limit <= 0 {
		limit = -1 // no LIMIT
	}
	out := []HistoryEntry{}
	e = q.each(ctx, `SELECT h.revision, h.request_hash, h.differences, h.body_diff, t.timestamp, t.intent, t.results
		FROM history h JOIN transactions t ON t.request_hash = h.request_hash
		WHERE h.owner_type = ? AND h.owner_id = ? AND h.revision > ? ORDER BY h.revision LIMIT ?`, []any{typ, id, afterRevision, limit}, func(s rowScanner) error {
		h := HistoryEntry{SchemaVersion: 1, OwnerID: id, BodyHunks: []BodyHunk{}}
		var differences, intent, results string
		var diff *string
		if e := s.Scan(&h.Revision, &h.RequestHash, &differences, &diff, &h.Timestamp, &intent, &results); e != nil {
			return e
		}
		h.BeforeRevision = h.Revision - 1
		if e := json.Unmarshal([]byte(differences), &h.Differences); e != nil {
			return fmt.Errorf("stored differences: %w", e)
		}
		var e error
		if h.Intent, h.Results, e = decodeTransaction(intent, results); e != nil {
			return e
		}
		before := body
		if e = replay(h.Revision, diff); e != nil {
			return e
		}
		if diff != nil {
			h.BodyHunks = append(h.BodyHunks, BodyHunk{Before: before, After: body})
		}
		out = append(out, h)
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

// HistoryRevision finds the revision of one record that the request with the
// given hash committed, and the request's timestamp; found is false when that
// request did not change the record.
func (q queries) HistoryRevision(ctx context.Context, typ, id, hash string) (revision int64, timestamp string, found bool, err error) {
	e := q.queryRow(ctx, []any{&revision, &timestamp}, `SELECT h.revision, t.timestamp FROM history h JOIN transactions t ON t.request_hash = h.request_hash
		WHERE h.owner_type = ? AND h.owner_id = ? AND h.request_hash = ?`, typ, id, hash)
	if errors.Is(e, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	return revision, timestamp, e == nil, e
}

// HistoryByRequest reads the revision of one record that the request with the
// given hash committed; found is false when that request did not change it.
func (q queries) HistoryByRequest(ctx context.Context, typ, id, hash string) (HistoryEntry, bool, error) {
	rev, _, found, e := q.HistoryRevision(ctx, typ, id, hash)
	if e != nil || !found {
		return HistoryEntry{}, false, e
	}
	hs, e := q.History(ctx, typ, id, rev-1, 1)
	if e != nil {
		return HistoryEntry{}, false, e
	}
	if len(hs) != 1 {
		return HistoryEntry{}, false, fmt.Errorf("history of %s %s lacks revision %d", typ, id, rev)
	}
	return hs[0], true, nil
}
