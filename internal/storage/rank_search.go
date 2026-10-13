package storage

import (
	"context"
	"database/sql"
	"errors"
)

// Ranked search (lit search) reads rank_index, the FTS5 word index over
// rank_text. One result is an issue, a project or a milestone: a comment's
// hits count for its issue. A result's score is the best bm25 of its rows,
// with the title weighted over the body (a project's description) over
// comments, so lower scores rank first.

// MaxRankedComments is the most matching comments a ranked issue result
// carries.
const MaxRankedComments = 3

// rankWeights are the bm25 weights of rank_index's columns: title, body,
// comment.
const rankWeights = "10.0, 3.0, 1.0"

// RankedKey is a result's position in rank order: by score, then type and ID.
type RankedKey struct {
	Score    float64
	Type, ID string
}

// RankedQuery is one page of a ranked search.
type RankedQuery struct {
	// Query is search box syntax (see rankQuery); it is never FTS5 syntax.
	Query string
	// ProjectID scopes the search to one project; empty means every project.
	ProjectID string
	// State, when "open" or "closed", returns only issues in that state.
	State string
	// Limit is the page size; After continues after a previous page's last
	// result.
	Limit int
	After *RankedKey
}

// RankedField is one matching field of a result: title, body, description
// (a project's) or comment (with its CommentID). Matches are the byte ranges
// of the matched words in Text, the field's original text; they are empty
// when the text itself contains the internal highlight markers.
type RankedField struct {
	Name, CommentID, Text string
	Matches               [][2]int
}

// RankedResult is one result of a ranked search with its matching fields:
// title and body first, then its best matching comments (at most
// MaxRankedComments of CommentMatches).
type RankedResult struct {
	RankedKey
	ProjectID, Title string
	State            string // an issue's state; empty otherwise
	Revision         int64
	Fields           []RankedField
	CommentMatches   int
}

// RankedSearch reads one page of ranked results in rank order and reports
// whether more follow. A query with nothing to search for has no results.
// Pages are read independently, so a page reflects the database when it is
// read; a write between pages can move results across their boundary.
func (q queries) RankedSearch(ctx context.Context, rq RankedQuery) ([]RankedResult, bool, error) {
	match, ok := rankQuery(rq.Query)
	if !ok {
		return []RankedResult{}, false, nil
	}
	results, e := q.rankedKeys(ctx, match, rq)
	if e != nil {
		return nil, false, e
	}
	more := len(results) > rq.Limit
	if more {
		results = results[:rq.Limit]
	}
	for i := range results {
		if e = q.describe(ctx, &results[i]); e != nil {
			return nil, false, e
		}
		if e = q.rankedFields(ctx, match, &results[i]); e != nil {
			return nil, false, e
		}
	}
	return results, more, nil
}

// rankedKeys reads the keys of the page's results and of one more, if any:
// every matching row of rank_text grouped into its result, filtered by scope
// and state, in rank order after rq.After.
func (q queries) rankedKeys(ctx context.Context, match string, rq RankedQuery) ([]RankedResult, error) {
	after := rq.After != nil
	var a RankedKey
	if after {
		a = *rq.After
	}
	query := `WITH m (seq, score) AS MATERIALIZED (SELECT rowid, bm25(rank_index, ` + rankWeights + `) FROM rank_index WHERE rank_index MATCH ?1),
		g (type, id, score) AS (
			SELECT CASE WHEN t.issue_id <> '' THEN 'issues' ELSE t.owner_type END, CASE WHEN t.issue_id <> '' THEN t.issue_id ELSE t.owner_id END, min(m.score)
			FROM m JOIN rank_text t ON t.seq = m.seq
			WHERE (?2 = '' OR t.project_id = ?2) AND (?3 = '' OR t.issue_id <> '')
			GROUP BY 1, 2)
		SELECT g.type, g.id, g.score FROM g LEFT JOIN issues i ON g.type = 'issues' AND i.id = g.id
		WHERE (?3 = '' OR i.state = ?3) AND (NOT ?4 OR g.score > ?5 OR g.score = ?5 AND (g.type > ?6 OR g.type = ?6 AND g.id > ?7))
		ORDER BY g.score, g.type, g.id LIMIT ?8`
	out := []RankedResult{}
	e := q.each(ctx, query, []any{match, rq.ProjectID, rq.State, after, a.Score, a.Type, a.ID, rq.Limit + 1}, func(s rowScanner) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		var r RankedResult
		e := s.Scan(&r.Type, &r.ID, &r.Score)
		out = append(out, r)
		return e
	})
	return out, e
}

// describe reads a result's project, title, state and revision.
func (q queries) describe(ctx context.Context, r *RankedResult) error {
	query := map[string]string{
		Projects:   "SELECT id, title, '', revision FROM projects WHERE id = ?",
		Issues:     "SELECT project_id, title, state, revision FROM issues WHERE id = ?",
		Milestones: "SELECT project_id, title, '', revision FROM milestones WHERE id = ?",
	}[r.Type]
	e := q.queryRow(ctx, []any{&r.ProjectID, &r.Title, &r.State, &r.Revision}, query, r.ID)
	if errors.Is(e, sql.ErrNoRows) {
		return errors.New("ranked search index names a missing " + r.Type + " record " + r.ID)
	}
	return e
}
