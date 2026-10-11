package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/textfold"
)

// ListQuery selects records of one type with the service's discovery filters,
// in sort order, one keyset page at a time. Zero values do not filter. The
// service validates which filters a type accepts; List refuses filters that
// cannot apply to the type.
type ListQuery struct {
	Type string // Projects, Issues, Comments or Milestones
	// Scope: an issue's or milestone's project (empty lists issues of every
	// project; milestones need one), a comment's issue (required), and for
	// issues the members of one milestone.
	ProjectID, IssueID, MilestoneID string

	IDs []string
	// Title equals the record's title by title key (projects: by lower-case
	// title); Text is a folded substring of the title, a line break and the
	// body (comments have an empty title).
	Title, Text string
	// RepositoryRefs: projects that have every one.
	RepositoryRefs []string

	// Issue filters. ParentID and Assignee: nil does not filter, a nil
	// *string selects records without one.
	ParentID, Assignee               **string
	State                            string
	LabelsAll, LabelsAny, LabelsNone []string
	// Blocked: blocked by at least one open issue.
	Blocked *bool
	// Claimed and OwnerClientID consider live claims only, which expire after Now.
	Claimed       *bool
	OwnerClientID string
	Now           time.Time

	// Author filters comments.
	Author string

	// RFC 3339 bounds: After is inclusive, Before exclusive.
	CreatedAfter, CreatedBefore, UpdatedAfter, UpdatedBefore string

	// Sort is created_at (the default), updated_at or title (not for comments);
	// ties are ordered by ID.
	Sort       string
	Descending bool
	// After continues behind the position of the previous page's last record.
	After *ListPosition
	// Limit is the most records returned, 0 for all. Ask for one more than a
	// page to learn whether another page follows.
	Limit int
}

// ListPosition is a record's place in a sort order: its SortKey and ID.
type ListPosition struct{ Key, ID string }

// SortKey is a record's key in the sort order (see ListQuery.Sort): its
// creation or update stamp, or its title key (a project's lower-case title).
func SortKey(sort string, r any) string {
	_, _, _, created, updated, _ := identity(r)
	switch sort {
	case "updated_at":
		return updated
	case "title":
		switch p := r.(type) {
		case protocol.Project:
			return strings.ToLower(p.Title)
		case protocol.Issue:
			return textfold.Fold(p.Title)
		case protocol.Milestone:
			return textfold.Fold(p.Title)
		}
		return ""
	}
	return created
}

// listWhere collects the conditions and arguments of a list query.
type listWhere struct {
	conds []string
	args  []any
}

func (w *listWhere) add(cond string, args ...any) {
	w.conds = append(w.conds, cond)
	w.args = append(w.args, args...)
}

// List reads the records that match l, in its order, with their computed lists.
func (q queries) List(ctx context.Context, l ListQuery) ([]any, error) {
	cols, e := recordColumns(l.Type)
	if e != nil {
		return nil, e
	}
	key, title, body, e := listColumns(l)
	if e != nil {
		return nil, e
	}
	var w listWhere
	if e = l.scope(&w); e != nil {
		return nil, e
	}
	if e = l.filters(&w, title, body); e != nil {
		return nil, e
	}
	order, cmp := "ASC", ">"
	if l.Descending {
		order, cmp = "DESC", "<"
	}
	if l.After != nil {
		w.add(fmt.Sprintf("(%s, r.id) %s (?, ?)", key, cmp), l.After.Key, l.After.ID)
	}
	query := "SELECT " + cols + " FROM " + l.Type + " r"
	if len(w.conds) > 0 {
		query += " WHERE " + strings.Join(w.conds, " AND ")
	}
	limit := l.Limit
	if limit <= 0 {
		limit = -1
	}
	query += fmt.Sprintf(" ORDER BY %s %s, r.id %s LIMIT ?", key, order, order)
	out := []any{}
	e = q.each(ctx, query, append(w.args, limit), func(s rowScanner) error {
		r, e := scanRecord(l.Type, s)
		out = append(out, r)
		return e
	})
	return out, e
}

// listColumns names the sort key, the folded title and the folded body.
func listColumns(l ListQuery) (key, title, body string, e error) {
	switch l.Type {
	case Projects:
		title, body = "lower(r.title)", "r.folded_description"
	case Comments:
		title, body = "''", "r.folded_body"
	default:
		title, body = "r.title_key", "r.folded_body"
	}
	switch l.Sort {
	case "", "created_at":
		key = "r.created_at"
	case "updated_at":
		key = "r.updated_at"
	case "title":
		if l.Type == Comments {
			return "", "", "", fmt.Errorf("comments have no title to sort by")
		}
		key = title
	default:
		return "", "", "", fmt.Errorf("unknown sort %q", l.Sort)
	}
	return key, title, body, nil
}

