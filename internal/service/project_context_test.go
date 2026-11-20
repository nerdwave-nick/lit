package service

import (
	"encoding/json"
	"maps"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// contextFixture is a service with two projects whose IDs share a prefix, so a
// UUID prefix can be ambiguous, and a connected client whose state a
// context request must never change.
type contextFixture struct {
	s      *Server
	client protocol.Client
	alpha  string
	beta   string
}

const (
	contextAlphaID = "a1a1a1a1-1111-4111-8111-111111111111"
	contextBetaID  = "a1a12222-2222-4222-8222-222222222222"
)

func newContextFixture(t *testing.T) contextFixture {
	t.Helper()
	s, c := projectTestServer(t, t.TempDir())
	t.Cleanup(func() { _ = s.DB.Close() })
	for id, title := range map[string]string{contextAlphaID: "feat/alpha", contextBetaID: "feat/beta"} {
		executeRecordTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: id, Title: textPointer(title), Content: textPointer("PRIVATE_DESCRIPTION")}))
	}
	return contextFixture{s: s, client: c, alpha: contextAlphaID, beta: contextBetaID}
}

// contextGet sends GET /v1/project-context without a client and returns the
// status, data and error of the envelope.
func contextGet(t *testing.T, s *Server, query url.Values) (int, map[string]any, map[string]any) {
	t.Helper()
	path := "/v1/project-context"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	code, body := directRequest(t, s, protocol.Client{}, "GET", path, nil)
	data, _ := body["data"].(map[string]any)
	e, _ := body["error"].(map[string]any)
	return code, data, e
}

// A reference resolves exactly as it does for every other project command, so
// the remembered project is the one `projects get REF` shows.
func TestProjectContextResolvesLikeProjectReads(t *testing.T) {
	f := newContextFixture(t)
	refs := []string{
		"feat/alpha", "FEAT/ALPHA", "title:feat/alpha", f.alpha, "id:" + f.alpha,
		"a1a1a1a1", "id:a1a1a1a1", "A1A1A1A1", "a1a1", "a1a1-", "a1a12", "feat/missing",
		"title:" + f.alpha, "id:feat/alpha", "00000000-0000-4000-8000-000000000000",
	}
	for _, ref := range refs {
		t.Run(ref, func(t *testing.T) {
			want, wantErr := inSnapshot(f.s, func(rs *Server) (protocol.Project, error) { return rs.resolveProject(ref) })
			code, data, e := contextGet(t, f.s, url.Values{"project": {ref}})
			if wantErr != nil {
				pe := wantErr.(*protocol.Error)
				if code != pe.Status || e["code"] != pe.Code {
					t.Fatalf("status %d error %v, want %d %s", code, e, pe.Status, pe.Code)
				}
				return
			}
			if code != 200 {
				t.Fatalf("status %d error %v", code, e)
			}
			got := data["project"].(map[string]any)
			if data["service_id"] != f.s.DB.ServiceID() || data["api_major"] != float64(1) || got["id"] != want.ID || got["title"] != want.Title {
				t.Fatalf("%v, want project %s %q on %s", data, want.ID, want.Title, f.s.DB.ServiceID())
			}
		})
	}
}

// Missing and ambiguous targets are refused with the codes the other project
// reads use.
func TestProjectContextRefusesMissingAndAmbiguousProjects(t *testing.T) {
	f := newContextFixture(t)
	for _, tc := range []struct {
		name  string
		query url.Values
		code  int
		err   string
	}{
		{"missing title", url.Values{"project": {"feat/none"}}, 404, "not_found"},
		{"missing UUID", url.Values{"project": {protocol.UUID()}}, 404, "not_found"},
		{"ambiguous prefix", url.Values{"project": {"a1a1"}}, 409, "ambiguous_reference"},
		{"missing stored ID", url.Values{"project_id": {protocol.UUID()}, "service_id": {f.s.DB.ServiceID()}}, 404, "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, data, e := contextGet(t, f.s, tc.query)
			if code != tc.code || e["code"] != tc.err || data != nil {
				t.Fatalf("status %d data %v error %v", code, data, e)
			}
		})
	}
}

