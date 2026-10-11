package storage

import (
	"context"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"strings"
	"testing"
)

// fixture holds committed rows that the constraint cases refer to.
type fixture struct {
	p1, p2, i1, i2, i3, m1, c1 string
}

const (
	issueSQL     = "INSERT INTO issues (id, project_id, revision, created_at, updated_at, title, title_key, body, folded_body, state, assignee, parent_id) VALUES (?, ?, 1, '" + stamp + "', '" + stamp + "', ?, ?, '', '', ?, NULL, ?)"
	milestoneSQL = "INSERT INTO milestones (id, project_id, revision, created_at, updated_at, title, title_key, body, folded_body) VALUES (?, ?, 1, '" + stamp + "', '" + stamp + "', ?, ?, '', '')"
	commentSQL   = "INSERT INTO comments (id, issue_id, revision, created_at, updated_at, author, body, folded_body) VALUES (?, ?, 1, '" + stamp + "', '" + stamp + "', 'me', 'Hi', 'hi')"
	clientSQL    = "INSERT INTO clients (client_id, state_revision, status, actor_name, actor_kind, project_id, runtime_vendor, runtime_session_id, output_format) VALUES (?, 1, ?, 'me', 'agent', NULL, '', '', NULL)"
	claimSQL     = "INSERT INTO claims (issue_id, owner_client_id, token, acquired_at, expires_at) VALUES (?, ?, ?, ?, ?)"
	txSQL        = "INSERT INTO transactions (request_hash, client_id, actor_name, actor_kind, timestamp, intent, results) VALUES (?, ?, 'me', 'agent', '" + stamp + "', ?, '[]')"
	historySQL   = "INSERT INTO history (owner_type, owner_id, revision, request_hash, differences, body_diff) VALUES (?, ?, 1, ?, '{}', NULL)"
)

