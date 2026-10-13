package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// SearchField is one searchable text field of a record.
type SearchField struct{ Name, Text string }

// SearchRecord is one record as the search scan reads it: its identity, its
// project, and its searchable fields in the order search reports them.
type SearchRecord struct {
	Type, ID  string
	Revision  int64
	ProjectID string
	// IssueID and IssueTitle name the issue itself, or a comment's issue;
	// they are empty for projects and milestones.
	IssueID, IssueTitle string
	Fields              []SearchField
}

// SearchScan calls fn for the records of projectID (of every project when
// empty) that lit grep may report for needle, ordered by project ID, record
// type and ID: a project's description, an issue's and a milestone's title
// and body, and a comment's body. needle is folded text that the
// textfold.Grep fold of every matching field contains; a record is read when
// its folded text in grep_text may contain it, through the trigram index when
// needle has three or more characters and by scanning grep_text otherwise. The records are a
// superset of the matches (NUL and U+FFFD read alike), so callers confirm each
// on its original text. An empty needle reads every record of the scope. The
// scan stops at the first error from fn or ctx.
func (q queries) SearchScan(ctx context.Context, projectID, needle string, fn func(SearchRecord) error) error {
	query, args := searchQuery(projectID, needle)
	return q.scanRecords(ctx, query, args, fn)
}

// scanRecords reads the search records that query (a searchQuery) selects
// and calls fn for each, stopping at the first error from fn or ctx.
func (q queries) scanRecords(ctx context.Context, query string, args []any, fn func(SearchRecord) error) error {
	return q.each(ctx, query, args, func(s rowScanner) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		var v SearchRecord
		var title sql.NullString
		var body string
		if e := s.Scan(&v.Type, &v.ID, &v.Revision, &v.ProjectID, &v.IssueID, &v.IssueTitle, &title, &body); e != nil {
			return e
		}
		if title.Valid {
			v.Fields = append(v.Fields, SearchField{"title", title.String})
		}
		v.Fields = append(v.Fields, SearchField{bodyField(v.Type), body})
		return fn(v)
	})
}

// searchQuery is SearchScan's query and its arguments.
func searchQuery(projectID, needle string) (string, []any) {
	needle = grepSafe(needle)
	return recordsQuery(grepHits(needle), projectID, globLiteral(needle), needle)
}

// recordsQuery is the query of the search records of the project ?1 (of
// every project when it is empty) that hits selects as (owner_type,
// owner_id), in SearchScan's order, and its arguments: projectID, then
// args, which hits refers to from ?2 on. An empty hits selects every record.
func recordsQuery(hits, projectID string, args ...any) (string, []any) {
	match := func(typ string) string {
		if hits == "" {
			return "1"
		}
		return "r.id IN (SELECT owner_id FROM hits WHERE owner_type = '" + typ + "')"
	}
	scope := func(column string) string {
		if projectID == "" {
			return ""
		}
		return " AND " + column + " = ?1"
	}
	query := `SELECT 'projects' AS type, r.id, r.revision, r.id AS project_id, '' AS issue_id, '' AS issue_title, NULL AS title, r.description AS body FROM projects r WHERE ` + match(Projects) + scope("r.id") + `
		UNION ALL SELECT 'issues', r.id, r.revision, r.project_id, r.id, r.title, r.title, r.body FROM issues r WHERE ` + match(Issues) + scope("r.project_id") + `
		UNION ALL SELECT 'comments', r.id, r.revision, i.project_id, i.id, i.title, NULL, r.body FROM comments r JOIN issues i ON i.id = r.issue_id WHERE ` + match(Comments) + scope("i.project_id") + `
		UNION ALL SELECT 'milestones', r.id, r.revision, r.project_id, '', '', r.title, r.body FROM milestones r WHERE ` + match(Milestones) + scope("r.project_id") + `
		ORDER BY project_id, type, id`
	if hits != "" {
		query = "WITH hits (owner_type, owner_id) AS MATERIALIZED (" + hits + ") " + query
	}
	return query, append([]any{projectID}, args...)
}

// grepHits selects the records (owner_type, owner_id) whose folded title or
// body may contain needle: a GLOB with the pattern ?2 on grep_index from
// three characters on (the trigram index needs three), otherwise a scan of
// grep_text for the needle ?3. An empty needle needs no selection ("").
func grepHits(needle string) string {
	if needle == "" {
		return ""
	}
	return needleHits(needle, "?2", "?3")
}

// needleHits selects the records whose folded title or body may contain the
// nonempty needle: a GLOB with the pattern glob on grep_index from three
// characters on, otherwise a scan of grep_text for raw (both parameters).
func needleHits(needle, glob, raw string) string {
	if utf8.RuneCountInString(needle) >= 3 {
		return "SELECT owner_type, owner_id FROM grep_text WHERE seq IN (SELECT rowid FROM grep_index WHERE title GLOB " + glob + " UNION ALL SELECT rowid FROM grep_index WHERE body GLOB " + glob + ")"
	}
	return "SELECT owner_type, owner_id FROM grep_text WHERE instr(title, " + raw + ") > 0 OR instr(body, " + raw + ") > 0"
}

// globLiteral is a GLOB pattern matching any text that contains s: s between
// two stars, with GLOB's special characters as one-character bracket
// expressions ([*], [?] and [[]). LIKE ... ESCAPE cannot use the trigram index.
func globLiteral(s string) string {
	var b strings.Builder
	b.WriteByte('*')
	for _, r := range s {
		switch r {
		case '*', '?', '[':
			b.WriteString("[" + string(r) + "]")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('*')
	return b.String()
}

// CompletionCandidates lists every record of type typ in ID order, or only
// those of projectID when it is not empty, with the identifiers and display
// metadata that completions expose: the title, the project and its title,
// an issue's state and a comment's issue (comments belong to their issue's
// project). Projects ignore projectID.
func (q queries) CompletionCandidates(ctx context.Context, typ, projectID string) ([]protocol.Completion, error) {
	var query string
	switch typ {
	case Projects:
		query = "SELECT id, title, '', '', '', '' FROM projects r WHERE ?1 = ?1"
	case Issues:
		query = "SELECT r.id, r.title, r.project_id, p.title, '', r.state FROM issues r JOIN projects p ON p.id = r.project_id WHERE ?1 = '' OR r.project_id = ?1"
	case Milestones:
		query = "SELECT r.id, r.title, r.project_id, p.title, '', '' FROM milestones r JOIN projects p ON p.id = r.project_id WHERE ?1 = '' OR r.project_id = ?1"
	case Comments:
		query = "SELECT r.id, '', i.project_id, p.title, i.id, '' FROM comments r JOIN issues i ON i.id = r.issue_id JOIN projects p ON p.id = i.project_id WHERE ?1 = '' OR i.project_id = ?1"
	default:
		return nil, fmt.Errorf("unknown record type %q", typ)
	}
	out := []protocol.Completion{}
	e := q.each(ctx, query+" ORDER BY r.id", []any{projectID}, func(s rowScanner) error {
		var v protocol.Completion
		e := s.Scan(&v.ID, &v.Title, &v.ProjectID, &v.ProjectTitle, &v.IssueID, &v.State)
		out = append(out, v)
		return e
	})
	return out, e
}
