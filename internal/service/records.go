package service

import (
	"encoding/json"
	"fmt"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/store"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/textfold"
	"os"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// IssueTitleKey is shared by lookup, uniqueness validation and derived indexes.
func IssueTitleKey(s string) string { return textfold.Fold(s) }
func validateIssueTitle(s string) error {
	if !utf8.ValidString(s) || s == "" || utf8.RuneCountInString(s) > 128 || strings.TrimSpace(s) != s {
		return invalid("invalid issue title")
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return invalid("invalid issue title")
		}
	}
	return nil
}
func recordPath(typ, id string) string { return typ + "/" + id }
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
func recordBody(record any) string {
	switch p := record.(type) {
	case protocol.Project:
		return p.Description
	case protocol.Issue:
		return p.Body
	case protocol.Comment:
		return p.Body
	case protocol.Milestone:
		return p.Body
	}
	return ""
}
func bodyField(typ string) string {
	if typ == "projects" {
		return "description"
	}
	return "body"
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
func issueRelationships(p protocol.Issue) map[string]any {
	return map[string]any{"schema_version": 1, "project_id": p.ProjectID, "parent_id": p.ParentID, "child_ids": p.ChildIDs, "comment_ids": p.CommentIDs, "blocks": p.Blocks, "blocked_by": p.BlockedBy, "related": p.Related}
}
func recordWrites(record any) []store.Write {
	typ, id, _, _, _ := recordIdentity(record)
	if p, ok := record.(protocol.Project); ok {
		return []store.Write{{Path: projectDir(id) + "/content.md", Data: projectBytes(p)}, store.JSONWrite(projectDir(id)+"/relationships.json", projectRelationships{1, p.IssueIDs})}
	}
	if p, ok := record.(protocol.Milestone); ok {
		m := map[string]any{"schema_version": p.SchemaVersion, "id": p.ID, "revision": p.Revision, "created_at": p.CreatedAt, "updated_at": p.UpdatedAt, "title": p.Title}
		return []store.Write{{Path: recordPath(typ, id) + "/content.md", Data: encodeFrontmatter(m, p.Body)}, store.JSONWrite(recordPath(typ, id)+"/relationships.json", map[string]any{"schema_version": 1, "project_id": p.ProjectID, "issue_ids": p.IssueIDs})}
	}
	m := recordFields(record)
	body := recordBody(record)
	delete(m, "body")
	var rel any
	switch p := record.(type) {
	case protocol.Issue:
		rel = issueRelationships(p)
		for _, key := range []string{"project_id", "parent_id", "child_ids", "comment_ids", "blocks", "blocked_by", "related"} {
			delete(m, key)
		}
	case protocol.Comment:
		rel = map[string]any{"schema_version": 1, "issue_id": p.IssueID}
		delete(m, "issue_id")
	}
	content := encodeFrontmatter(m, body)
	return []store.Write{{Path: recordPath(typ, id) + "/content.md", Data: content}, store.JSONWrite(recordPath(typ, id)+"/relationships.json", rel)}
}
func (s *Server) readRecord(typ, id string) (any, error) {
	if !protocol.ValidUUID(id) {
		return nil, protocol.E(404, "not_found", "record does not exist")
	}
	b, e := s.Store.Read(recordPath(typ, id) + "/content.md")
	if os.IsNotExist(e) {
		return nil, protocol.E(404, "not_found", "record does not exist")
	}
	if e != nil {
		return nil, e
	}
	m := map[string]json.RawMessage{}
	body, e := decodeFrontmatter(b, &m)
	if e != nil {
		return nil, e
	}
	metadataFields := []string{"schema_version", "id", "revision", "created_at", "updated_at"}
	if typ == "issues" {
		metadataFields = append(metadataFields, "title", "state", "labels", "assignee")
	} else if typ == "milestones" {
		metadataFields = append(metadataFields, "title")
	} else {
		metadataFields = append(metadataFields, "author")
	}
	if e = validateMetadataFields(m, metadataFields); e != nil {
		return nil, e
	}
	relBytes, e := s.Store.Read(recordPath(typ, id) + "/relationships.json")
	if e != nil {
		return nil, e
	}
	rel := map[string]json.RawMessage{}
	if e = protocol.Decode(relBytes, &rel); e != nil {
		return nil, e
	}
	if string(rel["schema_version"]) != "1" {
		return nil, fmt.Errorf("unsupported relationships version")
	}
	allowed := map[string]bool{"schema_version": true}
	if typ == "issues" {
		for _, k := range []string{"project_id", "parent_id", "child_ids", "comment_ids", "blocks", "blocked_by", "related"} {
			allowed[k] = true
		}
	} else if typ == "milestones" {
		for _, k := range []string{"project_id", "issue_ids"} {
			allowed[k] = true
		}
	} else {
		allowed["issue_id"] = true
	}
	for k := range allowed {
		if _, exists := rel[k]; !exists {
			return nil, fmt.Errorf("missing relationship field %s", k)
		}
	}
	for k, v := range rel {
		if !allowed[k] {
			return nil, fmt.Errorf("unknown relationship field")
		}
		if k == "schema_version" {
			continue
		}
		if _, exists := m[k]; exists {
			return nil, fmt.Errorf("relationship duplicated in metadata")
		}
		m[k] = v
	}
	m["body"], _ = json.Marshal(string(body))
	b, _ = json.Marshal(m)
	p, e := decodeRecord(typ, b)
	if e != nil {
		return nil, e
	}
	_, rid, rev, _, _ := recordIdentity(p)
	if rid != id || rev < 1 || string(m["schema_version"]) != "1" {
		return nil, fmt.Errorf("invalid record identity or version")
	}
	return p, nil
}
func (s *Server) Issue(id string) (protocol.Issue, error) {
	p, e := s.readRecord("issues", id)
	if e != nil {
		return protocol.Issue{}, e
	}
	return p.(protocol.Issue), nil
}
func (s *Server) Comment(id string) (protocol.Comment, error) {
	p, e := s.readRecord("comments", id)
	if e != nil {
		return protocol.Comment{}, e
	}
	return p.(protocol.Comment), nil
}
func (s *Server) Milestone(id string) (protocol.Milestone, error) {
	p, e := s.readRecord("milestones", id)
	if e != nil {
		return protocol.Milestone{}, e
	}
	return p.(protocol.Milestone), nil
}
func (s *Server) Records(typ string) ([]any, error) {
	if !protocol.ResourceType(typ) {
		return nil, invalid("unknown resource")
	}
	out := []any{}
	entries, e := s.Store.List(typ)
	if e != nil {
		return nil, e
	}
	for _, entry := range entries {
		if !entry.IsDir() || !protocol.ValidUUID(entry.Name()) {
			return nil, fmt.Errorf("invalid record directory")
		}
		var p any
		if typ == "projects" {
			p, e = s.Project(entry.Name())
		} else {
			p, e = s.readRecord(typ, entry.Name())
		}
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, nil
}
func (s *Server) Issues() ([]protocol.Issue, error) {
	rs, e := s.Records("issues")
	out := []protocol.Issue{}
	for _, r := range rs {
		out = append(out, r.(protocol.Issue))
	}
	return out, e
}
func (s *Server) Comments() ([]protocol.Comment, error) {
	rs, e := s.Records("comments")
	out := []protocol.Comment{}
	for _, r := range rs {
		out = append(out, r.(protocol.Comment))
	}
	return out, e
}
func (s *Server) Milestones() ([]protocol.Milestone, error) {
	rs, e := s.Records("milestones")
	out := []protocol.Milestone{}
	for _, r := range rs {
		out = append(out, r.(protocol.Milestone))
	}
	return out, e
}
func (s *Server) ResolveMilestone(selector, project string) (protocol.Milestone, error) {
	mode := ""
	for _, prefix := range []string{"title:", "id:"} {
		if strings.HasPrefix(selector, prefix) {
			mode = prefix
			selector = strings.TrimPrefix(selector, prefix)
			break
		}
	}
	idPrefix, validIDPrefix := uuidPrefix(selector)
	var projectID string
	if project != "" {
		p, e := s.ResolveProject(project)
		if e != nil {
			return protocol.Milestone{}, e
		}
		projectID = p.ID
	}
	if projectID == "" && (mode == "title:" || mode == "" && !validIDPrefix) {
		return protocol.Milestone{}, invalid("select a project for milestone title")
	}
	found := []protocol.Milestone{}
	all, e := s.Milestones()
	if e != nil {
		return protocol.Milestone{}, e
	}
	for _, m := range all {
		if projectID != "" && m.ProjectID != projectID {
			continue
		}
		byTitle := mode != "id:" && projectID != "" && m.ProjectID == projectID && IssueTitleKey(selector) == IssueTitleKey(m.Title)
		byID := mode != "title:" && validIDPrefix && strings.HasPrefix(strings.ReplaceAll(m.ID, "-", ""), idPrefix)
		if byTitle || byID {
			found = append(found, m)
		}
	}
	if len(found) == 0 {
		return protocol.Milestone{}, protocol.E(404, "not_found", "milestone does not exist")
	}
	if len(found) > 1 {
		return protocol.Milestone{}, protocol.E(409, "ambiguous_reference", "milestone selector is ambiguous")
	}
	return found[0], nil
}
func (s *Server) ResolveIssue(selector, project string) (protocol.Issue, error) {
	mode := ""
	qualified := false
	if !strings.HasPrefix(selector, "title:") && !strings.HasPrefix(selector, "id:") {
		if pos := strings.IndexByte(selector, ':'); pos >= 0 && strings.Contains(selector[:pos], "/") {
			p, e := s.ResolveProject("title:" + selector[:pos])
			if e != nil {
				return protocol.Issue{}, e
			}
			project = p.ID
			qualified = true
			selector = selector[pos+1:]
		}
	}
	for _, prefix := range []string{"title:", "id:"} {
		if strings.HasPrefix(selector, prefix) {
			mode = prefix
			selector = strings.TrimPrefix(selector, prefix)
			break
		}
	}
	if project != "" {
		p, e := s.ResolveProject(project)
		if e != nil {
			return protocol.Issue{}, e
		}
		project = p.ID
	}
	prefix, valid := uuidPrefix(selector)
	ps, e := s.Issues()
	if e != nil {
		return protocol.Issue{}, e
	}
	found := []protocol.Issue{}
	for _, p := range ps {
		byTitle := mode != "id:" && project != "" && p.ProjectID == project && IssueTitleKey(p.Title) == IssueTitleKey(selector)
		byID := mode != "title:" && valid && (!qualified || p.ProjectID == project) && strings.HasPrefix(strings.ReplaceAll(p.ID, "-", ""), prefix)
		if byTitle || byID {
			found = append(found, p)
		}
	}
	if len(found) == 0 {
		return protocol.Issue{}, protocol.E(404, "not_found", "issue does not exist")
	}
	if len(found) > 1 {
		return protocol.Issue{}, protocol.E(409, "ambiguous_reference", "issue selector is ambiguous")
	}
	return found[0], nil
}
func (s *Server) ResolveComment(selector string) (protocol.Comment, error) {
	selector = strings.TrimPrefix(selector, "id:")
	prefix, valid := uuidPrefix(selector)
	ps, e := s.Comments()
	if e != nil {
		return protocol.Comment{}, e
	}
	found := []protocol.Comment{}
	for _, p := range ps {
		if valid && strings.HasPrefix(strings.ReplaceAll(p.ID, "-", ""), prefix) {
			found = append(found, p)
		}
	}
	if len(found) == 0 {
		return protocol.Comment{}, protocol.E(404, "not_found", "comment does not exist")
	}
	if len(found) > 1 {
		return protocol.Comment{}, protocol.E(409, "ambiguous_reference", "comment selector is ambiguous")
	}
	return found[0], nil
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
