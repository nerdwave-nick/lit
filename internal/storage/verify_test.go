package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// verifyWorld is graphWorld with edited bodies (CRLF, no final newline,
// emptied), a renamed issue, a project description, a milestone, comment
// and closed issue edits, so every record has several revisions.
func verifyWorld(t *testing.T) *world {
	t.Helper()
	w := graphWorld(t)
	s := w.stamp()
	a, p, m, c := next(w.a, s), next(w.p, s), next(w.milestone, s), next(w.comment, s)
	a.Title, a.Body = "Renamed", "Body of A\r\nline two"
	p.Description = "Changed\n"
	m.Body, m.IssueIDs = "", []string{w.a.ID}
	c.Body = "Edited comment\n"
	mustApply(t, w.db, s, a, p, m, c)
	w.a, w.p, w.milestone, w.comment = a, p, m, c
	s = w.stamp()
	a = next(w.a, s)
	a.Body, a.State = "", "closed"
	mustApply(t, w.db, s, a)
	w.a = a
	return w
}

func verify(t *testing.T, db *DB) VerifyReport {
	t.Helper()
	var log logLines
	r, e := db.Verify(context.Background(), log.logf)
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestVerifyPassesOnAConsistentDatabase(t *testing.T) {
	w := verifyWorld(t)
	r := verify(t, w.db)
	if len(r.Problems) != 0 {
		t.Fatal(r.Problems)
	}
	// 2 projects, 5 issues, 1 comment, 1 milestone.
	var revisions int
	read(t, w.db, func(ctx context.Context, q *ReadTx) {
		if e := q.queryRow(ctx, []any{&revisions}, "SELECT count(*) FROM history"); e != nil {
			t.Fatal(e)
		}
	})
	if r.Records != 9 || r.Revisions != revisions {
		t.Fatalf("compared %d records and %d revisions, want 9 and %d", r.Records, r.Revisions, revisions)
	}
}

// Each change made behind the history, and each damaged history row, is a
// mismatch that names the record.
func TestVerifyReportsMismatches(t *testing.T) {
	for name, x := range map[string]struct {
		damage func(w *world) []string
		want   []string
	}{
		"field": {func(w *world) []string {
			return []string{"UPDATE issues SET state = 'open' WHERE id = '" + w.c.ID + "'"}
		},
			[]string{w0("c"), "state", `"open"`, `"closed"`}},
		"body": {func(w *world) []string { return []string{"UPDATE comments SET body = 'tampered'"} },
			[]string{w0("comment"), "body"}},
		"description": {func(w *world) []string {
			return []string{"UPDATE projects SET description = 'x' WHERE id = '" + w.p.ID + "'"}
		},
			[]string{w0("p"), "description"}},
		"label": {func(w *world) []string { return []string{"DELETE FROM issue_labels WHERE issue_id = '" + w.b.ID + "'"} },
			[]string{w0("b"), "labels"}},
		"link": {func(w *world) []string { return []string{"DELETE FROM issue_links WHERE kind = 'related'"} },
			[]string{w0("x"), "related"}},
		"member": {func(w *world) []string { return []string{"DELETE FROM milestone_members"} },
			[]string{w0("milestone"), "issue_ids"}},
		"revision": {func(w *world) []string {
			return []string{"UPDATE issues SET revision = revision + 1 WHERE id = '" + w.a.ID + "'"}
		},
			[]string{w0("a"), "revision"}},
		"missing revision": {func(w *world) []string {
			return []string{"DELETE FROM history WHERE owner_id = '" + w.a.ID + "' AND revision = 2"}
		}, []string{w0("a"), "lacks revision 2"}},
		"no history": {func(w *world) []string { return []string{"DELETE FROM history WHERE owner_id = '" + w.x.ID + "'"} },
			[]string{w0("x"), "no history"}},
		"no record": {func(w *world) []string {
			return []string{"DELETE FROM milestone_members", "DELETE FROM milestones"}
		}, []string{w0("milestone"), "no current record"}},
		"diff": {func(w *world) []string {
			return []string{"UPDATE history SET body_diff = replace(body_diff, '+Body of A', '+Body of B') WHERE owner_id = '" + w.a.ID + "' AND revision = 1"}
		}, []string{w0("a"), "body diff does not apply"}},
		"before": {func(w *world) []string {
			return []string{"UPDATE history SET differences = json_set(differences, '$.title.before', 'Wrong') WHERE owner_id = '" + w.a.ID + "' AND differences -> '$.title' IS NOT NULL AND revision > 1"}
		}, []string{w0("a"), "title", `"Wrong"`}},
	} {
		t.Run(name, func(t *testing.T) {
			w := verifyWorld(t)
			execAll(t, outside(t, w.db), x.damage(w)...)
			r := verify(t, w.db)
			if len(r.Problems) == 0 {
				t.Fatal("verified")
			}
			all := strings.Join(r.Problems, "\n")
			t.Log(all)
			for _, want := range x.want {
				if !strings.Contains(all, worldID(w, want)) {
					t.Fatalf("problems lack %q:\n%s", worldID(w, want), all)
				}
			}
		})
	}
}

// w0 names a world record in a want list; worldID turns it into its ID.
func w0(field string) string { return "\x00" + field }

func worldID(w *world, s string) string {
	if !strings.HasPrefix(s, "\x00") {
		return s
	}
	return map[string]string{"a": w.a.ID, "b": w.b.ID, "c": w.c.ID, "x": w.x.ID, "p": w.p.ID, "comment": w.comment.ID, "milestone": w.milestone.ID}[s[1:]]
}

// History rows kept verbatim from the legacy store may change the computed
// membership lists (a project's issue_ids, an issue's child_ids and
// comment_ids), even only them; verify skips those keys. A milestone's
// issue_ids are its own field and are compared.
func TestVerifySkipsComputedMembershipLists(t *testing.T) {
	w := verifyWorld(t)
	o := outside(t, w.db)
	const legacy = "2026-10-11T15:00:00.000000Z"
	execAll(t, o,
		"INSERT INTO transactions (request_hash, client_id, actor_name, actor_kind, timestamp, intent, results) VALUES ('legacy', 'c', 'me', 'agent', '"+legacy+"', '{}', '[]')",
		// A creation entry with child_ids: null -> [].
		"UPDATE history SET differences = json_set(differences, '$.child_ids', json('{\"before\":null,\"after\":[]}')) WHERE owner_id = '"+w.a.ID+"' AND revision = 1",
		// Membership-only revisions of an issue and a project.
		membershipOnly(w.a.ID, "issues", w.a.Revision, w.a.UpdatedAt, legacy, "comment_ids"),
		"UPDATE issues SET revision = revision + 1, updated_at = '"+legacy+"' WHERE id = '"+w.a.ID+"'",
		membershipOnly(w.p.ID, "projects", w.p.Revision, w.p.UpdatedAt, legacy, "issue_ids"),
		"UPDATE projects SET revision = revision + 1, updated_at = '"+legacy+"' WHERE id = '"+w.p.ID+"'",
	)
	if r := verify(t, w.db); len(r.Problems) != 0 {
		t.Fatal(r.Problems)
	}
}

func membershipOnly(owner, typ string, rev int64, before, after, key string) string {
	return fmt.Sprintf(`INSERT INTO history (owner_type, owner_id, revision, request_hash, differences, body_diff) VALUES ('%s', '%s', %d, 'legacy',
		json_object('revision', json_object('before', %d, 'after', %d), 'updated_at', json_object('before', '%s', 'after', '%s'),
		'%s', json_object('before', json('[]'), 'after', json('["x"]'))), NULL)`, typ, owner, rev+1, rev, rev+1, before, after, key)
}

// verify also runs the startup checks, and does not replay a database that
// fails them.
func TestVerifyRunsTheStartupChecks(t *testing.T) {
	w := verifyWorld(t)
	execAll(t, outside(t, w.db), "UPDATE comments SET issue_id = '"+id(99)+"'")
	r := verify(t, w.db)
	if len(r.Problems) == 0 || !strings.Contains(strings.Join(r.Problems, "\n"), "foreign_key_check") || r.Records != 0 {
		t.Fatal(r)
	}
}

func TestOpenExistingNeverCreatesOrMigrates(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "absent")
		if db, e := OpenExisting(dir); e == nil || !strings.Contains(e.Error(), "no lit database") {
			if db != nil {
				db.Close()
			}
			t.Fatal(e)
		}
		if _, e := os.Stat(dir); !os.IsNotExist(e) {
			t.Fatal("created", e)
		}
	})
	t.Run("empty", func(t *testing.T) {
		dir := t.TempDir()
		if _, e := OpenExisting(dir); e == nil || !strings.Contains(e.Error(), "no lit database") {
			t.Fatal(e)
		}
		if _, e := os.Stat(filepath.Join(dir, DatabaseFile)); !os.IsNotExist(e) {
			t.Fatal("created", e)
		}
	})
	t.Run("older", func(t *testing.T) {
		dir := t.TempDir()
		db, e := Open(dir)
		if e != nil {
			t.Fatal(e)
		}
		db.Close()
		if db, e = open(dir, withV2(), true); e == nil || !strings.Contains(e.Error(), "start lit-server") {
			if db != nil {
				db.Close()
			}
			t.Fatal(e)
		}
		if b := backups(t, dir); len(b) != 0 {
			t.Fatal("snapshotted", b)
		}
	})
	t.Run("current", func(t *testing.T) {
		w := verifyWorld(t)
		dir := w.db.Dir()
		w.db.Close()
		db, e := OpenExisting(dir)
		if e != nil {
			t.Fatal(e)
		}
		defer db.Close()
		if r := verify(t, db); len(r.Problems) != 0 {
			t.Fatal(r.Problems)
		}
		if _, e := OpenExisting(dir); e == nil || !strings.Contains(e.Error(), "already owned") {
			t.Fatal("second owner", e)
		}
	})
}