// write runs f in one write transaction and commits it only when f succeeds.
func write(t *testing.T, db *DB, f func(ctx context.Context, tx *WriteTx) error) error {
	t.Helper()
	ctx := context.Background()
	w, e := db.LockWrites(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Unlock()
	tx, e := w.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = f(ctx, tx); e != nil {
		return e
	}
	return tx.Commit()
}

func stmts(list ...[]any) func(ctx context.Context, tx *WriteTx) error {
	return func(ctx context.Context, tx *WriteTx) error {
		for _, s := range list {
			if _, e := tx.exec(ctx, s[0].(string), s[1:]...); e != nil {
				return e
			}
		}
		return nil
	}
}

func seed(t *testing.T, db *DB) fixture {
	t.Helper()
	f := fixture{protocol.UUID(), protocol.UUID(), protocol.UUID(), protocol.UUID(), protocol.UUID(), protocol.UUID(), protocol.UUID()}
	e := write(t, db, func(ctx context.Context, tx *WriteTx) error {
		for _, p := range [][2]string{{f.p1, "feat/one"}, {f.p2, "feat/two"}} {
			if _, e := tx.exec(ctx, "INSERT INTO projects (id, revision, created_at, updated_at, title, description, folded_description) VALUES (?, 1, ?, ?, ?, '', '')", p[0], stamp, stamp, p[1]); e != nil {
				return e
			}
		}
		return stmts(
			[]any{"INSERT INTO project_repositories (project_id, repository_ref) VALUES (?, 'https://example.org/a')", f.p1},
			[]any{issueSQL, f.i1, f.p1, "Straße", "strasse", "open", nil},
			[]any{issueSQL, f.i3, f.p1, "Child", "child", "closed", f.i1},
			[]any{issueSQL, f.i2, f.p2, "Straße", "strasse", "open", nil},
			[]any{"INSERT INTO issue_labels (issue_id, label) VALUES (?, 'bug')", f.i1},
			[]any{"INSERT INTO issue_links (kind, a, b) VALUES ('blocks', ?, ?)", f.i1, f.i2},
			[]any{"INSERT INTO issue_links (kind, a, b) VALUES ('related', min(?, ?), max(?, ?))", f.i1, f.i3, f.i1, f.i3},
			[]any{commentSQL, protocol.UUID(), f.i1},
			[]any{milestoneSQL, f.m1, f.p1, "M1", "m1"},
			[]any{"INSERT INTO milestone_members (milestone_id, issue_id, project_id) VALUES (?, ?, ?)", f.m1, f.i1, f.p1},
			[]any{clientSQL, f.c1, "connected"},
			[]any{claimSQL, f.i1, f.c1, protocol.UUID(), 100, 200},
			[]any{txSQL, "hash-1", f.c1, `{"operation":"x"}`},
			[]any{historySQL, "issues", f.i1, "hash-1"},
		)(ctx, tx)
	})
	if e != nil {
		t.Fatalf("seed: %v", e)
	}
	return f
}

func TestSchemaEnforcesConstraints(t *testing.T) {
	db := openTemp(t)
	f := seed(t, db)
	link := "INSERT INTO issue_links (kind, a, b) VALUES (?, ?, ?)"
	member := "INSERT INTO milestone_members (milestone_id, issue_id, project_id) VALUES (?, ?, ?)"
	cases := []struct {
		name string
		stmt []any
		want string
	}{
		{"project title differs only in case", []any{"INSERT INTO projects (id, revision, created_at, updated_at, title, description, folded_description) VALUES (?, 1, '', '', 'FEAT/One', '', '')", protocol.UUID()}, "UNIQUE"},
		{"project revision is an integer", []any{"INSERT INTO projects (id, revision, created_at, updated_at, title, description, folded_description) VALUES (?, 'one', '', '', 'feat/x', '', '')", protocol.UUID()}, "cannot store TEXT"},
		{"issue in an unknown project", []any{issueSQL, protocol.UUID(), protocol.UUID(), "New", "new", "open", nil}, "FOREIGN KEY"},
		{"issue title key taken in its project", []any{issueSQL, protocol.UUID(), f.p1, "STRASSE", "strasse", "open", nil}, "UNIQUE"},
		{"issue parent in another project", []any{issueSQL, protocol.UUID(), f.p1, "New", "new", "open", f.i2}, "FOREIGN KEY"},
		{"issue parent unknown", []any{issueSQL, protocol.UUID(), f.p1, "New", "new", "open", protocol.UUID()}, "FOREIGN KEY"},
		{"issue state", []any{issueSQL, protocol.UUID(), f.p1, "New", "new", "done", nil}, "CHECK"},
		{"issue is its own parent", []any{"UPDATE issues SET parent_id = id WHERE id = ?", f.i1}, "CHECK"},
		{"child moves away from its parent's project", []any{"UPDATE issues SET project_id = ? WHERE id = ?", f.p2, f.i3}, "FOREIGN KEY"},
		{"label of an unknown issue", []any{"INSERT INTO issue_labels (issue_id, label) VALUES (?, 'x')", protocol.UUID()}, "FOREIGN KEY"},
		{"label twice", []any{"INSERT INTO issue_labels (issue_id, label) VALUES (?, 'bug')", f.i1}, "UNIQUE"},
		{"issue blocks itself", []any{link, "blocks", f.i1, f.i1}, "CHECK"},
		{"related stored in descending order", []any{link, "related", max(f.i1, f.i2), min(f.i1, f.i2)}, "CHECK"},
		{"blocks stored twice", []any{link, "blocks", f.i1, f.i2}, "UNIQUE"},
		{"link to an unknown issue", []any{link, "blocks", f.i1, protocol.UUID()}, "FOREIGN KEY"},
		{"unknown link kind", []any{link, "parent", f.i1, f.i2}, "CHECK"},
		{"comment on an unknown issue", []any{commentSQL, protocol.UUID(), protocol.UUID()}, "FOREIGN KEY"},
		{"milestone title key taken in its project", []any{milestoneSQL, protocol.UUID(), f.p1, "m1", "m1"}, "UNIQUE"},
		{"milestone member from another project", []any{member, f.m1, f.i2, f.p1}, "FOREIGN KEY"},
		{"milestone member claimed in its own project", []any{member, f.m1, f.i2, f.p2}, "FOREIGN KEY"},
		{"client status", []any{clientSQL, protocol.UUID(), "gone"}, "CHECK"},
		{"second claim on an issue", []any{claimSQL, f.i1, f.c1, protocol.UUID(), 100, 300}, "UNIQUE"},
		{"claim by an unknown client", []any{claimSQL, f.i3, protocol.UUID(), protocol.UUID(), 100, 200}, "FOREIGN KEY"},
		{"claim on an unknown issue", []any{claimSQL, protocol.UUID(), f.c1, protocol.UUID(), 100, 200}, "FOREIGN KEY"},
		{"claim expires before it was acquired", []any{claimSQL, f.i3, f.c1, protocol.UUID(), 200, 200}, "CHECK"},
		{"request hash committed twice", []any{txSQL, "hash-1", f.c1, "{}"}, "UNIQUE"},
		{"intent is not JSON", []any{txSQL, "hash-2", f.c1, "{"}, "CHECK"},
		{"history of an unknown request", []any{historySQL, "issues", f.i3, "hash-2"}, "FOREIGN KEY"},
		{"history revision twice", []any{historySQL, "issues", f.i1, "hash-1"}, "UNIQUE"},
		{"second service row", []any{"INSERT INTO service (singleton, service_id, schema_version) VALUES (2, ?, 1)", protocol.UUID()}, "CHECK"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// A valid row first: the violation must also discard it.
			late := protocol.UUID()
			e := write(t, db, stmts([]any{issueSQL, late, f.p2, "Late " + late, "late " + late, "open", nil}, c.stmt))
			if e == nil || !strings.Contains(e.Error(), c.want) {
				t.Fatalf("got %v, want a %s violation", e, c.want)
			}
			r, e := db.Read(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			defer r.Close()
			var n int
			if e = r.queryRow(context.Background(), []any{&n}, "SELECT count(*) FROM issues WHERE id = ?", late); e != nil || n != 0 {
				t.Fatalf("the failed transaction kept its earlier row (%d, %v)", n, e)
			}
		})
	}
}

func TestSchemaAcceptsValidRows(t *testing.T) {
	db := openTemp(t)
	f := seed(t, db)
	other := protocol.UUID()
	e := write(t, db, stmts(
		[]any{issueSQL, other, f.p2, "Other", "other", "open", nil},
		[]any{"INSERT INTO issue_links (kind, a, b) VALUES ('blocks', ?, ?)", f.i2, f.i1},
		[]any{"INSERT INTO issue_links (kind, a, b) VALUES ('related', min(?, ?), max(?, ?))", f.i2, other, f.i2, other},
		[]any{"DELETE FROM claims WHERE issue_id = ?", f.i1},
		[]any{claimSQL, f.i1, f.c1, protocol.UUID(), 300, 400},
		[]any{"UPDATE issues SET parent_id = ? WHERE id = ?", f.i2, other},
	))
	if e != nil {
		t.Fatal(e)
	}
}
