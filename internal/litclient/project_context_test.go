package litclient_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// requests copies the counting front's per-route request counts.
func (h *harness) requests() map[string]int {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]int{}
	for k, v := range h.counts {
		out[k] = v
	}
	return out
}

func contextClient(h *harness) litclient.ProjectContextClient {
	return litclient.ProjectContextClient{Endpoint: h.url, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// Resolving and validating need no session: a fresh caller finds the project by
// every reference, follows a rename by ID, and sends one GET per call and
// nothing else, so no client, claim, mutation or pending request exists.
func TestProjectContextWithoutASession(t *testing.T) {
	h := newHarness(t)
	setup := h.connect("setup", "Setup")
	apply(t, setup, litclient.CreateProject("test/context", "PRIVATE"))
	project, err := litclient.Get[protocol.Project](context.Background(), setup, "", "test/context")
	if err != nil || len(project) != 1 {
		t.Fatal(project, err)
	}
	id := project[0].ID
	c := contextClient(h)
	ctx := context.Background()

	before := h.requests()
	var service string
	for _, ref := range []string{"test/context", "title:test/context", id, "id:" + id, id[:8], "TEST/CONTEXT"} {
		got, err := c.Resolve(ctx, ref)
		if err != nil || got.ProjectID != id || got.Title != "test/context" || !protocol.ValidUUID(got.ServiceID) {
			t.Fatalf("resolve %q: %+v %v", ref, got, err)
		}
		service = got.ServiceID
	}
	binding := litclient.ProjectBinding{ServiceID: service, ProjectID: id}
	got, err := c.Validate(ctx, binding)
	if err != nil || got != (litclient.ProjectContext{ServiceID: service, ProjectID: id, Title: "test/context"}) {
		t.Fatalf("validate: %+v %v", got, err)
	}
	h.requireOnlyContextRequests(before, 7)

	// A rename keeps the stored identity valid and shows the new title; the old
	// title is gone.
	apply(t, setup, litclient.UpdateProject("test/context", 0, litclient.ProjectChange{Title: str("test/renamed")}))
	before = h.requests()
	if got, err = c.Validate(ctx, binding); err != nil || got.Title != "test/renamed" || got.ProjectID != id {
		t.Fatalf("after rename: %+v %v", got, err)
	}
	if _, err = c.Resolve(ctx, "test/context"); litclient.Code(err) != "not_found" {
		t.Fatalf("old title: %v", err)
	}
	if _, err = c.Resolve(ctx, "test/missing"); litclient.Code(err) != "not_found" {
		t.Fatalf("missing: %v", err)
	}
	if _, err = c.Validate(ctx, litclient.ProjectBinding{ServiceID: service, ProjectID: protocol.UUID()}); litclient.Code(err) != "not_found" {
		t.Fatalf("missing ID: %v", err)
	}
	h.requireOnlyContextRequests(before, 4)
	if len(h.pending()) != 0 {
		t.Fatal("pending requests", h.pending())
	}
}

// requireOnlyContextRequests fails unless, since before, the service saw
// exactly want project context requests and no other request: no connect, no
// client read, no claim, no transaction.
func (h *harness) requireOnlyContextRequests(before map[string]int, want int) {
	h.t.Helper()
	for route, n := range h.requests() {
		wantN := before[route]
		if route == "GET /v1/project-context" {
			wantN += want
		}
		if n != wantN {
			h.t.Errorf("%s: %d requests, want %d", route, n, wantN)
		}
	}
}

// A binding for another service is refused, whether the service says so or an
// answer claims an identity other than the one asked for.
func TestProjectContextWrongService(t *testing.T) {
	a, b := newHarness(t), newHarness(t)
	setup := a.connect("setup", "Setup")
	apply(t, setup, litclient.CreateProject("test/context", "spec"))
	apply(t, b.connect("setup", "Setup"), litclient.CreateProject("test/context", "spec"))
	resolved, err := contextClient(a).Resolve(context.Background(), "test/context")
	if err != nil {
		t.Fatal(err)
	}
	binding := litclient.ProjectBinding{ServiceID: resolved.ServiceID, ProjectID: resolved.ProjectID}

	// The same name on a replacement service is neither the stored project
	// nor the stored service.
	_, err = contextClient(b).Validate(context.Background(), binding)
	var pe *protocol.Error
	if litclient.Code(err) != "wrong_service" || !errors.As(err, &pe) {
		t.Fatalf("replacement service: %v", err)
	}
	if details, _ := pe.Details.(map[string]any); details["service_id"] == resolved.ServiceID || !protocol.ValidUUID(fmt.Sprint(details["service_id"])) {
		t.Fatalf("details name the stored service instead of the answering one: %v", pe.Details)
	}

	// An answer from another service than the one asked for is refused by the
	// caller even if the server did not object.
	other := protocol.UUID()
	liar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respondData(w, fmt.Sprintf(`{"service_id":%q,"api_major":1,"project":{"id":%q,"title":"test/context"}}`, other, binding.ProjectID))
	}))
	defer liar.Close()
	if _, err = (litclient.ProjectContextClient{Endpoint: liar.URL}).Validate(context.Background(), binding); litclient.Code(err) != "wrong_service" {
		t.Fatalf("answer from another service: %v", err)
	}
	// A different project than the stored one is a broken answer, not a match.
	liar2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respondData(w, fmt.Sprintf(`{"service_id":%q,"api_major":1,"project":{"id":%q,"title":"test/context"}}`, binding.ServiceID, protocol.UUID()))
	}))
	defer liar2.Close()
	if _, err = (litclient.ProjectContextClient{Endpoint: liar2.URL}).Validate(context.Background(), binding); litclient.Code(err) != "invalid_response" {
		t.Fatalf("answer for another project: %v", err)
	}
}

