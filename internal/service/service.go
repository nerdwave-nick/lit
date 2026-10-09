// Package service serializes access to the file authority. Resource handlers added
// by subsequent slices run under Server.Mu and commit through Server.Store.
package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/store"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Server struct {
	Mu          sync.Mutex
	Store       *store.Store
	Config      Config
	stopping    atomic.Bool
	claimClock  func() time.Time
	requestTime *time.Time
}

func New(s *store.Store, c Config) (*Server, error) {
	v := &Server{Store: s, Config: c}
	entries, e := s.List("clients")
	if e != nil {
		return nil, e
	}
	for _, f := range entries {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			return nil, fmt.Errorf("unrecognized client record")
		}
		id := strings.TrimSuffix(f.Name(), ".json")
		if _, e := v.Client(id); e != nil {
			return nil, e
		}
	}
	if e := v.InitProjects(); e != nil {
		return nil, e
	}
	if e := v.InitClaims(); e != nil {
		return nil, e
	}
	return v, nil
}
func (s *Server) Stop() { s.stopping.Store(true) }

func (s *Server) Meta() protocol.Meta {
	return protocol.Meta{ServiceID: s.Store.Identity.ServiceID, APIMajor: 1, Version: protocol.ReleaseVersion, Limits: s.Config.Limits, Timing: map[string]int{"claim_default_seconds": 1800, "claim_max_seconds": 3600}, TitlePrefixes: s.Config.TitlePrefixes}
}
func (s *Server) Client(id string) (protocol.Client, error) {
	var c protocol.Client
	if !protocol.ValidUUID(id) {
		return c, protocol.E(404, "unknown_client", "unknown client identity")
	}
	b, e := s.Store.Read("clients/" + id + ".json")
	if os.IsNotExist(e) {
		return c, protocol.E(404, "unknown_client", "unknown client identity")
	}
	if e != nil {
		return c, e
	}
	if e = protocol.Decode(b, &c); e != nil {
		return c, fmt.Errorf("invalid client record: %w", e)
	}
	if c.SchemaVersion != 1 || c.ClientID != id || c.StateRevision < 1 || (c.Status != "connected" && c.Status != "disconnected") || validateClient(c) != nil {
		return c, fmt.Errorf("unsupported or invalid client record")
	}
	return c, nil
}
func validateClient(c protocol.Client) error {
	if strings.TrimSpace(c.Actor.Name) == "" || (c.Actor.Kind != "human" && c.Actor.Kind != "agent") {
		return fmt.Errorf("actor requires a name and human or agent kind")
	}
	if c.OutputFormat != nil && *c.OutputFormat != "json" && *c.OutputFormat != "cli" && *c.OutputFormat != "markdown" && *c.OutputFormat != "human" {
		return fmt.Errorf("output_format must be cli, markdown or json (human is a legacy alias)")
	}
	if c.ProjectID != nil && !protocol.ValidUUID(*c.ProjectID) {
		return fmt.Errorf("invalid project ID")
	}
	return nil
}

