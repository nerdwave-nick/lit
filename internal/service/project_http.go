package service

import (
	"encoding/json"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"net/http"
	"net/url"
	"strings"
)

type ProjectQuery struct {
	ID            []string        `json:"id,omitempty"`
	Title         string          `json:"title,omitempty"`
	Q             string          `json:"q,omitempty"`
	RepositoryRef []string        `json:"repository_ref,omitempty"`
	ParentID      **string        `json:"parent_id,omitempty"`
	Assignee      **string        `json:"assignee,omitempty"`
	State         string          `json:"state,omitempty"`
	LabelsAll     []string        `json:"labels_all,omitempty"`
	LabelsAny     []string        `json:"labels_any,omitempty"`
	LabelsNone    []string        `json:"labels_none,omitempty"`
	Blocked       *bool           `json:"blocked,omitempty"`
	Claimed       *bool           `json:"claimed,omitempty"`
	OwnerClientID string          `json:"owner_client_id,omitempty"`
	Author        string          `json:"author,omitempty"`
	CreatedAfter  string          `json:"created_after,omitempty"`
	CreatedBefore string          `json:"created_before,omitempty"`
	UpdatedAfter  string          `json:"updated_after,omitempty"`
	UpdatedBefore string          `json:"updated_before,omitempty"`
	Fields        map[string]bool `json:"-"`
	Type          string          `json:"type"`
	ProjectID     string          `json:"project_id,omitempty"`
	IssueID       string          `json:"issue_id,omitempty"`
	MilestoneID   string          `json:"milestone_id,omitempty"`
	AllProjects   bool            `json:"all_projects,omitempty"`
	Sort          string          `json:"sort,omitempty"`
	Direction     string          `json:"direction,omitempty"`
	Limit         int             `json:"limit,omitempty"`
	Cursor        string          `json:"cursor,omitempty"`
	All           bool            `json:"all,omitempty"`
}
type SnapshotTarget struct {
	Type     string `json:"type"`
	Selector string `json:"selector"`
	Project  string `json:"project,omitempty"`
}
type ProjectSnapshot struct {
	SchemaVersion int              `json:"schema_version"`
	Targets       []SnapshotTarget `json:"targets,omitempty"`
	Query         *ProjectQuery    `json:"query,omitempty"`
}
type ProjectPage struct {
	Items      []any   `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func (s *Server) projectRoute(w http.ResponseWriter, r *http.Request, c protocol.Client) (bool, error) {
	switch r.URL.Path {
	case "/v1/transaction-previews":
		if r.Method != "POST" {
			break
		}
		var req protocol.PrepareRequest
		if e := s.Body(r, &req); e != nil {
			return true, e
		}
		out, e := s.prepareRecords(req, c)
		if e != nil {
			return true, e
		}
		s.write(w, 200, out)
		return true, nil
	case "/v1/transactions":
		if r.Method == "POST" {
			return true, s.executeProjects(w, r, c, "", "")
		}
	case "/v1/snapshots":
		if r.Method != "POST" {
			break
		}
		var req ProjectSnapshot
		if e := s.Body(r, &req); e != nil {
			return true, e
		}
		if req.SchemaVersion != 0 && req.SchemaVersion != 1 {
			return true, invalid("unsupported schema_version")
		}
		if req.Query != nil {
			if len(req.Targets) > 0 {
				return true, invalid("targets conflict with query")
			}
			if (req.Query.Type == "issues" || req.Query.Type == "milestones") && req.Query.ProjectID == "" && !req.Query.AllProjects && c.ProjectID != nil {
				req.Query.ProjectID = *c.ProjectID
			}
			page, e := s.recordPage(*req.Query)
			if e != nil {
				return true, e
			}
			return true, s.writeReadPage(w, page)
		}
		if len(req.Targets) == 0 {
			return true, invalid("empty snapshot targets")
		}
		if len(req.Targets) > s.Config.Limits.ExplicitItems {
			return true, protocol.E(413, "limit_exceeded", "too many snapshot targets")
		}
		page := ProjectPage{Items: []any{}}
		seen := map[string]bool{}
		for _, target := range req.Targets {
			project := target.Project
			if project == "" && c.ProjectID != nil {
				project = *c.ProjectID
			}
			p, e := s.resolveRecord(target.Type, target.Selector, project)
			if e != nil {
				return true, e
			}
			typ, id, _, _, _ := recordIdentity(p)
			key := recordKey(typ, id)
			if !seen[key] {
				page.Items = append(page.Items, p)
				seen[key] = true
			}
		}
		return true, s.writeReadPage(w, page)
	case "/v1/projects":
		if r.Method == "POST" {
			return true, s.executeProjects(w, r, c, "", "create")
		}
		if r.Method == "GET" {
			q, e := projectURLQuery(r.URL.Query())
			if e != nil {
				return true, e
			}
			p, e := s.projectPage(q)
			if e != nil {
				return true, e
			}
			return true, s.writeReadPage(w, p)
		}
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/projects/") {
		return false, nil
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/projects/")
	// Exact project titles may contain a history or requests segment.
	if r.Method == "GET" || r.Method == "PATCH" {
		if project, e := s.resolveProject(path); e == nil {
			if r.Method == "PATCH" {
				return true, s.executeProjects(w, r, c, project.ID, "update")
			}
			s.write(w, 200, project)
			return true, nil
		} else if pe, ok := e.(*protocol.Error); !ok || pe.Code != "not_found" {
			return true, e
		}
	}
	// UUID owners keep history unambiguous even when titles contain slash segments.
	for _, suffix := range []string{"/history", "/requests/"} {
		if pos := strings.Index(path, suffix); pos >= 0 {
			selector := path[:pos]
			tail := path[pos+len(suffix):]
			p, e := s.resolveProject(selector)
			if e != nil {
				if suffix == "/requests/" && protocol.ValidUUID(selector) {
					if pe, ok := e.(*protocol.Error); ok && pe.Code == "not_found" {
						s.write(w, 200, map[string]any{"outcome": "not_recorded_here", "revision": nil})
						return true, nil
					}
				}
				return true, e
			}
			if r.Method != "GET" {
				return true, protocol.E(404, "not_found", "unknown route")
			}
			hash := strings.TrimPrefix(tail, "/")
			if hash == "" && suffix == "/history" {
				page, e := s.historyPage("projects", p.ID, p.ID, r.URL.Query())
				if e != nil {
					return true, e
				}
				return true, s.writeReadPage(w, page)
			}
			return true, s.historyEntry(w, "projects", p.ID, hash, suffix == "/requests/", p.Revision)
		}
	}
	p, e := s.resolveProject(path)
	if e != nil {
		return true, e
	}
	switch r.Method {
	case "GET":
		s.write(w, 200, p)
		return true, nil
	case "PATCH":
		return true, s.executeProjects(w, r, c, p.ID, "update")
	}
	return true, protocol.E(404, "not_found", "unknown route")
}
func projectURLQuery(v url.Values) (ProjectQuery, error) { return recordURLQuery("projects", v) }

func (s *Server) projectPage(q ProjectQuery) (ProjectPage, error) { return s.recordPage(q) }

func (s *Server) writeProjectPage(w http.ResponseWriter, p ProjectPage) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Items      []any   `json:"items"`
		NextCursor *string `json:"next_cursor"`
		ServerTime string  `json:"server_time"`
	}{p.Items, p.NextCursor, s.responseTime()})
}
