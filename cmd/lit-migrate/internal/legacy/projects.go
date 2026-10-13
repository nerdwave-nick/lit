// Frozen copy of the legacy lit-server service at c5af73e (internal/service/projects.go);
// see doc.go. Do not change it.

package legacy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

type projectMetadata struct {
	SchemaVersion  int      `json:"schema_version"`
	ID             string   `json:"id"`
	Revision       int64    `json:"revision"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
	Title          string   `json:"title"`
	RepositoryRefs []string `json:"repository_refs"`
}
type projectRelationships struct {
	SchemaVersion int      `json:"schema_version"`
	IssueIDs      []string `json:"issue_ids"`
}
type TitleIndex struct {
	SchemaVersion int                          `json:"schema_version"`
	Projects      map[string]string            `json:"projects"`
	Issues        map[string]map[string]string `json:"issues"`
}
type BodyHunk struct {
	Before string `json:"before"`
	After  string `json:"after"`
}
type FieldDifference struct {
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}
type ProjectHistory struct {
	SchemaVersion  int                        `json:"schema_version"`
	OwnerID        string                     `json:"owner_id"`
	BeforeRevision int64                      `json:"before_revision"`
	Revision       int64                      `json:"revision"`
	Timestamp      string                     `json:"timestamp"`
	RequestHash    string                     `json:"request_hash"`
	Intent         protocol.Intent            `json:"intent"`
	Results        []protocol.ChangedObject   `json:"results"`
	Differences    map[string]FieldDifference `json:"differences"`
	BodyHunks      []BodyHunk                 `json:"body_hunks"`
}

const historyTimeFormat = "2006-01-02T15:04:05.000000Z"

func projectDir(id string) string { return "projects/" + id }
func projectBytes(p protocol.Project) []byte {
	m := projectMetadata{p.SchemaVersion, p.ID, p.Revision, p.CreatedAt, p.UpdatedAt, p.Title, p.RepositoryRefs}
	return encodeFrontmatter(m, p.Description)
}
func (s *Server) Project(id string) (protocol.Project, error) {
	var p protocol.Project
	if !protocol.ValidUUID(id) {
		return p, protocol.E(404, "not_found", "project does not exist")
	}
	b, e := s.Store.Read(projectDir(id) + "/content.md")
	if os.IsNotExist(e) {
		return p, protocol.E(404, "not_found", "project does not exist")
	}
	if e != nil {
		return p, e
	}
	var fields map[string]json.RawMessage
	body, e := decodeFrontmatter(b, &fields)
	if e != nil {
		return p, e
	}
	if e = validateMetadataFields(fields, []string{"schema_version", "id", "revision", "created_at", "updated_at", "title", "repository_refs"}); e != nil {
		return p, e
	}
	var m projectMetadata
	metadata, _ := json.Marshal(fields)
	if e = protocol.Decode(metadata, &m); e != nil {
		return p, e
	}
	var r projectRelationships
	b, e = s.Store.Read(projectDir(id) + "/relationships.json")
	if e != nil {
		return p, e
	}
	if e = protocol.Decode(b, &r); e != nil {
		return p, e
	}
	p = protocol.Project{SchemaVersion: m.SchemaVersion, ID: m.ID, Revision: m.Revision, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, Title: m.Title, Description: string(body), RepositoryRefs: m.RepositoryRefs, IssueIDs: r.IssueIDs}
	if p.SchemaVersion != 1 || r.SchemaVersion != 1 || p.ID != id || p.Revision < 1 || s.validateProject(p) != nil {
		return p, fmt.Errorf("invalid project record")
	}
	return p, nil
}

var segmentRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func (s *Server) validateProject(p protocol.Project) error {
	return validateProjectPrefixes(p, s.Config.TitlePrefixes)
}
func validateProjectPrefixes(p protocol.Project, prefixes []string) error {
	if !utf8.ValidString(p.Description) {
		return invalid("description must be valid UTF-8")
	}
	parts := strings.Split(p.Title, "/")
	if len(p.Title) > 128 || len(parts) < 2 {
		return protocol.E(422, "validation_failed", "invalid project title")
	}
	allowed := false
	for _, v := range prefixes {
		allowed = allowed || parts[0] == v
	}
	if !allowed {
		return protocol.E(422, "validation_failed", "project title prefix not allowed")
	}
	for _, part := range parts {
		if !segmentRE.MatchString(part) {
			return protocol.E(422, "validation_failed", "invalid project title segment")
		}
	}
	for i, v := range p.RepositoryRefs {
		if v == "" || (i > 0 && p.RepositoryRefs[i-1] >= v) {
			return protocol.E(422, "validation_failed", "invalid repository set")
		}
	}
	for i, v := range p.IssueIDs {
		if !protocol.ValidUUID(v) || (i > 0 && p.IssueIDs[i-1] >= v) {
			return fmt.Errorf("invalid project memberships")
		}
	}
	return nil
}
func titleIndex(ps []protocol.Project) (TitleIndex, error) {
	v := TitleIndex{1, map[string]string{}, map[string]map[string]string{}}
	for _, p := range ps {
		k := strings.ToLower(p.Title)
		if _, ok := v.Projects[k]; ok {
			return v, protocol.E(422, "validation_failed", "duplicate project title")
		}
		v.Projects[k] = p.ID
	}
	return v, nil
}
func same(a, b any) bool { x, _ := json.Marshal(a); y, _ := json.Marshal(b); return bytes.Equal(x, y) }

// Validate all authoritative sources before rebuilding the disposable name index.
func (s *Server) InitProjects() error { return s.InitRecords() }
