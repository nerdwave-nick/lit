package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrIntegrity matches (with errors.Is) a failed startup check: the database
// is damaged or inconsistent and needs an operator.
var ErrIntegrity = errors.New("database integrity check failed")

// IntegrityError lists what a failed startup check found.
type IntegrityError struct{ Problems []string }

func (e *IntegrityError) Error() string {
	const shown = 3
	msg := strings.Join(e.Problems[:min(shown, len(e.Problems))], "; ")
	if n := len(e.Problems) - shown; n > 0 {
		msg += fmt.Sprintf("; and %d more", n)
	}
	return ErrIntegrity.Error() + ": " + msg
}

func (e *IntegrityError) Is(target error) bool { return target == ErrIntegrity }

// maxProblems bounds the problems one check reports, like integrity_check's
// own default.
const maxProblems = 100

// CheckIntegrity runs the startup checks, holding the write lock:
//
//  1. the FTS5 integrity-check of every FTS5 table, against its content
//     table too. Search tables are derived from rows of the same database, so
//     a table that fails is rebuilt, and logf reports it with one line; a
//     table that cannot be rebuilt is a problem;
//  2. the folded text of lit grep (grep_text), derived from the records in
//     Go, and the original text of lit search (rank_text): missing, stale and
//     orphaned rows are rewritten, and logf reports each record type with
//     changes; the search indexes follow;
//  3. PRAGMA integrity_check and PRAGMA foreign_key_check of the whole
//     database.
//
// The checks that only read (the pragmas and the comparison of the derived
// text with the records) run concurrently on read snapshots while the writer
// runs the FTS5 checks, which are write statements; see startReadChecks. When
// anything was repaired, or a read check could not run, the writer repeats
// steps 2 and 3 in order on the repaired database, as a sequential run would.
//
// Problems are returned as an *IntegrityError (ErrIntegrity); other errors
// mean the checks could not run. lit-server runs CheckIntegrity, its full
// integrity scan, before serving at a start after an unclean shutdown (see
// CleanMarker and PriorShutdown), and serves degraded mode when it reports
// ErrIntegrity; Verify runs it too. Restoring a backup snapshot relies on it.
func (db *DB) CheckIntegrity(ctx context.Context, logf func(format string, args ...any)) error {
	w, e := db.LockWrites(ctx)
	if e != nil {
		return e
	}
	defer w.Unlock()
	names, e := searchTables(ctx, w)
	if e != nil {
		return e
	}
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	reads := db.startReadChecks(readCtx)
	var problems []string
	repaired := false
	for _, name := range names {
		p, rebuilt, e := checkSearchTable(ctx, w, name, logf)
		if e != nil {
			cancel()
			reads()
			return e
		}
		problems = append(problems, p...)
		repaired = repaired || rebuilt
	}
	r := reads()
	if r.stale || r.err != nil {
		p, changed, e := checkFolded(ctx, w, logf)
		if e != nil {
			return e
		}
		problems = append(problems, p...)
		repaired = repaired || changed
	}
	p := r.tables
	if repaired || r.err != nil {
		if p, e = checkTables(ctx, w); e != nil {
			return e
		}
	}
	if problems = append(problems, p...); len(problems) > 0 {
		return &IntegrityError{problems}
	}
	return nil
}

// searchTables names every FTS5 table, in name order.
func searchTables(ctx context.Context, w *Writer) ([]string, error) {
	tx, e := w.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer func() { _ = tx.Rollback() }()
	var names []string
	e = tx.each(ctx, `SELECT name FROM sqlite_schema WHERE type = 'table' AND sql LIKE 'CREATE VIRTUAL TABLE%USING fts5%' ORDER BY name`, nil, func(s rowScanner) error {
		var n string
		e := s.Scan(&n)
		names = append(names, n)
		return e
	})
	return names, e
}

// checkSearchTable runs one FTS5 table's integrity-check and rebuilds the
// table when it fails. A table that still fails, or cannot be rebuilt, is a
// problem. rebuilt reports a rebuild attempt, which writes. Driver errors are
// expected here and stay unreported to OnFatal.
func checkSearchTable(ctx context.Context, w *Writer, name string, logf func(string, ...any)) (problems []string, rebuilt bool, e error) {
	q := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	check := "INSERT INTO " + q + "(" + q + ", rank) VALUES ('integrity-check', 1)"
	failed, e := inTransaction(ctx, w, false, check)
	if failed == nil || e != nil {
		return nil, false, e
	}
	again, e := inTransaction(ctx, w, true, "INSERT INTO "+q+"("+q+") VALUES ('rebuild')", check)
	if e != nil {
		return nil, true, e
	}
	if again != nil {
		return []string{fmt.Sprintf("search index %s failed its integrity check (%v) and could not be rebuilt: %v", name, failed, again)}, true, nil
	}
	logf("lit-server: search index %s failed its integrity check (%v); rebuilt it", name, failed)
	return nil, true, nil
}

