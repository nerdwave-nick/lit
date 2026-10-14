package checkoutcontext_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/checkoutcontext"
	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

const (
	serviceA = "5e5e5e5e-1111-4111-8111-abcdefabcdef"
	serviceB = "6f6f6f6f-2222-4222-8222-abcdefabcdef"
	project  = "22220001-0000-4000-8000-000000000001"
	nowhere  = "http://127.0.0.1:1" // nothing listens here
)

// fakeService records its requests and answers each with answer.
type fakeService struct {
	*httptest.Server
	mu      sync.Mutex
	seen    []string
	clients []string
}

func newFake(t *testing.T, answer func(w http.ResponseWriter, r *http.Request)) *fakeService {
	t.Helper()
	f := &fakeService{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.seen = append(f.seen, r.Method+" "+r.URL.RequestURI())
		f.clients = append(f.clients, r.Header.Get("X-Lit-Client-ID"))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		answer(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeService) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

func projectIs(service, id, title string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"data":{"service_id":%q,"api_major":1,"project":{"id":%q,"title":%q}},"next_cursor":null,"server_time":"2026-10-14T00:00:00Z"}`, service, id, title)
	}
}

func failsWith(status int, code, message, details string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"error":{"code":%q,"message":%q,"details":%s}}`, code, message, details)
	}
}

