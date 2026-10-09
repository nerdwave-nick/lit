// Package litclient is lit's client safety core: a logical session bound to
// one service and client, and calls that journal every mutation before it is
// sent, classify uncertain outcomes and reconcile them read-only. It never
// starts lit-server and never replays a request.
package litclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/clientendpoint"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// Session is one logical session's connection state.
//
// A Session is not safe for concurrent use. Callers serialize its calls (for
// example behind one mutation gate) and may replace Context between calls to
// give each operation its own deadline.
type Session struct {
	// StateDir holds mappings, locks and pending-request evidence.
	StateDir string
	Endpoint string
	HTTP     *http.Client
	Context  context.Context
	// Warnings receives non-fatal cleanup warnings; nil discards them.
	Warnings io.Writer

	Mapping    Mapping
	Meta       protocol.Meta
	Client     protocol.Client
	ServerTime string

	unlock      func()
	mappingPath string
}

// OpenOptions select the endpoint and which session states Open accepts.
type OpenOptions struct {
	// Endpoint is an explicitly requested endpoint, honoured when EndpointSet,
	// even if empty. EnvEndpoint is the environment fallback; the mapping's
	// remembered endpoint and the default follow it.
	Endpoint    string
	EndpointSet bool
	EnvEndpoint string
	// AllowUnmapped accepts a session without a mapping (registering a client).
	AllowUnmapped bool
	// AllowDisconnected accepts a mapped client that is disconnected.
	AllowDisconnected bool
}

// Lock acquires the named session's mapping lock, waiting until deadline, and
// reads its mapping. A missing mapping leaves Mapping empty. The lock is held
// until Close, serializing the mapping across reads, registration, updates and
// disconnect.
func (s *Session) Lock(name string, deadline time.Time) error {
	path := MappingPath(s.StateDir, name)
	unlock, e := AcquireLock(path+".lock", deadline)
	if e != nil {
		return protocol.E(409, "session_locked", e.Error())
	}
	m, e := ReadMapping(path)
	if e != nil && !os.IsNotExist(e) {
		unlock()
		return protocol.E(500, "invalid_local_state", e.Error())
	}
	s.Mapping, s.unlock, s.mappingPath = m, unlock, path
	return nil
}

// Close releases the mapping lock taken by Lock.
func (s *Session) Close() {
	if s.unlock != nil {
		s.unlock()
		s.unlock = nil
	}
}

// Open binds a locked session to its service: it resolves the endpoint, checks
// the service's API major and identity against the mapping, and reads the
// mapped client. Unless o allows them, an unmapped session or a disconnected
// client is refused; Client holds the record read even when it is refused.
func (s *Session) Open(o OpenOptions) error {
	mapped := s.Mapping.ClientID != ""
	var e error
	s.Endpoint, e = clientendpoint.Resolve(o.Endpoint, o.EndpointSet, o.EnvEndpoint, s.Mapping.Endpoint)
	if e != nil {
		return protocol.E(400, "invalid_endpoint", e.Error())
	}
	var meta protocol.Meta
	if e = s.Call("GET", "/v1/meta", nil, nil, &meta, false); e != nil {
		return e
	}
	if meta.APIMajor != 1 || !protocol.ValidUUID(meta.ServiceID) {
		return protocol.E(500, "incompatible_service", "unsupported API major or invalid service identity")
	}
	s.Meta = meta
	if mapped && s.Mapping.ServiceID != meta.ServiceID {
		return protocol.E(409, "wrong_service", "endpoint is bound to a different service")
	}
	if !mapped {
		if !o.AllowUnmapped {
			return protocol.E(400, "not_connected", "connect this session explicitly first")
		}
		return nil
	}
	var c protocol.Client
	if e = s.Call("GET", "/v1/clients/"+s.Mapping.ClientID, nil, nil, &c, false); e != nil {
		return e
	}
	s.Client = c
	if !o.AllowDisconnected {
		return s.RequireConnected()
	}
	return nil
}

// RequireConnected refuses a session whose client is not connected.
func (s *Session) RequireConnected() error {
	if s.Client.Status != "connected" {
		return protocol.E(409, "client_disconnected", "connect explicitly to resume this client")
	}
	return nil
}

func (s *Session) warn(args ...any) {
	if s.Warnings != nil {
		fmt.Fprintln(s.Warnings, args...)
	}
}
