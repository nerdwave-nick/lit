package storage

import (
	"context"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// listWorld extends the world with varied stamps, labels, links and claims:
//
//	a: created 1, labels bug+ui, assignee nick, blocks b (b is blocked while a is open), live claim
//	b: created 1, updated 3, child of a, label bug, expired claim
//	c: created 1, updated 2, closed, body mentions Needle
//	x: created 1, in project q
//	d: created 4, in project p, related to nothing
func listWorld(t *testing.T) (*world, protocol.Issue) {
	t.Helper()
	w := newWorld(t)
	c := next(w.c, at(2))
	c.Body = "A Needle in STRASSE\n"
	mustApply(t, w.db, at(2), c)
	w.c = c
	a, b := next(w.a, at(3)), next(w.b, at(3))
	a.Blocks, b.BlockedBy, b.Labels = []string{b.ID}, []string{a.ID}, []string{"bug"}
	mustApply(t, w.db, at(3), a, b)
	w.a, w.b = a, b
	d := newIssue(id(15), w.p.ID, "D", "", at(4))
	mustApply(t, w.db, at(4), d)
	w.lastTimestamp = 4
	client := id(80)
	if e := write(t, w.db, stmts(
		[]any{clientSQL, client, "connected"},
		[]any{claimSQL, w.a.ID, client, id(81), 100, time.Date(2026, 10, 11, 14, 0, 0, 0, time.UTC).UnixNano()},
		[]any{claimSQL, w.b.ID, client, id(82), 100, 200},
	)); e != nil {
		t.Fatal(e)
	}
	return w, d
}

func listIDs(t *testing.T, db *DB, q ListQuery) []string {
	t.Helper()
	ids := []string{}
	read(t, db, func(ctx context.Context, r *ReadTx) {
		rs, e := r.List(ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		for _, rec := range rs {
			_, id, _, _, _, _ := identity(rec)
			ids = append(ids, id)
		}
	})
	return ids
}

func TestListFiltersIssues(t *testing.T) {
	w, d := listWorld(t)
	a, b, c, x := w.a.ID, w.b.ID, w.c.ID, w.x.ID
	yes, no := true, false
	nick, none, aID := "nick", (*string)(nil), w.a.ID
	now := time.Date(2026, 10, 11, 13, 0, 0, 0, time.UTC)
	p := w.p.ID
	for _, c := range []struct {
		name string
		q    ListQuery
		want []string
	}{
		{"project", ListQuery{ProjectID: p}, []string{a, b, c, d.ID}},
		{"all projects", ListQuery{}, []string{a, b, c, x, d.ID}},
		{"ids", ListQuery{IDs: []string{c, x, id(99)}}, []string{c, x}},
		{"state", ListQuery{ProjectID: p, State: "closed"}, []string{c}},
		{"title key", ListQuery{Title: "STRASSE"}, []string{a, x}},
		{"title key in project", ListQuery{ProjectID: p, Title: "d"}, []string{d.ID}},
		{"text in title or body, folded", ListQuery{Text: "strasse"}, []string{a, c, x}},
		{"text across title and body", ListQuery{Text: "c\na needle"}, []string{c}},
		{"parent", ListQuery{ParentID: &[]*string{&aID}[0]}, []string{b}},
		{"no parent", ListQuery{ProjectID: p, ParentID: &none}, []string{a, c, d.ID}},
		{"assignee", ListQuery{Assignee: &[]*string{&nick}[0]}, []string{a}},
		{"no assignee", ListQuery{ProjectID: p, Assignee: &none}, []string{b, c, d.ID}},
		{"labels all", ListQuery{LabelsAll: []string{"bug", "ui"}}, []string{a}},
		{"labels any", ListQuery{LabelsAny: []string{"ui", "bug"}}, []string{a, b}},
		{"labels none", ListQuery{ProjectID: p, LabelsNone: []string{"ui"}}, []string{b, c, d.ID}},
		{"blocked", ListQuery{Blocked: &yes}, []string{b}},
		{"not blocked", ListQuery{ProjectID: p, Blocked: &no}, []string{a, c, d.ID}},
		{"claimed (live claims only)", ListQuery{Claimed: &yes, Now: now}, []string{a}},
		{"unclaimed", ListQuery{ProjectID: p, Claimed: &no, Now: now}, []string{b, c, d.ID}},
		{"claim owner", ListQuery{OwnerClientID: id(80), Now: now}, []string{a}},
		{"claim owner, after expiry", ListQuery{OwnerClientID: id(80), Now: now.Add(2 * time.Hour)}, []string{}},
		{"milestone members", ListQuery{ProjectID: p, MilestoneID: w.milestone.ID}, []string{a, b}},
		{"created after (inclusive)", ListQuery{CreatedAfter: at(4)}, []string{d.ID}},
		{"created before (exclusive)", ListQuery{CreatedBefore: at(4)}, []string{a, b, c, x}},
		{"updated after, sub-microsecond bound", ListQuery{UpdatedAfter: "2026-10-11T13:00:02.0000001Z"}, []string{a, b, d.ID}},
		{"updated before, other zone", ListQuery{UpdatedBefore: "2026-10-11T15:00:03+02:00"}, []string{c, x}},
		{"bounds far out", ListQuery{CreatedAfter: "0001-01-01T00:00:00Z", CreatedBefore: "9999-12-31T23:59:59.9999999Z"}, []string{a, b, c, x, d.ID}},
		{"combined", ListQuery{ProjectID: p, State: "open", LabelsAny: []string{"bug"}, Blocked: &no}, []string{a}},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.q.Type = Issues
			jsonEqual(t, listIDs(t, w.db, c.q), c.want)
		})
	}
}

