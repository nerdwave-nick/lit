package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// indexHits lists the IDs of the records of type typ whose folded column
// (title or body) matches the GLOB pattern, through grep_index alone.
func indexHits(t *testing.T, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, typ, column, pattern string) []string {
	t.Helper()
	rows, e := q.QueryContext(context.Background(), "SELECT g.owner_id FROM grep_index x JOIN grep_text g ON g.seq = x.rowid WHERE g.owner_type = ? AND x."+column+" GLOB ? ORDER BY g.owner_id", typ, pattern)
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

// checkGrepIndex runs the FTS5 integrity-check of grep_index against
// grep_text.
func checkGrepIndex(t *testing.T, db *sql.DB) {
	t.Helper()
	if e := execWhenFree(db, "INSERT INTO grep_index (grep_index, rank) VALUES ('integrity-check', 1)"); e != nil {
		t.Fatal(e)
	}
}

// execWhenFree runs a write from outside, such as an index integrity check,
// retrying while a write of the owner holds SQLite's write lock: the merger
// merges the search indexes in the background after commits.
func execWhenFree(db *sql.DB, query string) error {
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		_, e := db.Exec(query)
		if !errors.Is(classify(e), ErrBusy) || time.Now().After(deadline) {
			return e
		}
	}
}

// foldedText reads a record's grep_text row.
func foldedText(t *testing.T, db *sql.DB, typ, id string) (title, body string) {
	t.Helper()
	if e := db.QueryRow("SELECT title, body FROM grep_text WHERE owner_type = ? AND owner_id = ?", typ, id).Scan(&title, &body); e != nil {
		t.Fatalf("%s %s: %v", typ, id, e)
	}
	return title, body
}

func TestFreshDatabaseHasTheTrigramIndex(t *testing.T) {
	db := openTemp(t)
	if db.SchemaVersion() != current {
		t.Fatalf("schema version %d, want %d", db.SchemaVersion(), current)
	}
	var text string
	if e := outside(t, db).QueryRow("SELECT sql FROM sqlite_schema WHERE name = 'grep_index'").Scan(&text); e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"fts5", "content='grep_text'", "content_rowid='seq'", "tokenize='trigram case_sensitive 1'"} {
		if !strings.Contains(text, want) {
			t.Errorf("grep_index lacks %s: %s", want, text)
		}
	}
}

// Writes keep grep_text and its trigram index in step with the records, in
// the same transaction; NUL is stored as U+FFFD because GLOB stops at NUL.
func TestWritesMaintainTheGrepColumnsAndIndex(t *testing.T) {
	w := newWorld(t)
	o := outside(t, w.db)
	if title, body := foldedText(t, o, Issues, w.a.ID); title != "strasse" || body != "body of a\n" {
		t.Fatalf("issue folded text %q %q", title, body)
	}
	if title, body := foldedText(t, o, Projects, w.p.ID); title != "" || body != "ünïcode\r\ndescription" {
		t.Fatalf("project folded text %q %q", title, body)
	}
	for _, c := range []struct{ typ, column, pattern string }{
		{Issues, "title", "*strasse*"},
		{Projects, "body", "*cod*"},
		{Comments, "body", "*comment*"},
		{Milestones, "body", "*goa*"},
		{Milestones, "title", "*m1*"},
	} {
		if got := indexHits(t, o, c.typ, c.column, c.pattern); len(got) == 0 {
			t.Errorf("%s.%s %s found nothing", c.typ, c.column, c.pattern)
		}
	}
	a := next(w.a, w.stamp())
	a.Title, a.Body = "Renamed", "NUL\x00after the NUL"
	comment := next(w.comment, w.stamp())
	comment.Body = "Edited"
	mustApply(t, w.db, a.UpdatedAt, a)
	mustApply(t, w.db, comment.UpdatedAt, comment)
	if _, body := foldedText(t, o, Issues, w.a.ID); body != "nul\uFFFDafter the nul" {
		t.Fatalf("issue folded body %q", body)
	}
	for _, c := range []struct {
		typ, column, pattern string
		want                 []string
	}{
		{Issues, "title", "*strasse*", []string{w.x.ID}},
		{Issues, "title", "*renamed*", []string{w.a.ID}},
		{Issues, "body", "*body of*", []string{}},
		{Issues, "body", "*after the nul*", []string{w.a.ID}},
		{Comments, "body", "*comment*", []string{}},
		{Comments, "body", "*edited*", []string{w.comment.ID}},
	} {
		if got := indexHits(t, o, c.typ, c.column, c.pattern); !slices.Equal(got, c.want) {
			t.Errorf("%s.%s %s = %v, want %v", c.typ, c.column, c.pattern, got, c.want)
		}
	}
	checkGrepIndex(t, o)
}

