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
//  2. the grep columns of every record table (see grepColumns), which are
//     derived from the rows' text in Go: stale ones are recomputed, and logf
//     reports each table with stale rows; the search tables follow;
//  3. PRAGMA integrity_check and PRAGMA foreign_key_check of the whole
//     database.
//
// Problems are returned as an *IntegrityError (ErrIntegrity); other errors
// mean the checks could not run. lit-server runs CheckIntegrity at every start
// before serving, and serves degraded mode when it reports ErrIntegrity;
// Verify runs it too. Restoring a backup snapshot relies on it.
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
	var problems []string
	for _, name := range names {
		p, e := checkSearchTable(ctx, w, name, logf)
		if e != nil {
			return e
		}
		problems = append(problems, p...)
	}
	for _, t := range grepColumns {
		p, e := checkFolded(ctx, w, t, logf)
		if e != nil {
			return e
		}
		problems = append(problems, p...)
	}
	p, e := checkTables(ctx, w)
	if e != nil {
		return e
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
// problem. Driver errors are expected here and stay unreported to OnFatal.
func checkSearchTable(ctx context.Context, w *Writer, name string, logf func(string, ...any)) ([]string, error) {
	q := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	check := "INSERT INTO " + q + "(" + q + ", rank) VALUES ('integrity-check', 1)"
	failed, e := inTransaction(ctx, w, false, check)
	if failed == nil || e != nil {
		return nil, e
	}
	again, e := inTransaction(ctx, w, true, "INSERT INTO "+q+"("+q+") VALUES ('rebuild')", check)
	if e != nil {
		return nil, e
	}
	if again != nil {
		return []string{fmt.Sprintf("search index %s failed its integrity check (%v) and could not be rebuilt: %v", name, failed, again)}, nil
	}
	logf("lit-server: search index %s failed its integrity check (%v); rebuilt it", name, failed)
	return nil, nil
}

// checkFolded recomputes the grep columns of table t in one write
// transaction and commits it when rows were stale. A damaged table that
// cannot be read is a problem; PRAGMA integrity_check reports the details.
func checkFolded(ctx context.Context, w *Writer, t grepTable, logf func(string, ...any)) ([]string, error) {
	tx, e := w.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer func() { _ = tx.Rollback() }()
	n, e := refold(ctx, tx.tx, t)
	if e = classify(e); errors.Is(e, ErrFatal) {
		return []string{fmt.Sprintf("folded text of %s could not be checked: %v", t.name, e)}, nil
	}
	if e != nil || n == 0 {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	logf("lit-server: folded text of %d %s rows was stale; rewrote it", n, t.name)
	return nil, nil
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

// checkTables runs PRAGMA integrity_check and PRAGMA foreign_key_check. A
// damaged database that cannot even run them is a problem too.
func checkTables(ctx context.Context, w *Writer) ([]string, error) {
	tx, e := w.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer func() { _ = tx.Rollback() }()
	var problems []string
	add := func(format string, args ...any) {
		if len(problems) < maxProblems {
			problems = append(problems, fmt.Sprintf(format, args...))
		}
	}
	scan := func(pragma string, f func(*sql.Rows) error) error {
		rows, e := tx.tx.QueryContext(ctx, "PRAGMA "+pragma)
		if e == nil {
			defer func() { _ = rows.Close() }()
			for rows.Next() && e == nil {
				e = f(rows)
			}
			if e == nil {
				e = rows.Err()
			}
		}
		if e = classify(e); errors.Is(e, ErrFatal) {
			add("%s: %v", pragma, e)
			return nil
		}
		return e
	}
	e = scan("integrity_check", func(r *sql.Rows) error {
		var msg string
		// "*** in database main ***" heads the problems of each schema.
		if e := r.Scan(&msg); e != nil || msg == "ok" || strings.HasPrefix(msg, "*** in database ") {
			return e
		}
		add("integrity_check: %s", msg)
		return nil
	})
	if e != nil {
		return nil, e
	}
	e = scan("foreign_key_check", func(r *sql.Rows) error {
		var table, parent string
		var row sql.NullInt64
		var fk int
		if e := r.Scan(&table, &row, &parent, &fk); e != nil {
			return e
		}
		add("foreign_key_check: %s row %d refers to a missing %s row (foreign key %d)", table, row.Int64, parent, fk)
		return nil
	})
	return problems, e
}
