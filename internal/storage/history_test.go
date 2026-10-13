package storage

import (
	"context"
	"encoding/json"
	"math/rand"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/textdiff"
)

func history(t *testing.T, db *DB, typ, id string, after int64, limit int) []HistoryEntry {
	t.Helper()
	var out []HistoryEntry
	read(t, db, func(ctx context.Context, r *ReadTx) {
		var e error
		if out, e = r.History(ctx, typ, id, after, limit); e != nil {
			t.Fatal(e)
		}
	})
	return out
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func TestHistoryRebuildsFullBodiesFromStoredDiffs(t *testing.T) {
	db := openTemp(t)
	p := newProject(id(1), "feat/one", "", at(1))
	mustApply(t, db, at(1), p)
	bodies := []string{"one\ntwo\nthree\n", "one\r\nTWO\r\nthree", "one\r\nTWO\r\nthree", ""}
	i := newIssue(id(2), p.ID, "Title", bodies[0], at(2))
	requests := []Request{request(t, at(2), i)}
	mustApply(t, db, at(2), i)
	for n, body := range bodies[1:] {
		i = next(i, at(3+n))
		i.Body = body
		if n == 1 {
			i.Title, i.Labels = "Renamed", []string{"x"}
		}
		requests = append(requests, request(t, at(3+n), i))
		mustApply(t, db, at(3+n), i)
	}
	got := history(t, db, Issues, i.ID, 0, 0)
	want := []HistoryEntry{
		{1, i.ID, 0, 1, at(2), requests[0].Hash, requests[0].Intent, requests[0].Results, map[string]FieldDifference{
			"schema_version": {raw("null"), raw("1")}, "id": {raw("null"), raw(`"` + i.ID + `"`)}, "revision": {raw("null"), raw("1")},
			"created_at": {raw("null"), raw(`"` + at(2) + `"`)}, "updated_at": {raw("null"), raw(`"` + at(2) + `"`)}, "title": {raw("null"), raw(`"Title"`)},
			"state": {raw("null"), raw(`"open"`)}, "labels": {raw("null"), raw("[]")}, "project_id": {raw("null"), raw(`"` + p.ID + `"`)},
			"blocks": {raw("null"), raw("[]")}, "blocked_by": {raw("null"), raw("[]")}, "related": {raw("null"), raw("[]")},
		}, []BodyHunk{{"", bodies[0]}}},
		{1, i.ID, 1, 2, at(3), requests[1].Hash, requests[1].Intent, requests[1].Results, map[string]FieldDifference{
			"revision": {raw("1"), raw("2")}, "updated_at": {raw(`"` + at(2) + `"`), raw(`"` + at(3) + `"`)},
		}, []BodyHunk{{bodies[0], bodies[1]}}},
		{1, i.ID, 2, 3, at(4), requests[2].Hash, requests[2].Intent, requests[2].Results, map[string]FieldDifference{
			"revision": {raw("2"), raw("3")}, "updated_at": {raw(`"` + at(3) + `"`), raw(`"` + at(4) + `"`)},
			"title": {raw(`"Title"`), raw(`"Renamed"`)}, "labels": {raw("[]"), raw(`["x"]`)},
		}, []BodyHunk{}},
		{1, i.ID, 3, 4, at(5), requests[3].Hash, requests[3].Intent, requests[3].Results, map[string]FieldDifference{
			"revision": {raw("3"), raw("4")}, "updated_at": {raw(`"` + at(4) + `"`), raw(`"` + at(5) + `"`)},
		}, []BodyHunk{{bodies[2], ""}}},
	}
	sameJSON(t, got, want)
	// Keyset pages replay the earlier diffs they do not return.
	sameJSON(t, history(t, db, Issues, i.ID, 1, 2), want[1:3])
	sameJSON(t, history(t, db, Issues, i.ID, 3, 5), want[3:])
	sameJSON(t, history(t, db, Issues, i.ID, 4, 0), []HistoryEntry{})
	read(t, db, func(ctx context.Context, r *ReadTx) {
		e, found, err := r.HistoryByRequest(ctx, Issues, i.ID, requests[1].Hash)
		if err != nil || !found {
			t.Fatal(found, err)
		}
		sameJSON(t, e, want[1])
		if _, found, err = r.HistoryByRequest(ctx, Issues, i.ID, requests[0].Hash+"0"); err != nil || found {
			t.Fatal("unknown hash", found, err)
		}
		if _, found, err = r.HistoryByRequest(ctx, Projects, p.ID, requests[1].Hash); err != nil || found {
			t.Fatal("another owner's request", found, err)
		}
		// The rows hold forward diffs, and nothing when the body is unchanged.
		var diffs []*string
		if err = r.each(ctx, "SELECT body_diff FROM history WHERE owner_id = ? ORDER BY revision", []any{i.ID}, func(s rowScanner) error {
			var d *string
			e := s.Scan(&d)
			diffs = append(diffs, d)
			return e
		}); err != nil {
			t.Fatal(err)
		}
		if len(diffs) != 4 || diffs[2] != nil || *diffs[1] != strings.Join(textdiff.Diff(bodies[0], bodies[1]), "\n") {
			t.Fatalf("stored diffs %v", diffs)
		}
	})
}

func TestHistoryOfEveryRecordType(t *testing.T) {
	w := newWorld(t)
	s := w.stamp()
	p, c, m := next(w.p, s), next(w.comment, s), next(w.milestone, s)
	p.Description, c.Body, m.Body, m.IssueIDs = "New description", "Edited", "Goal\nmore", []string{w.a.ID}
	mustApply(t, w.db, s, p, c, m)
	for _, x := range []struct {
		typ, id       string
		before, after string
		field         string
	}{{Projects, p.ID, w.p.Description, p.Description, "repository_refs"}, {Comments, c.ID, w.comment.Body, c.Body, "author"}, {Milestones, m.ID, w.milestone.Body, m.Body, "issue_ids"}} {
		hs := history(t, w.db, x.typ, x.id, 0, 0)
		if len(hs) != 2 {
			t.Fatalf("%s: %d entries", x.typ, len(hs))
		}
		sameJSON(t, hs[0].BodyHunks, []BodyHunk{{"", x.before}})
		sameJSON(t, hs[1].BodyHunks, []BodyHunk{{x.before, x.after}})
		if _, ok := hs[0].Differences[x.field]; !ok {
			t.Fatalf("%s creation lacks %s: %v", x.typ, x.field, hs[0].Differences)
		}
		if _, ok := hs[0].Differences[bodyField(x.typ)]; ok {
			t.Fatalf("%s body is a field difference", x.typ)
		}
	}
	// Computed membership lists are never field differences.
	for _, h := range append(history(t, w.db, Projects, w.p.ID, 0, 0), history(t, w.db, Issues, w.a.ID, 0, 0)...) {
		for _, k := range []string{"issue_ids", "child_ids", "comment_ids"} {
			if _, ok := h.Differences[k]; ok {
				t.Fatalf("%s %s records %s", h.OwnerID, h.RequestHash, k)
			}
		}
	}
	if hs := history(t, w.db, Milestones, m.ID, 1, 0); string(hs[0].Differences["issue_ids"].After) != `["`+w.a.ID+`"]` {
		t.Fatalf("explicit milestone members: %+v", hs[0].Differences)
	}
}

func TestHistoryFailsClosedOnDamagedRows(t *testing.T) {
	for _, damage := range []string{
		"UPDATE history SET body_diff = replace(body_diff, '+two', '+zwei') WHERE revision = 2",
		"UPDATE history SET body_diff = NULL WHERE revision = 2",
		"DELETE FROM history WHERE revision = 2",
		// An intent that still decodes, but is not the one its request hash names.
		`UPDATE transactions SET intent = json_set(intent, '$.operation', 'tampered')
			WHERE request_hash = (SELECT request_hash FROM history WHERE revision = 3)`,
	} {
		t.Run(damage, func(t *testing.T) {
			db := openTemp(t)
			p := newProject(id(1), "feat/one", "one\n", at(1))
			mustApply(t, db, at(1), p)
			p = next(p, at(2))
			p.Description = "one\ntwo\n"
			mustApply(t, db, at(2), p)
			p = next(p, at(3))
			p.Description = "one\ntwo\nthree\n"
			mustApply(t, db, at(3), p)
			if e := write(t, db, stmts([]any{damage})); e != nil {
				t.Fatal(e)
			}
			read(t, db, func(ctx context.Context, r *ReadTx) {
				if _, e := r.History(ctx, Projects, p.ID, 2, 0); e == nil {
					t.Fatal("rebuilt history from damaged rows")
				}
			})
		})
	}
}

// Replaying the stored diffs gives back every body, for random edit chains
// with CRLF, missing final newlines and empty bodies.
func TestHistoryReplayRoundTripsRandomEdits(t *testing.T) {
	db := openTemp(t)
	rnd := rand.New(rand.NewSource(7))
	lines := []string{"a\n", "b\n", "c\r\n", "\n", "ß\n", "d", "e"}
	body := func() string {
		var b strings.Builder
		for n := rnd.Intn(12); n > 0; n-- {
			b.WriteString(lines[rnd.Intn(len(lines)-2)])
		}
		if rnd.Intn(3) == 0 {
			b.WriteString(lines[len(lines)-1-rnd.Intn(2)])
		}
		return b.String()
	}
	c := newComment(id(3), id(2), body(), at(2))
	mustApply(t, db, at(1), newProject(id(1), "feat/one", "", at(1)), newIssue(id(2), id(1), "I", "", at(1)))
	mustApply(t, db, at(2), c)
	bodies := []string{"", c.Body}
	for n := 3; n < 80; n++ {
		b := body()
		if b == c.Body {
			continue
		}
		c = next(c, at(n))
		c.Body = b
		mustApply(t, db, at(n), c)
		bodies = append(bodies, b)
	}
	k := 0
	for _, h := range history(t, db, Comments, c.ID, 0, 0) {
		if len(h.BodyHunks) != 1 || h.BodyHunks[0].Before != bodies[k] || h.BodyHunks[0].After != bodies[k+1] {
			t.Fatalf("revision %d: %+v, want %q -> %q", h.Revision, h.BodyHunks, bodies[k], bodies[k+1])
		}
		k++
	}
	if k != len(bodies)-1 {
		t.Fatalf("%d body changes, want %d", k, len(bodies)-1)
	}
}

func TestHistoryRevisionFindsARequestsRevision(t *testing.T) {
	db := openTemp(t)
	p := newProject(id(1), "feat/one", "", at(1))
	mustApply(t, db, at(1), p)
	q := next(p, at(2))
	q.Description = "changed"
	r := request(t, at(2), q)
	mustApply(t, db, at(2), q)
	other := newProject(id(2), "feat/two", "", at(3))
	o := request(t, at(3), other)
	mustApply(t, db, at(3), other)
	read(t, db, func(ctx context.Context, tx *ReadTx) {
		rev, stamp, found, e := tx.HistoryRevision(ctx, Projects, p.ID, r.Hash)
		if e != nil || !found || rev != 2 || stamp != at(2) {
			t.Fatalf("revision %d at %s, found %v: %v", rev, stamp, found, e)
		}
		// A request that changed another record did not change this one.
		if _, _, found, e = tx.HistoryRevision(ctx, Projects, p.ID, o.Hash); e != nil || found {
			t.Fatalf("found another record's request: %v", e)
		}
	})
}
