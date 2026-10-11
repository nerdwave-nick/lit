package storage

import (
	"context"
	"database/sql"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/textfold"
)

// grepFold is a field's text as grep_text stores it: textfold.Grep of the
// text, then grepSafe.
func grepFold(s string) string { return grepSafe(textfold.Grep(s)) }

// grepSafe writes NUL as U+FFFD, because GLOB stops at NUL. Stored text and
// needles are mapped alike, scalar by scalar, so a substring of a text still
// maps to a substring of its stored fold; lit grep confirms every candidate
// on the original text.
func grepSafe(s string) string { return strings.ReplaceAll(s, "\x00", "\uFFFD") }

// grepSource is a record table and the columns of its searchable fields
// (” for a record without a title).
type grepSource struct{ table, title, body string }

// grepSources are the record tables whose text grep_text folds.
var grepSources = []grepSource{
	{Projects, "''", "r.description"},
	{Issues, "r.title", "r.body"},
	{Comments, "''", "r.body"},
	{Milestones, "r.title", "r.body"},
}

// writeGrepText is the upsert of a record's grep_text row; an unchanged row is
// not rewritten, so its index entries stay.
const writeGrepText = `INSERT INTO grep_text (owner_type, owner_id, title, body) VALUES (?, ?, ?, ?)
	ON CONFLICT (owner_type, owner_id) DO UPDATE SET title = excluded.title, body = excluded.body
	WHERE title <> excluded.title OR body <> excluded.body`

// writeGrep stores the folded text of one record in grep_text.
func (t *WriteTx) writeGrep(ctx context.Context, typ, id, title, body string) error {
	_, e := t.exec(ctx, writeGrepText, typ, id, grepFold(title), grepFold(body))
	return e
}

// sqlTx is the part of a transaction that refolding uses.
type sqlTx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// refoldBatch bounds the records read at once while refolding.
const refoldBatch = 500

// refold recomputes the grep_text rows of every record of src: missing and
// stale rows are written and rows without a record deleted (the triggers keep
// grep_index in step). It returns the number of rows changed.
func refold(ctx context.Context, tx sqlTx, src grepSource) (int, error) {
	read := "SELECT r.id, " + src.title + ", " + src.body + ", g.title, g.body FROM " + src.table + " r LEFT JOIN grep_text g ON g.owner_type = '" + src.table + "' AND g.owner_id = r.id WHERE r.id > ? ORDER BY r.id LIMIT ?"
	changed := 0
	for after := ""; ; {
		stale, last, e := staleText(ctx, tx, read, after)
		if e != nil {
			return changed, e
		}
		for _, row := range stale {
			if _, e = tx.ExecContext(ctx, writeGrepText, src.table, row[0], row[1], row[2]); e != nil {
				return changed, e
			}
		}
		changed += len(stale)
		if last == "" {
			break
		}
		after = last
	}
	r, e := tx.ExecContext(ctx, "DELETE FROM grep_text WHERE owner_type = ? AND NOT EXISTS (SELECT 1 FROM "+src.table+" r WHERE r.id = grep_text.owner_id)", src.table)
	if e != nil {
		return changed, e
	}
	n, e := r.RowsAffected()
	return changed + int(n), e
}

// staleText reads one batch of records after the ID after and returns the
// rows (ID, folded title, folded body) whose grep_text row is missing or
// stale, and the batch's last ID ("" after the last batch).
func staleText(ctx context.Context, tx sqlTx, query, after string) ([][3]string, string, error) {
	rows, e := tx.QueryContext(ctx, query, after, refoldBatch)
	if e != nil {
		return nil, "", e
	}
	defer func() { _ = rows.Close() }()
	var stale [][3]string
	last, count := "", 0
	for rows.Next() {
		var id, title, body string
		var storedTitle, storedBody sql.NullString
		if e = rows.Scan(&id, &title, &body, &storedTitle, &storedBody); e != nil {
			return nil, "", e
		}
		count++
		last = id
		title, body = grepFold(title), grepFold(body)
		if !storedTitle.Valid || storedTitle.String != title || storedBody.String != body {
			stale = append(stale, [3]string{id, title, body})
		}
	}
	if e = rows.Err(); e != nil {
		return nil, "", e
	}
	if count < refoldBatch {
		last = ""
	}
	return stale, last, nil
}

// migrateGrepIndex is schema version 2: grep_text and its trigram index,
// filled from the existing records.
func migrateGrepIndex(ctx context.Context, tx *sql.Tx) error {
	if _, e := tx.ExecContext(ctx, schemaV2); e != nil {
		return e
	}
	for _, src := range grepSources {
		if _, e := refold(ctx, tx, src); e != nil {
			return e
		}
	}
	return nil
}
