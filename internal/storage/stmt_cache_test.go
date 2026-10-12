package storage

import (
	"context"
	"fmt"
	"testing"
)

// writeOnce runs one write transaction that reads query's single integer.
func writeOnce(t *testing.T, db *DB, query string, args ...any) int {
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
	var v int
	if e = tx.queryRow(ctx, []any{&v}, query, args...); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	return v
}

// A statement a write transaction ran is prepared once the transaction ends
// and is reused, with fresh arguments, by later write transactions.
func TestWriterReusesPreparedStatements(t *testing.T) {
	db := openTemp(t)
	const q = "SELECT ? + 1"
	if v := writeOnce(t, db, q, 1); v != 2 {
		t.Fatalf("first run: %d", v)
	}
	first := db.stmts.stmts[q]
	if first == nil {
		t.Fatal("statement not prepared after its first transaction")
	}
	if v := writeOnce(t, db, q, 41); v != 42 {
		t.Fatalf("cached run: %d", v)
	}
	if db.stmts.stmts[q] != first {
		t.Fatal("statement prepared again")
	}
}

// The cache stops growing at its limit; further statements still run.
func TestWriterStatementCacheIsBounded(t *testing.T) {
	db := openTemp(t)
	for n := range stmtCacheLimit + 20 {
		if v := writeOnce(t, db, fmt.Sprintf("SELECT %d", n)); v != n {
			t.Fatalf("statement %d: %d", n, v)
		}
	}
	if got := len(db.stmts.stmts) + len(db.stmts.seen); got > stmtCacheLimit {
		t.Fatalf("cache holds %d statements", got)
	}
}
