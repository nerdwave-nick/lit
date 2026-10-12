package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
)

// readChecked is what the startup checks that only read found.
type readChecked struct {
	// tables are the problems of PRAGMA integrity_check and
	// foreign_key_check, at most maxProblems.
	tables []string
	// stale reports grep_text or rank_text rows that are missing, stale or
	// orphaned, or a record table that could not be compared: the writer
	// then runs checkFolded, which rewrites them or reports the problem.
	stale bool
	// err is the first failure of a check that could not run on a read
	// snapshot; the writer then repeats every read check.
	err error
}

// readCheck runs one read-only startup check in a read snapshot.
type readCheck func(ctx context.Context, tx *sql.Tx) (tables []string, stale bool, e error)

// startReadChecks starts the startup checks that only read, each on its own
// read snapshot, and returns the function that waits for them all. They are
// PRAGMA integrity_check, PRAGMA foreign_key_check and the comparison of
// grep_text and rank_text with the records of each record table, which
// writes nothing when the text is current, as it is unless a writer failed
// to maintain it. CheckIntegrity holds the write lock while they run, and
// its FTS5 checks only roll back or, on a rebuild, change the search tables,
// so every snapshot sees the records and derived text the writer sees.
//
// The snapshots come from connections of their own, closed afterwards, at
// most as many as read snapshots are allowed: a pooled reader keeps the FTS5
// index structure it read last, which PRAGMA integrity_check would read as
// corruption after a writer changed the index (it does not reload it as a
// search does). Driver errors are expected here and stay unreported to
// OnFatal.
func (db *DB) startReadChecks(ctx context.Context) func() readChecked {
	var checks []readCheck
	for _, c := range tableChecks {
		checks = append(checks, func(ctx context.Context, tx *sql.Tx) ([]string, bool, error) {
			p, e := runTableCheck(ctx, tx, c)
			return p, false, e
		})
	}
	for _, src := range grepSources {
		checks = append(checks, staleCheck(func(ctx context.Context, tx *sql.Tx) (int, error) { return countStaleFolded(ctx, tx, src) }))
	}
	for _, src := range rankSources {
		checks = append(checks, staleCheck(func(ctx context.Context, tx *sql.Tx) (int, error) { return countStaleRank(ctx, tx, src) }))
	}
	conns, e := sql.Open("sqlite", dsn(filepath.Join(db.dir, DatabaseFile), true))
	if e != nil {
		return func() readChecked { return readChecked{err: classify(e)} }
	}
	conns.SetMaxOpenConns(db.readers.Stats().MaxOpenConnections)
	results := make([]readChecked, len(checks))
	var wg sync.WaitGroup
	for i, check := range checks {
		wg.Go(func() {
			tx, e := conns.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
			if e != nil {
				results[i].err = classify(e)
				return
			}
			defer func() { _ = tx.Rollback() }()
			r := &results[i]
			r.tables, r.stale, r.err = check(ctx, tx)
		})
	}
	return func() readChecked {
		wg.Wait()
		var all readChecked
		if e := conns.Close(); e != nil {
			all.err = classify(e)
		}
		for _, r := range results {
			all.tables = append(all.tables, r.tables...)
			all.stale = all.stale || r.stale
			if all.err == nil {
				all.err = r.err
			}
		}
		all.tables = all.tables[:min(len(all.tables), maxProblems)]
		return all
	}
}

// staleCheck is the read check of one record table's derived text: count
// returns the rows that need a rewrite. A table that cannot be read is stale,
// so that checkFolded reports it as it would in a sequential run.
func staleCheck(count func(ctx context.Context, tx *sql.Tx) (int, error)) readCheck {
	return func(ctx context.Context, tx *sql.Tx) ([]string, bool, error) {
		n, e := count(ctx, tx)
		if e = classify(e); errors.Is(e, ErrFatal) {
			return nil, true, nil
		}
		return nil, n > 0, e
	}
}

// countStaleFolded counts the grep_text rows of src that refold would write
// or delete, without writing.
func countStaleFolded(ctx context.Context, tx sqlTx, src grepSource) (int, error) {
	stale := 0
	for after := ""; ; {
		rows, last, e := staleText(ctx, tx, refoldRead(src), after)
		if e != nil {
			return stale, e
		}
		stale += len(rows)
		if last == "" {
			break
		}
		after = last
	}
	n, e := countRows(ctx, tx, "SELECT count(*) FROM grep_text WHERE "+orphanedText(src.table, "grep_text"), src.table)
	return stale + n, e
}

// countStaleRank counts the rank_text rows of src that resyncRank would
// write or delete, without writing.
func countStaleRank(ctx context.Context, tx sqlTx, src rankSource) (int, error) {
	stale, e := countRows(ctx, tx, staleRankRows(src, "SELECT count(*)"))
	if e != nil {
		return stale, e
	}
	n, e := countRows(ctx, tx, "SELECT count(*) FROM rank_text WHERE "+orphanedText(src.table, "rank_text"), src.table)
	return stale + n, e
}

func countRows(ctx context.Context, tx sqlTx, query string, args ...any) (int, error) {
	rows, e := tx.QueryContext(ctx, query, args...)
	if e != nil {
		return 0, e
	}
	defer func() { _ = rows.Close() }()
	n := 0
	if rows.Next() {
		e = rows.Scan(&n)
	}
	if e == nil {
		e = rows.Err()
	}
	return n, e
}

// orphanedText is the condition on a derived text table's rows of owner type
// ? (the record table) that have no record left.
func orphanedText(table, text string) string {
	return "owner_type = ? AND NOT EXISTS (SELECT 1 FROM " + table + " r WHERE r.id = " + text + ".owner_id)"
}