// A stored binding is checked against the answering service in the same
// response: a rename shows the new title under the same ID, a different service
// is refused before any project is looked up, and the stored ID is exact.
func TestProjectContextValidatesStoredBinding(t *testing.T) {
	f := newContextFixture(t)
	binding := url.Values{"project_id": {f.alpha}, "service_id": {f.s.DB.ServiceID()}}
	code, data, e := contextGet(t, f.s, binding)
	project, _ := data["project"].(map[string]any)
	if code != 200 || project["id"] != f.alpha || project["title"] != "feat/alpha" || data["service_id"] != f.s.DB.ServiceID() {
		t.Fatal(code, data, e)
	}
	executeRecordTest(t, f.s, f.client, prepareProjectTest(t, f.s, f.client, "update", protocol.ProjectInput{Target: f.alpha, Set: protocol.ProjectSet{Title: textPointer("feat/renamed")}}))
	code, data, e = contextGet(t, f.s, binding)
	project, _ = data["project"].(map[string]any)
	if code != 200 || project["id"] != f.alpha || project["title"] != "feat/renamed" {
		t.Fatal("rename not followed", code, data, e)
	}
	// The old title no longer resolves, the stored ID still does.
	if code, _, e = contextGet(t, f.s, url.Values{"project": {"feat/alpha"}}); code != 404 || e["code"] != "not_found" {
		t.Fatal(code, e)
	}

	other := protocol.UUID()
	code, data, e = contextGet(t, f.s, url.Values{"project_id": {f.alpha}, "service_id": {other}})
	details, _ := e["details"].(map[string]any)
	if code != 409 || e["code"] != "wrong_service" || data != nil || details["service_id"] != f.s.DB.ServiceID() {
		t.Fatal("other service accepted", code, data, e)
	}
	// The service check comes first: a project missing here is not "missing"
	// on a service it was never bound to.
	code, _, e = contextGet(t, f.s, url.Values{"project_id": {protocol.UUID()}, "service_id": {other}})
	if code != 409 || e["code"] != "wrong_service" {
		t.Fatal(code, e)
	}
	// A prefix is not a stored identity.
	code, _, e = contextGet(t, f.s, url.Values{"project_id": {f.alpha[:8]}, "service_id": {f.s.DB.ServiceID()}})
	if code != 422 || e["code"] != "validation_failed" {
		t.Fatal(code, e)
	}
}

func TestProjectContextRefusesMalformedRequests(t *testing.T) {
	f := newContextFixture(t)
	service := f.s.DB.ServiceID()
	for _, tc := range []struct {
		name  string
		query string
	}{
		{"no parameters", ""},
		{"empty reference", "project="},
		{"reference and stored ID", "project=feat/alpha&project_id=" + f.alpha + "&service_id=" + service},
		{"reference with service", "project=feat/alpha&service_id=" + service},
		{"stored ID without service", "project_id=" + f.alpha},
		{"service without stored ID", "service_id=" + service},
		{"empty stored ID", "project_id=&service_id=" + service},
		{"stored ID prefix", "project_id=" + f.alpha[:13] + "&service_id=" + service},
		{"uppercase stored ID", "project_id=" + strings.ToUpper(f.alpha) + "&service_id=" + service},
		{"service prefix", "project_id=" + f.alpha + "&service_id=" + service[:8]},
		{"empty service", "project_id=" + f.alpha + "&service_id="},
		{"unknown parameter", "project=feat/alpha&content=true"},
		{"repeated reference", "project=feat/alpha&project=feat/beta"},
		{"repeated stored ID", "project_id=" + f.alpha + "&project_id=" + f.beta + "&service_id=" + service},
		{"control character", "project=feat%0Aalpha"},
		{"invalid UTF-8", "project=feat%ff"},
		{"too long", "project=" + strings.Repeat("a", 301)},
		{"malformed query escape", "project=%zz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := directRequest(t, f.s, protocol.Client{}, "GET", "/v1/project-context?"+tc.query, nil)
			e, _ := body["error"].(map[string]any)
			if code != 422 || e["code"] != "validation_failed" || body["data"] != nil {
				t.Fatalf("status %d body %v", code, body)
			}
		})
	}
	// Only GET reads context.
	for _, method := range []string{"POST", "PATCH", "PUT", "DELETE"} {
		code, body := directRequest(t, f.s, protocol.Client{}, method, "/v1/project-context?project=feat/alpha", map[string]any{})
		e, _ := body["error"].(map[string]any)
		if code != 405 || e["code"] != "method_not_allowed" {
			t.Fatal(method, code, body)
		}
	}
	// The longest allowed reference is a validation, not a lookup, failure.
	if code, _, e := contextGet(t, f.s, url.Values{"project": {strings.Repeat("a", 300)}}); code != 404 || e["code"] != "not_found" {
		t.Fatal(code, e)
	}
}

