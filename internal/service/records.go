package service

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/storage"
	"github.com/nerdwave-nick/lit/internal/textfold"
)

// IssueTitleKey is shared by lookup, uniqueness validation and derived indexes.
func IssueTitleKey(s string) string { return textfold.Fold(s) }
func validateIssueTitle(s string) error {
	if !utf8.ValidString(s) || s == "" || utf8.RuneCountInString(s) > 128 || strings.TrimSpace(s) != s {
		return invalid("invalid issue title")
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return invalid("invalid issue title")
		}
	}
	return nil
}
func recordFields(record any) map[string]json.RawMessage {
	b, _ := json.Marshal(record)
	m := map[string]json.RawMessage{}
	json.Unmarshal(b, &m)
	return m
}
func recordIdentity(record any) (typ, id string, rev int64, created, updated string) {
	switch p := record.(type) {
	case protocol.Project:
		return "projects", p.ID, p.Revision, p.CreatedAt, p.UpdatedAt
	case protocol.Issue:
		return "issues", p.ID, p.Revision, p.CreatedAt, p.UpdatedAt
	case protocol.Comment:
		return "comments", p.ID, p.Revision, p.CreatedAt, p.UpdatedAt
	case protocol.Milestone:
		return "milestones", p.ID, p.Revision, p.CreatedAt, p.UpdatedAt
	}
	panic("unknown record")
}
func decodeRecord(typ string, b []byte) (any, error) {
	switch typ {
	case "projects":
		var p protocol.Project
		e := protocol.Decode(b, &p)
		return p, e
	case "issues":
		var p protocol.Issue
		e := protocol.Decode(b, &p)
		return p, e
	case "comments":
		var p protocol.Comment
		e := protocol.Decode(b, &p)
		return p, e
	case "milestones":
		var p protocol.Milestone
		e := protocol.Decode(b, &p)
		return p, e
	}
	return nil, invalid("unknown resource")
}

// readRecord reads one current record with its computed membership lists.
func (s *Server) readRecord(typ, id string) (any, error) {
	notFound := protocol.E(404, "not_found", "record does not exist")
	if typ == "projects" {
		notFound = protocol.E(404, "not_found", "project does not exist")
	}
	if !protocol.ValidUUID(id) {
		return nil, notFound
	}
	r, e := s.q().Record(s.ctx(), typ, id)
	if errors.Is(e, storage.ErrRecordNotFound) {
		return nil, notFound
	}
	return r, e
}
func (s *Server) project(id string) (protocol.Project, error) {
	p, e := s.readRecord("projects", id)
	v, _ := p.(protocol.Project)
	return v, e
}
func (s *Server) issue(id string) (protocol.Issue, error) {
	p, e := s.readRecord("issues", id)
	v, _ := p.(protocol.Issue)
	return v, e
}
func (s *Server) comment(id string) (protocol.Comment, error) {
	p, e := s.readRecord("comments", id)
	v, _ := p.(protocol.Comment)
	return v, e
}
func (s *Server) milestone(id string) (protocol.Milestone, error) {
	p, e := s.readRecord("milestones", id)
	v, _ := p.(protocol.Milestone)
	return v, e
}

// selectorMode strips an explicit title: or id: prefix.
func selectorMode(selector string, prefixes ...string) (mode, rest string) {
	for _, prefix := range prefixes {
		if strings.HasPrefix(selector, prefix) {
			return prefix, strings.TrimPrefix(selector, prefix)
		}
	}
	return "", selector
}

// resolveIDs finds the records a selector names: the one whose title it is
// (titleOK) and those whose ID starts with it (idOK), deduplicated.
func (s *Server) resolveIDs(typ, selector, titleProject, idProject string, titleOK, idOK bool) ([]string, error) {
	found := map[string]bool{}
	if titleOK {
		id, ok, e := s.q().IDByTitle(s.ctx(), typ, titleProject, selector)
		if e != nil {
			return nil, e
		}
		if ok {
			found[id] = true
		}
	}
	if _, valid := uuidPrefix(selector); idOK && valid {
		ids, e := s.q().IDsByPrefix(s.ctx(), typ, selector, idProject, 2)
		if e != nil {
			return nil, e
		}
		for _, id := range ids {
			found[id] = true
		}
	}
	return sortedKeys(found), nil
}

