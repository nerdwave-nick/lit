package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/storage"
)

// ProjectHistory is one revision of any record as the history API returns it.
type ProjectHistory = storage.HistoryEntry

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
func uuidPrefix(s string) (string, bool) {
	if s == "" {
		return "", false
	}
	for i, r := range s {
		if r == '-' {
			if i != 8 && i != 13 && i != 18 && i != 23 {
				return "", false
			}
		} else if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return "", false
		}
	}
	v := strings.ToLower(strings.ReplaceAll(s, "-", ""))
	return v, len(v) > 0 && len(v) <= 32
}
func same(a, b any) bool { x, _ := json.Marshal(a); y, _ := json.Marshal(b); return bytes.Equal(x, y) }