// The projection holds identity and the display title only, and a context
// request registers nothing, changes no record, client or claim, and does not
// consult a client ID it is sent.
func TestProjectContextIsReadOnlyAndSessionFree(t *testing.T) {
	f := newContextFixture(t)
	before := completionTree(t, f.s, f.client)
	service := f.s.DB.ServiceID()
	unknown := protocol.UUID()
	for _, c := range []protocol.Client{{}, {ClientID: unknown}, f.client} {
		for _, q := range []url.Values{
			{"project": {"feat/alpha"}},
			{"project_id": {f.beta}, "service_id": {service}},
			{"project": {"feat/none"}},
			{"project_id": {f.beta}, "service_id": {protocol.UUID()}},
			{"project": {""}},
		} {
			code, body := directRequest(t, f.s, c, "GET", "/v1/project-context?"+q.Encode(), nil)
			if encoded, _ := json.Marshal(body); strings.Contains(string(encoded), "PRIVATE_DESCRIPTION") {
				t.Fatal("content leaked", string(encoded))
			}
			if data, ok := body["data"].(map[string]any); ok {
				if !reflect.DeepEqual(keys(data), []string{"api_major", "project", "service_id"}) || !reflect.DeepEqual(keys(data["project"].(map[string]any)), []string{"id", "title"}) {
					t.Fatalf("unapproved fields: %v", data)
				}
			} else if code == 200 {
				t.Fatal(body)
			}
		}
	}
	if _, err := f.s.Client(unknown); err == nil {
		t.Fatal("a context request registered the client ID it was sent")
	}
	if !reflect.DeepEqual(before, completionTree(t, f.s, f.client)) {
		t.Fatal("a context request changed durable state")
	}
}

// The new route is the only session-free project read: the generic project
// routes and every other route keep requiring a connected client.
func TestProjectContextLeavesSessionRoutesGated(t *testing.T) {
	f := newContextFixture(t)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/v1/projects"},
		{"GET", "/v1/projects/feat/alpha"},
		{"GET", "/v1/projects/" + f.alpha},
		{"GET", "/v1/projects/" + f.alpha + "/history"},
		{"POST", "/v1/snapshots"},
		{"POST", "/v1/transaction-previews"},
		{"POST", "/v1/transactions"},
		{"POST", "/v1/operations"},
		{"GET", "/v1/claims"},
		{"POST", "/v1/search"},
		{"GET", "/v1/project-context/feat/alpha"},
		{"GET", "/v1/project-contexts"},
	} {
		code, body := directRequest(t, f.s, protocol.Client{}, tc.method, tc.path, nil)
		e, _ := body["error"].(map[string]any)
		if code != 400 || e["code"] != "client_required" {
			t.Errorf("%s %s: %d %v", tc.method, tc.path, code, body)
		}
	}
	// With a connected client the project reads still work as before.
	code, body := directRequest(t, f.s, f.client, "GET", "/v1/projects/feat/alpha", nil)
	if code != 200 || body["data"].(map[string]any)["title"] != "feat/alpha" {
		t.Fatal(code, body)
	}
}

func keys(m map[string]any) []string { return slices.Sorted(maps.Keys(m)) }
