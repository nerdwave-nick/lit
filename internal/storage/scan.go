package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
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
// and body, and a comment's body. needle is already folded the way grep
// matches (textfold.Grep); a record is read when one of its grep-folded fields
// may contain it, through the trigram tables when needle has three or more
// characters and by scanning the grep columns otherwise. The records are a
// superset of the matches (NUL and U+FFFD read alike), so callers confirm each
// on its original text. An empty needle reads every record of the scope. The
// scan stops at the first error from fn or ctx.
func (q queries) SearchScan(ctx context.Context, projectID, needle string, fn func(SearchRecord) error) error {
	query, args := searchQuery(projectID, needle)
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
	needle = strings.ReplaceAll(needle, "\x00", "\uFFFD")
	match := grepCondition(needle)
	scope := func(column string) string {
		if projectID == "" {
			return ""
		}
		return " AND " + column + " = ?1"
	}
	query := `SELECT 'projects' AS type, r.id, r.revision, r.id AS project_id, '' AS issue_id, '' AS issue_title, NULL AS title, r.description AS body FROM projects r WHERE ` + match("projects", "grep_description") + scope("r.id") + `
		UNION ALL SELECT 'issues', r.id, r.revision, r.project_id, r.id, r.title, r.title, r.body FROM issues r WHERE ` + match("issues", "grep_title", "grep_body") + scope("r.project_id") + `
		UNION ALL SELECT 'comments', r.id, r.revision, i.project_id, i.id, i.title, NULL, r.body FROM comments r JOIN issues i ON i.id = r.issue_id WHERE ` + match("comments", "grep_body") + scope("i.project_id") + `
		UNION ALL SELECT 'milestones', r.id, r.revision, r.project_id, '', '', r.title, r.body FROM milestones r WHERE ` + match("milestones", "grep_title", "grep_body") + scope("r.project_id") + `
		ORDER BY project_id, type, id`
	return query, []any{projectID, globLiteral(needle), needle}
}

// grepCondition returns, for a record table aliased r and its grep columns,
// the condition that one of the columns may contain needle: a GLOB with the
// pattern ?2 on the table's trigram table from three characters on (the
// trigram index needs three), otherwise a scan of the columns for the needle
// ?3. An empty needle matches every row.
func grepCondition(needle string) func(table string, columns ...string) string {
	return func(table string, columns ...string) string {
		var any []string
		for _, c := range columns {
			switch {
			case needle == "":
				return "1"
			case utf8.RuneCountInString(needle) >= 3:
				any = append(any, "SELECT rowid FROM "+table+"_grep WHERE "+c+" GLOB ?2")
			default:
				any = append(any, "instr(r."+c+", ?3) > 0")
			}
		}
		if utf8.RuneCountInString(needle) >= 3 {
			return "r.rowid IN (" + strings.Join(any, " UNION ALL ") + ")"
		}
		return "(" + strings.Join(any, " OR ") + ")"
	}
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
