package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

func (q *ProjectQuery) UnmarshalJSON(b []byte) error {
	type plain ProjectQuery
	var fields map[string]json.RawMessage
	if e := protocol.Decode(b, &fields); e != nil {
		return e
	}
	if fields == nil {
		return fmt.Errorf("query must be an object")
	}
	for k, v := range fields {
		if string(v) == "null" && k != "parent_id" && k != "assignee" {
			return fmt.Errorf("query.%s cannot be null", k)
		}
	}
	var v plain
	if e := protocol.Decode(b, &v); e != nil {
		return e
	}
	*q = ProjectQuery(v)
	if string(fields["parent_id"]) == "null" {
		q.ParentID = nullable(nil)
	}
	if string(fields["assignee"]) == "null" {
		q.Assignee = nullable(nil)
	}
	q.Fields = map[string]bool{}
	for k := range fields {
		q.Fields[k] = true
	}
	return nil
}
func validateQueryFields(q ProjectQuery) error {
	allowed := map[string]bool{}
	for _, k := range []string{"type", "sort", "direction", "limit", "cursor", "all", "id", "q", "created_after", "created_before", "updated_after", "updated_before"} {
		allowed[k] = true
	}
	if q.Type != "comments" {
		allowed["title"] = true
	}
	switch q.Type {
	case "projects":
		allowed["repository_ref"] = true
	case "issues":
		for _, k := range []string{"project_id", "all_projects", "parent_id", "state", "labels_all", "labels_any", "labels_none", "assignee", "blocked", "claimed", "owner_client_id", "milestone_id"} {
			allowed[k] = true
		}
	case "milestones":
		allowed["project_id"] = true
	case "comments":
		allowed["issue_id"] = true
		allowed["author"] = true
	}
	b, _ := json.Marshal(q)
	var fields map[string]json.RawMessage
	json.Unmarshal(b, &fields)
	for k := range q.Fields {
		fields[k] = nil
	}
	for k := range fields {
		if !allowed[k] {
			return protocol.E(400, "unsupported_filter", "inapplicable query field: "+k)
		}
	}
	return nil
}

