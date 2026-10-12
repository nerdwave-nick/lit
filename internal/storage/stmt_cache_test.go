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

// A statement a read snapshot ran is prepared once the snapshot closes and
// is reused by later snapshots.
func TestReadersReusePreparedStatements(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	const q = "SELECT ? * 2"
	for n, want := range []int{2, 4} {
		r, e := db.Read(ctx)
		if e != nil {
			t.Fatal(e)
		}
		var v int
		if e = r.queryRow(ctx, []any{&v}, q, n+1); e != nil || v != want {
			t.Fatalf("run %d: %d %v", n, v, e)
		}
		if e = r.Close(); e != nil {
			t.Fatal(e)
		}
		if db.readStmts.stmts[q] == nil {
			t.Fatalf("run %d: statement not prepared", n)
		}
	}
}

// Cached statements hold no read transaction once their snapshot closed, so
// a checkpoint can still copy and truncate the whole WAL.
func TestCachedStatementsDoNotBlockCheckpoints(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	for range 3 {
		r, e := db.Read(ctx)
		if e != nil {
			t.Fatal(e)
		}
		var v int
		if e = r.queryRow(ctx, []any{&v}, "SELECT count(*) FROM service WHERE singleton = ?", 1); e != nil {
			t.Fatal(e)
		}
		r.Close()
		writeOnce(t, db, "SELECT count(*) FROM service WHERE singleton = ?", 1)
	}
	w, e := db.LockWrites(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Unlock()
	tx, e := w.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.exec(ctx, "UPDATE service SET schema_version = schema_version"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	var busy, log, done int
	if e = db.writer.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &done); e != nil || busy != 0 {
		t.Fatalf("checkpoint busy=%d log=%d done=%d: %v", busy, log, done, e)
	}
}
