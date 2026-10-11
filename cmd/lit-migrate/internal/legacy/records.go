// Frozen copy of the legacy lit-server service at c5af73e (internal/service/records.go);
// see doc.go. Do not change it.

package legacy

import (
	"encoding/json"
	"fmt"
	store "github.com/nerdwave-nick/nerdwave-workflows/cmd/lit-migrate/internal/legacystore"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"os"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// IssueTitleKey is shared by lookup, uniqueness validation and derived indexes.
func IssueTitleKey(s string) string { return norm.NFC.String(cases.Fold().String(norm.NFC.String(s))) }
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