type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header         { return b.header }
func (b *bufferedResponse) WriteHeader(status int)      { b.status = status }
func (b *bufferedResponse) Write(p []byte) (int, error) { return b.body.Write(p) }
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.stopping.Load() {
		protocol.WriteError(w, protocol.E(503, "shutting_down", "service is shutting down"))
		return
	}
	if r.Body != nil {
		body, e := io.ReadAll(io.LimitReader(r.Body, s.Config.Limits.RequestBytes+1))
		if e != nil {
			protocol.WriteError(w, protocol.E(400, "invalid_json", "cannot read request body"))
			return
		}
		if int64(len(body)) > s.Config.Limits.RequestBytes {
			protocol.WriteError(w, protocol.E(413, "limit_exceeded", "request body exceeds limit"))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	b := &bufferedResponse{header: make(http.Header), status: 200}
	s.Mu.Lock()
	func() {
		defer s.Mu.Unlock()
		now := s.claimNow()
		s.requestTime = &now
		defer func() { s.requestTime = nil }()
		s.handle(b, r)
	}()
	if int64(b.body.Len()) > s.Config.Limits.SnapshotBytes {
		protocol.WriteError(w, protocol.E(413, "limit_exceeded", "response exceeds snapshot limit"))
		return
	}
	for k, v := range b.header {
		w.Header()[k] = v
	}
	w.WriteHeader(b.status)
	_, _ = w.Write(b.body.Bytes())
}
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if s.stopping.Load() {
		protocol.WriteError(w, protocol.E(503, "shutting_down", "service is shutting down"))
		return
	}
	if e := s.Store.Check(); e != nil {
		protocol.WriteError(w, protocol.E(503, "recovery_required", e.Error()))
		return
	}
	if r.URL.Path == "/v1/meta" && r.Method == "GET" {
		s.write(w, 200, s.Meta())
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		protocol.WriteError(w, protocol.E(404, "unsupported_api_version", "use API major version 1"))
		return
	}
	var e error
	if strings.HasPrefix(r.URL.Path, "/v1/completions/") {
		e = s.completionRoute(w, r)
	} else if r.URL.Path == "/v1/connect" && r.Method == "POST" {
		e = s.connect(w, r)
	} else {
		id := r.Header.Get("X-Lit-Client-ID")
		if id == "" {
			e = protocol.E(400, "client_required", "X-Lit-Client-ID is required")
		} else {
			var c protocol.Client
			c, e = s.Client(id)
			if e == nil {
				bootstrapRead := r.Method == "GET" && r.URL.Path == "/v1/clients/"+id
				if c.Status != "connected" && !bootstrapRead && r.URL.Path != "/v1/disconnect" && r.URL.Path != "/v1/transaction-status" {
					e = protocol.E(409, "client_disconnected", "connect explicitly to resume this client")
				} else {
					e = s.route(w, r, c)
				}
			}
		}
	}
	if e != nil {
		if pe, ok := e.(*protocol.Error); ok {
			log.Printf("lit request failed: %s", pe.Code)
			protocol.WriteError(w, pe)
		} else {
			log.Print("lit request failed: storage_error")
			protocol.WriteError(w, protocol.E(503, "storage_error", e.Error()))
		}
	}
}
func (s *Server) Body(r *http.Request, v any) error {
	b, e := io.ReadAll(io.LimitReader(r.Body, s.Config.Limits.RequestBytes+1))
	if e != nil {
		return protocol.E(400, "invalid_json", "cannot read JSON body")
	}
	if int64(len(b)) > s.Config.Limits.RequestBytes {
		return protocol.E(413, "limit_exceeded", "request body exceeds limit")
	}
	if e = protocol.Decode(b, v); e != nil {
		code := "invalid_json"
		if strings.HasPrefix(e.Error(), "json: unknown field ") {
			code = "unknown_field"
		}
		return protocol.E(400, code, e.Error())
	}
	return nil
}
func (s *Server) route(w http.ResponseWriter, r *http.Request, c protocol.Client) error {
	if r.Method == "POST" && r.URL.Path == "/v1/transaction-status" {
		return s.reconcileHTTP(w, r, c)
	}
	if handled, e := s.claimRoute(w, r, c); handled {
		return e
	}
	if handled, e := s.searchRoute(w, r, c); handled {
		return e
	}
	if handled, e := s.issueSummaryRoute(w, r, c); handled {
		return e
	}
	if handled, e := s.recordRoute(w, r, c); handled {
		return e
	}
	if handled, e := s.projectRoute(w, r, c); handled {
		return e
	}
	if r.URL.Path == "/v1/disconnect" && r.Method == "POST" {
		var input struct{}
		if e := s.Body(r, &input); e != nil {
			return e
		}
		if e := precondition(r, c); e != nil {
			return e
		}
		if c.Status == "disconnected" {
			s.write(w, 200, map[string]any{"outcome": "already_disconnected", "client": c})
			return nil
		}
		c.Status = "disconnected"
		c.StateRevision++
		writes, e := s.disconnectWrites(c)
		if e != nil {
			return e
		}
		return s.commitOperationalResponse(w, 200, map[string]any{"outcome": "applied", "client": c}, writes)
	}
	if strings.HasPrefix(r.URL.Path, "/v1/clients/") {
		if r.URL.Path != "/v1/clients/"+c.ClientID {
			return protocol.E(403, "wrong_client", "client may access only its own state")
		}
		switch r.Method {
		case "GET":
			s.write(w, 200, c)
			return nil
		case "PATCH":
			if e := precondition(r, c); e != nil {
				return e
			}
			var p map[string]json.RawMessage
			if e := s.Body(r, &p); e != nil {
				return e
			}
			old, _ := json.Marshal(c)
			if e := s.patchClient(&c, p); e != nil {
				return e
			}
			next, _ := json.Marshal(c)
			if string(old) != string(next) {
				c.StateRevision++

			}
			return s.commitClientResponse(w, 200, c, c, string(old) != string(next))
		}
	}
	return protocol.E(404, "not_found", "unknown route")
}
func precondition(r *http.Request, c protocol.Client) error {
	v := r.Header.Get("If-Match")
	if v == "" {
		return protocol.E(428, "precondition_required", "If-Match client revision required")
	}
	if v != fmt.Sprintf("\"client:%d\"", c.StateRevision) {
		return protocol.E(409, "client_state_conflict", "client state revision changed")
	}
	return nil
}
func (s *Server) SaveClient(c protocol.Client) error {
	return s.Store.Commit([]store.Write{store.JSONWrite("clients/"+c.ClientID+".json", c)})
}
func patch(c *protocol.Client, p map[string]json.RawMessage) error {
	for k, b := range p {
		switch k {
		case "actor":
			var fields map[string]json.RawMessage
			if e := protocol.Decode(b, &fields); e != nil || fields == nil {
				return protocol.E(422, "validation_failed", "invalid actor")
			}
			for key, v := range fields {
				switch key {
				case "name":
					if e := json.Unmarshal(v, &c.Actor.Name); e != nil || string(v) == "null" {
						return protocol.E(422, "validation_failed", "invalid actor name")
					}
				case "kind":
					if e := json.Unmarshal(v, &c.Actor.Kind); e != nil || string(v) == "null" {
						return protocol.E(422, "validation_failed", "invalid actor kind")
					}
				default:
					return protocol.E(400, "unknown_property", "unknown actor field")
				}
			}
		case "runtime":
			if string(b) == "null" {
				c.Runtime = protocol.Runtime{}
				break
			}
			var fields map[string]*string
			if e := protocol.Decode(b, &fields); e != nil {
				return protocol.E(422, "validation_failed", "invalid runtime")
			}
			for key, v := range fields {
				x := ""
				if v != nil {
					x = *v
				}
				switch key {
				case "vendor":
					c.Runtime.Vendor = x
				case "session_id":
					c.Runtime.SessionID = x
				default:
					return protocol.E(400, "unknown_property", "unknown runtime field")
				}
			}
		case "output_format":
			if e := json.Unmarshal(b, &c.OutputFormat); e != nil {
				return protocol.E(422, "validation_failed", "invalid output format")
			}
		case "project_id":
			if e := json.Unmarshal(b, &c.ProjectID); e != nil {
				return protocol.E(422, "validation_failed", "invalid project selector")
			}

		default:
			return protocol.E(400, "unknown_property", "unknown client property: "+k)
		}
	}
	if e := validateClient(*c); e != nil {
		return protocol.E(422, "validation_failed", e.Error())
	}
	return nil
}
func (s *Server) connect(w http.ResponseWriter, r *http.Request) error {
	var p map[string]json.RawMessage
	if e := s.Body(r, &p); e != nil {
		return e
	}
	var c protocol.Client
	status := 201
	changed := true
	if b, ok := p["client_id"]; ok {
		var id string
		if e := json.Unmarshal(b, &id); e != nil {
			return protocol.E(422, "validation_failed", "invalid client_id")
		}
		delete(p, "client_id")
		var e error
		c, e = s.Client(id)
		if e != nil {
			return e
		}
		if len(p) > 0 {
			if e = precondition(r, c); e != nil {
				return e
			}
		}
		before, _ := json.Marshal(c)
		if e = s.patchClient(&c, p); e != nil {
			return e
		}
		c.Status = "connected"
		after, _ := json.Marshal(c)
		changed = string(before) != string(after)
		if changed {
			c.StateRevision++
		}
		status = 200
	} else {
		c = protocol.Client{SchemaVersion: 1, ClientID: protocol.UUID(), StateRevision: 1, Status: "connected"}
		if e := s.patchClient(&c, p); e != nil {
			return e
		}

	}
	return s.commitClientResponse(w, status, c, map[string]any{"client": c, "meta": s.Meta()}, changed)
}

// Materialize and bound the exact success envelope before any operational write.
func (s *Server) commitClientResponse(w http.ResponseWriter, status int, c protocol.Client, data any, changed bool) error {
	b, e := json.Marshal(protocol.Response{Data: data, ServerTime: s.responseTime()})
	if e != nil {
		return e
	}
	b = append(b, '\n')
	if int64(len(b)) > s.Config.Limits.SnapshotBytes {
		return protocol.E(413, "limit_exceeded", "response exceeds snapshot limit")
	}
	if changed {
		if e = s.SaveClient(c); e != nil {
			return e
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, e = w.Write(b)
	return e
}

func (s *Server) patchClient(c *protocol.Client, p map[string]json.RawMessage) error {
	if b, ok := p["project_id"]; ok && string(b) != "null" {
		var selector string
		if json.Unmarshal(b, &selector) != nil {
			return protocol.E(422, "validation_failed", "invalid project selector")
		}
		project, e := s.ResolveProject(selector)
		if e != nil {
			return e
		}
		p["project_id"], _ = json.Marshal(project.ID)
	}
	return patch(c, p)
}
