package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/textfold"
)

// IDsByPrefix lists, in ID order, up to limit IDs (0 for all) of records of
// type typ that start with an ID prefix: 1 to 32 hex digits in any case,
// optionally with the UUID's dashes in their places. A non-empty projectID
// restricts issues and milestones to that project; it is ignored for projects
// and comments. Services pass a limit of 2 to detect an ambiguous selector.
func (q queries) IDsByPrefix(ctx context.Context, typ, prefix, projectID string, limit int) ([]string, error) {
	dashed, e := dashedPrefix(prefix)
	if e != nil {
		return nil, e
	}
	if _, e = recordColumns(typ); e != nil {
		return nil, e
	}
	// IDs are lower-case hex and dashes, all before '~': a range scan of the key.
	query := "SELECT id FROM " + typ + " WHERE id >= ?1 AND id < ?1 || '~'"
	if projectID != "" && (typ == Issues || typ == Milestones) {
		query += " AND project_id = ?2"
	}
	if limit <= 0 {
		limit = -1
	}
	out := []string{}
	e = q.each(ctx, query+" ORDER BY id LIMIT ?3", []any{dashed, projectID, limit}, func(s rowScanner) error {
		var id string
		e := s.Scan(&id)
		out = append(out, id)
		return e
	})
	return out, e
}

// dashedPrefix turns an ID prefix into the leading text of matching UUIDs.
func dashedPrefix(prefix string) (string, error) {
	var b strings.Builder
	for i, r := range strings.ToLower(prefix) {
		switch {
		case r == '-' && (i == 8 || i == 13 || i == 18 || i == 23):
		case r >= '0' && r <= '9' || r >= 'a' && r <= 'f':
			b.WriteRune(r)
		default:
			return "", fmt.Errorf("invalid ID prefix %q", prefix)
		}
	}
	hex := b.String()
	if hex == "" || len(hex) > 32 {
		return "", fmt.Errorf("invalid ID prefix %q", prefix)
	}
	var out strings.Builder
	for i, r := range hex {
		if i == 8 || i == 12 || i == 16 || i == 20 {
			out.WriteByte('-')
		}
		out.WriteRune(r)
	}
	return out.String(), nil
}

// IDByTitle finds the record that a title names. A project title is global
// and compared case-insensitively (strings.EqualFold; project titles are
// ASCII). Issue and milestone titles are compared by title key within
// projectID; without a project nothing matches. Title keys are unique per
// project, so at most one record matches.
func (q queries) IDByTitle(ctx context.Context, typ, projectID, title string) (string, bool, error) {
	var query string
	var args []any
	switch typ {
	case Projects:
		lower, ok := asciiFold(title)
		if !ok {
			return "", false, nil
		}
		query, args = "SELECT id FROM projects WHERE lower(title) = ?", []any{lower}
	case Issues, Milestones:
		query, args = "SELECT id FROM "+typ+" WHERE project_id = ? AND title_key = ?", []any{projectID, textfold.Fold(title)}
	default:
		return "", false, fmt.Errorf("%s have no title", typ)
	}
	var id string
	e := q.queryRow(ctx, []any{&id}, query, args...)
	if errors.Is(e, sql.ErrNoRows) {
		return "", false, nil
	}
	return id, e == nil, e
}

// asciiFold maps s to the lower-case ASCII text that strings.EqualFold
// equates it with, or reports that no ASCII text is equal to it.
func asciiFold(s string) (string, bool) {
	var b strings.Builder
	for _, r := range s {
		f := r
		for f > unicode.MaxASCII {
			if f = unicode.SimpleFold(f); f == r {
				return "", false
			}
		}
		b.WriteRune(unicode.ToLower(f))
	}
	return b.String(), true
}
