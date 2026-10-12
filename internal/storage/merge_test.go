package storage

import (
	"context"
	"database/sql"
	"testing"
)

// automerge reads a search index's stored automerge setting; null when it
// was never set (FTS5's default, 4).
func automerge(t *testing.T, db *sql.DB, table string) sql.NullInt64 {
	t.Helper()
	var v sql.NullInt64
	if e := db.QueryRow("SELECT v FROM " + table + "_config WHERE k = 'automerge'").Scan(&v); e != nil && e != sql.ErrNoRows {
		t.Fatal(e)
	}
	return v
}

// FTS5 merges the search indexes inside commits (automerge). An index that a
// background merger left with automerge 0 gets FTS5's default back when the
// database opens.
func TestOpenRestoresInlineSearchIndexMerges(t *testing.T) {
	dir := t.TempDir()
	db, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	for _, table := range []string{"grep_index", "rank_index"} {
		if v := automerge(t, db.writer, table); v.Valid && v.Int64 != 4 {
			t.Fatalf("fresh %s: automerge %d", table, v.Int64)
		}
		if _, e = db.writer.ExecContext(context.Background(), "INSERT INTO "+table+" ("+table+", rank) VALUES ('automerge', 0)"); e != nil {
			t.Fatal(e)
		}
	}
	db.Close()
	if db, e = Open(dir); e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	for _, table := range []string{"grep_index", "rank_index"} {
		if v := automerge(t, db.writer, table); !v.Valid || v.Int64 != 4 {
			t.Fatalf("reopened %s: automerge %v, want 4", table, v)
		}
	}
}
