package storage

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// stmtCacheLimit bounds the distinct statements a pool keeps prepared.
const stmtCacheLimit = 512

// prepareWait bounds how long prepareSeen waits for a free connection of the
// pool; a statement not prepared in time stays noted for the next call.
const prepareWait = 5 * time.Millisecond

// stmtCache keeps a pool's statements prepared across transactions, so a
// request does not parse its SQL (and, for a write, compile the search index
// triggers it fires) again each time. A statement seen for the first time
// runs unprepared and is prepared once its transaction has ended and freed its
// connection (prepareSeen): the writer pool has exactly one connection, which
// an open transaction holds, and reader connections are capped.
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
// after a transaction ended, holding no connection; a statement that fails to
// prepare is not cached, one that found no free connection in time stays
// noted.
func (c *stmtCache) prepareSeen() {
	if c == nil {
		return
	}
	c.mu.Lock()
	queries := make([]string, 0, len(c.seen))
	for query := range c.seen {
		queries = append(queries, query)
	}
	c.mu.Unlock()
	// Prepare without holding mu: a transaction waiting in lookup must not
	// keep the connection this waits for.
	for _, query := range queries {
		ctx, cancel := context.WithTimeout(context.Background(), prepareWait)
		s, e := c.pool.PrepareContext(ctx, query)
		cancel()
		c.mu.Lock()
		switch {
		case errors.Is(e, context.DeadlineExceeded): // no free connection: retry later
		case e != nil:
			delete(c.seen, query)
		case c.stmts[query] != nil || !c.seen[query]: // prepared concurrently or closed
			_ = s.Close()
		default:
			c.stmts[query] = s
			delete(c.seen, query)
		}
		c.mu.Unlock()
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