func TestListSortsAndPagesByKeyset(t *testing.T) {
	w, d := listWorld(t)
	a, b, c := w.a.ID, w.b.ID, w.c.ID
	p := w.p.ID
	for _, s := range []struct {
		sort string
		desc bool
		want []string
	}{
		{"created_at", false, []string{a, b, c, d.ID}},
		{"created_at", true, []string{d.ID, c, b, a}},
		{"updated_at", false, []string{c, a, b, d.ID}},
		{"updated_at", true, []string{d.ID, b, a, c}},
		{"title", false, []string{b, c, d.ID, a}}, // b, c, d, strasse
		{"title", true, []string{a, d.ID, c, b}},
	} {
		q := ListQuery{Type: Issues, ProjectID: p, Sort: s.sort, Descending: s.desc}
		jsonEqual(t, listIDs(t, w.db, q), s.want)
		// Page through two at a time from each returned position.
		var paged []string
		q.Limit = 2
		for {
			var page []any
			read(t, w.db, func(ctx context.Context, r *ReadTx) {
				var e error
				if page, e = r.List(ctx, q); e != nil {
					t.Fatal(e)
				}
			})
			for _, rec := range page {
				_, id, _, _, _, _ := identity(rec)
				paged = append(paged, id)
			}
			if len(page) < 2 {
				break
			}
			last := page[len(page)-1]
			_, id, _, _, _, _ := identity(last)
			q.After = &ListPosition{Key: SortKey(s.sort, last), ID: id}
		}
		jsonEqual(t, paged, s.want)
	}
}

func TestListOtherRecordTypes(t *testing.T) {
	w, _ := listWorld(t)
	extra := newComment(id(22), w.a.ID, "Second", at(5))
	extra.Author = "other"
	mustApply(t, w.db, at(5), extra)
	for _, c := range []struct {
		name string
		q    ListQuery
		want []string
	}{
		{"projects", ListQuery{Type: Projects}, []string{w.p.ID, w.q.ID}},
		{"projects by repository", ListQuery{Type: Projects, RepositoryRefs: []string{"https://example.org/a", "https://example.org/b"}}, []string{w.p.ID}},
		{"projects by folded description", ListQuery{Type: Projects, Text: "ÜNÏCODE"}, []string{w.p.ID}},
		{"projects by title", ListQuery{Type: Projects, Title: "FEAT/TWO"}, []string{w.q.ID}},
		{"projects by title, descending", ListQuery{Type: Projects, Sort: "title", Descending: true}, []string{w.q.ID, w.p.ID}},
		{"comments of an issue", ListQuery{Type: Comments, IssueID: w.a.ID}, []string{w.comment.ID, extra.ID}},
		{"comments by author", ListQuery{Type: Comments, IssueID: w.a.ID, Author: "other"}, []string{extra.ID}},
		{"comments by text", ListQuery{Type: Comments, IssueID: w.a.ID, Text: "A COMMENT"}, []string{w.comment.ID}},
		{"comments updated after", ListQuery{Type: Comments, IssueID: w.a.ID, UpdatedAfter: at(5)}, []string{extra.ID}},
		{"milestones of a project", ListQuery{Type: Milestones, ProjectID: w.p.ID}, []string{w.milestone.ID}},
		{"milestones by title text", ListQuery{Type: Milestones, ProjectID: w.p.ID, Text: "m1\ngoal"}, []string{w.milestone.ID}},
		{"milestones of another project", ListQuery{Type: Milestones, ProjectID: w.q.ID}, []string{}},
	} {
		t.Run(c.name, func(t *testing.T) { jsonEqual(t, listIDs(t, w.db, c.q), c.want) })
	}
	// Listed records are complete, with their computed lists.
	read(t, w.db, func(ctx context.Context, r *ReadTx) {
		rs, e := r.List(ctx, ListQuery{Type: Issues, IDs: []string{w.a.ID}})
		if e != nil || len(rs) != 1 {
			t.Fatal(rs, e)
		}
		one, e := r.Issue(ctx, w.a.ID)
		if e != nil {
			t.Fatal(e)
		}
		jsonEqual(t, rs[0], one)
	})
}

func TestListRefusesInapplicableQueries(t *testing.T) {
	w, _ := listWorld(t)
	yes := true
	read(t, w.db, func(ctx context.Context, r *ReadTx) {
		for _, q := range []ListQuery{
			{Type: "clients"},
			{Type: Comments},
			{Type: Milestones},
			{Type: Comments, IssueID: w.a.ID, Sort: "title"},
			{Type: Issues, Sort: "rank"},
			{Type: Projects, State: "open"},
			{Type: Comments, IssueID: w.a.ID, LabelsAny: []string{"x"}},
			{Type: Issues, Author: "me"},
			{Type: Issues, Claimed: &yes},
			{Type: Issues, CreatedAfter: "yesterday"},
		} {
			if _, e := r.List(ctx, q); e == nil {
				t.Fatalf("accepted %+v", q)
			}
		}
	})
}