func (l ListQuery) scope(w *listWhere) error {
	issueOnly := l.MilestoneID != "" || l.ParentID != nil || l.Assignee != nil || l.State != "" || len(l.LabelsAll)+len(l.LabelsAny)+len(l.LabelsNone) > 0 || l.Blocked != nil || l.Claimed != nil || l.OwnerClientID != ""
	switch {
	case issueOnly && l.Type != Issues, l.Author != "" && l.Type != Comments, len(l.RepositoryRefs) > 0 && l.Type != Projects:
		return fmt.Errorf("filters inapplicable to %s", l.Type)
	case (l.Claimed != nil || l.OwnerClientID != "") && l.Now.IsZero():
		return fmt.Errorf("claim filters need the request time")
	}
	switch l.Type {
	case Issues, Milestones:
		if l.ProjectID != "" {
			w.add("r.project_id = ?", l.ProjectID)
		} else if l.Type == Milestones {
			return fmt.Errorf("milestones are listed per project")
		}
	case Comments:
		if l.IssueID == "" {
			return fmt.Errorf("comments are listed per issue")
		}
		w.add("r.issue_id = ?", l.IssueID)
	}
	if l.MilestoneID != "" {
		w.add("EXISTS (SELECT 1 FROM milestone_members x WHERE x.milestone_id = ? AND x.issue_id = r.id)", l.MilestoneID)
	}
	return nil
}

func (l ListQuery) filters(w *listWhere, title, body string) error {
	if len(l.IDs) > 0 {
		w.add("r.id IN (SELECT value FROM json_each(?))", jsonIDs(l.IDs))
	}
	if l.Title != "" {
		w.add(title+" = ?", textfold.Fold(l.Title))
	}
	if l.Text != "" {
		// Blobs compare bytes, including NULs.
		w.add("instr(CAST("+title+" || char(10) || "+body+" AS BLOB), CAST(? AS BLOB)) > 0", textfold.Fold(l.Text))
	}
	for _, v := range l.RepositoryRefs {
		w.add("EXISTS (SELECT 1 FROM project_repositories x WHERE x.project_id = r.id AND x.repository_ref = ?)", v)
	}
	for _, f := range []struct {
		column string
		value  **string
	}{{"parent_id", l.ParentID}, {"assignee", l.Assignee}} {
		if f.value != nil && *f.value == nil {
			w.add("r." + f.column + " IS NULL")
		} else if f.value != nil {
			w.add("r."+f.column+" = ?", **f.value)
		}
	}
	if l.State != "" {
		w.add("r.state = ?", l.State)
	}
	for _, v := range l.LabelsAll {
		w.add("EXISTS (SELECT 1 FROM issue_labels x WHERE x.issue_id = r.id AND x.label = ?)", v)
	}
	if len(l.LabelsAny) > 0 {
		w.add("EXISTS (SELECT 1 FROM issue_labels x WHERE x.issue_id = r.id AND x.label IN (SELECT value FROM json_each(?)))", jsonIDs(l.LabelsAny))
	}
	if len(l.LabelsNone) > 0 {
		w.add("NOT EXISTS (SELECT 1 FROM issue_labels x WHERE x.issue_id = r.id AND x.label IN (SELECT value FROM json_each(?)))", jsonIDs(l.LabelsNone))
	}
	if l.Blocked != nil {
		w.add("EXISTS (SELECT 1 FROM issue_links x JOIN issues o ON o.id = x.a WHERE x.kind = 'blocks' AND x.b = r.id AND o.state = 'open') = ?", *l.Blocked)
	}
	if l.Claimed != nil {
		w.add("EXISTS (SELECT 1 FROM claims x WHERE x.issue_id = r.id AND x.expires_at > ?) = ?", l.Now.UnixNano(), *l.Claimed)
	}
	if l.OwnerClientID != "" {
		w.add("EXISTS (SELECT 1 FROM claims x WHERE x.issue_id = r.id AND x.expires_at > ? AND x.owner_client_id = ?)", l.Now.UnixNano(), l.OwnerClientID)
	}
	if l.Author != "" {
		w.add("r.author = ?", l.Author)
	}
	for _, b := range []struct{ column, bound, op string }{{"created_at", l.CreatedAfter, ">="}, {"created_at", l.CreatedBefore, "<"}, {"updated_at", l.UpdatedAfter, ">="}, {"updated_at", l.UpdatedBefore, "<"}} {
		if b.bound == "" {
			continue
		}
		v, e := stampBound(b.bound)
		if e != nil {
			return e
		}
		w.add("r."+b.column+" "+b.op+" ?", v)
	}
	return nil
}

// stampBound turns an RFC 3339 bound into TimeFormat text that compares with
// stored stamps as the times compare. Stamps are whole microseconds, so for
// both stamp >= bound and stamp < bound the bound rounds up to a microsecond.
func stampBound(s string) (string, error) {
	v, e := time.Parse(time.RFC3339Nano, s)
	if e != nil {
		return "", fmt.Errorf("invalid timestamp bound %q", s)
	}
	v = v.UTC()
	if r := v.Truncate(time.Microsecond); r.Before(v) {
		v = r.Add(time.Microsecond)
	}
	switch {
	case v.Year() < 0:
		return "", nil // before every stamp
	case v.Year() > 9999:
		return "~", nil // after every stamp
	}
	return v.Format(TimeFormat), nil
}
