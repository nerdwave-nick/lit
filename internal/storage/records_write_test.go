package storage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// world is a small tracker written through WriteRequest.
type world struct {
	p, q          protocol.Project
	a, b, c, x    protocol.Issue // a, b and c in p; x in q
	comment       protocol.Comment
	milestone     protocol.Milestone
	db            *DB
	lastTimestamp int
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{db: openTemp(t)}
	w.p = newProject(id(1), "feat/one", "Ünïcode\r\nDescription", at(1), "https://example.org/a", "https://example.org/b")
	w.q = newProject(id(2), "feat/two", "", at(1))
	w.a = newIssue(id(11), w.p.ID, "Straße", "Body of A\n", at(1))
	w.a.Labels = []string{"bug", "ui"}
	nick := "nick"
	w.a.Assignee = &nick
	w.b = newIssue(id(12), w.p.ID, "B", "", at(1))
	w.b.ParentID = &w.a.ID
	w.c = newIssue(id(13), w.p.ID, "C", "", at(1))
	w.c.State = "closed"
	w.x = newIssue(id(14), w.q.ID, "Straße", "", at(1))
	w.comment = newComment(id(21), w.a.ID, "A comment", at(1))
	w.milestone = newMilestone(id(31), w.p.ID, "M1", "Goal", at(1), w.a.ID, w.b.ID)
	// Children, comments and members before their owners: the write orders them.
	mustApply(t, w.db, at(1), w.milestone, w.comment, w.b, w.a, w.c, w.x, w.q, w.p)
	w.lastTimestamp = 1
	return w
}

// stamp is the next request time.
func (w *world) stamp() string { w.lastTimestamp++; return at(w.lastTimestamp) }

func TestWriteRequestCreatesRecordsReadBackWithComputedLists(t *testing.T) {
	w := newWorld(t)
	read(t, w.db, func(ctx context.Context, r *ReadTx) {
		p, e := r.Project(ctx, w.p.ID)
		if e != nil {
			t.Fatal(e)
		}
		want := w.p
		want.IssueIDs = []string{w.a.ID, w.b.ID, w.c.ID}
		jsonEqual(t, p, want)
		a, e := r.Issue(ctx, w.a.ID)
		if e != nil {
			t.Fatal(e)
		}
		wantA := w.a
		wantA.ChildIDs, wantA.CommentIDs = []string{w.b.ID}, []string{w.comment.ID}
		jsonEqual(t, a, wantA)
		for _, rec := range []any{w.b, w.c, w.x, w.comment, w.milestone} {
			typ, id, _, _, _, _ := identity(rec)
			got, e := r.Record(ctx, typ, id)
			if e != nil {
				t.Fatal(e)
			}
			jsonEqual(t, got, rec)
		}
		// Folded columns: title keys and folded bodies, computed in Go.
		var key, body, desc string
		if e = r.queryRow(ctx, []any{&key, &body}, "SELECT title_key, folded_body FROM issues WHERE id = ?", w.a.ID); e != nil || key != "strasse" || body != "body of a\n" {
			t.Fatalf("issue folds %q %q %v", key, body, e)
		}
		if e = r.queryRow(ctx, []any{&desc}, "SELECT folded_description FROM projects WHERE id = ?", w.p.ID); e != nil || desc != "ünïcode\r\ndescription" {
			t.Fatalf("project fold %q %v", desc, e)
		}
		if e = r.queryRow(ctx, []any{&key, &body}, "SELECT title_key, folded_body FROM milestones WHERE id = ?", w.milestone.ID); e != nil || key != "m1" || body != "goal" {
			t.Fatalf("milestone folds %q %q %v", key, body, e)
		}
		if e = r.queryRow(ctx, []any{&body}, "SELECT folded_body FROM comments WHERE id = ?", w.comment.ID); e != nil || body != "a comment" {
			t.Fatalf("comment fold %q %v", body, e)
		}
	})
}

