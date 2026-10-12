package storage

import (
	"context"
	"database/sql"
	"errors"
)

// searchIndexes are the FTS5 tables whose segments the merger merges.
var searchIndexes = []string{"grep_index", "rank_index"}

// mergePages bounds the pages one merge step writes per search index, so a
// write that arrives during a step waits for at most that much work.
const mergePages = 64

// FTS5 writes each commit's new index entries as a new b-tree segment and, by
// default, merges segments inside the commits that cross its work units
// (automerge): a few commits pay for merges of hundreds of pages while they
// hold the write lock. The search indexes set automerge to 0 instead, and the
// merger merges them in small steps while no write holds the write lock,
// starting after every mergeEvery commits. A step merges a level of the same size
// automerge would (usermerge, 4 segments), so the indexes end up with as
// many segments as before; FTS5 still merges a level of 16 segments inside a
// commit (crisismerge) should the merger fall behind. Merging changes no
// indexed content: the indexes stay consistent with their records.
type merger struct {
	wake chan struct{}
	done chan struct{}
	// commits counts the commits since the merger was last woken. Only the
	// write lock's holder touches it.
	commits int
}

// mergeEvery is how many commits wake the merger: enough new segments for a
// level FTS5 merges (usermerge, 4).
const mergeEvery = 4

// committed counts n commits and reports whether the merger is due.
func (m *merger) committed(n int) bool {
	if m.commits += n; m.commits < mergeEvery {
		return false
	}
	m.commits = 0
	return true
}

// configureMerges turns FTS5's merges inside commits off in the search
// indexes the schema has, unless they are.
func configureMerges(ctx context.Context, pool *sql.DB) error {
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
		if automerge.Valid && automerge.Int64 == 0 {
			continue
		}
		if _, e = pool.ExecContext(ctx, "INSERT INTO "+table+" ("+table+", rank) VALUES ('automerge', 0)"); e != nil {
			return e
		}
	}
	return nil
}

func (db *DB) startMerger() {
	db.merge = &merger{wake: make(chan struct{}, 1), done: make(chan struct{})}
	go db.merge.run(db)
}

// run merges after each wake-up until a step finds nothing to merge or a
// write holds the lock (later commits wake the merger again), until wake is
// closed.
func (m *merger) run(db *DB) {
	defer close(m.done)
	for range m.wake {
		for db.mergeStep() {
		}
	}
}

// woken wakes the merger; the caller released the write lock.
func (m *merger) woken() {
	select {
	case m.wake <- struct{}{}:
	default: // already due
	}
}

// mergeStep merges up to mergePages pages of each search index in one write
// transaction, if no write holds the write lock. It reports whether a step
// merged anything, so that another may follow. A failure is reported like any
// other (see OnFatal) and ends the merging until the next commit.
func (db *DB) mergeStep() bool {
	w, ok := db.tryLockWrites()
	if !ok {
		return false
	}
	w.background = true
	defer w.Unlock()
	ctx := context.Background()
	tx, e := w.Begin(ctx)
	if e != nil {
		return false
	}
	merged := false
	for _, table := range searchIndexes {
		var before, after int64
		if e = tx.queryRow(ctx, []any{&before}, "SELECT total_changes()"); e != nil {
			return false
		}
		if _, e = tx.exec(ctx, "INSERT INTO "+table+" ("+table+", rank) VALUES ('merge', ?)", mergePages); e != nil {
			return false
		}
		if e = tx.queryRow(ctx, []any{&after}, "SELECT total_changes()"); e != nil {
			return false
		}
		// The command itself counts as one change; a merge writes more.
		merged = merged || after-before > 1
	}
	if !merged {
		return false // Unlock rolls the empty transaction back
	}
	return tx.Commit() == nil
}

// stop ends the merger, after the step it may be running.
func (m *merger) stop() {
	close(m.wake)
	<-m.done
}