// checkFolded recomputes the grep_text and rank_text rows of every record in
// one write transaction and commits it when rows changed. A damaged table that
// cannot be read is a problem; PRAGMA integrity_check reports the details.
// changed reports a commit, or a table that could not be checked.
func checkFolded(ctx context.Context, w *Writer, logf func(string, ...any)) (problems []string, changed bool, e error) {
	tx, e := w.Begin(ctx)
	if e != nil {
		return nil, false, e
	}
	defer func() { _ = tx.Rollback() }()
	type pass struct {
		what, table string
		run         func() (int, error)
	}
	var passes []pass
	for _, src := range grepSources {
		passes = append(passes, pass{"folded text", src.table, func() (int, error) { return refold(ctx, tx.tx, src) }})
	}
	for _, src := range rankSources {
		passes = append(passes, pass{"ranked search text", src.table, func() (int, error) { return resyncRank(ctx, tx.tx, src) }})
	}
	rows := make([]int, len(passes))
	total := 0
	for i, p := range passes {
		rows[i], e = p.run()
		if e = classify(e); errors.Is(e, ErrFatal) {
			return []string{fmt.Sprintf("%s of %s could not be checked: %v", p.what, p.table, e)}, true, nil
		}
		if e != nil {
			return nil, false, e
		}
		total += rows[i]
	}
	if total == 0 {
		return nil, false, nil
	}
	if e = tx.Commit(); e != nil {
		return nil, false, e
	}
	for i, p := range passes {
		if rows[i] > 0 {
			logf("lit-server: %s of %d %s was stale; rewrote it", p.what, rows[i], p.table)
		}
	}
	return nil, true, nil
}

// inTransaction runs statements in one write transaction, committed only when
// commit is set and every statement succeeds. failed is the first statement
// failure; e is an error beyond the statements (context, transaction).
func inTransaction(ctx context.Context, w *Writer, commit bool, statements ...string) (failed, e error) {
	tx, e := w.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer func() { _ = tx.Rollback() }()
	for _, s := range statements {
		if _, failed = tx.tx.ExecContext(ctx, s); failed != nil {
			return failed, ctx.Err()
		}
	}
	if commit {
		return nil, tx.Commit()
	}
	return nil, nil
}

// checkTables runs PRAGMA integrity_check and PRAGMA foreign_key_check in a
// write transaction. A damaged database that cannot even run them is a
// problem too.
func checkTables(ctx context.Context, w *Writer) ([]string, error) {
	tx, e := w.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer func() { _ = tx.Rollback() }()
	var problems []string
	for _, c := range tableChecks {
		p, e := runTableCheck(ctx, tx.tx, c)
		if e != nil {
			return nil, e
		}
		problems = append(problems, p...)
	}
	return problems[:min(len(problems), maxProblems)], nil
}

// tableCheck is one pragma of checkTables and the reading of its rows: a
// problem, or "" for none.
type tableCheck struct {
	pragma string
	row    func(*sql.Rows) (string, error)
}

var tableChecks = []tableCheck{
	{"integrity_check", func(r *sql.Rows) (string, error) {
		var msg string
		// "*** in database main ***" heads the problems of each schema.
		if e := r.Scan(&msg); e != nil || msg == "ok" || strings.HasPrefix(msg, "*** in database ") {
			return "", e
		}
		return "integrity_check: " + msg, nil
	}},
	{"foreign_key_check", func(r *sql.Rows) (string, error) {
		var table, parent string
		var row sql.NullInt64
		var fk int
		if e := r.Scan(&table, &row, &parent, &fk); e != nil {
			return "", e
		}
		return fmt.Sprintf("foreign_key_check: %s row %d refers to a missing %s row (foreign key %d)", table, row.Int64, parent, fk), nil
	}},
}

// runTableCheck runs one pragma of checkTables in tx, a read snapshot or a
// write transaction, and returns at most maxProblems problems. A damaged
// database that cannot run it is a problem too.
func runTableCheck(ctx context.Context, tx *sql.Tx, c tableCheck) ([]string, error) {
	var problems []string
	rows, e := tx.QueryContext(ctx, "PRAGMA "+c.pragma)
	if e == nil {
		defer func() { _ = rows.Close() }()
		for rows.Next() && e == nil {
			var p string
			if p, e = c.row(rows); p != "" && len(problems) < maxProblems {
				problems = append(problems, p)
			}
		}
		if e == nil {
			e = rows.Err()
		}
	}
	if e = classify(e); errors.Is(e, ErrFatal) {
		return append(problems, fmt.Sprintf("%s: %v", c.pragma, e)), nil
	}
	return problems, e
}
