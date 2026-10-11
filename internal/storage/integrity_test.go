package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// logLines collects the lines a check logs.
type logLines []string

func (l *logLines) logf(format string, args ...any) { *l = append(*l, fmt.Sprintf(format, args...)) }

// closed runs f on the database file of db's directory after db is closed,
// through a separate connection without foreign keys, as a damaging process
// would, and returns the directory.
func closed(t *testing.T, db *DB, f func(o *sql.DB)) string {
	t.Helper()
	dir := db.Dir()
	if e := db.Close(); e != nil {
		t.Fatal(e)
	}
	o, e := sql.Open("sqlite", "file:"+filepath.Join(dir, DatabaseFile))
	if e != nil {
		t.Fatal(e)
	}
	o.SetMaxOpenConns(1)
	f(o)
	if e = o.Close(); e != nil {
		t.Fatal(e)
	}
	return dir
}

func execAll(t *testing.T, o *sql.DB, list ...string) {
	t.Helper()
	for _, s := range list {
		if _, e := o.Exec(s); e != nil {
			t.Fatalf("%s: %v", s, e)
		}
	}
}

// reopen opens dir again and closes it when the test ends.
func reopen(t *testing.T, dir string) *DB {
	t.Helper()
	db, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// searchIndex adds an external-content FTS5 table over comment bodies, as a
// search table of the storage module would be, and fills it.
func searchIndex(t *testing.T, o *sql.DB, name string) {
	t.Helper()
	execAll(t, o,
		"CREATE VIRTUAL TABLE "+name+" USING fts5(body, content='comments', content_rowid='rowid')",
		"INSERT INTO "+name+"("+name+") VALUES ('rebuild')")
}

// ftsCheck runs the FTS5 integrity-check of name against its content.
func ftsCheck(t *testing.T, db *DB, name string) error {
	return write(t, db, stmts([]any{"INSERT INTO " + name + "(" + name + ", rank) VALUES ('integrity-check', 1)"}))
}

func check(t *testing.T, db *DB) (logLines, error) {
	t.Helper()
	var log logLines
	return log, db.CheckIntegrity(context.Background(), log.logf)
}

func TestStartupCheckPassesOnAHealthyDatabase(t *testing.T) {
	w := graphWorld(t)
	dir := closed(t, w.db, func(o *sql.DB) { searchIndex(t, o, "comment_search") })
	db := reopen(t, dir)
	if log, e := check(t, db); e != nil || len(log) != 0 {
		t.Fatal(log, e)
	}
}

// Damaged pages of an authoritative table fail PRAGMA integrity_check.
func TestStartupCheckFailsOnDamagedPages(t *testing.T) {
	w := graphWorld(t)
	var root, size int64
	dir := closed(t, w.db, func(o *sql.DB) {
		if e := o.QueryRow("SELECT rootpage, (SELECT page_size FROM pragma_page_size) FROM sqlite_schema WHERE name = 'comments'").Scan(&root, &size); e != nil {
			t.Fatal(e)
		}
	})
	f, e := os.OpenFile(filepath.Join(dir, DatabaseFile), os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	// An invalid b-tree page type at the head of the comments table's root page.
	if _, e = f.WriteAt([]byte{0x6b, 0x6b, 0x6b, 0x6b}, (root-1)*size); e != nil {
		t.Fatal(e)
	}
	f.Close()
	db := reopen(t, dir)
	_, e = check(t, db)
	t.Log(e)
	var ie *IntegrityError
	if !errors.Is(e, ErrIntegrity) || !errors.As(e, &ie) || len(ie.Problems) == 0 || !strings.Contains(e.Error(), "integrity_check") {
		t.Fatalf("got %v", e)
	}
}

func TestStartupCheckFailsOnForeignKeyViolations(t *testing.T) {
	w := graphWorld(t)
	dir := closed(t, w.db, func(o *sql.DB) {
		execAll(t, o, "UPDATE comments SET issue_id = '"+id(99)+"'")
	})
	_, e := check(t, reopen(t, dir))
	t.Log(e)
	if !errors.Is(e, ErrIntegrity) || !strings.Contains(e.Error(), "foreign_key_check") || !strings.Contains(e.Error(), "comments") || !strings.Contains(e.Error(), "issues") {
		t.Fatalf("got %v", e)
	}
}

// A search index that disagrees with its content is rebuilt, with a log line,
// and the check passes; so is one whose own pages are damaged.
func TestStartupCheckRebuildsADamagedSearchIndex(t *testing.T) {
	for name, damage := range map[string][]string{
		"stale":   {"UPDATE comments SET body = 'changed behind the index'"},
		"damaged": {"UPDATE comment_search_data SET block = x'00' WHERE id > 10"},
	} {
		t.Run(name, func(t *testing.T) {
			w := graphWorld(t)
			dir := closed(t, w.db, func(o *sql.DB) {
				searchIndex(t, o, "comment_search")
				execAll(t, o, damage...)
			})
			db := reopen(t, dir)
			if ftsCheck(t, db, "comment_search") == nil {
				t.Fatal("the damage went unnoticed")
			}
			log, e := check(t, db)
			t.Log(log)
			if e != nil || len(log) != 1 || !strings.Contains(log[0], "comment_search") || !strings.Contains(log[0], "rebuilt") {
				t.Fatal(log, e)
			}
			if e = ftsCheck(t, db, "comment_search"); e != nil {
				t.Fatal(e)
			}
			if log, e = check(t, db); e != nil || len(log) != 0 {
				t.Fatal("second check", log, e)
			}
		})
	}
}

// A search index that cannot be rebuilt fails the check.
func TestStartupCheckFailsWhenARebuildFails(t *testing.T) {
	w := graphWorld(t)
	dir := closed(t, w.db, func(o *sql.DB) {
		execAll(t, o, "CREATE VIRTUAL TABLE orphan_search USING fts5(body, content='no_such_table')")
	})
	log, e := check(t, reopen(t, dir))
	t.Log(e)
	if !errors.Is(e, ErrIntegrity) || !strings.Contains(e.Error(), "orphan_search") || len(log) != 0 {
		t.Fatal(log, e)
	}
}
