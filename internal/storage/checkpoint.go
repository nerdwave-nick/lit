package storage

import (
	"context"
	"database/sql"
	"errors"

	"modernc.org/sqlite"
)

// checkpointPages is how many pages written to the WAL start a checkpoint:
// SQLite's default auto-checkpoint threshold.
const checkpointPages = 1000

// checkpointer copies the WAL into the database file on a connection of its
// own, outside the write lock. The writer connection never checkpoints
// (wal_autocheckpoint=0): SQLite would otherwise run the checkpoint, with its
// fsyncs of the WAL and the database file, inside the commit that crossed the
// threshold, while that commit holds the write lock and every other write
// waits. The threshold is SQLite's, so the commits a power loss can lose
// (docs/storage-format.md) are the same: those since the last checkpoint.
type checkpointer struct {
	pool *sql.DB // one query-only connection; a checkpoint is not a query
	wake chan struct{}
	done chan struct{}
	// pending counts the pages written since the last checkpoint was
	// started. Only the write lock's holder touches it.
	pending int
}

// startCheckpointer opens the checkpointer's connection to the database at
// path and starts it.
func (db *DB) startCheckpointer(path string) error {
	pool, e := sql.Open("sqlite", dsn(path, true))
	if e != nil {
		return e
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	pool.SetConnMaxLifetime(0)
	db.ckpt = &checkpointer{pool: pool, wake: make(chan struct{}, 1), done: make(chan struct{})}
	go db.ckpt.run(db)
	return nil
}

// run checkpoints once per wake-up until wake is closed. A PASSIVE checkpoint
// copies what no open read snapshot still needs and never waits for one; a
// later checkpoint, or the final one when the database closes, copies the
// rest. A fatal error is reported like any other (see OnFatal); any other
// failure leaves the WAL for the next checkpoint.
func (c *checkpointer) run(db *DB) {
	defer close(c.done)
	for range c.wake {
		_, e := c.pool.Exec("PRAGMA wal_checkpoint(PASSIVE)")
		_ = db.report(e)
	}
}

// noteWrites adds the pages the writer connection wrote to the WAL since the
// last call, and wakes the checkpointer once they reach checkpointPages. The
// caller holds the write lock and no write transaction is open.
func (db *DB) noteWrites() {
	if db.ckpt == nil {
		return
	}
	n, e := writtenPages(db.writer)
	if e != nil {
		return // counted again by the next call
	}
	if db.ckpt.pending += n; db.ckpt.pending < checkpointPages {
		return
	}
	db.ckpt.pending = 0
	select {
	case db.ckpt.wake <- struct{}{}:
	default: // a checkpoint is already due
	}
}

// writtenPages reads and resets the count of pages the pool's only
// connection wrote to the WAL.
func writtenPages(pool *sql.DB) (n int, err error) {
	conn, err := pool.Conn(context.Background())
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	err = conn.Raw(func(dc any) error {
		s, ok := dc.(sqlite.DBStatus)
		if !ok {
			return errors.New("the SQLite driver reports no connection status")
		}
		var e error
		n, _, e = s.Status(sqlite.DBStatusCacheWrite, true)
		return e
	})
	return n, err
}

// stop ends the checkpointer and closes its connection; the writer's close
// checkpoints what is left.
func (c *checkpointer) stop() error {
	close(c.wake)
	<-c.done
	return c.pool.Close()
}
