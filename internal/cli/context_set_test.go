package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestContextSetRemembersAValidatedProjectWithoutASession(t *testing.T) {
	w := newContextWorld(t)
	w.seedNeighbors(t)
	svc := newContextService(t, "test/alpha", "test/beta")
	root, _ := fixtureRepository(t)
	neighbors := w.neighbors(t)
	// No session, no usable client state, and an ambient endpoint: the command
	// needs none of the first two and follows the third.
	t.Setenv("LIT_SESSION", "no-such-session")
	t.Setenv("LIT_STATE_DIR", "/dev/null/unusable")
	t.Setenv("LIT_ENDPOINT", svc.URL)
	want := svc.preference("test/alpha")

	it := contextItem(t, filepath.Join(root, "pkg", "deep"), "context", "set", "--project", "test/alpha")
	pref, _ := it["preference"].(map[string]any)
	checkout, _ := it["checkout"].(map[string]any)
	if it["outcome"] != "applied" || it["remembered"] != true || it["validated"] != true || it["previous"] != nil ||
		checkout["path"] != root || checkout["kind"] != "git" ||
		pref["service_id"] != want.ServiceID || pref["project_id"] != want.ProjectID || pref["project_title"] != "test/alpha" || pref["endpoint"] != svc.URL {
		t.Fatalf("set: %v", it)
	}
	svc.only(t, requestFor(ref("test/alpha")))

	shown := contextItem(t, root, "context", "show")
	if shown["remembered"] != true || shown["validated"] != false {
		t.Fatalf("show after set: %v", shown)
	}
	if got, _ := shown["preference"].(map[string]any); got["saved_at"] != pref["saved_at"] || got["project_id"] != want.ProjectID {
		t.Fatalf("show after set: %v", shown)
	}
	svc.only(t, requestFor(ref("test/alpha"))) // show is offline

	if w.neighbors(t) != neighbors {
		t.Error("set changed sessions, pending requests, location maps or other workflow state")
	}
	for format, wants := range map[string][]string{
		"cli":      {"Outcome: applied", "Remembered: true", "Validated: true", "Project title: test/alpha", "Project ID: " + want.ProjectID, "Endpoint: " + svc.URL},
		"markdown": {"# Context set", "| Validated | true |", "| Preference / Project title | test/alpha |"},
	} {
		code, out, errOut := runLit(t, root, "context", "set", "--project", "test/alpha", "--format", format)
		if code != 0 || errOut != "" {
			t.Fatalf("%s: exit %d\n%s\n%s", format, code, out, errOut)
		}
		for _, text := range wants {
			if !strings.Contains(out, text) {
				t.Errorf("%s output lacks %q:\n%s", format, text, out)
			}
		}
	}
}

func TestContextSetStoresTheStableIdentityWhateverTheReference(t *testing.T) {
	w := newContextWorld(t)
	svc := newContextService(t, "test/alpha", "test/beta")
	t.Setenv("LIT_ENDPOINT", svc.URL)
	alpha, beta := svc.context("test/alpha"), svc.context("test/beta")
	dir := physicalDir(t)

	for _, tc := range []struct {
		reference string
		want      string
	}{
		{"test/alpha", alpha.ProjectID}, {"TEST/ALPHA", alpha.ProjectID}, {"title:test/alpha", alpha.ProjectID},
		{alpha.ProjectID, alpha.ProjectID}, {alpha.ProjectID[:10], alpha.ProjectID}, {"id:" + alpha.ProjectID[:10], alpha.ProjectID},
		{"test/beta", beta.ProjectID},
	} {
		it := contextItem(t, dir, "context", "set", "--project", tc.reference)
		pref, _ := it["preference"].(map[string]any)
		if pref["project_id"] != tc.want || pref["service_id"] != alpha.ServiceID {
			t.Errorf("%q: %v", tc.reference, pref)
		}
		if title := pref["project_title"]; title != map[string]string{alpha.ProjectID: "test/alpha", beta.ProjectID: "test/beta"}[tc.want] {
			t.Errorf("%q stored the title %v, not the project's own", tc.reference, title)
		}
	}
	record, _ := w.store().Load(mustCheckout(t, dir))
	if record == nil || record.ProjectID != beta.ProjectID {
		t.Fatalf("the last set must win: %+v", record)
	}
}

func TestContextSetReplacesAnEarlierPreferenceOnlyWhenAsked(t *testing.T) {
	w := newContextWorld(t)
	first := newContextService(t, "test/alpha")
	second := newContextService(t, "test/alpha")
	dir := physicalDir(t)
	t.Setenv("LIT_ENDPOINT", first.URL)

	contextItem(t, dir, "context", "set", "--project", "test/alpha")
	// An explicit set may point the checkout at another service: that is the
	// only way a binding changes, and the earlier one is reported.
	it := contextItem(t, dir, "context", "set", "--project", "test/alpha", "--endpoint", second.URL)
	previous, _ := it["previous"].(map[string]any)
	pref, _ := it["preference"].(map[string]any)
	if previous["service_id"] != first.context("test/alpha").ServiceID || pref["service_id"] != second.context("test/alpha").ServiceID || pref["endpoint"] != second.URL {
		t.Fatalf("rebinding set: %v", it)
	}
	if record, _ := w.store().Load(mustCheckout(t, dir)); record == nil || record.Endpoint != second.URL {
		t.Fatalf("stored %+v", record)
	}
}

