package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/textdiff"
)

// VerifyReport is what Verify compared and what it found.
type VerifyReport struct {
	Records   int      `json:"records"`   // current records compared with their replayed history
	Revisions int      `json:"revisions"` // history rows replayed
	Problems  []string `json:"problems"`  // mismatches and failed checks; empty when the database verified
}

// membershipKeys are the computed membership lists of each record type. Old
// history rows kept verbatim by the setup import may change them, even only
// them; Verify skips them when replaying and comparing. A milestone's
// issue_ids are its own field and are compared.
var membershipKeys = map[string][]string{Projects: {"issue_ids"}, Issues: {"child_ids", "comment_ids"}}

// Verify checks the whole database, for `lit-server verify`: it runs the
// startup checks (CheckIntegrity, including its automatic search index
// rebuild, reported through logf), then replays the field and body history of
// every record from its creation, in one read snapshot, and compares the
// result with the record's current own fields. Every record must have
// consecutive history from revision 1, each stored "before" value must match
// the replay, each body diff must apply, and the replay must equal the
// current record. History is not replayed when the startup checks fail.
//
// Problems are reported in the VerifyReport; an error means the verification
// could not run. A restored backup snapshot is verified the same way: open its
// data directory with OpenExisting and call Verify.
func (db *DB) Verify(ctx context.Context, logf func(format string, args ...any)) (VerifyReport, error) {
	r := VerifyReport{Problems: []string{}}
	if e := db.CheckIntegrity(ctx, logf); e != nil {
		var ie *IntegrityError
		if !errors.As(e, &ie) {
			return r, e
		}
		r.Problems = append(ie.Problems, "history was not replayed, because the startup checks failed")
		return r, nil
	}
	q, e := db.Read(ctx)
	if e != nil {
		return r, e
	}
	defer q.Close()
	for _, typ := range []string{Projects, Issues, Comments, Milestones} {
		var ids []string
		e = q.each(ctx, "SELECT id FROM "+typ+" UNION SELECT owner_id FROM history WHERE owner_type = ? ORDER BY 1", []any{typ}, func(s rowScanner) error {
			var id string
			e := s.Scan(&id)
			ids = append(ids, id)
			return e
		})
		if e != nil {
			return r, e
		}
		for _, id := range ids {
			if e = q.verifyRecord(ctx, typ, id, &r); e != nil {
				return r, e
			}
		}
	}
	return r, nil
}

// verifyRecord replays one record's history and compares it with the record.
func (q queries) verifyRecord(ctx context.Context, typ, id string, r *VerifyReport) error {
	current, e := q.Record(ctx, typ, id)
	missing := errors.Is(e, ErrRecordNotFound)
	if e != nil && !missing {
		return e
	}
	fail := func(format string, args ...any) {
		r.Problems = append(r.Problems, fmt.Sprintf("%s %s: ", typ, id)+fmt.Sprintf(format, args...))
	}
	skip := map[string]bool{bodyField(typ): true}
	for _, k := range membershipKeys[typ] {
		skip[k] = true
	}
	replayed, body := map[string]json.RawMessage{}, ""
	revisions, broken := int64(0), false
	e = q.each(ctx, "SELECT revision, differences, body_diff FROM history WHERE owner_type = ? AND owner_id = ? ORDER BY revision", []any{typ, id}, func(s rowScanner) error {
		var rev int64
		var differences string
		var diff *string
		if e := s.Scan(&rev, &differences, &diff); e != nil {
			return e
		}
		r.Revisions++
		if revisions++; broken {
			return nil
		}
		if rev != revisions {
			fail("history lacks revision %d", revisions)
			broken = true
			return nil
		}
		var diffs map[string]FieldDifference
		if e := json.Unmarshal([]byte(differences), &diffs); e != nil {
			fail("revision %d: stored differences: %v", rev, e)
			broken = true
			return nil
		}
		for _, k := range sortedKeys(diffs) {
			if skip[k] {
				continue
			}
			if before := valueOf(replayed, k); !sameValue(before, diffs[k].Before) {
				fail("revision %d: %s was %s before the change, history replays %s", rev, k, show(diffs[k].Before), show(before))
			}
			replayed[k] = diffs[k].After
		}
		if diff != nil {
			next, e := textdiff.Apply(strings.Split(*diff, "\n"), body)
			if e != nil {
				fail("revision %d: the body diff does not apply: %v", rev, e)
				broken = true
				return nil
			}
			body = next
		}
		return nil
	})
	switch {
	case e != nil:
		return e
	case revisions == 0:
		r.Records++
		fail("has no history")
		return nil
	case missing:
		fail("has history (%d revisions) but no current record", revisions)
		return nil
	}
	r.Records++
	if broken {
		return nil
	}
	replayed[bodyField(typ)], _ = json.Marshal(body)
	own := ownFields(current)
	for _, k := range sortedKeys(own, replayed) {
		if skip[k] && k != bodyField(typ) {
			continue
		}
		if a, b := valueOf(own, k), valueOf(replayed, k); !sameValue(a, b) {
			fail("%s is %s, history replays %s", k, show(a), show(b))
		}
	}
	return nil
}

// valueOf is a field's JSON value; a missing field is null.
func valueOf(m map[string]json.RawMessage, k string) json.RawMessage {
	if v, ok := m[k]; ok {
		return v
	}
	return json.RawMessage("null")
}

// sameValue compares two JSON values, ignoring formatting and key order.
func sameValue(a, b json.RawMessage) bool {
	decode := func(raw json.RawMessage) (any, bool) {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var v any
		return v, d.Decode(&v) == nil
	}
	x, okX := decode(a)
	y, okY := decode(b)
	return okX && okY && reflect.DeepEqual(x, y)
}

// show shortens a JSON value for a problem line.
func show(v json.RawMessage) string {
	const limit = 80
	if s := string(v); len(s) <= limit {
		return s
	}
	return fmt.Sprintf("%s... (%d bytes)", v[:limit], len(v))
}

func sortedKeys[V any](maps ...map[string]V) []string {
	seen := map[string]bool{}
	var keys []string
	for _, m := range maps {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)
	return keys
}
