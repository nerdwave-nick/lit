package storage

import (
	"context"
	"database/sql"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/textfold"
)

// grepFold is a field's stored grep column: textfold.Grep of its text, with
// NUL as U+FFFD, because GLOB stops at NUL. Both are mapped scalar by scalar,
// so a substring of the text still maps to a substring of the column; lit
// grep confirms every candidate on the original text.
func grepFold(s string) string { return strings.ReplaceAll(textfold.Grep(s), "\x00", "�") }

// foldedTable lists a record table's folded columns, each computed in Go
// from a source column.
type foldedTable struct {
	name    string
	columns []foldedColumn
}

type foldedColumn struct {
	source, column string
	fold           func(string) string
}

// foldedTables are every folded column: the grep columns that the trigram
// search tables index, and the folded bodies that the discovery text filter
// reads. Title keys are excluded: they are unique keys, not derived data.
var foldedTables = []foldedTable{
	{"projects", []foldedColumn{{"description", "grep_description", grepFold}, {"description", "folded_description", textfold.Fold}}},
	{"issues", []foldedColumn{{"title", "grep_title", grepFold}, {"body", "grep_body", grepFold}, {"body", "folded_body", textfold.Fold}}},
	{"comments", []foldedColumn{{"body", "grep_body", grepFold}, {"body", "folded_body", textfold.Fold}}},
	{"milestones", []foldedColumn{{"title", "grep_title", grepFold}, {"body", "grep_body", grepFold}, {"body", "folded_body", textfold.Fold}}},
}

// sqlTx is the part of a transaction that refolding uses.
type sqlTx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// refoldBatch bounds the rows read at once while refolding.
const refoldBatch = 500

// refold recomputes the folded columns of every row of t and rewrites the
// rows where a stored column differs; the search triggers keep the trigram
// tables in step. It returns the number of rewritten rows.
func refold(ctx context.Context, tx sqlTx, t foldedTable) (int, error) {
	var sources, columns, sets []string
	for _, c := range t.columns {
		sources = append(sources, c.source)
		columns = append(columns, c.column)
		sets = append(sets, c.column+" = ?")
	}
	read := "SELECT rowid, " + strings.Join(sources, ", ") + ", " + strings.Join(columns, ", ") + " FROM " + t.name + " WHERE rowid > ? ORDER BY rowid LIMIT ?"
	update := "UPDATE " + t.name + " SET " + strings.Join(sets, ", ") + " WHERE rowid = ?"
	n := len(t.columns)
	rewritten := 0
	for after := int64(-1 << 63); ; {
		stale, last, e := staleRows(ctx, tx, read, after, t)
		if e != nil {
			return rewritten, e
		}
		for _, row := range stale {
			args := make([]any, 0, n+1)
			for _, v := range row.folded {
				args = append(args, v)
			}
			if _, e = tx.ExecContext(ctx, update, append(args, row.rowid)...); e != nil {
				return rewritten, e
			}
		}
		rewritten += len(stale)
		if last == nil {
			return rewritten, nil
		}
		after = *last
	}
}

type staleRow struct {
	rowid  int64
	folded []string
}

// staleRows reads one batch of rows after the rowid after and returns those
// whose folded columns are stale, with their new values, and the batch's last
// rowid (nil after the last batch).
func staleRows(ctx context.Context, tx sqlTx, query string, after int64, t foldedTable) ([]staleRow, *int64, error) {
	rows, e := tx.QueryContext(ctx, query, after, refoldBatch)
	if e != nil {
		return nil, nil, e
	}
	defer func() { _ = rows.Close() }()
	n := len(t.columns)
	var stale []staleRow
	var last *int64
	count := 0
	for rows.Next() {
		var rowid int64
		values := make([]string, 2*n)
		dest := []any{&rowid}
		for i := range values {
			dest = append(dest, &values[i])
		}
		if e = rows.Scan(dest...); e != nil {
			return nil, nil, e
		}
		count++
		last = &rowid
		folded := make([]string, n)
		changed := false
		for i, c := range t.columns {
			folded[i] = c.fold(values[i])
			changed = changed || folded[i] != values[n+i]
		}
		if changed {
			stale = append(stale, staleRow{rowid, folded})
		}
	}
	if e = rows.Err(); e != nil {
		return nil, nil, e
	}
	if count < refoldBatch {
		last = nil
	}
	return stale, last, nil
}

// migrateGrepIndex is schema version 2: the grep columns, filled from the
// existing rows before the trigram tables and their triggers exist, then the
// tables, built from the filled columns.
func migrateGrepIndex(ctx context.Context, tx *sql.Tx) error {
	if _, e := tx.ExecContext(ctx, schemaV2); e != nil {
		return e
	}
	for _, t := range foldedTables {
		if _, e := refold(ctx, tx, t); e != nil {
			return e
		}
	}
	if _, e := tx.ExecContext(ctx, schemaV2Index); e != nil {
		return e
	}
	for _, t := range foldedTables {
		if _, e := tx.ExecContext(ctx, "INSERT INTO "+t.name+"_grep ("+t.name+"_grep) VALUES ('rebuild')"); e != nil {
			return e
		}
	}
	return nil
}