func TestContextSetLeavesThePreferenceAloneWhenValidationFails(t *testing.T) {
	w := newContextWorld(t)
	w.seedNeighbors(t)
	titles := []string{"test/alpha"}
	for i := 0; i < 17; i++ { // by pigeonhole two of 17 IDs share their first hex digit
		titles = append(titles, "test/many-"+string(rune('a'+i)))
	}
	svc := newContextService(t, titles...)
	shared := sharedFirstDigit(t, svc, titles)
	held := newContextService(t, "test/alpha")
	incompatible := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"client_required","message":"connect first","details":{}}}`))
	}))
	defer incompatible.Close()
	garbled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"service_id":"nope","api_major":1}}`))
	}))
	defer garbled.Close()
	release := held.holdRequests()
	defer release()

	failures := []struct {
		name, code string
		exit       int
		args       []string
	}{
		{"missing project", "not_found", 1, []string{"--project", "test/missing", "--endpoint", svc.URL}},
		{"ambiguous prefix", "ambiguous_reference", 1, []string{"--project", "id:" + shared, "--endpoint", svc.URL}},
		{"nothing listening", "service_unavailable", 1, []string{"--project", "test/alpha", "--endpoint", nothing}},
		{"timeout", "service_unavailable", 1, []string{"--project", "test/alpha", "--endpoint", held.URL, "--timeout", "150ms"}},
		{"unusable endpoint", "invalid_endpoint", 2, []string{"--project", "test/alpha", "--endpoint", "not-a-url"}},
		{"empty endpoint does not fall back", "invalid_endpoint", 2, []string{"--project", "test/alpha", "--endpoint", ""}},
		{"service without the operation", "incompatible_service", 1, []string{"--project", "test/alpha", "--endpoint", incompatible.URL}},
		{"malformed answer", "incompatible_service", 1, []string{"--project", "test/alpha", "--endpoint", garbled.URL}},
	}
	dir := physicalDir(t)
	t.Setenv("LIT_ENDPOINT", svc.URL)
	neighbors := w.neighbors(t)
	attempt := func(label string) {
		for _, tc := range failures {
			code, e := contextFailure(t, dir, append([]string{"context", "set"}, tc.args...)...)
			if code != tc.exit || e["code"] != tc.code {
				t.Errorf("%s, %s: exit %d, %v", label, tc.name, code, e)
			}
		}
	}

	attempt("nothing remembered")
	if _, err := os.Stat(filepath.Join(w.workflow, "checkout-context")); !os.IsNotExist(err) {
		t.Errorf("failed sets created the preference directory: %v", err)
	}
	contextItem(t, dir, "context", "set", "--project", "test/alpha", "--endpoint", svc.URL)
	file := w.record(t, dir)
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	attempt("something remembered")
	if after, err := os.ReadFile(file); err != nil || string(after) != string(before) {
		t.Errorf("a failed set changed the remembered project: %v", err)
	}
	if w.neighbors(t) != neighbors {
		t.Error("failed sets changed other workflow state")
	}
}

func TestContextSetRefusesADamagedRecordBeforeAskingTheService(t *testing.T) {
	w := newContextWorld(t)
	svc := newContextService(t, "test/alpha")
	dir := physicalDir(t)
	t.Setenv("LIT_ENDPOINT", svc.URL)
	w.save(t, dir, 1)
	file := w.record(t, dir)
	if err := os.WriteFile(file, []byte(`{"schema_version":1,"checkout":`), 0o600); err != nil {
		t.Fatal(err)
	}

	code, e := contextFailure(t, dir, "context", "set", "--project", "test/alpha")
	if code != 1 || e["code"] != "invalid_local_state" || !strings.Contains(contextProblem(t, e)["recovery"].(string), "lit context clear") {
		t.Fatalf("exit %d, %v", code, e)
	}
	svc.only(t)
	if got, err := os.ReadFile(file); err != nil || string(got) != `{"schema_version":1,"checkout":` {
		t.Fatalf("the damaged record was replaced: %q %v", got, err)
	}
	contextItem(t, dir, "context", "clear")
	if it := contextItem(t, dir, "context", "set", "--project", "test/alpha"); it["outcome"] != "applied" {
		t.Fatalf("set after clear: %v", it)
	}
}

func TestContextSetUsesEndpointFlagThenEnvironmentThenTheDefault(t *testing.T) {
	newContextWorld(t) // LIT_ENDPOINT points at a service that fails the test on any request
	svc := newContextService(t, "test/alpha")
	dir := physicalDir(t)

	// The flag beats an environment that would fail the test if it were used.
	contextItem(t, dir, "context", "set", "--project", "test/alpha", "--endpoint", svc.URL)
	svc.only(t, requestFor(ref("test/alpha")))
	svc.forget()
	// Without a flag the environment is used; the saved endpoint is not consulted by set.
	t.Setenv("LIT_ENDPOINT", svc.URL)
	contextItem(t, dir, "context", "set", "--project", "test/alpha")
	svc.only(t, requestFor(ref("test/alpha")))
}

func TestContextSetWaitsAtMostTheTimeoutForTheService(t *testing.T) {
	newContextWorld(t)
	svc := newContextService(t, "test/alpha")
	release := svc.holdRequests()
	defer release()
	started := time.Now()
	code, e := contextFailure(t, physicalDir(t), "context", "set", "--project", "test/alpha", "--endpoint", svc.URL, "--timeout", "200ms")
	if code != 1 || e["code"] != "service_unavailable" || time.Since(started) > 10*time.Second {
		t.Fatalf("exit %d after %s, %v", code, time.Since(started), e)
	}
}
