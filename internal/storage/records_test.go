package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// rawRecords seeds rows directly, independent of the write path.
type rawRecords struct {
	p1, p2, i1, i2, i3, c1, m1 string
}

func seedRaw(t *testing.T, db *DB) rawRecords {
	t.Helper()
	f := rawRecords{"10000000-0000-4000-8000-000000000001", "10000000-0000-4000-8000-000000000002", "20000000-0000-4000-8000-000000000001", "20000000-0000-4000-8000-000000000002", "20000000-0000-4000-8000-000000000003", "30000000-0000-4000-8000-000000000001", "40000000-0000-4000-8000-000000000001"}
	project := "INSERT INTO projects (id, revision, created_at, updated_at, title, description, folded_description) VALUES (?, 2, '" + stamp + "', '2026-10-11T12:00:01.000000Z', ?, ?, '')"
	issue := "INSERT INTO issues (id, project_id, revision, created_at, updated_at, title, title_key, body, folded_body, state, assignee, parent_id) VALUES (?, ?, 3, '" + stamp + "', '" + stamp + "', ?, ?, ?, '', ?, ?, ?)"
	e := write(t, db, stmts(
		[]any{project, f.p1, "feat/one", "First\r\nproject"},
		[]any{project, f.p2, "feat/two", ""},
		[]any{"INSERT INTO project_repositories (project_id, repository_ref) VALUES (?, 'https://example.org/b'), (?, 'https://example.org/a')", f.p1, f.p1},
		[]any{issue, f.i1, f.p1, "Straße", "strasse", "body\x00 with NUL", "open", "nick", nil},
		[]any{issue, f.i3, f.p1, "Child", "child", "", "closed", nil, f.i1},
		[]any{issue, f.i2, f.p2, "Other", "other", "", "open", nil, nil},
		[]any{"INSERT INTO issue_labels (issue_id, label) VALUES (?, 'ui'), (?, 'bug')", f.i1, f.i1},
		[]any{"INSERT INTO issue_links (kind, a, b) VALUES ('blocks', ?, ?), ('blocks', ?, ?), ('related', ?, ?)", f.i1, f.i2, f.i3, f.i1, f.i1, f.i3},
		[]any{"INSERT INTO comments (id, issue_id, revision, created_at, updated_at, author, body, folded_body) VALUES (?, ?, 1, '" + stamp + "', '" + stamp + "', 'me', 'Hi', 'hi')", f.c1, f.i1},
		[]any{"INSERT INTO milestones (id, project_id, revision, created_at, updated_at, title, title_key, body, folded_body) VALUES (?, ?, 1, '" + stamp + "', '" + stamp + "', 'M1', 'm1', 'goal', 'goal')", f.m1, f.p1},
		[]any{"INSERT INTO milestone_members (milestone_id, issue_id, project_id) VALUES (?, ?, ?), (?, ?, ?)", f.m1, f.i3, f.p1, f.m1, f.i1, f.p1},
	))
	if e != nil {
		t.Fatalf("seed: %v", e)
	}
	return f
}

func TestRecordsReadWithComputedMembershipLists(t *testing.T) {
	db := openTemp(t)
	f := seedRaw(t, db)
	nick := "nick"
	read(t, db, func(ctx context.Context, r *ReadTx) {
		p, e := r.Project(ctx, f.p1)
		if e != nil {
			t.Fatal(e)
		}
		sameJSON(t, p, protocol.Project{SchemaVersion: 1, ID: f.p1, Revision: 2, CreatedAt: stamp, UpdatedAt: "2026-10-11T12:00:01.000000Z", Title: "feat/one", Description: "First\r\nproject", RepositoryRefs: []string{"https://example.org/a", "https://example.org/b"}, IssueIDs: []string{f.i1, f.i3}})
		i, e := r.Issue(ctx, f.i1)
		if e != nil {
			t.Fatal(e)
		}
		sameJSON(t, i, protocol.Issue{SchemaVersion: 1, ID: f.i1, Revision: 3, CreatedAt: stamp, UpdatedAt: stamp, Title: "Straße", Body: "body\x00 with NUL", State: "open", Labels: []string{"bug", "ui"}, Assignee: &nick, ProjectID: f.p1, ChildIDs: []string{f.i3}, CommentIDs: []string{f.c1}, Blocks: []string{f.i2}, BlockedBy: []string{f.i3}, Related: []string{f.i3}})
		i, e = r.Issue(ctx, f.i2)
		if e != nil {
			t.Fatal(e)
		}
		sameJSON(t, i, protocol.Issue{SchemaVersion: 1, ID: f.i2, Revision: 3, CreatedAt: stamp, UpdatedAt: stamp, Title: "Other", State: "open", Labels: []string{}, ProjectID: f.p2, ChildIDs: []string{}, CommentIDs: []string{}, Blocks: []string{}, BlockedBy: []string{f.i1}, Related: []string{}})
		c, e := r.Comment(ctx, f.c1)
		if e != nil {
			t.Fatal(e)
		}
		sameJSON(t, c, protocol.Comment{SchemaVersion: 1, ID: f.c1, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp, Body: "Hi", Author: "me", IssueID: f.i1})
		m, e := r.Milestone(ctx, f.m1)
		if e != nil {
			t.Fatal(e)
		}
		sameJSON(t, m, protocol.Milestone{SchemaVersion: 1, ID: f.m1, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp, Title: "M1", Body: "goal", ProjectID: f.p1, IssueIDs: []string{f.i1, f.i3}})
		any, e := r.Record(ctx, Issues, f.i3)
		if e != nil {
			t.Fatal(e)
		}
		if got := any.(protocol.Issue); got.ParentID == nil || *got.ParentID != f.i1 || got.State != "closed" {
			t.Fatalf("record %+v", got)
		}
	})
}

func TestRecordsMissingAreNotFound(t *testing.T) {
	db := openTemp(t)
	f := seedRaw(t, db)
	read(t, db, func(ctx context.Context, r *ReadTx) {
		for _, c := range []struct{ typ, id string }{{Projects, f.i1}, {Issues, f.p1}, {Comments, f.i1}, {Milestones, f.p1}, {Issues, "not-a-uuid"}} {
			if _, e := r.Record(ctx, c.typ, c.id); !errors.Is(e, ErrRecordNotFound) {
				t.Fatalf("%s/%s: %v", c.typ, c.id, e)
			}
		}
		if _, e := r.Record(ctx, "clients", f.i1); e == nil || errors.Is(e, ErrRecordNotFound) {
			t.Fatalf("unknown type: %v", e)
		}
	})
}
