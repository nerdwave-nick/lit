package storage

import (
	"context"
	"database/sql"
)

// rankSource is a record table and the query of the rank_text rows of its
// records, in rank_text's column order: owner_type, owner_id, project_id,
// issue_id, title, body, comment.
type rankSource struct{ table, rows string }

// rankSources are the record tables whose text rank_text copies.
var rankSources = []rankSource{
	{Projects, "SELECT 'projects', r.id, r.id, '', r.title, r.description, '' FROM projects r"},
	{Issues, "SELECT 'issues', r.id, r.project_id, r.id, r.title, r.body, '' FROM issues r"},
	{Comments, "SELECT 'comments', r.id, i.project_id, r.issue_id, '', '', r.body FROM comments r JOIN issues i ON i.id = r.issue_id"},
	{Milestones, "SELECT 'milestones', r.id, r.project_id, '', r.title, r.body, '' FROM milestones r"},
}

// rankUpsert ends both rank_text writes: an existing row is rewritten only
// when it changed, so its index entries stay otherwise.
const rankUpsert = ` ON CONFLICT (owner_type, owner_id) DO UPDATE SET project_id = excluded.project_id,
	issue_id = excluded.issue_id, title = excluded.title, body = excluded.body, comment = excluded.comment
	WHERE project_id <> excluded.project_id OR issue_id <> excluded.issue_id OR title <> excluded.title
	OR body <> excluded.body OR comment <> excluded.comment`

// writeRankText is the upsert of one record's rank_text row. A nil project
// (?3) is the project of the issue ?4, for a comment.
const writeRankText = `INSERT INTO rank_text (owner_type, owner_id, project_id, issue_id, title, body, comment)
	VALUES (?1, ?2, coalesce(?3, (SELECT project_id FROM issues WHERE id = ?4)), ?4, ?5, ?6, ?7)` + rankUpsert

// writeRank stores the original text of one record in rank_text. project is
// nil for a comment, whose project is its issue's.
func (t *WriteTx) writeRank(ctx context.Context, typ, id string, project any, issue, title, body, comment string) error {
	_, e := t.exec(ctx, writeRankText, typ, id, project, issue, title, body, comment)
	return e
}

// resyncRank rewrites the rank_text rows of the records of src that are
// missing or differ from their records, and deletes rows without a record
// (the triggers keep rank_index in step). It returns the number of rows
// changed.
func resyncRank(ctx context.Context, tx sqlTx, src rankSource) (int, error) {
	r, e := tx.ExecContext(ctx, `WITH s (owner_type, owner_id, project_id, issue_id, title, body, comment) AS (`+src.rows+`)
		INSERT INTO rank_text (owner_type, owner_id, project_id, issue_id, title, body, comment)
		SELECT s.* FROM s LEFT JOIN rank_text g ON g.owner_type = s.owner_type AND g.owner_id = s.owner_id
		WHERE g.seq IS NULL OR g.project_id <> s.project_id OR g.issue_id <> s.issue_id OR g.title <> s.title
		OR g.body <> s.body OR g.comment <> s.comment`+rankUpsert)
	if e != nil {
		return 0, e
	}
	written, e := r.RowsAffected()
	if e != nil {
		return 0, e
	}
	if r, e = tx.ExecContext(ctx, "DELETE FROM rank_text WHERE owner_type = ? AND NOT EXISTS (SELECT 1 FROM "+src.table+" r WHERE r.id = rank_text.owner_id)", src.table); e != nil {
		return int(written), e
	}
	deleted, e := r.RowsAffected()
	return int(written + deleted), e
}

// migrateRankIndex is schema version 3: rank_text and its word index, filled
// from the existing records.
func migrateRankIndex(ctx context.Context, tx *sql.Tx) error {
	if _, e := tx.ExecContext(ctx, schemaV3); e != nil {
		return e
	}
	for _, src := range rankSources {
		if _, e := resyncRank(ctx, tx, src); e != nil {
			return e
		}
	}
	return nil
}
