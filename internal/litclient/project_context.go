package litclient

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/nerdwave-nick/lit/internal/clientendpoint"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// ProjectBinding is a stored project identity to validate: the full lowercase
// UUID of the project and of the service that issued it. Titles are display
// text and are not part of the identity.
type ProjectBinding struct {
	ServiceID string
	ProjectID string
}

// ProjectContext is what a single service response established: the service
// that answered, and the project it resolved with its current title. Both IDs
// are valid UUIDs and Title is not empty.
type ProjectContext struct {
	ServiceID string
	ProjectID string
	Title     string
}

// ProjectContextClient reads GET /v1/project-context from one service. It
// needs no logical session: it holds no mapping or lock, registers no client,
// and never writes local state, so it is safe to call from offline-first
// commands and hooks. Each call sends one GET request and nothing else.
//
// Errors are *protocol.Error values; branch on Code(err). Besides the
// service's own codes (not_found, ambiguous_reference, wrong_service,
// validation_failed, storage_busy, shutting_down, ...):
//
//	invalid_arguments     the caller's reference or binding is unusable; nothing was sent
//	invalid_endpoint      Endpoint is not an HTTP(S) origin; nothing was sent
//	service_unavailable   transport failure, timeout or cancelled context
//	incompatible_service  the service predates the operation, speaks another API
//	                      major or reports an invalid service identity
//	invalid_response      the answer is malformed or contradicts the request
//	wrong_service         the service that answered is not the bound one
type ProjectContextClient struct {
	// Endpoint is the service origin, for example "http://127.0.0.1:7411".
	// It is used as given: choosing among a flag, LIT_ENDPOINT, a saved
	// endpoint and the default is the caller's job (see clientendpoint.Resolve).
	Endpoint string
	// HTTP is the client used for the request. Nil uses a client with a 30
	// second timeout. The call's context bounds the request as well.
	HTTP *http.Client
}

const projectContextTimeout = 30 * time.Second

// Resolve finds the project a reference names, using the selectors every
// project command accepts (title, UUID or unique UUID prefix, with the
// optional "title:" and "id:" forms). It fails with not_found for a missing
// project and ambiguous_reference for a prefix that names several.
func (c ProjectContextClient) Resolve(ctx context.Context, ref string) (ProjectContext, error) {
	if ref == "" {
		return ProjectContext{}, protocol.E(400, "invalid_arguments", "a project reference is required")
	}
	got, err := c.read(ctx, url.Values{"project": {ref}})
	if err != nil {
		return ProjectContext{}, err
	}
	return got, nil
}

// Validate checks a stored binding against the service at Endpoint, in one
// request: the service compares its own identity with b.ServiceID before it
// looks up b.ProjectID, so a replacement service at the same address is
// refused with wrong_service and a project that is gone with not_found. A
// renamed project is valid and returns its current title. The returned
// context equals the binding plus the title.
func (c ProjectContextClient) Validate(ctx context.Context, b ProjectBinding) (ProjectContext, error) {
	if !protocol.ValidUUID(b.ServiceID) || !protocol.ValidUUID(b.ProjectID) {
		return ProjectContext{}, protocol.E(400, "invalid_arguments", "service and project IDs must be full lowercase UUIDs")
	}
	got, err := c.read(ctx, url.Values{"project_id": {b.ProjectID}, "service_id": {b.ServiceID}})
	if err != nil {
		return ProjectContext{}, err
	}
	if got.ServiceID != b.ServiceID {
		e := protocol.E(409, "wrong_service", "the service that answered is not the one the project is bound to")
		e.Details = map[string]any{"service_id": got.ServiceID}
		return ProjectContext{}, e
	}
	if got.ProjectID != b.ProjectID {
		return ProjectContext{}, protocol.E(500, "invalid_response", "the service answered with a different project than the one asked for")
	}
	return got, nil
}

func (c ProjectContextClient) read(ctx context.Context, query url.Values) (ProjectContext, error) {
	endpoint, err := clientendpoint.Resolve(c.Endpoint, true, "", "")
	if err != nil {
		return ProjectContext{}, protocol.E(400, "invalid_endpoint", err.Error())
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: projectContextTimeout}
	}
	// A zero Session sends no client header and journals nothing for a read.
	session := &Session{Endpoint: endpoint, HTTP: httpClient, Context: ctx}
	var wire protocol.ProjectContext
	if err = session.Call(http.MethodGet, "/v1/project-context?"+query.Encode(), nil, nil, &wire, false); err != nil {
		if Code(err) == "client_required" {
			// A service without the operation sends this request to its
			// connected-client gate.
			return ProjectContext{}, protocol.E(500, "incompatible_service", "the service does not support project context; upgrade lit-server")
		}
		return ProjectContext{}, err
	}
	if wire.APIMajor != 1 || !protocol.ValidUUID(wire.ServiceID) {
		return ProjectContext{}, protocol.E(500, "incompatible_service", "unsupported API major or invalid service identity")
	}
	if !protocol.ValidUUID(wire.Project.ID) || wire.Project.Title == "" {
		return ProjectContext{}, protocol.E(500, "invalid_response", "the service answered without a valid project identity")
	}
	return ProjectContext{ServiceID: wire.ServiceID, ProjectID: wire.Project.ID, Title: wire.Project.Title}, nil
}