func TestWriteRequestUpdatesFieldsSetsAndLinks(t *testing.T) {
	w := newWorld(t)
	s := w.stamp()
	p := next(w.p, s)
	p.Title, p.Description, p.RepositoryRefs = "feat/renamed", "Other", []string{"https://example.org/c"}
	a, b, c := next(w.a, s), next(w.b, s), next(w.c, s)
	a.Title, a.Body, a.Labels, a.Assignee, a.ParentID = "A", "New body", []string{"ui", "x"}, nil, nil
	a.Blocks, a.Related = []string{w.b.ID}, []string{w.c.ID}
	b.BlockedBy, b.ParentID = []string{w.a.ID}, &w.c.ID
	c.Related, c.State = []string{w.a.ID}, "open"
	comment := next(w.comment, s)
	comment.Body, comment.Author = "Edited", "other"
	m := next(w.milestone, s)
	m.Title, m.Body, m.IssueIDs = "M2", "", []string{w.b.ID, w.c.ID}
	mustApply(t, w.db, s, a, b, c, p, comment, m)
	// Unlinking later removes the stored edges again.
	s = w.stamp()
	a2, b2, c2 := next(a, s), next(b, s), next(c, s)
	a2.Blocks, a2.Related, b2.BlockedBy, c2.Related = []string{}, []string{}, []string{}, []string{}
	mustApply(t, w.db, s, a2, b2, c2)
	read(t, w.db, func(ctx context.Context, r *ReadTx) {
		got, e := r.Project(ctx, p.ID)
		if e != nil {
			t.Fatal(e)
		}
		p.IssueIDs = []string{w.a.ID, w.b.ID, w.c.ID}
		jsonEqual(t, got, p)
		ga, e := r.Issue(ctx, a.ID)
		if e != nil {
			t.Fatal(e)
		}
		a2.CommentIDs = []string{w.comment.ID}
		jsonEqual(t, ga, a2)
		gc, e := r.Issue(ctx, c.ID)
		if e != nil {
			t.Fatal(e)
		}
		c2.ChildIDs = []string{b.ID}
		jsonEqual(t, gc, c2)
		for _, rec := range []any{b2, comment, m} {
			typ, id, _, _, _, _ := identity(rec)
			got, e := r.Record(ctx, typ, id)
			if e != nil {
				t.Fatal(e)
			}
			jsonEqual(t, got, rec)
		}
		var n int
		if e = r.queryRow(ctx, []any{&n}, "SELECT count(*) FROM issue_links"); e != nil || n != 0 {
			t.Fatal("links left", n, e)
		}
	})
}

