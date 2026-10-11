package storage

import (
	"context"
	"database/sql"
	"fmt"

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

// SearchScan calls fn for every record of projectID (of every project when
// empty), ordered by project ID, record type and ID: a project's description,
// an issue's and a milestone's title and body, and a comment's body. It reads
// the whole scope; it stops at the first error from fn or ctx.
func (q queries) SearchScan(ctx context.Context, projectID string, fn func(SearchRecord) error) error {
	return q.each(ctx, `SELECT * FROM (
		SELECT 'projects' AS type, id, revision, id AS project_id, '' AS issue_id, '' AS issue_title, NULL AS title, description AS body FROM projects
		UNION ALL SELECT 'issues', id, revision, project_id, id, title, title, body FROM issues
		UNION ALL SELECT 'comments', c.id, c.revision, i.project_id, i.id, i.title, NULL, c.body FROM comments c JOIN issues i ON i.id = c.issue_id
		UNION ALL SELECT 'milestones', id, revision, project_id, '', '', title, body FROM milestones
	) WHERE ?1 = '' OR project_id = ?1 ORDER BY project_id, type, id`, []any{projectID}, func(s rowScanner) error {
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
