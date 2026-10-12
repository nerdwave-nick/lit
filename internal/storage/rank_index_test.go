package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// rankHits lists the owners ("type/id") of the rank_text rows that the FTS5
// query match finds through rank_index alone, in owner order.
func rankHits(t *testing.T, db *sql.DB, match string) []string {
	t.Helper()
	rows, e := db.Query("SELECT g.owner_type || '/' || g.owner_id FROM rank_index x JOIN rank_text g ON g.seq = x.rowid WHERE rank_index MATCH ? ORDER BY 1", match)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if e := rows.Scan(&v); e != nil {
			t.Fatal(e)
		}
		out = append(out, v)
	}
	if e := rows.Err(); e != nil {
		t.Fatal(e)
	}
	return out
}

// checkRankIndex runs the FTS5 integrity-check of rank_index against
// rank_text.
func checkRankIndex(t *testing.T, db *sql.DB) {
	t.Helper()
	if e := execWhenFree(db, "INSERT INTO rank_index (rank_index, rank) VALUES ('integrity-check', 1)"); e != nil {
		t.Fatal(e)
	}
}

// rankText reads a record's rank_text row: its project, its issue and its
// three text columns.
func rankText(t *testing.T, db *sql.DB, typ, id string) [5]string {
	t.Helper()
	var v [5]string
	if e := db.QueryRow("SELECT project_id, issue_id, title, body, comment FROM rank_text WHERE owner_type = ? AND owner_id = ?", typ, id).Scan(&v[0], &v[1], &v[2], &v[3], &v[4]); e != nil {
		t.Fatalf("%s %s: %v", typ, id, e)
	}
	return v
}

func owner(typ, id string) string { return typ + "/" + id }

func TestFreshDatabaseHasTheRankedIndex(t *testing.T) {
	db := openTemp(t)
	if db.SchemaVersion() != 3 {
		t.Fatalf("schema version %d, want 3", db.SchemaVersion())
	}
	var text string
	if e := outside(t, db).QueryRow("SELECT sql FROM sqlite_schema WHERE name = 'rank_index'").Scan(&text); e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"fts5", "content='rank_text'", "content_rowid='seq'", "tokenize='unicode61 remove_diacritics 2'"} {
		if !strings.Contains(text, want) {
			t.Errorf("rank_index lacks %s: %s", want, text)
		}
	}
}

// Writes keep rank_text (the original text, with each record's project and
// issue) and its word index in step with the records, in the same
// transaction.
func TestWritesMaintainTheRankedTextAndIndex(t *testing.T) {
	w := newWorld(t)
	o := outside(t, w.db)
	for _, c := range []struct {
		typ, id string
		want    [5]string
	}{
		{Projects, w.p.ID, [5]string{w.p.ID, "", "feat/one", "Ünïcode\r\nDescription", ""}},
		{Issues, w.a.ID, [5]string{w.p.ID, w.a.ID, "Straße", "Body of A\n", ""}},
		{Comments, w.comment.ID, [5]string{w.p.ID, w.a.ID, "", "", "A comment"}},
		{Milestones, w.milestone.ID, [5]string{w.p.ID, "", "M1", "Goal", ""}},
	} {
		if got := rankText(t, o, c.typ, c.id); got != c.want {
			t.Errorf("%s %s rank_text %q, want %q", c.typ, c.id, got, c.want)
		}
	}
	for _, c := range []struct {
		match string
		want  []string
	}{
		{`"unicode"`, []string{owner(Projects, w.p.ID)}},
		{`"strasse"`, []string{}},
		{`"straße"`, []string{owner(Issues, w.a.ID), owner(Issues, w.x.ID)}},
		{`title : "body"`, []string{}},
		{`body : "body"`, []string{owner(Issues, w.a.ID)}},
		{`comment : "comment"`, []string{owner(Comments, w.comment.ID)}},
		{`"goal"`, []string{owner(Milestones, w.milestone.ID)}},
	} {
		if got := rankHits(t, o, c.match); !slices.Equal(got, c.want) {
			t.Errorf("%s = %v, want %v", c.match, got, c.want)
		}
	}
	a := next(w.a, w.stamp())
	a.Title, a.Body = "Renamed café", "New body"
	comment := next(w.comment, w.stamp())
	comment.Body = "Edited"
	mustApply(t, w.db, a.UpdatedAt, a)
	mustApply(t, w.db, comment.UpdatedAt, comment)
	for _, c := range []struct {
		match string
		want  []string
	}{
		{`"cafe"`, []string{owner(Issues, w.a.ID)}},
		{`"straße"`, []string{owner(Issues, w.x.ID)}},
		{`"body"`, []string{owner(Issues, w.a.ID)}},
		{`"comment"`, []string{}},
		{`"edited"`, []string{owner(Comments, w.comment.ID)}},
	} {
		if got := rankHits(t, o, c.match); !slices.Equal(got, c.want) {
			t.Errorf("after the update %s = %v, want %v", c.match, got, c.want)
		}
	}
	checkRankIndex(t, o)
}