func TestWriteRequestRejectsInvalidWritesAndChangesNothing(t *testing.T) {
	w := newWorld(t)
	s := at(100)
	edit := func(i protocol.Issue) protocol.Issue { i = next(i, s); i.Body += "!"; return i }
	cases := []struct {
		name    string
		records []any
		alter   func(*Request)
		want    string // ErrInvalidWrite when empty
	}{
		{"hash of another intent", []any{edit(w.a)}, func(r *Request) { r.Hash = strings.Repeat("0", 64) }, ""},
		{"timestamp not after the newest", []any{edit(w.a)}, func(r *Request) { r.Timestamp = at(1) }, ""},
		{"timestamp in another form", []any{edit(w.a)}, func(r *Request) { r.Timestamp = "2026-10-11T13:00:00Z" }, ""},
		{"record without a result", []any{edit(w.a), edit(w.c)}, func(r *Request) { r.Results = r.Results[:1] }, ""},
		{"result for another revision", []any{edit(w.a)}, func(r *Request) { r.Results[0].Revision = 9 }, ""},
		{"no records", nil, nil, ""},
		{"revision skipped", []any{next(edit(w.a), s)}, nil, ""},
		{"creation of an existing record", []any{newIssue(w.a.ID, w.p.ID, "Fresh", "", s)}, nil, ""},
		{"update of a missing record", []any{next(newIssue(id(99), w.p.ID, "Gone", "", s), s)}, nil, ""},
		{"update not stamped with the request time", []any{func() protocol.Issue { i := edit(w.a); i.UpdatedAt = at(50); return i }()}, nil, ""},
		{"creation time changed", []any{func() protocol.Issue { i := edit(w.a); i.CreatedAt = s; return i }()}, nil, ""},
		{"creation not stamped with the request time", []any{newIssue(id(98), w.p.ID, "New", "", at(50))}, nil, ""},
		{"only the revision changes", []any{next(w.a, s)}, nil, ""},
		{"issue moves to another project", []any{func() protocol.Issue { i := edit(w.c); i.ProjectID = w.q.ID; return i }()}, nil, ""},
		{"comment moves to another issue", []any{func() protocol.Comment { c := next(w.comment, s); c.IssueID = w.b.ID; return c }()}, nil, ""},
		{"milestone moves to another project", []any{func() protocol.Milestone {
			m := next(w.milestone, s)
			m.ProjectID = w.q.ID
			m.IssueIDs = []string{}
			return m
		}()}, nil, ""},
		{"link to an issue outside the request", []any{func() protocol.Issue { i := edit(w.a); i.Blocks = []string{w.c.ID}; return i }()}, nil, ""},
		{"link not mirrored", []any{func() protocol.Issue { i := edit(w.a); i.Blocks = []string{w.c.ID}; return i }(), edit(w.c)}, nil, ""},
		{"labels out of order", []any{func() protocol.Issue { i := edit(w.a); i.Labels = []string{"z", "a"}; return i }()}, nil, ""},
		{"blocking cycle", []any{func() protocol.Issue {
			i := edit(w.a)
			i.Blocks, i.BlockedBy = []string{w.c.ID}, []string{w.c.ID}
			return i
		}(), func() protocol.Issue {
			i := edit(w.c)
			i.Blocks, i.BlockedBy = []string{w.a.ID}, []string{w.a.ID}
			return i
		}()}, nil, ""},
		{"parent cycle", []any{func() protocol.Issue { i := edit(w.a); i.ParentID = &w.b.ID; return i }()}, nil, ""},
		{"title key taken in the project, late in a batch", []any{newIssue(id(97), w.p.ID, "Fine", "", s), edit(w.c), newIssue(id(96), w.p.ID, "STRASSE", "", s)}, nil, "UNIQUE"},
		{"comment on an unknown issue", []any{newComment(id(95), id(99), "", s)}, nil, "FOREIGN KEY"},
		{"milestone member from another project", []any{newMilestone(id(94), w.p.ID, "M9", "", s, w.x.ID)}, nil, "FOREIGN KEY"},
		{"parent in another project", []any{func() protocol.Issue { i := edit(w.x); i.ParentID = &w.a.ID; return i }()}, nil, "FOREIGN KEY"},
		{"project title taken", []any{newProject(id(93), "FEAT/ONE", "", s)}, nil, "UNIQUE"},
	}
	before := dump(t, w.db)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var r Request
			if len(c.records) > 0 {
				r = request(t, s, c.records...)
			} else {
				r = request(t, s, edit(w.a))
				r.Results = nil
			}
			if c.alter != nil {
				c.alter(&r)
			}
			// A valid request first: the failure must not undo it, and must leave
			// nothing of its own behind.
			ok := newIssue(id(90), w.q.ID, "Unrelated "+c.name, "", at(99))
			okReq := request(t, at(99), ok)
			e := write(t, w.db, func(ctx context.Context, tx *WriteTx) error {
				if e := tx.WriteRequest(ctx, okReq, []any{ok}); e != nil {
					t.Fatal(e)
				}
				snapshot := dumpQueries(t, ctx, tx.queries)
				e := tx.WriteRequest(ctx, r, c.records)
				if e == nil {
					t.Fatal("accepted")
				}
				if c.want == "" && !errors.Is(e, ErrInvalidWrite) || c.want != "" && !strings.Contains(e.Error(), c.want) {
					t.Fatalf("got %v, want %q", e, c.want)
				}
				if got := dumpQueries(t, ctx, tx.queries); got != snapshot {
					t.Fatalf("the failed request left changes:\n%s", got)
				}
				return e
			})
			if e == nil {
				t.Fatal("committed")
			}
			if got := dump(t, w.db); got != before {
				t.Fatalf("database changed:\n%s", got)
			}
		})
	}
}

// dump renders every table's rows, for comparing whole database states.
func dump(t *testing.T, db *DB) string {
	t.Helper()
	var out string
	read(t, db, func(ctx context.Context, r *ReadTx) { out = dumpQueries(t, ctx, r.queries) })
	return out
}

func dumpQueries(t *testing.T, ctx context.Context, q queries) string {
	t.Helper()
	var tables []string
	if e := q.each(ctx, "SELECT name FROM sqlite_schema WHERE type = 'table' ORDER BY name", nil, func(s rowScanner) error {
		var n string
		e := s.Scan(&n)
		tables = append(tables, n)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	var b strings.Builder
	for _, table := range tables {
		rows, e := q.tx.QueryContext(ctx, "SELECT * FROM "+table)
		if e != nil {
			t.Fatal(e)
		}
		cols, _ := rows.Columns()
		var lines []string
		for rows.Next() {
			v := make([]any, len(cols))
			p := make([]any, len(cols))
			for i := range v {
				p[i] = &v[i]
			}
			if e = rows.Scan(p...); e != nil {
				t.Fatal(e)
			}
			lines = append(lines, fmt.Sprintf("%v", v))
		}
		_ = rows.Close()
		sort.Strings(lines)
		fmt.Fprintf(&b, "%s:\n%s\n", table, strings.Join(lines, "\n"))
	}
	return b.String()
}
