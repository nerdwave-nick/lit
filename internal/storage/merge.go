package storage

import (
	"context"
	"database/sql"
	"errors"
)

// searchIndexes are the FTS5 tables of lit grep and lit search.
var searchIndexes = []string{"grep_index", "rank_index"}

// defaultAutomerge is FTS5's automerge setting: segments are merged inside
// the commits that cross its work units, a level of 4 segments at a time.
const defaultAutomerge = 4

// restoreMerges gives a search index whose automerge was turned off (0) by an
// earlier build, which merged segments in a background merger instead, FTS5's
// default back. That merger made writes queue behind its steps and left the
// indexes less merged, which slowed searches (ticket 2aaa11f1).
func restoreMerges(ctx context.Context, pool *sql.DB) error {
	for _, table := range searchIndexes {
		var exists bool
		if e := pool.QueryRowContext(ctx, "SELECT count(*) > 0 FROM sqlite_schema WHERE type = 'table' AND name = ?", table+"_config").Scan(&exists); e != nil {
			return e
		}
		if !exists {
			continue
		}
		var automerge sql.NullInt64
		e := pool.QueryRowContext(ctx, "SELECT v FROM "+table+"_config WHERE k = 'automerge'").Scan(&automerge)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if !automerge.Valid || automerge.Int64 != 0 {
			continue
		}
		if _, e = pool.ExecContext(ctx, "INSERT INTO "+table+" ("+table+", rank) VALUES ('automerge', ?)", defaultAutomerge); e != nil {
			return e
		}
	}
	return nil
}
