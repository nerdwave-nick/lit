package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// Record types, as protocol.ResourceType names them.
const (
	Projects   = "projects"
	Issues     = "issues"
	Comments   = "comments"
	Milestones = "milestones"
)

// ErrRecordNotFound matches (with errors.Is) a read of a record that does not exist.
var ErrRecordNotFound = errors.New("record does not exist")

// Each record type is read by one SELECT over its table (aliased as r) whose
// set-valued and computed fields are JSON arrays in ID or value order. The
// computed membership lists (a project's issue_ids, an issue's child_ids and
// comment_ids) are derived here and never stored.
const (
	projectColumns = `r.id, r.revision, r.created_at, r.updated_at, r.title, r.description,
	(SELECT json_group_array(x.repository_ref ORDER BY x.repository_ref) FROM project_repositories x WHERE x.project_id = r.id),
	(SELECT json_group_array(x.id ORDER BY x.id) FROM issues x WHERE x.project_id = r.id)`
	issueColumns = `r.id, r.revision, r.created_at, r.updated_at, r.title, r.body, r.state, r.assignee, r.project_id, r.parent_id,
	(SELECT json_group_array(x.label ORDER BY x.label) FROM issue_labels x WHERE x.issue_id = r.id),
	(SELECT json_group_array(x.id ORDER BY x.id) FROM issues x WHERE x.parent_id = r.id),
	(SELECT json_group_array(x.id ORDER BY x.id) FROM comments x WHERE x.issue_id = r.id),
	(SELECT json_group_array(x.b ORDER BY x.b) FROM issue_links x WHERE x.kind = 'blocks' AND x.a = r.id),
	(SELECT json_group_array(x.a ORDER BY x.a) FROM issue_links x WHERE x.kind = 'blocks' AND x.b = r.id),
	(SELECT json_group_array(x.id ORDER BY x.id) FROM (SELECT b AS id FROM issue_links WHERE kind = 'related' AND a = r.id UNION ALL SELECT a FROM issue_links WHERE kind = 'related' AND b = r.id) x)`
	commentColumns   = `r.id, r.revision, r.created_at, r.updated_at, r.body, r.author, r.issue_id`
	milestoneColumns = `r.id, r.revision, r.created_at, r.updated_at, r.title, r.body, r.project_id,
	(SELECT json_group_array(x.issue_id ORDER BY x.issue_id) FROM milestone_members x WHERE x.milestone_id = r.id)`
)

func recordColumns(typ string) (string, error) {
	switch typ {
	case Projects:
		return projectColumns, nil
	case Issues:
		return issueColumns, nil
	case Comments:
		return commentColumns, nil
	case Milestones:
		return milestoneColumns, nil
	}
	return "", fmt.Errorf("unknown record type %q", typ)
}

// rowScanner is *sql.Row or *sql.Rows.
type rowScanner interface{ Scan(dest ...any) error }

// jsonList decodes a JSON array column into dest.
type jsonList struct{ dest *[]string }

func (l jsonList) Scan(src any) error {
	var b []byte
	switch v := src.(type) {
	case string:
		b = []byte(v)
	case []byte:
		b = v
	default:
		return fmt.Errorf("list column holds %T", src)
	}
	*l.dest = []string{}
	return json.Unmarshal(b, l.dest)
}

// nullText scans a nullable TEXT column into a *string.
type nullText struct{ dest **string }

func (n nullText) Scan(src any) error {
	var v sql.NullString
	if e := v.Scan(src); e != nil {
		return e
	}
	*n.dest = nil
	if v.Valid {
		*n.dest = &v.String
	}
	return nil
}

func scanRecord(typ string, s rowScanner) (any, error) {
	var e error
	switch typ {
	case Projects:
		p := protocol.Project{SchemaVersion: 1}
		e = s.Scan(&p.ID, &p.Revision, &p.CreatedAt, &p.UpdatedAt, &p.Title, &p.Description, jsonList{&p.RepositoryRefs}, jsonList{&p.IssueIDs})
		return p, e
	case Issues:
		p := protocol.Issue{SchemaVersion: 1}
		e = s.Scan(&p.ID, &p.Revision, &p.CreatedAt, &p.UpdatedAt, &p.Title, &p.Body, &p.State, nullText{&p.Assignee}, &p.ProjectID, nullText{&p.ParentID},
			jsonList{&p.Labels}, jsonList{&p.ChildIDs}, jsonList{&p.CommentIDs}, jsonList{&p.Blocks}, jsonList{&p.BlockedBy}, jsonList{&p.Related})
		return p, e
	case Comments:
		p := protocol.Comment{SchemaVersion: 1}
		e = s.Scan(&p.ID, &p.Revision, &p.CreatedAt, &p.UpdatedAt, &p.Body, &p.Author, &p.IssueID)
		return p, e
	case Milestones:
		p := protocol.Milestone{SchemaVersion: 1}
		e = s.Scan(&p.ID, &p.Revision, &p.CreatedAt, &p.UpdatedAt, &p.Title, &p.Body, &p.ProjectID, jsonList{&p.IssueIDs})
		return p, e
	}
	return nil, fmt.Errorf("unknown record type %q", typ)
}

// each runs a query and calls scan for every result row.
func (q queries) each(ctx context.Context, query string, args []any, scan func(rowScanner) error) error {
	rows, e := q.tx.QueryContext(ctx, query, args...)
	if e != nil {
		return classify(e)
	}
	defer rows.Close()
	for rows.Next() {
		if e = scan(rows); e != nil {
			return classify(e)
		}
	}
	return classify(rows.Err())
}

// Record reads the current record of type typ (Projects, Issues, Comments or
// Milestones) as its protocol type. A missing record is ErrRecordNotFound.
func (q queries) Record(ctx context.Context, typ, id string) (any, error) {
	cols, e := recordColumns(typ)
	if e != nil {
		return nil, e
	}
	r, e := scanRecord(typ, q.tx.QueryRowContext(ctx, "SELECT "+cols+" FROM "+typ+" r WHERE r.id = ?", id))
	if errors.Is(e, sql.ErrNoRows) {
		return nil, fmt.Errorf("%s %s: %w", typ, id, ErrRecordNotFound)
	}
	return r, classify(e)
}

// Project reads one project; see Record.
func (q queries) Project(ctx context.Context, id string) (protocol.Project, error) {
	r, e := q.Record(ctx, Projects, id)
	p, _ := r.(protocol.Project)
	return p, e
}

// Issue reads one issue; see Record.
func (q queries) Issue(ctx context.Context, id string) (protocol.Issue, error) {
	r, e := q.Record(ctx, Issues, id)
	p, _ := r.(protocol.Issue)
	return p, e
}

// Comment reads one comment; see Record.
func (q queries) Comment(ctx context.Context, id string) (protocol.Comment, error) {
	r, e := q.Record(ctx, Comments, id)
	p, _ := r.(protocol.Comment)
	return p, e
}

// Milestone reads one milestone; see Record.
func (q queries) Milestone(ctx context.Context, id string) (protocol.Milestone, error) {
	r, e := q.Record(ctx, Milestones, id)
	p, _ := r.(protocol.Milestone)
	return p, e
}
