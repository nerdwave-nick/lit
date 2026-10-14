package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/checkoutcontext"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

func TestContextShowValidateWithNothingRememberedMakesNoRequest(t *testing.T) {
	w := newContextWorld(t)
	svc := newContextService(t, "test/alpha")
	dir := physicalDir(t)
	t.Setenv("LIT_ENDPOINT", svc.URL)

	for _, args := range [][]string{
		{"context", "show", "--validate"},
		{"context", "show", "--validate", "--endpoint", svc.URL, "--timeout", "5s"},
	} {
		it := contextItem(t, dir, args...)
		if it["remembered"] != false || it["validated"] != false || it["preference"] != nil || it["message"] != "No project remembered for this checkout" {
			t.Fatalf("%v: %v", args, it)
		}
	}
	for _, format := range []string{"cli", "markdown"} {
		code, out, errOut := runLit(t, dir, "context", "show", "--validate", "--format", format)
		if code != 0 || errOut != "" || !strings.Contains(out, "No project remembered for this checkout") {
			t.Errorf("%s: exit %d\n%s\n%s", format, code, out, errOut)
		}
	}
	svc.only(t)
	if w.requests.Load() != 0 {
		t.Error("the ambient service was contacted")
	}
}

func TestContextShowValidateReportsTheCurrentProjectAndChangesNothing(t *testing.T) {
	w := newContextWorld(t)
	w.seedNeighbors(t)
	svc := newContextService(t, "test/alpha")
	root, linked := fixtureRepository(t)
	saved := svc.preference("test/alpha")
	w.remember(t, root, saved)
	w.save(t, linked, 9)
	otherFile := w.record(t, linked)
	otherBefore, _ := os.ReadFile(otherFile)
	t.Setenv("LIT_SESSION", "no-such-session")
	t.Setenv("LIT_STATE_DIR", "/dev/null/unusable")
	t.Setenv("LIT_ENDPOINT", "") // the saved endpoint is used
	file := w.record(t, root)
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(file)
	neighbors := w.neighbors(t)

	it := contextItem(t, root+"/pkg/deep", "context", "show", "--validate")
	pref, _ := it["preference"].(map[string]any)
	current, _ := it["current"].(map[string]any)
	if it["remembered"] != true || it["validated"] != true || it["renamed"] != false ||
		pref["project_title"] != "test/alpha" || pref["project_id"] != saved.ProjectID || pref["endpoint"] != svc.URL ||
		current["project_title"] != "test/alpha" || current["project_id"] != saved.ProjectID || current["service_id"] != saved.ServiceID || current["endpoint"] != svc.URL {
		t.Fatalf("validated: %v", it)
	}
	svc.only(t, requestFor(contextQuery(saved)))

	for format, wants := range map[string][]string{
		"cli":      {"Remembered: true", "Validated: true", "Renamed: false", "Project title: test/alpha", "Service ID: " + saved.ServiceID, "Endpoint: " + svc.URL},
		"markdown": {"# Context show", "| Validated | true |", "| Current / Project title | test/alpha |"},
	} {
		code, out, errOut := runLit(t, root, "context", "show", "--validate", "--format", format)
		if code != 0 || errOut != "" {
			t.Fatalf("%s: exit %d\n%s\n%s", format, code, out, errOut)
		}
		for _, want := range wants {
			if !strings.Contains(out, want) {
				t.Errorf("%s output lacks %q:\n%s", format, want, out)
			}
		}
	}
	if after, err := os.ReadFile(file); err != nil || string(after) != string(before) {
		t.Errorf("validation rewrote the preference: %v", err)
	}
	if after, _ := os.Stat(file); !after.ModTime().Equal(info.ModTime()) {
		t.Error("validation touched the preference file")
	}
	if w.neighbors(t) != neighbors {
		t.Error("validation changed sessions, pending requests or location maps")
	}
	if after, _ := os.ReadFile(otherFile); string(after) != string(otherBefore) {
		t.Error("validation changed the other worktree's preference")
	}
	for _, r := range svc.requests() {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URI, "/v1/project-context?") || r.Client != "" {
			t.Errorf("unexpected request %+v", r)
		}
	}
}

