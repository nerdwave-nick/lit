package service

import (
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// projectContextPath is the one session-free project read. It exists for
// project context (remembering a project for a checkout and validating it
// later), not as a general bypass of the connected-client requirement.
const projectContextPath = "/v1/project-context"

// maxContextReference bounds a project reference in Unicode characters, like a
// completion prefix; a title has at most 128.
const maxContextReference = 300

// projectContextRoute is dispatched before the connected-client gate. It never
// consults X-Lit-Client-ID, registers or reconnects a client, or writes; its
// only output is the explicit projection protocol.ProjectContext.
//
// A request has one of two shapes:
//
//	?project=REF                     resolve a reference like every project read
//	?project_id=UUID&service_id=UUID validate a stored binding
//
// Validation checks the service identity first, in the same request that reads
// the project, so a restart or a replacement service between two requests
// cannot make a binding look valid.
func (s *Server) projectContextRoute(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodGet {
		return protocol.E(405, "method_not_allowed", "project context requires GET")
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return invalid("invalid project context query")
	}
	for key, values := range q {
		switch key {
		case "project", "project_id", "service_id":
		default:
			return invalid("unsupported project context parameter")
		}
		if len(values) != 1 {
			return invalid("duplicate project context parameter")
		}
	}
	var identity protocol.ProjectIdentity
	switch {
	case q.Has("project") && !q.Has("project_id") && !q.Has("service_id"):
		ref := q.Get("project")
		if ref == "" || !utf8.ValidString(ref) || utf8.RuneCountInString(ref) > maxContextReference || strings.ContainsFunc(ref, unicode.IsControl) {
			return invalid("invalid project reference")
		}
		p, e := s.resolveProject(ref)
		if e != nil {
			return e
		}
		identity = protocol.ProjectIdentity{ID: p.ID, Title: p.Title}
	case q.Has("project_id") && q.Has("service_id") && !q.Has("project"):
		id, service := q.Get("project_id"), q.Get("service_id")
		if !protocol.ValidUUID(id) || !protocol.ValidUUID(service) {
			return invalid("project_id and service_id must be full UUIDs")
		}
		if service != s.DB.ServiceID() {
			e := protocol.E(409, "wrong_service", "the stored project belongs to a different service")
			e.Details = map[string]any{"service_id": s.DB.ServiceID()}
			return e
		}
		p, e := s.project(id)
		if e != nil {
			return e
		}
		identity = protocol.ProjectIdentity{ID: p.ID, Title: p.Title}
	default:
		return invalid("give project, or project_id with service_id")
	}
	s.write(w, http.StatusOK, protocol.ProjectContext{ServiceID: s.DB.ServiceID(), APIMajor: 1, Project: identity})
	return nil
}
