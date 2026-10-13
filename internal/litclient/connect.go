package litclient

import (
	"context"
	"fmt"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// Registration describes a new client.
type Registration struct {
	Actor protocol.Actor
	// Project optionally selects the client's project (title or UUID).
	Project string
	Runtime protocol.Runtime
}

// Connect registers a new client for this locked, unmapped session (after Lock
// and Open with AllowUnmapped) and saves the session mapping. It never adopts
// an existing client. If the registration's outcome is uncertain, its pending
// evidence is retained and no mapping is written.
func (s *Session) Connect(ctx context.Context, r Registration) error {
	if s.mappingPath == "" || s.unlock == nil {
		return protocol.E(400, "invalid_arguments", "lock the session before connecting it")
	}
	if s.Mapping.ClientID != "" {
		return protocol.E(400, "invalid_arguments", "connect registers a new client; this session is already mapped")
	}
	if !protocol.ValidUUID(s.Meta.ServiceID) {
		return protocol.E(400, "invalid_arguments", "open the session before connecting it")
	}
	defer s.bind(ctx)()
	p := map[string]any{"actor": map[string]any{"name": r.Actor.Name, "kind": r.Actor.Kind}}
	if r.Project != "" {
		p["project_id"] = r.Project
	}
	if r.Runtime != (protocol.Runtime{}) {
		p["runtime"] = r.Runtime
	}
	var wire struct {
		Client protocol.Client `json:"client"`
	}
	if e := s.Call("POST", "/v1/connect", p, nil, &wire, true); e != nil {
		return e
	}
	s.Client = wire.Client
	m := Mapping{SchemaVersion: 1, ClientID: wire.Client.ClientID, ServiceID: s.Meta.ServiceID, Endpoint: s.Endpoint}
	if e := WriteJSON(s.mappingPath, m); e != nil {
		failure := protocol.E(500, "local_state_error", fmt.Sprintf("connected client %s, but could not save its session mapping: %s", m.ClientID, e))
		failure.Details = map[string]string{"client_id": m.ClientID, "service_id": m.ServiceID, "endpoint": m.Endpoint}
		return failure
	}
	s.Mapping = m
	return nil
}

// Disconnect disconnects this session's client. The service releases every
// claim the client holds in the same atomic operation. It returns "applied" or
// "already_disconnected". The mapping is kept.
func (s *Session) Disconnect(ctx context.Context) (string, error) {
	defer s.bind(ctx)()
	h := map[string]string{"If-Match": fmt.Sprintf("\"client:%d\"", s.Client.StateRevision)}
	var v struct {
		Client  protocol.Client `json:"client"`
		Outcome string          `json:"outcome"`
	}
	if e := s.Call("POST", "/v1/disconnect", map[string]any{}, h, &v, true); e != nil {
		return "", e
	}
	s.Client = v.Client
	return v.Outcome, nil
}