// resolved reads the single record a selector named.
func (s *Server) resolved(typ, kind string, ids []string) (any, error) {
	if len(ids) == 0 {
		return nil, protocol.E(404, "not_found", kind+" does not exist")
	}
	if len(ids) > 1 {
		return nil, protocol.E(409, "ambiguous_reference", kind+" selector is ambiguous")
	}
	return s.readRecord(typ, ids[0])
}
func (s *Server) resolveProject(selector string) (protocol.Project, error) {
	mode, selector := selectorMode(selector, "id:", "title:")
	ids, e := s.resolveIDs("projects", selector, "", "", mode != "id:", mode != "title:")
	if e != nil {
		return protocol.Project{}, e
	}
	r, e := s.resolved("projects", "project", ids)
	v, _ := r.(protocol.Project)
	return v, e
}
func (s *Server) resolveMilestone(selector, project string) (protocol.Milestone, error) {
	mode, selector := selectorMode(selector, "title:", "id:")
	_, validIDPrefix := uuidPrefix(selector)
	var projectID string
	if project != "" {
		p, e := s.resolveProject(project)
		if e != nil {
			return protocol.Milestone{}, e
		}
		projectID = p.ID
	}
	if projectID == "" && (mode == "title:" || mode == "" && !validIDPrefix) {
		return protocol.Milestone{}, invalid("select a project for milestone title")
	}
	ids, e := s.resolveIDs("milestones", selector, projectID, projectID, mode != "id:" && projectID != "", mode != "title:")
	if e != nil {
		return protocol.Milestone{}, e
	}
	r, e := s.resolved("milestones", "milestone", ids)
	v, _ := r.(protocol.Milestone)
	return v, e
}
func (s *Server) resolveIssue(selector, project string) (protocol.Issue, error) {
	qualified := false
	if !strings.HasPrefix(selector, "title:") && !strings.HasPrefix(selector, "id:") {
		if pos := strings.IndexByte(selector, ':'); pos >= 0 && strings.Contains(selector[:pos], "/") {
			p, e := s.resolveProject("title:" + selector[:pos])
			if e != nil {
				return protocol.Issue{}, e
			}
			project = p.ID
			qualified = true
			selector = selector[pos+1:]
		}
	}
	mode, selector := selectorMode(selector, "title:", "id:")
	if project != "" {
		p, e := s.resolveProject(project)
		if e != nil {
			return protocol.Issue{}, e
		}
		project = p.ID
	}
	// An unqualified ID prefix is global even when a project is selected.
	idProject := ""
	if qualified {
		idProject = project
	}
	ids, e := s.resolveIDs("issues", selector, project, idProject, mode != "id:" && project != "", mode != "title:")
	if e != nil {
		return protocol.Issue{}, e
	}
	r, e := s.resolved("issues", "issue", ids)
	v, _ := r.(protocol.Issue)
	return v, e
}
func (s *Server) resolveComment(selector string) (protocol.Comment, error) {
	selector = strings.TrimPrefix(selector, "id:")
	ids, e := s.resolveIDs("comments", selector, "", "", false, true)
	if e != nil {
		return protocol.Comment{}, e
	}
	r, e := s.resolved("comments", "comment", ids)
	v, _ := r.(protocol.Comment)
	return v, e
}
func sortedSet(v []string, uuid bool) bool {
	if v == nil {
		return false
	}
	for i, s := range v {
		if s == "" || !utf8.ValidString(s) || uuid && !protocol.ValidUUID(s) || i > 0 && v[i-1] >= s {
			return false
		}
	}
	return true
}
func sortedKeys[V any](m map[string]V) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