func (s *Server) resolveRecord(typ, selector, project string) (any, error) {
	switch typ {
	case "projects":
		return s.resolveProject(selector)
	case "issues":
		return s.resolveIssue(selector, project)
	case "comments":
		return s.resolveComment(selector)
	case "milestones":
		return s.resolveMilestone(selector, project)
	}
	return nil, invalid("unsupported resource")
}
func (s *Server) recordRoute(w http.ResponseWriter, r *http.Request, c protocol.Client) (bool, error) {
	typ := ""
	for _, t := range []string{"issues", "comments", "milestones"} {
		if r.URL.Path == "/v1/"+t || strings.HasPrefix(r.URL.Path, "/v1/"+t+"/") {
			typ = t
			break
		}
	}
	if typ == "" {
		return false, nil
	}
	project := r.URL.Query().Get("project_id")
	if project == "" && c.ProjectID != nil {
		project = *c.ProjectID
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/"+typ)
	if path == "" {
		switch r.Method {
		case "POST":
			return true, s.executeProjects(w, r, c, "", "create")
		case "GET":
			q, e := recordURLQuery(typ, r.URL.Query())
			if e != nil {
				return true, e
			}
			if q.ProjectID == "" && (typ == "issues" || typ == "milestones") && !q.AllProjects {
				q.ProjectID = project
			}
			page, e := s.recordPage(q)
			if e != nil {
				return true, e
			}
			return true, s.writeReadPage(w, page)
		}
		return true, protocol.E(404, "not_found", "unknown route")
	}
	selector := strings.TrimPrefix(path, "/")
	// History routes use UUID owners only, allowing slash-bearing literal titles.
	segments := strings.Split(selector, "/")
	if typ == "issues" && len(segments) == 2 && protocol.ValidUUID(segments[0]) && segments[1] == "relationship-changes" {
		if r.Method != "POST" {
			return true, protocol.E(404, "not_found", "unknown route")
		}
		return true, s.executeProjects(w, r, c, segments[0], "relationships")
	}
	if len(segments) >= 2 && protocol.ValidUUID(segments[0]) && (segments[1] == "history" || segments[1] == "requests") {
		if r.Method != "GET" || len(segments) > 3 {
			return true, protocol.E(404, "not_found", "unknown route")
		}
		owner, e := s.resolveRecord(typ, "id:"+segments[0], project)
		if e != nil {
			if pe, ok := e.(*protocol.Error); ok && pe.Code == "not_found" && segments[1] == "requests" && len(segments) == 3 {
				s.write(w, 200, map[string]any{"outcome": "not_recorded_here", "revision": nil})
				return true, nil
			}
			return true, e
		}
		if len(segments) == 2 && segments[1] == "history" {
			values := r.URL.Query()
			// A milestone's project_id scopes the owner lookup above; it is not a
			// history pagination option. Strip it only after that scoped resolution.
			if typ == "milestones" {
				values.Del("project_id")
			}
			page, e := s.historyPage(typ, segments[0], typ+":"+segments[0], values)
			if e != nil {
				return true, e
			}
			return true, s.writeReadPage(w, page)
		}
		if len(segments) != 3 {
			return true, invalid("request hash required")
		}
		return true, s.historyEntry(w, typ, segments[0], segments[2], segments[1] == "requests", recordRevision(owner))
	}
	owner, e := s.resolveRecord(typ, selector, project)
	if e != nil {
		return true, e
	}
	_, id, _, _, _ := recordIdentity(owner)
	switch r.Method {
	case "GET":
		projection, e := s.readProjection(owner)
		if e != nil {
			return true, e
		}
		s.write(w, 200, projection)
		return true, nil
	case "PATCH":
		return true, s.executeProjects(w, r, c, id, "update")
	}
	return true, protocol.E(404, "not_found", "unknown route")
}
func recordURLQuery(typ string, values url.Values) (ProjectQuery, error) {
	fields := map[string]any{"type": typ}
	for k, vs := range values {
		switch k {
		case "id", "repository_ref", "labels_all", "labels_any", "labels_none":
			fields[k] = vs
		default:
			if len(vs) != 1 {
				return ProjectQuery{}, invalid("duplicate query parameter")
			}
			v := vs[0]
			switch k {
			case "all", "all_projects", "blocked", "claimed":
				if v != "true" && v != "false" {
					return ProjectQuery{}, invalid("invalid boolean filter")
				}
				fields[k] = v == "true"
			case "limit":
				n, e := strconv.Atoi(v)
				if e != nil {
					return ProjectQuery{}, invalid("invalid limit")
				}
				fields[k] = n
			case "parent_id", "assignee":
				if v == "none" {
					fields[k] = nil
				} else {
					fields[k] = v
				}
			default:
				fields[k] = v
			}
		}
	}
	var q ProjectQuery
	if e := protocol.Decode(mustJSON(fields), &q); e != nil {
		return q, protocol.E(400, "unsupported_filter", e.Error())
	}
	return q, validateQueryFields(q)
}

func (p *ProjectSnapshot) UnmarshalJSON(b []byte) error {
	type plain ProjectSnapshot
	var fields map[string]json.RawMessage
	if e := protocol.Decode(b, &fields); e != nil {
		return e
	}
	if fields == nil {
		return fmt.Errorf("snapshot must be an object")
	}
	for k, v := range fields {
		if string(v) == "null" {
			return fmt.Errorf("snapshot.%s cannot be null", k)
		}
	}
	_, targets := fields["targets"]
	_, query := fields["query"]
	if targets == query {
		return fmt.Errorf("snapshot requires exactly targets or query")
	}
	var value plain
	if e := protocol.Decode(b, &value); e != nil {
		return e
	}
	*p = ProjectSnapshot(value)
	return nil
}