func respondData(w http.ResponseWriter, data string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"data":%s,"next_cursor":null,"server_time":"2026-10-14T00:00:00Z"}`, data)
}

func respondRaw(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}
}

// Arguments the caller can see are wrong never reach the network.
func TestProjectContextRejectsBadArgumentsLocally(t *testing.T) {
	requested := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requested = true }))
	defer server.Close()
	good := litclient.ProjectContextClient{Endpoint: server.URL}
	id := protocol.UUID()
	for _, tc := range []struct {
		name string
		call func() error
		code string
	}{
		{"empty reference", func() error { _, e := good.Resolve(context.Background(), ""); return e }, "invalid_arguments"},
		{"empty stored project", func() error {
			_, e := good.Validate(context.Background(), litclient.ProjectBinding{ServiceID: id})
			return e
		}, "invalid_arguments"},
		{"empty stored service", func() error {
			_, e := good.Validate(context.Background(), litclient.ProjectBinding{ProjectID: id})
			return e
		}, "invalid_arguments"},
		{"project prefix", func() error {
			_, e := good.Validate(context.Background(), litclient.ProjectBinding{ServiceID: id, ProjectID: id[:8]})
			return e
		}, "invalid_arguments"},
		{"uppercase service", func() error {
			_, e := good.Validate(context.Background(), litclient.ProjectBinding{ServiceID: strings.ToUpper(id), ProjectID: id})
			return e
		}, "invalid_arguments"},
		{"endpoint without scheme", func() error {
			_, e := litclient.ProjectContextClient{Endpoint: "localhost:7411"}.Resolve(context.Background(), "test/a")
			return e
		}, "invalid_endpoint"},
		{"empty endpoint", func() error {
			_, e := litclient.ProjectContextClient{}.Resolve(context.Background(), "test/a")
			return e
		}, "invalid_endpoint"},
		{"endpoint with path", func() error {
			_, e := litclient.ProjectContextClient{Endpoint: server.URL + "/v1"}.Resolve(context.Background(), "test/a")
			return e
		}, "invalid_endpoint"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code := litclient.Code(tc.call()); code != tc.code {
				t.Fatalf("code %q, want %q", code, tc.code)
			}
		})
	}
	if requested {
		t.Fatal("a request was sent for an argument that is wrong locally")
	}
}

// The service's own refusals of a request the caller could not see to be wrong
// come through with their codes.
func TestProjectContextRelaysServiceRefusals(t *testing.T) {
	h := newHarness(t)
	_, err := contextClient(h).Resolve(context.Background(), "bad\nreference")
	if litclient.Code(err) != "validation_failed" {
		t.Fatalf("control character: %v", err)
	}
	_, err = contextClient(h).Resolve(context.Background(), strings.Repeat("a", 301))
	if litclient.Code(err) != "validation_failed" {
		t.Fatalf("long reference: %v", err)
	}
}

// Every way the answer can be missing or wrong is a classified error, never a
// zero context that looks valid.
func TestProjectContextTransportAndResponseErrors(t *testing.T) {
	service, project := protocol.UUID(), protocol.UUID()
	valid := fmt.Sprintf(`{"service_id":%q,"api_major":1,"project":{"id":%q,"title":"test/a"}}`, service, project)
	binding := litclient.ProjectBinding{ServiceID: service, ProjectID: project}
	cases := []struct {
		name    string
		handler http.HandlerFunc
		code    string
	}{
		{"old service without the operation", respondRaw(400, `{"error":{"code":"client_required","message":"X-Lit-Client-ID is required","details":{}},"server_time":"2026-10-14T00:00:00Z"}`), "incompatible_service"},
		{"service refusal", respondRaw(503, `{"error":{"code":"storage_busy","message":"storage is busy; retry the request","details":{}},"server_time":"2026-10-14T00:00:00Z"}`), "storage_busy"},
		{"shutting down", respondRaw(503, `{"error":{"code":"shutting_down","message":"service is shutting down","details":{}},"server_time":"2026-10-14T00:00:00Z"}`), "shutting_down"},
		{"not JSON", respondRaw(200, `<html>proxy</html>`), "invalid_response"},
		{"truncated JSON", respondRaw(200, `{"data":{"service_id":`), "invalid_response"},
		{"empty body", respondRaw(200, ``), "invalid_response"},
		{"no data", respondRaw(200, `{"next_cursor":null,"server_time":"2026-10-14T00:00:00Z"}`), "invalid_response"},
		{"proxy error status", respondRaw(502, `{"next_cursor":null,"server_time":"2026-10-14T00:00:00Z"}`), "invalid_response"},
		{"other API major", respondRaw(200, fmt.Sprintf(`{"data":{"service_id":%q,"api_major":2,"project":{"id":%q,"title":"test/a"}},"server_time":"x"}`, service, project)), "incompatible_service"},
		{"invalid service ID", respondRaw(200, fmt.Sprintf(`{"data":{"service_id":"x","api_major":1,"project":{"id":%q,"title":"test/a"}},"server_time":"x"}`, project)), "incompatible_service"},
		{"invalid project ID", respondRaw(200, fmt.Sprintf(`{"data":{"service_id":%q,"api_major":1,"project":{"id":"x","title":"test/a"}},"server_time":"x"}`, service)), "invalid_response"},
		{"no title", respondRaw(200, fmt.Sprintf(`{"data":{"service_id":%q,"api_major":1,"project":{"id":%q,"title":""}},"server_time":"x"}`, service, project)), "invalid_response"},
		{"no project", respondRaw(200, fmt.Sprintf(`{"data":{"service_id":%q,"api_major":1},"server_time":"x"}`, service)), "invalid_response"},
		{"slow service", func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		}, "service_unavailable"},
		{"valid", func(w http.ResponseWriter, r *http.Request) { respondData(w, valid) }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.handler)
			defer server.Close()
			c := litclient.ProjectContextClient{Endpoint: server.URL, HTTP: &http.Client{Timeout: 150 * time.Millisecond}}
			for op, call := range map[string]func() (litclient.ProjectContext, error){
				"resolve":  func() (litclient.ProjectContext, error) { return c.Resolve(context.Background(), "test/a") },
				"validate": func() (litclient.ProjectContext, error) { return c.Validate(context.Background(), binding) },
			} {
				got, err := call()
				if code := litclient.Code(err); code != tc.code {
					t.Fatalf("%s: code %q (%v), want %q", op, code, err, tc.code)
				}
				if tc.code != "" && got != (litclient.ProjectContext{}) {
					t.Fatalf("%s: context %+v with an error", op, got)
				}
				if tc.code == "" && got != (litclient.ProjectContext{ServiceID: service, ProjectID: project, Title: "test/a"}) {
					t.Fatalf("%s: %+v", op, got)
				}
			}
		})
	}

	t.Run("unreachable service", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		url := server.URL
		server.Close()
		if _, err := (litclient.ProjectContextClient{Endpoint: url}).Resolve(context.Background(), "test/a"); litclient.Code(err) != "service_unavailable" {
			t.Fatal(err)
		}
	})
	t.Run("cancelled context", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { respondData(w, valid) }))
		defer server.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := (litclient.ProjectContextClient{Endpoint: server.URL}).Validate(ctx, binding); litclient.Code(err) != "service_unavailable" {
			t.Fatal(err)
		}
	})
}
