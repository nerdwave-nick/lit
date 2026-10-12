package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// errNotHeld reports a Writer used after Unlock.
var errNotHeld = errors.New("storage write lock is not held")

// queries runs statements inside one transaction. Typed reads shared by read
// snapshots and write transactions are methods on queries; writes are methods
// on WriteTx.
type queries struct {
	tx *sql.Tx
	db *DB
	// stmts are the writer's prepared statements; read snapshots have none.
	stmts *stmtCache
}

// classify types a driver error and reports a fatal one to the owning DB
// (see OnFatal); snapshots of a ReadOnlyDB have no owner to report to.
func (q queries) classify(e error) error {
	if q.db == nil {
		return classify(e)
	}
	return q.db.report(e)
}

func (q queries) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	var r sql.Result
	var e error
	if s := q.stmt(ctx, query); s != nil {
		r, e = s.ExecContext(ctx, args...)
	} else {
		r, e = q.tx.ExecContext(ctx, query, args...)
	}
	return r, q.classify(e)
}

// queryRow scans the single result row into dest; sql.ErrNoRows is kept.
func (q queries) queryRow(ctx context.Context, dest []any, query string, args ...any) error {
	return q.classify(q.row(ctx, query, args...).Scan(dest...))
}

// Writer proves that its holder owns the process write lock. Only a Writer
// begins a write transaction, so every write is serialized by construction.
// It belongs to one goroutine and is invalid after Unlock.
type Writer struct {
	db   *DB
	now  time.Time
	held bool
	open *WriteTx
}

// LockWrites waits for the process write lock, or until ctx ends. The
// returned Writer's time is sampled from the clock after the lock is taken.
// Release it with Unlock, normally deferred.
func (db *DB) LockWrites(ctx context.Context) (*Writer, error) {
	select {
	case db.writeLock <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &Writer{db: db, now: db.clock.Now(), held: true}, nil
}

// Now is the request time of this write.
func (w *Writer) Now() time.Time { return w.now }

// Begin starts the write transaction with BEGIN IMMEDIATE, taking SQLite's
// write lock at once. One transaction may be open per Writer at a time.
func (w *Writer) Begin(ctx context.Context) (*WriteTx, error) {
	if !w.held {
		return nil, errNotHeld
	}
	if w.open != nil {
		return nil, errors.New("a write transaction is already open")
	}
	tx, e := w.db.writer.BeginTx(ctx, nil)
	if e != nil {
		return nil, w.db.report(e)
	}
	w.open = &WriteTx{queries: queries{tx, w.db, w.db.stmts}, w: w}
	return w.open, nil
}

// Unlock rolls back a transaction left open and releases the write lock.
// Further calls do nothing.
func (w *Writer) Unlock() {
	if !w.held {
		return
	}
	if w.open != nil {
		_ = w.open.Rollback()
	}
	w.db.stmts.prepareSeen()
	w.held = false
	<-w.db.writeLock
}

// WriteTx is one BEGIN IMMEDIATE transaction: it reads, validates and writes.
// Commit or roll it back; dry runs roll back.
type WriteTx struct {
	queries
	w *Writer
}

// Now is the request time of the Writer that began this transaction.
func (t *WriteTx) Now() time.Time { return t.w.now }

// Commit makes the transaction durable.
func (t *WriteTx) Commit() error { return t.finish(t.tx.Commit) }

// Rollback discards the transaction.
func (t *WriteTx) Rollback() error { return t.finish(t.tx.Rollback) }

func (t *WriteTx) finish(f func() error) error {
	if t.w.open == t {
		t.w.open = nil
	}
	return t.classify(f())
}

// ReadTx is one WAL read snapshot for a whole handler: every query sees the
// database as of the moment Read returned, unaffected by later commits, and
// never waits for writers. Close it when the handler is done.
type ReadTx struct {
	queries
	now time.Time
}

// Read samples the clock, then begins a read snapshot. Read waits while the
// maximum number of snapshots is open, until ctx ends.
func (db *DB) Read(ctx context.Context) (*ReadTx, error) {
	now := db.clock.Now()
	tx, e := db.readers.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, db.report(e)
	}
	r := &ReadTx{queries{tx, db, nil}, now}
	// A deferred transaction takes its snapshot at the first read; take it now.
	var v int
	if e = r.queryRow(ctx, []any{&v}, "SELECT schema_version FROM service"); e != nil {
		_ = tx.Rollback()
		return nil, e
	}
	return r, nil
}

// Now is the request time, sampled before the snapshot began.
func (r *ReadTx) Now() time.Time { return r.now }

// Close ends the snapshot.
func (r *ReadTx) Close() error { return r.classify(r.tx.Rollback()) }