func TestContextShowValidateFollowsARenameWithoutRewritingThePreference(t *testing.T) {
	w := newContextWorld(t)
	svc := newContextService(t, "test/alpha")
	dir := physicalDir(t)
	saved := svc.preference("test/alpha")
	w.remember(t, dir, saved)
	t.Setenv("LIT_ENDPOINT", "") // the saved endpoint is used
	svc.admin("projects", "update", "test/alpha", "--title", "test/renamed")
	svc.forget()
	file := w.record(t, dir)
	before, _ := os.ReadFile(file)

	it := contextItem(t, dir, "context", "show", "--validate")
	pref, _ := it["preference"].(map[string]any)
	current, _ := it["current"].(map[string]any)
	if it["validated"] != true || it["renamed"] != true || current["project_title"] != "test/renamed" || current["project_id"] != saved.ProjectID ||
		pref["project_title"] != "test/alpha" || !strings.Contains(it["message"].(string), "test/renamed") {
		t.Fatalf("renamed: %v", it)
	}
	if after, _ := os.ReadFile(file); string(after) != string(before) {
		t.Error("validation rewrote the saved title")
	}
	if shown := contextItem(t, dir, "context", "show"); shown["preference"].(map[string]any)["project_title"] != "test/alpha" {
		t.Errorf("show after validation: %v", shown)
	}
}

func TestContextShowValidateChoosesTheEndpointByPrecedence(t *testing.T) {
	w := newContextWorld(t)
	svc := newContextService(t, "test/alpha")
	alias := svc.front() // the same service at another address
	other := newContextService(t, "test/alpha")
	dir := physicalDir(t)
	saved := svc.preference("test/alpha")
	saved.Endpoint = nothing // the address it was saved with no longer works
	w.remember(t, dir, saved)
	file := w.record(t, dir)
	before, _ := os.ReadFile(file)
	host := func(url string) string { return strings.TrimPrefix(url, "http://") }
	answeredBy := func(it map[string]any) any { return it["current"].(map[string]any)["endpoint"] }

	// The saved endpoint is used when nothing else is given.
	t.Setenv("LIT_ENDPOINT", "")
	code, e := contextFailure(t, dir, "context", "show", "--validate")
	if code != 1 || e["code"] != "service_unavailable" {
		t.Fatalf("saved endpoint: exit %d, %v", code, e)
	}
	// LIT_ENDPOINT beats it, and a different address reaching the same service is accepted.
	t.Setenv("LIT_ENDPOINT", alias)
	it := contextItem(t, dir, "context", "show", "--validate")
	if answeredBy(it) != alias || it["validated"] != true {
		t.Fatalf("environment: %v", it)
	}
	if reqs := svc.requests(); len(reqs) != 1 || reqs[0].Host != host(alias) {
		t.Fatalf("the environment's address was not the one asked: %+v", reqs)
	}
	// The flag beats the environment: the environment names another service.
	t.Setenv("LIT_ENDPOINT", other.URL)
	svc.forget()
	it = contextItem(t, dir, "context", "show", "--validate", "--endpoint", svc.URL)
	if answeredBy(it) != svc.URL || it["validated"] != true {
		t.Fatalf("flag: %v", it)
	}
	other.only(t)
	// Without the flag the environment's service is not the remembered one.
	code, e = contextFailure(t, dir, "context", "show", "--validate")
	if code != 1 || e["code"] != "wrong_service" {
		t.Fatalf("environment naming another service: exit %d, %v", code, e)
	}
	// An unusable explicit endpoint is an error, never a fallback to the others.
	svc.forget()
	for _, bad := range []string{"", "not-a-url", "http://127.0.0.1:1/path"} {
		code, e = contextFailure(t, dir, "context", "show", "--validate", "--endpoint", bad)
		if code != 2 || e["code"] != "invalid_endpoint" {
			t.Errorf("--endpoint %q: exit %d, %v", bad, code, e)
		}
	}
	svc.only(t)
	t.Setenv("LIT_ENDPOINT", "not-a-url")
	if code, e = contextFailure(t, dir, "context", "show", "--validate"); code != 2 || e["code"] != "invalid_endpoint" {
		t.Errorf("unusable LIT_ENDPOINT: exit %d, %v", code, e)
	}
	if after, _ := os.ReadFile(file); string(after) != string(before) {
		t.Error("the saved endpoint was rewritten")
	}
}

