// Package tuitest supports lit-tui's lifecycle and recovery tests: a temporary
// in-process lit service, lit-tui-like instances in a registry, and retained
// evidence whose outcome the service cannot establish. Only tests import it.
package tuitest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/service"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/store"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/registry"
)

// Server is a temporary lit service behind a counting front. Intercept, when
// set, may answer a request instead of the service.
type Server struct {
	URL string

	mu        sync.Mutex
	counts    map[string]int
	intercept func(w http.ResponseWriter, r *http.Request) bool
}

// Service starts a temporary lit service for the test.
func Service(t *testing.T) *Server { return ServiceWithLimits(t, protocol.DefaultLimits()) }

// ServiceWithLimits starts a temporary lit service with the given limits.
func ServiceWithLimits(t *testing.T, limits protocol.Limits) *Server {
	t.Helper()
	storage, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	backend, err := service.New(storage, service.Config{Limits: limits, TitlePrefixes: []string{"test"}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{counts: map[string]int{}}
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.counts[r.Method+" "+r.URL.Path]++
		intercept := s.intercept
		s.mu.Unlock()
		if intercept != nil && intercept(w, r) {
			return
		}
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(h.Close)
	s.URL = h.URL
	return s
}

// Count returns how many requests matched "METHOD /path".
func (s *Server) Count(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[key]
}

// Intercept installs (or, with nil, removes) a request interceptor.
func (s *Server) Intercept(f func(w http.ResponseWriter, r *http.Request) bool) {
	s.mu.Lock()
	s.intercept = f
	s.mu.Unlock()
}

// HangUp is an interceptor body that drops the connection without answering,
// so a mutation's outcome is uncertain.
func HangUp(w http.ResponseWriter) {
	if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
		_ = conn.Close()
	}
}

// Instance publishes a registry entry in state and connects a fresh client
// for it at url, as a launching lit-tui does. The returned session holds the
// entry's liveness lock until the test ends or Crash releases it.
func Instance(t *testing.T, state, url string) (registry.Entry, *litclient.Session) {
	t.Helper()
	e := registry.New(state, protocol.UUID())
	s := &litclient.Session{StateDir: state, HTTP: &http.Client{Timeout: 10 * time.Second}, Context: context.Background()}
	if err := s.LockPath(e.SessionPath(), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	host, _ := os.Hostname()
	info := registry.Info{SchemaVersion: 1, InstanceID: e.ID, PID: 1, Host: host, StartedAt: protocol.Now(), Endpoint: url}
	if err := registry.Publish(e, info); err != nil {
		t.Fatal(err)
	}
	if err := s.Open(litclient.OpenOptions{Endpoint: url, EndpointSet: true, AllowUnmapped: true}); err != nil {
		t.Fatal(err)
	}
	reg := litclient.Registration{Actor: protocol.Actor{Name: "tester", Kind: "human"}, Runtime: protocol.Runtime{Vendor: "lit-tui", SessionID: e.ID}}
	if err := s.Connect(context.Background(), reg); err != nil {
		t.Fatal(err)
	}
	return e, s
}

// Crash releases s's liveness lock and keeps every file, as a killed process
// does.
func Crash(s *litclient.Session) { s.Close() }

// ClientStatus reads the client's status ("connected" or "disconnected") from
// the service.
func ClientStatus(t *testing.T, url, clientID string) string {
	t.Helper()
	s := &litclient.Session{StateDir: t.TempDir(), HTTP: http.DefaultClient, Context: context.Background(), Endpoint: url,
		Mapping: litclient.Mapping{ClientID: clientID}}
	var c protocol.Client
	if err := s.Call("GET", "/v1/clients/"+clientID, nil, nil, &c, false); err != nil {
		t.Fatalf("read client %s: %v", clientID, err)
	}
	return c.Status
}

// UncertainAcquire creates an issue through s and writes retained evidence of
// a claims.acquire for it by s's client, as if the response had been lost.
// The service cannot establish such an outcome: its status stays uncertain
// (acquisition_token_not_received). It returns the evidence path.
func UncertainAcquire(t *testing.T, s *litclient.Session) string {
	t.Helper()
	ctx := context.Background()
	project := "test/" + s.Mapping.ClientID[:8]
	for _, req := range []protocol.PrepareRequest{litclient.CreateProject(project, "spec"), litclient.CreateIssue(project, "Claimed", "body", "")} {
		p, err := s.Prepare(ctx, req)
		if err == nil {
			_, err = s.Commit(ctx, p)
		}
		if err != nil {
			t.Fatalf("seed %s: %v", req.Operation, err)
		}
	}
	issues, err := litclient.Get[protocol.Issue](ctx, s, project, "title:Claimed")
	if err != nil || len(issues) != 1 {
		t.Fatalf("issue: %v %v", issues, err)
	}
	body, _ := json.Marshal(map[string]any{"schema_version": 1, "operation": "claims.acquire", "owner_client_id": s.Mapping.ClientID, "selection": "explicit",
		"items": []map[string]any{{"issue_id": issues[0].ID, "extend_to": time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339Nano)}}})
	p := litclient.Pending{SchemaVersion: 1, RequestID: protocol.UUID(), ServiceID: s.Mapping.ServiceID, ClientID: s.Mapping.ClientID, Endpoint: s.Endpoint,
		Method: "POST", Path: "/v1/operations", Body: body, Headers: map[string]string{"Content-Type": "application/json", "X-Lit-Client-ID": s.Mapping.ClientID}, CreatedAt: protocol.Now()}
	path := filepath.Join(s.StateDir, "pending", p.RequestID+".json")
	if err := litclient.WriteJSON(path, p); err != nil {
		t.Fatal(err)
	}
	status, err := s.TransactionStatus(ctx, litclient.StatusOptions{})
	if err != nil || len(status) != 1 || status[0].Status != "uncertain" {
		t.Fatalf("fabricated evidence is not uncertain: %+v %v", status, err)
	}
	return path
}

// UnconfirmedRegistration writes retained evidence of a first connect for
// instance id whose response never arrived: no client identity was received.
func UnconfirmedRegistration(t *testing.T, state, url, serviceID, id string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"actor": map[string]any{"name": "tester", "kind": "human"}, "runtime": protocol.Runtime{Vendor: "lit-tui", SessionID: id}})
	p := litclient.Pending{SchemaVersion: 1, RequestID: protocol.UUID(), ServiceID: serviceID, Endpoint: url, Method: "POST", Path: "/v1/connect",
		Body: body, Headers: map[string]string{"Content-Type": "application/json"}, CreatedAt: protocol.Now()}
	path := filepath.Join(state, "pending", p.RequestID+".json")
	if err := litclient.WriteJSON(path, p); err != nil {
		t.Fatal(err)
	}
	return path
}

// Apply prepares req through s and commits exactly that preview.
func Apply(t *testing.T, s *litclient.Session, req protocol.PrepareRequest) {
	t.Helper()
	p, err := s.Prepare(context.Background(), req)
	if err == nil {
		_, err = s.Commit(context.Background(), p)
	}
	if err != nil {
		t.Fatalf("%s: %v", req.Operation, err)
	}
}

// CreateIssues creates one issue per title in project, in one transaction;
// parent is an issue UUID or "". It returns the new issues' UUIDs.
func CreateIssues(t *testing.T, s *litclient.Session, project, parent string, titles ...string) []string {
	t.Helper()
	req := protocol.PrepareRequest{SchemaVersion: 1, Operation: "issue.create", Project: project}
	var ids []string
	for _, title := range titles {
		title, body := title, "body of "+title
		item := protocol.ProjectInput{ID: protocol.UUID(), Title: &title, Content: &body}
		if parent != "" {
			p := parent
			item.Parent = &p
		}
		req.Items = append(req.Items, item)
		ids = append(ids, item.ID)
	}
	Apply(t, s, req)
	return ids
}
