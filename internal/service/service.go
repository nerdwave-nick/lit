// Package service serves the lit protocol over the storage module. A static
// route table (routes.go) classifies each request. Reads run in parallel, each
// in one WAL read snapshot. Writes hold the process write lock and run in one
// storage write transaction, which handlers commit only after their response is
// complete and bounded.
package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// Server serves the lit protocol. Each request runs on its own request-scoped
// Server, which shares everything but the request being served.
type Server struct {
	*shared
	// cur is the request being served; it is nil outside a request.
	cur *requestState
}

// shared is the state of a Server that every request sees.
type shared struct {
	DB         *storage.DB
	Config     Config
	stopping   atomic.Bool
	claimClock func() time.Time
}

// New serves the opened storage module db with configuration c.
func New(db *storage.DB, c Config) (*Server, error) {
	if db == nil {
		return nil, errors.New("service needs an open storage module")
	}
	return &Server{shared: &shared{DB: db, Config: c}}, nil
}
func (s *Server) Stop() { s.stopping.Store(true) }

func (s *Server) Meta() protocol.Meta {
	return protocol.Meta{ServiceID: s.DB.ServiceID(), APIMajor: 1, Version: protocol.ReleaseVersion, Limits: s.Config.Limits, Timing: map[string]int{"claim_default_seconds": 1800, "claim_max_seconds": 3600}, TitlePrefixes: s.Config.TitlePrefixes}
}

type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header         { return b.header }
func (b *bufferedResponse) WriteHeader(status int)      { b.status = status }
func (b *bufferedResponse) Write(p []byte) (int, error) { return b.body.Write(p) }

var errShuttingDown = protocol.E(503, "shutting_down", "service is shutting down")

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.stopping.Load() {
		protocol.WriteError(w, errShuttingDown)
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
	// The response is buffered, so a slow reader never holds the write lock or
	// a read snapshot.
	b := &bufferedResponse{header: make(http.Header), status: 200}
	run := s.serialized
	if routeAccess(r.Method, r.URL.Path) == readAccess {
		run = s.snapshot
	}
	if e := run(r.Context(), func(rs *Server) error { return rs.handle(b, r) }); e != nil {
		writeFailure(b, e)
	}
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

// writeFailure reports e: protocol errors as they are, contention as a
// retryable storage_busy, and anything else as a storage_error.
func writeFailure(w http.ResponseWriter, e error) {
	var pe *protocol.Error
	switch {
	case errors.As(e, &pe):
		log.Printf("lit request failed: %s", pe.Code)
		protocol.WriteError(w, pe)
	case errors.Is(e, storage.ErrBusy), errors.Is(e, context.Canceled), errors.Is(e, context.DeadlineExceeded):
		log.Print("lit request failed: storage_busy")
		protocol.WriteError(w, protocol.E(503, "storage_busy", "storage is busy; retry the request"))
	default:
		log.Print("lit request failed: storage_error")
		protocol.WriteError(w, protocol.E(503, "storage_error", e.Error()))
	}
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) error {
	if r.URL.Path == "/v1/meta" && r.Method == "GET" {
		s.write(w, 200, s.Meta())
		return nil
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		return protocol.E(404, "unsupported_api_version", "use API major version 1")
	}
	if strings.HasPrefix(r.URL.Path, "/v1/completions/") {
		return s.completionRoute(w, r)
	}
	if r.URL.Path == "/v1/connect" && r.Method == "POST" {
		return s.connect(w, r)
	}
	id := r.Header.Get("X-Lit-Client-ID")
	if id == "" {
		return protocol.E(400, "client_required", "X-Lit-Client-ID is required")
	}
	// The client is re-read inside the request's transaction.
	c, e := s.client(id)
	if e != nil {
		return e
	}
	bootstrapRead := r.Method == "GET" && r.URL.Path == "/v1/clients/"+id
	if c.Status != "connected" && !bootstrapRead && r.URL.Path != "/v1/disconnect" && r.URL.Path != "/v1/transaction-status" {
		return protocol.E(409, "client_disconnected", "connect explicitly to resume this client")
	}
	return s.route(w, r, c)
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
		return s.disconnect(w, r, c)
	}
	if strings.HasPrefix(r.URL.Path, "/v1/clients/") {
		return s.clientRoute(w, r, c)
	}
	return protocol.E(404, "not_found", "unknown route")
}
