package litclient

import (
	"context"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// Record is a durable record type readable through snapshots.
type Record interface {
	protocol.Project | protocol.Issue | protocol.Comment | protocol.Milestone
}

// Query is a typed snapshot query (POST /v1/snapshots). The record type is
// implied by the type parameter of List. Zero fields are omitted; the service
// rejects filters inapplicable to the record type. Issue and milestone queries
// without ProjectID or AllProjects use the client's selected project.
type Query struct {
	ProjectID   string `json:"project_id,omitempty"`
	IssueID     string `json:"issue_id,omitempty"`
	MilestoneID string `json:"milestone_id,omitempty"`
	AllProjects bool   `json:"all_projects,omitempty"`

	ID            []string `json:"id,omitempty"`
	Title         string   `json:"title,omitempty"`
	Q             string   `json:"q,omitempty"`
	RepositoryRef []string `json:"repository_ref,omitempty"`
	// ParentID and Assignee filter by value; a non-nil pointer to nil matches
	// records without a parent or assignee.
	ParentID      **string `json:"parent_id,omitempty"`
	Assignee      **string `json:"assignee,omitempty"`
	State         string   `json:"state,omitempty"`
	LabelsAll     []string `json:"labels_all,omitempty"`
	LabelsAny     []string `json:"labels_any,omitempty"`
	LabelsNone    []string `json:"labels_none,omitempty"`
	Blocked       *bool    `json:"blocked,omitempty"`
	Claimed       *bool    `json:"claimed,omitempty"`
	OwnerClientID string   `json:"owner_client_id,omitempty"`
	Author        string   `json:"author,omitempty"`
	CreatedAfter  string   `json:"created_after,omitempty"`
	CreatedBefore string   `json:"created_before,omitempty"`
	UpdatedAfter  string   `json:"updated_after,omitempty"`
	UpdatedBefore string   `json:"updated_before,omitempty"`

	// Sort is created_at (default), updated_at or title; Direction asc or desc.
	Sort      string `json:"sort,omitempty"`
	Direction string `json:"direction,omitempty"`
	// Limit bounds one page; Cursor continues the same query from a previous
	// page's NextCursor. All returns every match as one bounded snapshot and
	// conflicts with Limit and Cursor.
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
	All    bool   `json:"all,omitempty"`
}

// Page is one page of a query. A non-nil NextCursor continues the same query.
type Page[T Record] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func recordType[T Record]() string {
	var v T
	switch any(v).(type) {
	case protocol.Project:
		return "projects"
	case protocol.Issue:
		return "issues"
	case protocol.Comment:
		return "comments"
	}
	return "milestones"
}

func validRecord(v any) bool {
	switch r := v.(type) {
	case protocol.Project:
		return r.SchemaVersion == 1 && protocol.ValidUUID(r.ID)
	case protocol.Issue:
		return r.SchemaVersion == 1 && protocol.ValidUUID(r.ID)
	case protocol.Comment:
		return r.SchemaVersion == 1 && protocol.ValidUUID(r.ID)
	case protocol.Milestone:
		return r.SchemaVersion == 1 && protocol.ValidUUID(r.ID)
	}
	return false
}

func snapshot[T Record](s *Session, body any) (Page[T], error) {
	var page Page[T]
	if e := s.Call("POST", "/v1/snapshots", body, nil, &page, false); e != nil {
		return Page[T]{}, e
	}
	if page.Items == nil {
		return Page[T]{}, protocol.E(500, "invalid_response", "snapshot omitted items")
	}
	for _, v := range page.Items {
		if !validRecord(any(v)) {
			return Page[T]{}, protocol.E(500, "invalid_response", "snapshot returned an invalid "+recordType[T]()+" record")
		}
	}
	return page, nil
}

// Get reads a consistent snapshot of the records named by selectors (titles,
// UUIDs or UUID prefixes), in request order. project scopes issue and milestone
// selectors; "" uses the client's selected project. Duplicate references yield
// one record. A missing record fails the whole read (not_found).
func Get[T Record](ctx context.Context, s *Session, project string, selectors ...string) ([]T, error) {
	if len(selectors) == 0 {
		return nil, protocol.E(400, "invalid_arguments", "targets required")
	}
	defer s.bind(ctx)()
	typ := recordType[T]()
	targets := make([]map[string]string, 0, len(selectors))
	for _, selector := range selectors {
		target := map[string]string{"type": typ, "selector": selector}
		if project != "" && typ != "projects" {
			target["project"] = project
		}
		targets = append(targets, target)
	}
	page, e := snapshot[T](s, map[string]any{"schema_version": 1, "targets": targets})
	return page.Items, e
}

// List reads one page of records matching q.
func List[T Record](ctx context.Context, s *Session, q Query) (Page[T], error) {
	defer s.bind(ctx)()
	typed := struct {
		Type string `json:"type"`
		Query
	}{recordType[T](), q}
	return snapshot[T](s, map[string]any{"schema_version": 1, "query": typed})
}