// A version 2 database is snapshotted, then gains rank_text, filled from its
// records, and the word index.
func TestVersion2DatabaseIsMigratedWithItsRankedIndex(t *testing.T) {
	dir := t.TempDir()
	db, e := open(dir, migrations[:2], false)
	if e != nil {
		t.Fatal(e)
	}
	if db.SchemaVersion() != 2 {
		t.Fatalf("schema version %d, want 2", db.SchemaVersion())
	}
	f := seed(t, db)
	e = write(t, db, stmts(
		[]any{"UPDATE projects SET description = ? WHERE id = ?", "Ünïcode DESCRIPTION", f.p1},
		[]any{"UPDATE issues SET title = 'Café', body = ? WHERE id = ?", "Résumé body", f.i1},
		[]any{"UPDATE milestones SET body = 'Milestone GOAL' WHERE id = ?", f.m1},
	))
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	if db, e = Open(dir); e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if db.SchemaVersion() != 3 {
		t.Fatalf("migrated to version %d, want 3", db.SchemaVersion())
	}
	if snaps := backups(t, dir); len(snaps) != 1 || !strings.Contains(filepath.Base(snaps[0]), "pre-migration-v2") {
		t.Fatalf("snapshots %v", snaps)
	}
	o := outside(t, db)
	var comment string
	if e := o.QueryRow("SELECT id FROM comments").Scan(&comment); e != nil {
		t.Fatal(e)
	}
	if got := rankText(t, o, Comments, comment); got[0] != f.p1 || got[1] != f.i1 || got[4] != "Hi" {
		t.Fatalf("comment rank_text %q", got)
	}
	for _, c := range []struct {
		match string
		want  []string
	}{
		{`"unicode" "description"`, []string{owner(Projects, f.p1)}},
		{`"cafe"`, []string{owner(Issues, f.i1)}},
		{`"resume"`, []string{owner(Issues, f.i1)}},
		{`"hi"`, []string{owner(Comments, comment)}},
		{`"goal"`, []string{owner(Milestones, f.m1)}},
	} {
		if got := rankHits(t, o, c.match); !slices.Equal(got, c.want) {
			t.Errorf("%s = %v, want %v", c.match, got, c.want)
		}
	}
	checkRankIndex(t, o)
}

// The setup import (Create, then ImportRecords through the row writer) gives
// a database whose ranked text and word index are complete: every imported
// record is found, and the startup checks pass without rewriting anything.
func TestImportedRecordsAreFoundThroughTheRankedIndex(t *testing.T) {
	dir := t.TempDir()
	db, e := Create(dir, testService)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if db.SchemaVersion() != current {
		t.Fatalf("created schema version %d, want %d", db.SchemaVersion(), current)
	}
	w := newLegacyWorld(t)
	w.db.Close()
	w.db = db
	if e := write(t, db, func(ctx context.Context, tx *WriteTx) error { return w.importAll(ctx, tx) }); e != nil {
		t.Fatal(e)
	}
	o := outside(t, db)
	var rows int
	if e := o.QueryRow("SELECT count(*) FROM rank_text").Scan(&rows); e != nil || rows != 3 {
		t.Fatalf("rank_text holds %d rows (%v), want 3", rows, e)
	}
	checkRankIndex(t, o)
	for _, c := range []struct{ match, want string }{
		{`"final" "newline"`, owner(Issues, w.a.ID)},
		{`"comment"`, owner(Comments, w.c.ID)},
		{`"hello"`, owner(Projects, w.p.ID)},
	} {
		if got := rankHits(t, o, c.match); !slices.Equal(got, []string{c.want}) {
			t.Errorf("%s found %v, want %s", c.match, got, c.want)
		}
	}
	if log, e := check(t, db); e != nil || len(log) != 0 {
		t.Fatal("startup checks:", log, e)
	}
}

