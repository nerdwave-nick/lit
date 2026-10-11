package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// grepTables are the trigram search tables, one per record table.
var grepTables = []string{"comments_grep", "issues_grep", "milestones_grep", "projects_grep"}

// grepHits lists the IDs of table's records whose search table matches the
// GLOB pattern in column, through the search table alone.
func grepHits(t *testing.T, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, table, column, pattern string) []string {
	t.Helper()
	rows, e := q.QueryContext(context.Background(), "SELECT r.id FROM "+table+"_grep s JOIN "+table+" r ON r.rowid = s.rowid WHERE s."+column+" GLOB ? ORDER BY r.id", pattern)
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

// checkGrepTables runs each search table's FTS5 integrity-check against its
// content table.
func checkGrepTables(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, name := range grepTables {
		if _, e := db.Exec("INSERT INTO " + name + "(" + name + ", rank) VALUES ('integrity-check', 1)"); e != nil {
			t.Fatalf("%s: %v", name, e)
		}
	}
}

func TestFreshDatabaseHasTheTrigramSearchTables(t *testing.T) {
	db := openTemp(t)
	if db.SchemaVersion() != 2 {
		t.Fatalf("schema version %d, want 2", db.SchemaVersion())
	}
	var names []string
	o := outside(t, db)
	rows, e := o.Query("SELECT name, sql FROM sqlite_schema WHERE type = 'table' AND sql LIKE 'CREATE VIRTUAL TABLE%' ORDER BY name")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	for rows.Next() {
		var name, text string
		if e := rows.Scan(&name, &text); e != nil {
			t.Fatal(e)
		}
		names = append(names, name)
		table := strings.TrimSuffix(name, "_grep")
		for _, want := range []string{"fts5", "content='" + table + "'", "content_rowid='rowid'", "tokenize='trigram case_sensitive 1'"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s lacks %s: %s", name, want, text)
			}
		}
	}
	if !slices.Equal(names, grepTables) {
		t.Fatalf("search tables %v, want %v", names, grepTables)
	}
}

// Writes keep the folded grep columns and the trigram tables in step with the
// records, in the same transaction; NUL is stored as U+FFFD because GLOB stops
// at NUL.
func TestWritesMaintainTheGrepColumnsAndIndex(t *testing.T) {
	w := newWorld(t)
	o := outside(t, w.db)
	var title, body string
	if e := o.QueryRow("SELECT grep_title, grep_body FROM issues WHERE id = ?", w.a.ID).Scan(&title, &body); e != nil || title != "strasse" || body != "body of a\n" {
		t.Fatalf("issue grep columns %q %q %v", title, body, e)
	}
	if e := o.QueryRow("SELECT grep_description FROM projects WHERE id = ?", w.p.ID).Scan(&body); e != nil || body != "ünïcode\r\ndescription" {
		t.Fatalf("project grep column %q %v", body, e)
	}
	for _, c := range []struct{ table, column, pattern string }{
		{"issues", "grep_title", "*strasse*"},
		{"projects", "grep_description", "*cod*"},
		{"comments", "grep_body", "*comment*"},
		{"milestones", "grep_body", "*goa*"},
		{"milestones", "grep_title", "*m1*"},
	} {
		if got := grepHits(t, o, c.table, c.column, c.pattern); len(got) == 0 {
			t.Errorf("%s.%s %s found nothing", c.table, c.column, c.pattern)
		}
	}
	a := next(w.a, w.stamp())
	a.Title, a.Body = "Renamed", "NUL\x00after the NUL"
	comment := next(w.comment, w.stamp())
	comment.Body = "Edited"
	mustApply(t, w.db, a.UpdatedAt, a)
	mustApply(t, w.db, comment.UpdatedAt, comment)
	if e := o.QueryRow("SELECT grep_body FROM issues WHERE id = ?", w.a.ID).Scan(&body); e != nil || body != "nul\uFFFDafter the nul" {
		t.Fatalf("issue grep body %q %v", body, e)
	}
	for _, c := range []struct {
		table, column, pattern string
		want                   []string
	}{
		{"issues", "grep_title", "*strasse*", []string{w.x.ID}},
		{"issues", "grep_title", "*renamed*", []string{w.a.ID}},
		{"issues", "grep_body", "*body of*", []string{}},
		{"issues", "grep_body", "*after the nul*", []string{w.a.ID}},
		{"comments", "grep_body", "*comment*", []string{}},
		{"comments", "grep_body", "*edited*", []string{w.comment.ID}},
	} {
		if got := grepHits(t, o, c.table, c.column, c.pattern); !slices.Equal(got, c.want) {
			t.Errorf("%s.%s %s = %v, want %v", c.table, c.column, c.pattern, got, c.want)
		}
	}
	checkGrepTables(t, o)
}

// A version 1 database is snapshotted, then gains the grep columns, filled
// from its rows, and their trigram tables.
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
	if db.SchemaVersion() != 2 {
		t.Fatalf("migrated to version %d, want 2", db.SchemaVersion())
	}
	if snaps := backups(t, dir); len(snaps) != 1 || !strings.Contains(filepath.Base(snaps[0]), "pre-migration-v1") {
		t.Fatalf("snapshots %v", snaps)
	}
	o := outside(t, db)
	var body, comment string
	if e := o.QueryRow("SELECT grep_body FROM issues WHERE id = ?", f.i1).Scan(&body); e != nil || body != "nul\uFFFDbody" {
		t.Fatalf("issue grep body %q %v", body, e)
	}
	if e := o.QueryRow("SELECT id FROM comments").Scan(&comment); e != nil {
		t.Fatal(e)
	}
	both := []string{f.i1, f.i2}
	slices.Sort(both)
	for _, c := range []struct {
		table, column, pattern string
		want                   []string
	}{
		{"projects", "grep_description", "*ünïcode desc*", []string{f.p1}},
		{"issues", "grep_title", "*strasse*", both},
		{"issues", "grep_body", "*\uFFFDbody*", []string{f.i1}},
		{"comments", "grep_body", "*hi*", []string{comment}},
		{"milestones", "grep_body", "*goal*", []string{f.m1}},
	} {
		if got := grepHits(t, o, c.table, c.column, c.pattern); !slices.Equal(got, c.want) {
			t.Errorf("%s.%s %s = %v, want %v", c.table, c.column, c.pattern, got, c.want)
		}
	}
	checkGrepTables(t, o)
}
