package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/checkoutcontext"
	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/service"
	"github.com/nerdwave-nick/lit/internal/storage"
)

// nothing is an endpoint no service listens on.
const nothing = "http://127.0.0.1:1"

// contextService is a real in-process lit service behind recording fronts, so
// tests can prove what was, and was not, sent to it, and to which address.
// More than one front can answer for the same service, as a service reachable
// at two addresses does.
type contextService struct {
	t       *testing.T
	URL     string
	backend http.Handler

	session string // the administrator session, bound to this service alone
	mu      sync.Mutex
	seen    []seenRequest
	hold    chan struct{}
}

var administrators atomic.Int32

type seenRequest struct{ Host, Method, URI, Client string }

func (r seenRequest) String() string { return r.Method + " " + r.URI }

// newContextService starts a service with one project per title, created
// through an administrator session of its own in the world's client state.
func newContextService(t *testing.T, titles ...string) *contextService {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	backend, err := service.New(db, service.Config{Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"test"}})
	if err != nil {
		t.Fatal(err)
	}
	s := &contextService{t: t, backend: backend, session: fmt.Sprintf("admin-%d", administrators.Add(1))}
	s.URL = s.front()
	s.admin("connect")
	for _, title := range titles {
		s.admin("projects", "create", "--project-title", title, "--content", "seed")
	}
	s.forget()
	return s
}

// front serves the service at one more address.
func (s *contextService) front() string {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.seen = append(s.seen, seenRequest{r.Host, r.Method, r.URL.RequestURI(), r.Header.Get("X-Lit-Client-ID")})
		hold := s.hold
		s.mu.Unlock()
		if hold != nil {
			select {
			case <-hold:
			case <-r.Context().Done():
				return
			}
		}
		s.backend.ServeHTTP(w, r)
	}))
	s.t.Cleanup(server.Close)
	return server.URL
}

// admin runs a lit command in an administrator session against this service
// and returns the first item of its result.
func (s *contextService) admin(args ...string) map[string]any {
	s.t.Helper()
	return contextItem(s.t, physicalDir(s.t), append(args, "--session", s.session, "--endpoint", s.URL)...)
}

func (s *contextService) forget() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = nil
}

func (s *contextService) requests() []seenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]seenRequest(nil), s.seen...)
}

// holdRequests makes the service wait before answering until the returned
// function is called (or the test ends).
func (s *contextService) holdRequests() (release func()) {
	s.mu.Lock()
	hold := make(chan struct{})
	s.hold = hold
	s.mu.Unlock()
	var once sync.Once
	release = func() { once.Do(func() { close(hold) }) }
	s.t.Cleanup(release)
	return release
}

// context is what the service itself says about a project.
func (s *contextService) context(title string) litclient.ProjectContext {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, err := litclient.ProjectContextClient{Endpoint: s.URL}.Resolve(ctx, title)
	if err != nil {
		s.t.Fatal(err)
	}
	s.forget()
	return got
}

// preference is the preference a user would have remembered for the project.
func (s *contextService) preference(title string) checkoutcontext.Preference {
	got := s.context(title)
	return checkoutcontext.Preference{ServiceID: got.ServiceID, ProjectID: got.ProjectID, ProjectTitle: got.Title, Endpoint: s.URL}
}

// only fails unless the service saw exactly the given requests, none of them
// with a client identity.
func (s *contextService) only(t *testing.T, want ...string) {
	t.Helper()
	got := s.requests()
	lines := []string{}
	for _, r := range got {
		lines = append(lines, r.String())
		if r.Client != "" {
			t.Errorf("%s carried client %s", r, r.Client)
		}
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("service saw\n  %s\nwant\n  %s", strings.Join(lines, "\n  "), strings.Join(want, "\n  "))
	}
}

// remember saves p for dir's checkout without going through a command.
func (w *contextWorld) remember(t *testing.T, dir string, p checkoutcontext.Preference) {
	t.Helper()
	c, err := checkoutcontext.Resolve(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.store().Save(c, p); err != nil {
		t.Fatal(err)
	}
}

// contextProblem is a failure's details as a map.
func contextProblem(t *testing.T, e map[string]any) map[string]any {
	t.Helper()
	details, ok := e["details"].(map[string]any)
	if !ok {
		t.Fatalf("error without details: %v", e)
	}
	return details
}

func requestFor(q string) string { return "GET /v1/project-context?" + q }

func contextQuery(p checkoutcontext.Preference) string {
	return "project_id=" + p.ProjectID + "&service_id=" + p.ServiceID
}

func ref(title string) string { return "project=" + strings.ReplaceAll(title, "/", "%2F") }

func mustCheckout(t *testing.T, dir string) checkoutcontext.Checkout {
	t.Helper()
	c, err := checkoutcontext.Resolve(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// sharedFirstDigit finds a hexadecimal digit that starts the IDs of at least
// two of the projects, which makes it an ambiguous ID prefix.
func sharedFirstDigit(t *testing.T, s *contextService, titles []string) string {
	t.Helper()
	seen := map[string]int{}
	for _, title := range titles {
		seen[s.context(title).ProjectID[:1]]++
	}
	for digit, n := range seen {
		if n > 1 {
			return digit
		}
	}
	t.Fatal("no shared first digit; the pigeonhole needs more than 16 projects")
	return ""
}