// rank_index is covered by the generic FTS5 check: an index that no longer
// matches rank_text is rebuilt at startup, with a log line.
func TestStartupCheckRebuildsAStaleRankedIndex(t *testing.T) {
	w := newWorld(t)
	dir := closed(t, w.db, func(o *sql.DB) {
		execAll(t, o, "INSERT INTO rank_index (rank_index, rowid, title, body, comment) SELECT 'delete', seq, title, body, comment FROM rank_text WHERE owner_id = '"+w.a.ID+"'")
	})
	db := reopen(t, dir)
	o := outside(t, db)
	if got := rankHits(t, o, `"body"`); len(got) != 0 {
		t.Fatalf("damage left %v found", got)
	}
	log, e := check(t, db)
	if e != nil || len(log) != 1 || !strings.Contains(log[0], "search index rank_index failed its integrity check") || !strings.Contains(log[0], "rebuilt it") {
		t.Fatal(log, e)
	}
	if got := rankHits(t, o, `"body"`); !slices.Equal(got, []string{owner(Issues, w.a.ID)}) {
		t.Fatalf("after the rebuild: %v", got)
	}
	checkRankIndex(t, o)
}

// The ranked text is derived data too: stale rows, missing rows (a writer
// that skipped them) and rows without a record are rewritten at startup, with
// a log line per record type, and the word index follows.
func TestStartupCheckRewritesStaleRankedText(t *testing.T) {
	w := newWorld(t)
	dir := closed(t, w.db, func(o *sql.DB) {
		execAll(t, o,
			"UPDATE rank_text SET title = 'stale' WHERE owner_type = 'issues'",
			"DELETE FROM rank_text WHERE owner_type = 'comments'",
			"UPDATE rank_text SET project_id = 'stale' WHERE owner_id = '"+w.p.ID+"'",
			"INSERT INTO rank_text (owner_type, owner_id, project_id, issue_id, title, body, comment) VALUES ('milestones', '"+id(99)+"', '', '', 'orphan', '', '')")
	})
	db := reopen(t, dir)
	log, e := check(t, db)
	if e != nil {
		t.Fatal(e)
	}
	want := []string{
		"lit-server: ranked search text of 1 projects was stale; rewrote it",
		"lit-server: ranked search text of 4 issues was stale; rewrote it",
		"lit-server: ranked search text of 1 comments was stale; rewrote it",
		"lit-server: ranked search text of 1 milestones was stale; rewrote it",
	}
	if strings.Join(log, "\n") != strings.Join(want, "\n") {
		t.Fatalf("log %q, want %q", log, want)
	}
	o := outside(t, db)
	for match, want := range map[string][]string{
		`"stale"`:   {},
		`"orphan"`:  {},
		`"straße"`:  {owner(Issues, w.a.ID), owner(Issues, w.x.ID)},
		`"comment"`: {owner(Comments, w.comment.ID)},
	} {
		if got := rankHits(t, o, match); !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", match, got, want)
		}
	}
	if got := rankText(t, o, Projects, w.p.ID); got[0] != w.p.ID {
		t.Fatalf("project rank_text %q", got)
	}
	checkRankIndex(t, o)
	if log, e := check(t, db); e != nil || len(log) != 0 {
		t.Fatal("second check:", log, e)
	}
}