// A version 1 database is snapshotted, then gains grep_text, filled from its
// records, and the trigram index.
func TestVersion1DatabaseIsMigratedWithItsGrepIndex(t *testing.T) {
	dir := t.TempDir()
	db, e := open(dir, migrations[:1], false)
	if e != nil {
		t.Fatal(e)
	}
	if db.SchemaVersion() != 1 {
		t.Fatalf("schema version %d, want 1", db.SchemaVersion())
	}
	f := seed(t, db)
	e = write(t, db, stmts(
		[]any{"UPDATE projects SET description = ? WHERE id = ?", "Ünïcode DESCRIPTION", f.p1},
		[]any{"UPDATE issues SET title = 'Straße', body = ? WHERE id = ?", "NUL\x00Body", f.i1},
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
	if db.SchemaVersion() != current {
		t.Fatalf("migrated to version %d, want %d", db.SchemaVersion(), current)
	}
	if snaps := backups(t, dir); len(snaps) != 1 || !strings.Contains(filepath.Base(snaps[0]), "pre-migration-v1") {
		t.Fatalf("snapshots %v", snaps)
	}
	o := outside(t, db)
	var comment string
	if _, body := foldedText(t, o, Issues, f.i1); body != "nul\uFFFDbody" {
		t.Fatalf("issue folded body %q", body)
	}
	if e := o.QueryRow("SELECT id FROM comments").Scan(&comment); e != nil {
		t.Fatal(e)
	}
	both := []string{f.i1, f.i2}
	slices.Sort(both)
	for _, c := range []struct {
		typ, column, pattern string
		want                 []string
	}{
		{Projects, "body", "*ünïcode desc*", []string{f.p1}},
		{Issues, "title", "*strasse*", both},
		{Issues, "body", "*\uFFFDbody*", []string{f.i1}},
		{Comments, "body", "*hi*", []string{comment}},
		{Milestones, "body", "*goal*", []string{f.m1}},
	} {
		if got := indexHits(t, o, c.typ, c.column, c.pattern); !slices.Equal(got, c.want) {
			t.Errorf("%s.%s %s = %v, want %v", c.typ, c.column, c.pattern, got, c.want)
		}
	}
	checkGrepIndex(t, o)
}

// The setup import (Create, then ImportRecords through the row writer) gives
// a database at the current schema version whose folded text and trigram index are
// complete: grep finds the imported records through the index, and the
// startup checks and Verify pass without rewriting anything.
func TestImportedRecordsAreFoundThroughTheTrigramIndex(t *testing.T) {
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
	if e := o.QueryRow("SELECT count(*) FROM grep_text").Scan(&rows); e != nil || rows != 3 {
		t.Fatalf("grep_text holds %d rows (%v), want 3", rows, e)
	}
	checkGrepIndex(t, o)
	for _, c := range []struct{ needle, want string }{
		{"final newline", w.a.ID},
		{"a2\r\nno", w.a.ID},
		{"comment", w.c.ID},
		{"hello\n", w.p.ID},
	} {
		if query, args := searchQuery("", c.needle); !strings.Contains(query, "grep_index") || len(args) != 3 {
			t.Fatalf("needle %q does not use the trigram index", c.needle)
		}
		if got := scanFor(t, db, "", c.needle); len(got) != 1 || got[0].ID != c.want {
			t.Errorf("needle %q found %v, want %s", c.needle, got, c.want)
		}
	}
	if log, e := check(t, db); e != nil || len(log) != 0 {
		t.Fatal("startup checks:", log, e)
	}
	if r := verify(t, db); len(r.Problems) != 0 || r.Records != 3 {
		t.Fatalf("verify %+v", r)
	}
}
