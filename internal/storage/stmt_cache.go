package storage

import (
	"context"
	"database/sql"
	"sync"
)

// stmtCacheLimit bounds the distinct statements the writer keeps prepared.
const stmtCacheLimit = 512

// stmtCache keeps the writer connection's statements prepared across write
// transactions, so a write does not parse its SQL (and compile the search
// index triggers it fires) again each time. A statement seen for the first
// time runs unprepared and is prepared once the transaction has ended and the
// writer connection is free (prepareSeen): the writer pool has exactly one
// connection, which an open transaction holds.
type stmtCache struct {
	pool  *sql.DB
	mu    sync.Mutex
	stmts map[string]*sql.Stmt
	seen  map[string]bool
}

func newStmtCache(pool *sql.DB) *stmtCache {
	return &stmtCache{pool: pool, stmts: map[string]*sql.Stmt{}, seen: map[string]bool{}}
}

// lookup returns the prepared statement for query, or nil after noting the
// query for prepareSeen while the cache has room.
func (c *stmtCache) lookup(query string) *sql.Stmt {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.stmts[query]; ok {
		return s
	}
	if len(c.stmts)+len(c.seen) < stmtCacheLimit {
		c.seen[query] = true
	}
	return nil
}

// prepareSeen prepares the statements noted since the last call. It runs
// while no transaction holds the writer connection; a statement that fails to
// prepare is simply not cached.
func (c *stmtCache) prepareSeen() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for query := range c.seen {
		if s, e := c.pool.PrepareContext(context.Background(), query); e == nil {
			c.stmts[query] = s
		}
		delete(c.seen, query)
	}
}

// close closes the prepared statements before their pool closes.
func (c *stmtCache) close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for query, s := range c.stmts {
		_ = s.Close()
		delete(c.stmts, query)
	}
	c.seen = map[string]bool{}
}

// stmt returns query's cached statement bound to the transaction, or nil.
func (q queries) stmt(ctx context.Context, query string) *sql.Stmt {
	s := q.stmts.lookup(query)
	if s == nil {
		return nil
	}
	return q.tx.StmtContext(ctx, s)
}

// query runs a statement returning rows.
func (q queries) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if s := q.stmt(ctx, query); s != nil {
		return s.QueryContext(ctx, args...)
	}
	return q.tx.QueryContext(ctx, query, args...)
}

// row runs a statement returning at most one row.
func (q queries) row(ctx context.Context, query string, args ...any) *sql.Row {
	if s := q.stmt(ctx, query); s != nil {
		return s.QueryRowContext(ctx, args...)
	}
	return q.tx.QueryRowContext(ctx, query, args...)
}