func TestContextShowValidateRefusesAnotherServiceAtTheSameAddress(t *testing.T) {
	w := newContextWorld(t)
	original := newContextService(t, "test/alpha")
	replacement := newContextService(t, "test/alpha") // same title, other identity
	dir := physicalDir(t)
	saved := original.preference("test/alpha")
	w.remember(t, dir, saved)
	file := w.record(t, dir)
	before, _ := os.ReadFile(file)
	t.Setenv("LIT_ENDPOINT", replacement.URL)

	for _, format := range []string{"json", "cli", "markdown"} {
		code, out, errOut := runLit(t, dir, "context", "show", "--validate", "--format", format)
		if code != 1 || out != "" {
			t.Fatalf("%s: exit %d\n%s\n%s", format, code, out, errOut)
		}
		for _, want := range map[string][]string{
			"json":     {`"code":"wrong_service"`, saved.ServiceID, replacement.context("test/alpha").ServiceID},
			"cli":      {"Error [wrong_service]", saved.ServiceID, replacement.URL},
			"markdown": {"**Error [wrong&#95;service]:**", saved.ServiceID},
		}[format] {
			if !strings.Contains(errOut, want) {
				t.Errorf("%s error lacks %q:\n%s", format, want, errOut)
			}
		}
	}
	_, e := contextFailure(t, dir, "context", "show", "--validate")
	details := contextProblem(t, e)
	remembered, _ := details["remembered"].(map[string]any)
	if details["endpoint"] != replacement.URL || details["answered_by"] != replacement.context("test/alpha").ServiceID ||
		remembered["service_id"] != saved.ServiceID || remembered["project_id"] != saved.ProjectID || remembered["project_title"] != "test/alpha" ||
		!strings.Contains(details["recovery"].(string), "lit context") {
		t.Fatalf("details: %v", details)
	}
	if after, _ := os.ReadFile(file); string(after) != string(before) {
		t.Error("the binding was rewritten after a wrong-service answer")
	}
	replacement.forget()
	if shown := contextItem(t, dir, "context", "show"); shown["preference"].(map[string]any)["service_id"] != saved.ServiceID {
		t.Errorf("show after the refusal: %v", shown)
	}
}

