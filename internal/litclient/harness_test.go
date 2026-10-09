package litclient_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/service"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/store"
)

// harness is a real temporary lit service behind a counting front, so tests can
// prove what was (and was not) sent. intercept, when set, may answer a request
// instead of the service.
type harness struct {
	t        *testing.T
	url      string
	stateDir string
	backend  http.Handler

	mu        sync.Mutex
	counts    map[string]int
	intercept func(w http.ResponseWriter, r *http.Request) bool
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	storage, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	backend, err := service.New(storage, service.Config{Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"test"}})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, stateDir: t.TempDir(), backend: backend, counts: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.counts[r.Method+" "+r.URL.Path]++
		intercept := h.intercept
		h.mu.Unlock()
		if intercept != nil && intercept(w, r) {
			return
		}
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	h.url = server.URL
	return h
}

func (h *harness) count(key string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.counts[key]
}

func (h *harness) setIntercept(f func(w http.ResponseWriter, r *http.Request) bool) {
	h.mu.Lock()
	h.intercept = f
	h.mu.Unlock()
}

// open locks and opens the named session without connecting it.
func (h *harness) open(name string) *litclient.Session {
	h.t.Helper()
	s := &litclient.Session{StateDir: h.stateDir, HTTP: &http.Client{Timeout: 10 * time.Second}, Context: context.Background()}
	if err := s.Lock(name, time.Now().Add(time.Second)); err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(s.Close)
	if err := s.Open(litclient.OpenOptions{Endpoint: h.url, EndpointSet: true, AllowUnmapped: true}); err != nil {
		h.t.Fatal(err)
	}
	return s
}

// connect registers a fresh client for a new logical session.
func (h *harness) connect(name, actor string) *litclient.Session {
	h.t.Helper()
	s := h.open(name)
	if err := s.Connect(context.Background(), litclient.Registration{Actor: protocol.Actor{Name: actor, Kind: "human"}}); err != nil {
		h.t.Fatalf("connect %s: %v", name, err)
	}
	return s
}

func (h *harness) pending() []string {
	h.t.Helper()
	files, err := filepath.Glob(filepath.Join(h.stateDir, "pending", "*.json"))
	if err != nil {
		h.t.Fatal(err)
	}
	return files
}

// apply prepares and commits req, failing the test on any error.
func apply(t *testing.T, s *litclient.Session, req protocol.PrepareRequest) protocol.MutationResult {
	t.Helper()
	p, err := s.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("prepare %s: %v", req.Operation, err)
	}
	v, err := s.Commit(context.Background(), p)
	if err != nil {
		t.Fatalf("commit %s: %v", req.Operation, err)
	}
	return v
}

func issue(t *testing.T, s *litclient.Session, project, ref string) protocol.Issue {
	t.Helper()
	v, err := litclient.Get[protocol.Issue](context.Background(), s, project, ref)
	if err != nil || len(v) != 1 {
		t.Fatalf("get issue %s: %v %v", ref, v, err)
	}
	return v[0]
}

// seed creates project test/<name> with the given issue titles and returns the
// project ID and the issues by title.
func seed(t *testing.T, s *litclient.Session, name string, titles ...string) (string, map[string]protocol.Issue) {
	t.Helper()
	project := "test/" + name
	apply(t, s, litclient.CreateProject(project, "spec"))
	out := map[string]protocol.Issue{}
	for _, title := range titles {
		apply(t, s, litclient.CreateIssue(project, title, title+" body", ""))
		out[title] = issue(t, s, project, "title:"+title)
	}
	return project, out
}

func str(v string) *string { return &v }