func saved(endpoint string) checkoutcontext.Record {
	return checkoutcontext.Record{Preference: checkoutcontext.Preference{ServiceID: serviceA, ProjectID: project, ProjectTitle: "feat/old", Endpoint: endpoint}}
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestValidateAsksOnceWithoutAClientAndFollowsARename(t *testing.T) {
	f := newFake(t, projectIs(serviceA, project, "feat/renamed"))
	rec := saved(f.URL)

	v, err := checkoutcontext.Validate(ctx(t), rec, checkoutcontext.Endpoint{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := litclient.ProjectContext{ServiceID: serviceA, ProjectID: project, Title: "feat/renamed"}
	if v.Current != want || !v.Renamed || v.Endpoint != f.URL {
		t.Fatalf("validation: %+v", v)
	}
	if got := f.requests(); len(got) != 1 || got[0] != "GET /v1/project-context?project_id="+project+"&service_id="+serviceA {
		t.Fatalf("requests: %v", got)
	}
	if f.clients[0] != "" {
		t.Errorf("a client identity was sent: %q", f.clients[0])
	}
	if v.Record != rec {
		t.Error("the validated record is not the saved one")
	}
}

func TestValidateReportsAnUnchangedTitleAsNotRenamed(t *testing.T) {
	f := newFake(t, projectIs(serviceA, project, "feat/old"))
	v, err := checkoutcontext.Validate(ctx(t), saved(f.URL), checkoutcontext.Endpoint{}, nil)
	if err != nil || v.Renamed {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestEndpointPrecedenceForASavedBindingIsFlagThenEnvironmentThenSaved(t *testing.T) {
	servers := map[string]*fakeService{}
	for _, name := range []string{"flag", "environment", "saved"} {
		servers[name] = newFake(t, projectIs(serviceA, project, "feat/old"))
	}
	for _, tc := range []struct {
		name string
		ep   checkoutcontext.Endpoint
		used string
	}{
		{"flag beats environment and saved", checkoutcontext.Endpoint{Explicit: servers["flag"].URL, Supplied: true, Environment: servers["environment"].URL}, "flag"},
		{"environment beats saved", checkoutcontext.Endpoint{Environment: servers["environment"].URL}, "environment"},
		{"saved when nothing else is given", checkoutcontext.Endpoint{}, "saved"},
		{"trailing slashes are ignored", checkoutcontext.Endpoint{Environment: servers["environment"].URL + "/"}, "environment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, f := range servers {
				f.mu.Lock()
				f.seen = nil
				f.mu.Unlock()
			}
			v, err := checkoutcontext.Validate(ctx(t), saved(servers["saved"].URL), tc.ep, nil)
			if err != nil || v.Endpoint != servers[tc.used].URL {
				t.Fatalf("%+v %v", v, err)
			}
			for name, f := range servers {
				if got := len(f.requests()); (name == tc.used) != (got == 1) || got > 1 {
					t.Errorf("%s saw %d requests", name, got)
				}
			}
		})
	}
}

func TestAnUnusableEndpointIsNeverReplacedByALowerPrecedenceOne(t *testing.T) {
	f := newFake(t, projectIs(serviceA, project, "feat/old"))
	for _, tc := range []struct {
		name string
		ep   checkoutcontext.Endpoint
	}{
		{"empty flag", checkoutcontext.Endpoint{Explicit: "", Supplied: true, Environment: f.URL}},
		{"flag with a path", checkoutcontext.Endpoint{Explicit: f.URL + "/api", Supplied: true}},
		{"not a URL", checkoutcontext.Endpoint{Explicit: "localhost", Supplied: true}},
		{"environment", checkoutcontext.Endpoint{Environment: "ftp://example.org"}},
	} {
		_, err := checkoutcontext.Validate(ctx(t), saved(f.URL), tc.ep, nil)
		if litclient.Code(err) != "invalid_endpoint" {
			t.Errorf("%s: %v", tc.name, err)
		}
		if _, err = checkoutcontext.ResolveProject(ctx(t), "feat/old", tc.ep, nil); litclient.Code(err) != "invalid_endpoint" {
			t.Errorf("%s (resolve): %v", tc.name, err)
		}
	}
	if got := f.requests(); len(got) != 0 {
		t.Errorf("requests despite unusable endpoints: %v", got)
	}
}

func TestValidateExplainsEveryFailureAndKeepsItsCode(t *testing.T) {
	slow := newFake(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	for _, tc := range []struct {
		name     string
		answer   func(http.ResponseWriter, *http.Request)
		endpoint string // overrides the fake's address
		timeout  time.Duration
		code     string
		status   int
		answered string // the service that answered, for wrong_service
	}{
		{name: "project gone", answer: failsWith(404, "not_found", "project does not exist", `{}`), code: "not_found", status: 404},
		{name: "other service", answer: failsWith(409, "wrong_service", "the stored project belongs to a different service", `{"service_id":"`+serviceB+`"}`), code: "wrong_service", status: 409, answered: serviceB},
		{name: "answer from other service", answer: projectIs(serviceB, project, "feat/old"), code: "wrong_service", status: 409, answered: serviceB},
		{name: "answer for other project", answer: projectIs(serviceA, "22220002-0000-4000-8000-000000000002", "feat/other"), code: "invalid_response", status: 500},
		{name: "old service", answer: failsWith(400, "client_required", "connect first", `{}`), code: "incompatible_service", status: 500},
		{name: "rejected request", answer: failsWith(422, "validation_failed", "bad query", `{}`), code: "validation_failed", status: 422},
		{name: "busy", answer: failsWith(503, "storage_busy", "try later", `{}`), code: "storage_busy", status: 503},
		{name: "nothing listening", endpoint: nowhere, code: "service_unavailable", status: 503},
		{name: "too slow", endpoint: slow.URL, timeout: 100 * time.Millisecond, code: "service_unavailable", status: 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := tc.endpoint
			if tc.answer != nil {
				endpoint = newFake(t, tc.answer).URL
			}
			c := ctx(t)
			if tc.timeout > 0 {
				var cancel context.CancelFunc
				c, cancel = context.WithTimeout(c, tc.timeout)
				defer cancel()
			}
			rec := saved(endpoint)
			_, err := checkoutcontext.Validate(c, rec, checkoutcontext.Endpoint{}, &http.Client{Timeout: tc.timeout})
			var pe *protocol.Error
			if !errors.As(err, &pe) || pe.Code != tc.code || pe.Status != tc.status {
				t.Fatalf("error %v, want %s/%d", err, tc.code, tc.status)
			}
			details, _ := pe.Details.(map[string]any)
			remembered, _ := details["remembered"].(map[string]any)
			if details["endpoint"] != endpoint || remembered["service_id"] != serviceA || remembered["project_id"] != project ||
				remembered["project_title"] != "feat/old" || remembered["endpoint"] != endpoint {
				t.Errorf("details: %v", details)
			}
			if recovery, _ := details["recovery"].(string); !strings.Contains(recovery, "lit context") {
				t.Errorf("recovery: %v", details["recovery"])
			}
			if got, _ := details["answered_by"].(string); got != tc.answered {
				t.Errorf("answered_by %q, want %q", got, tc.answered)
			}
			if !strings.Contains(pe.Message, "feat/old") || !strings.Contains(pe.Message, endpoint) {
				t.Errorf("message does not name the project and endpoint: %s", pe.Message)
			}
		})
	}
}

func TestValidateStopsWhenTheContextIsCancelled(t *testing.T) {
	slow := newFake(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	c, cancel := context.WithCancel(ctx(t))
	cancel()
	if _, err := checkoutcontext.Validate(c, saved(slow.URL), checkoutcontext.Endpoint{}, nil); litclient.Code(err) != "service_unavailable" {
		t.Fatal(err)
	}
}

func TestResolveProjectReturnsAPreferenceTheStoreAccepts(t *testing.T) {
	f := newFake(t, projectIs(serviceA, project, "feat/canonical"))
	pref, err := checkoutcontext.ResolveProject(ctx(t), "FEAT/Canonical", checkoutcontext.Endpoint{Explicit: f.URL + "/", Supplied: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := checkoutcontext.Preference{ServiceID: serviceA, ProjectID: project, ProjectTitle: "feat/canonical", Endpoint: f.URL}
	if pref != want {
		t.Fatalf("preference %+v, want %+v", pref, want)
	}
	if got := f.requests(); len(got) != 1 || got[0] != "GET /v1/project-context?project=FEAT%2FCanonical" || f.clients[0] != "" {
		t.Fatalf("requests: %v %v", got, f.clients)
	}
	store := checkoutcontext.OpenAt(physical(t))
	c := plain(t)
	if _, err = store.Save(c, pref); err != nil {
		t.Fatalf("the store refused %+v: %v", pref, err)
	}
}

func TestResolveProjectExplainsWhyNothingWasChosen(t *testing.T) {
	for _, tc := range []struct {
		name     string
		answer   func(http.ResponseWriter, *http.Request)
		endpoint string
		code     string
	}{
		{"missing", failsWith(404, "not_found", "project does not exist", `{}`), "", "not_found"},
		{"ambiguous", failsWith(409, "ambiguous_reference", "project selector is ambiguous", `{}`), "", "ambiguous_reference"},
		{"unreachable", nil, nowhere, "service_unavailable"},
		{"old service", failsWith(400, "client_required", "connect first", `{}`), "", "incompatible_service"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := tc.endpoint
			if tc.answer != nil {
				endpoint = newFake(t, tc.answer).URL
			}
			_, err := checkoutcontext.ResolveProject(ctx(t), "feat/x", checkoutcontext.Endpoint{Explicit: endpoint, Supplied: true}, nil)
			var pe *protocol.Error
			if !errors.As(err, &pe) || pe.Code != tc.code {
				t.Fatalf("error %v, want %s", err, tc.code)
			}
			details, _ := pe.Details.(map[string]any)
			if details["endpoint"] != endpoint || details["reference"] != "feat/x" || details["recovery"] == "" || !strings.Contains(pe.Message, "feat/x") {
				t.Errorf("message %q details %v", pe.Message, details)
			}
		})
	}
}

func TestResolveProjectRefusesAnEmptyReferenceWithoutAsking(t *testing.T) {
	f := newFake(t, projectIs(serviceA, project, "feat/x"))
	if _, err := checkoutcontext.ResolveProject(ctx(t), "", checkoutcontext.Endpoint{Explicit: f.URL, Supplied: true}, nil); litclient.Code(err) != "invalid_arguments" {
		t.Fatal(err)
	}
	if len(f.requests()) != 0 {
		t.Error("an empty reference reached the service")
	}
}