func TestContextShowValidateReportsEveryOtherFailureClearly(t *testing.T) {
	w := newContextWorld(t)
	svc := newContextService(t, "test/alpha")
	dir := physicalDir(t)
	saved := svc.preference("test/alpha")
	incompatible := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"client_required","message":"connect first","details":{}}}`))
	}))
	defer incompatible.Close()
	held := newContextService(t, "test/alpha")
	release := held.holdRequests()
	defer release()
	gone := saved
	gone.ProjectID = protocol.UUID()
	t.Setenv("LIT_ENDPOINT", "") // each case uses the endpoint it was saved with

	for _, tc := range []struct {
		name       string
		pref       checkoutcontext.Preference
		endpoint   string
		extra      []string
		code       string
		exit       int
		mentioning string
	}{
		{"project missing on the service", gone, svc.URL, nil, "not_found", 1, "feat/none"},
		{"service not running", saved, nothing, nil, "service_unavailable", 1, nothing},
		{"service too slow", saved, held.URL, []string{"--timeout", "150ms"}, "service_unavailable", 1, held.URL},
		{"service without the operation", saved, incompatible.URL, nil, "incompatible_service", 1, "upgrade"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.pref.Endpoint = tc.endpoint
			tc.pref.ProjectTitle = "feat/none"
			w.remember(t, dir, tc.pref)
			before, _ := os.ReadFile(w.record(t, dir))
			started := time.Now()
			code, e := contextFailure(t, dir, append([]string{"context", "show", "--validate"}, tc.extra...)...)
			if code != tc.exit || e["code"] != tc.code || time.Since(started) > 10*time.Second {
				t.Fatalf("exit %d after %s, %v", code, time.Since(started), e)
			}
			details := contextProblem(t, e)
			remembered, _ := details["remembered"].(map[string]any)
			if details["endpoint"] != tc.endpoint || remembered["project_title"] != "feat/none" || remembered["project_id"] != tc.pref.ProjectID ||
				details["recovery"] == "" || !strings.Contains(e["message"].(string)+details["recovery"].(string), tc.mentioning) {
				t.Errorf("details: %v", details)
			}
			if after, _ := os.ReadFile(w.record(t, dir)); string(after) != string(before) {
				t.Error("the failed validation changed the preference")
			}
		})
	}
}

func TestContextShowValidateReportsALocalRecordProblemWithoutAskingTheService(t *testing.T) {
	w := newContextWorld(t)
	svc := newContextService(t, "test/alpha")
	t.Setenv("LIT_ENDPOINT", svc.URL)
	dir := physicalDir(t)
	w.remember(t, dir, svc.preference("test/alpha"))
	file := w.record(t, dir)
	if err := os.WriteFile(file, []byte(`{"schema_version":2,"anything":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	code, e := contextFailure(t, dir, "context", "show", "--validate")
	details := contextProblem(t, e)
	if code != 1 || e["code"] != "invalid_local_state" || details["problem"] != "unsupported" || details["record"] != file {
		t.Fatalf("exit %d, %v", code, e)
	}
	svc.only(t)
	if got, _ := os.ReadFile(file); string(got) != `{"schema_version":2,"anything":true}` {
		t.Errorf("the record changed: %s", got)
	}
}

func TestContextSetThenValidateThenClearIsOneFlowThatNeverNeedsASession(t *testing.T) {
	w := newContextWorld(t)
	svc := newContextService(t, "test/alpha")
	dir := physicalDir(t)
	t.Setenv("LIT_ENDPOINT", svc.URL)
	t.Setenv("LIT_SESSION", "")

	contextItem(t, dir, "context", "set", "--project", "test/alpha")
	if it := contextItem(t, dir, "context", "show", "--validate"); it["validated"] != true {
		t.Fatalf("validate: %v", it)
	}
	svc.admin("projects", "update", "test/alpha", "--title", "test/after-rename")
	svc.forget()
	if it := contextItem(t, dir, "context", "show", "--validate"); it["renamed"] != true || it["current"].(map[string]any)["project_title"] != "test/after-rename" {
		t.Fatalf("validate after rename: %v", it)
	}
	// The service goes away: show and clear still work, validation says so.
	t.Setenv("LIT_ENDPOINT", nothing)
	if it := contextItem(t, dir, "context", "show"); it["remembered"] != true {
		t.Fatalf("offline show: %v", it)
	}
	if code, e := contextFailure(t, dir, "context", "show", "--validate", "--endpoint", nothing); code != 1 || e["code"] != "service_unavailable" {
		t.Fatalf("offline validate: exit %d, %v", code, e)
	}
	if it := contextItem(t, dir, "context", "clear"); it["outcome"] != "applied" {
		t.Fatalf("clear: %v", it)
	}
	if it := contextItem(t, dir, "context", "show", "--validate"); it["remembered"] != false {
		t.Fatalf("validate after clear: %v", it)
	}
	if _, err := w.store().Load(mustCheckout(t, dir)); err != nil {
		t.Fatal(err)
	}
}
