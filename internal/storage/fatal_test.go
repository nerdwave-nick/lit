package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// corruptSchema makes the stored schema unparsable through a separate
// connection and bumps the schema cookie, so every connection of db reloads
// the schema at its next statement and fails with SQLITE_CORRUPT.
func corruptSchema(t *testing.T, db *DB) {
	t.Helper()
	o := outside(t, db)
	for _, s := range []string{
		"PRAGMA writable_schema = ON",
		"UPDATE sqlite_schema SET sql = 'CREATE INDEX issues_parent ON' WHERE name = 'issues_parent'",
		"PRAGMA writable_schema = OFF",
	} {
		if _, e := o.Exec(s); e != nil {
			t.Fatal(s, e)
		}
	}
	var v int
	if e := o.QueryRow("PRAGMA schema_version").Scan(&v); e != nil {
		t.Fatal(e)
	}
	if _, e := o.Exec(fmt.Sprintf("PRAGMA schema_version = %d", v+1)); e != nil {
		t.Fatal(e)
	}
}

// fatalLog records what db reports through OnFatal.
type fatalLog struct {
	mu   sync.Mutex
	errs []error
}

func (l *fatalLog) add(e error) { l.mu.Lock(); l.errs = append(l.errs, e); l.mu.Unlock() }
func (l *fatalLog) count() int  { l.mu.Lock(); defer l.mu.Unlock(); return len(l.errs) }

func TestOrdinaryErrorsAreNotFatal(t *testing.T) {
	db := openTemp(t)
	var log fatalLog
	db.OnFatal(log.add)
	read(t, db, func(ctx context.Context, r *ReadTx) {
		if _, e := r.Issue(ctx, id(9)); !errors.Is(e, ErrRecordNotFound) || errors.Is(e, ErrFatal) {
			t.Fatal(e)
		}
		if e := r.queryRow(ctx, nil, "SELECT * FROM no_such_table"); e == nil || errors.Is(e, ErrFatal) {
			t.Fatal(e)
		}
	})
	if e := write(t, db, stmts([]any{"INSERT INTO issues (id) VALUES ('x')"})); e == nil || errors.Is(e, ErrFatal) {
		t.Fatal(e)
	}
	if log.count() != 0 {
		t.Fatalf("reported %v", log.errs)
	}
}

// A damaged database found while serving is reported through OnFatal and
// matches ErrFatal, from read snapshots and write transactions alike.
func TestCorruptionAtRuntimeIsFatal(t *testing.T) {
	for _, path := range []string{"read", "write"} {
		t.Run(path, func(t *testing.T) {
			db := openTemp(t)
			var log fatalLog
			db.OnFatal(log.add)
			mustApply(t, db, at(1), newProject(id(1), "feat/one", "", at(1)))
			corruptSchema(t, db)
			ctx := context.Background()
			var e error
			if path == "read" {
				var r *ReadTx
				if r, e = db.Read(ctx); e == nil {
					_, e = r.Project(ctx, id(1))
					r.Close()
				}
			} else {
				e = write(t, db, func(ctx context.Context, tx *WriteTx) error {
					_, e := tx.Project(ctx, id(1))
					return e
				})
			}
			if !errors.Is(e, ErrFatal) {
				t.Fatalf("got %v", e)
			}
			if log.count() == 0 {
				t.Fatal("not reported")
			}
			for _, r := range log.errs {
				if !errors.Is(r, ErrFatal) {
					t.Fatalf("reported %v", r)
				}
			}
		})
	}
}

// A file that is not a database fails to open with ErrFatal, which lit-server
// answers with degraded mode instead of exiting.
func TestOpenOfAFileThatIsNotADatabaseIsFatal(t *testing.T) {
	dir := t.TempDir()
	garbage := make([]byte, 8192)
	for i := range garbage {
		garbage[i] = byte(i*7 + 3)
	}
	if e := os.WriteFile(filepath.Join(dir, DatabaseFile), garbage, 0o600); e != nil {
		t.Fatal(e)
	}
	db, e := Open(dir)
	if e == nil {
		db.Close()
		t.Fatal("opened garbage")
	}
	if !errors.Is(e, ErrFatal) {
		t.Fatalf("got %v", e)
	}
	// Ownership is released: opening again meets the damage, not an owner.
	if _, e = Open(dir); !errors.Is(e, ErrFatal) || strings.Contains(e.Error(), "already owned") {
		t.Fatalf("again: %v", e)
	}
}
