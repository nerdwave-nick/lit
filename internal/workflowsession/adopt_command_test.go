package workflowsession

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
)

// fresh runs `workflow-session fresh` in dir as the command does, with the
// given environment endpoint, and returns its exit code, JSON output and stderr.
func (f *adoption) fresh(dir, environment, id string, args ...string) (int, map[string]any, string) {
	f.t.Helper()
	f.t.Chdir(dir)
	f.t.Setenv("LIT_ENDPOINT", environment)
	var out, errOut bytes.Buffer
	argv := append([]string{"--host", "claude", "--runtime-id", id, "--cli", f.lit, "fresh"}, args...)
	code := Run(argv, &out, &errOut)
	var printed map[string]any
	if code == 0 {
		if e := json.Unmarshal(out.Bytes(), &printed); e != nil {
			f.t.Fatalf("output is not JSON: %v\n%s", e, &out)
		}
	}
	return code, printed, errOut.String()
}

func selectionOf(t *testing.T, printed map[string]any) map[string]any {
	t.Helper()
	s, ok := printed["project_selection"].(map[string]any)
	if !ok {
		t.Fatalf("the association has no project_selection: %v", printed)
	}
	return s
}

// The command adopts the remembered project of the current directory's
// checkout and says so in the JSON it prints; --endpoint and LIT_ENDPOINT are
// the caller's inputs to the endpoint choice (the saved endpoint is covered by
// TestFreshChoosesTheEndpointOfAnAdoptedProject; a command test that fell back
// to the default endpoint would reach a developer's own lit-server).
func TestFreshCommandAdoptsAndAnnounces(t *testing.T) {
	f := newAdoption(t)
	alpha := f.project(f.endpoint, "feat/alpha")
	beta := f.project(f.endpoint, "feat/beta")
	via := proxy(t, f.endpoint)
	dir := f.gitCheckout()
	f.remember(dir, f.endpoint, "feat/alpha")
	sub := dir + "/pkg/deep"
	if e := os.MkdirAll(sub, 0o755); e != nil {
		t.Fatal(e)
	}

	code, one, stderr := f.fresh(sub, f.endpoint, "cmd-one") // a subdirectory shares the checkout
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	s := selectionOf(t, one)
	if s["source"] != "remembered" || s["project_id"] != alpha || s["project_title"] != "feat/alpha" || s["checkout"] != dir || one["project_id"] != alpha || one["endpoint"] != f.endpoint {
		t.Fatalf("adopted: %v", one)
	}
	if _, renamed := s["saved_title"]; renamed {
		t.Fatalf("an unrenamed project reports a saved title: %v", s)
	}
	code, two, stderr := f.fresh(dir, via, "cmd-two") // LIT_ENDPOINT beats the saved endpoint
	if code != 0 || two["endpoint"] != via || two["project_id"] != alpha {
		t.Fatalf("environment: exit %d %v %s", code, two, stderr)
	}
	if one["client_id"] == two["client_id"] || one["local_session"] == two["local_session"] {
		t.Fatal("two runtime IDs share a client")
	}
	code, three, stderr := f.fresh(dir, via, "cmd-three", "--endpoint", f.endpoint) // the flag beats LIT_ENDPOINT
	if code != 0 || three["endpoint"] != f.endpoint {
		t.Fatalf("flag: exit %d %v %s", code, three, stderr)
	}
	code, explicit, stderr := f.fresh(dir, f.endpoint, "cmd-explicit", "--project", "feat/beta") // an explicit project wins
	if code != 0 || explicit["project_id"] != beta || selectionOf(t, explicit)["source"] != "explicit" {
		t.Fatalf("explicit: exit %d %v %s", code, explicit, stderr)
	}
	if _, adopted := selectionOf(t, explicit)["project_id"]; adopted {
		t.Fatal("an explicit selection describes the preference")
	}
	other := f.checkout()
	code, none, stderr := f.fresh(other, f.endpoint, "cmd-none")
	if code != 0 || none["project_id"] != nil || selectionOf(t, none)["source"] != "none" || selectionOf(t, none)["checkout"] != other {
		t.Fatalf("no preference: exit %d %v %s", code, none, stderr)
	}
}

// A preference that cannot be adopted makes the command fail, with the fix in
// its message, before it creates a runtime association or a client.
func TestFreshCommandStopsOnAStalePreference(t *testing.T) {
	f := newAdoption(t)
	f.project(f.endpoint, "feat/alpha")
	dir := f.checkout()
	f.remember(dir, f.endpoint, "feat/alpha")
	other := testService(t, nil) // a replacement service at the environment's address
	connects0, workflowBefore := f.seen.count(connects), tree(t, f.workflow)

	code, printed, stderr := f.fresh(dir, other, "cmd-stale")
	if code == 0 || printed != nil {
		t.Fatalf("a stale preference was adopted: %v", printed)
	}
	for _, word := range []string{"remembered for this checkout", "no session was created", "lit context clear", "--project"} {
		if !strings.Contains(stderr, word) {
			t.Errorf("stderr lacks %q:\n%s", word, stderr)
		}
	}
	a := f.adapter("cmd-stale")
	if _, e := a.Read(); !errors.Is(e, fs.ErrNotExist) {
		t.Fatalf("an association was saved: %v", e)
	}
	if f.seen.count(connects) != connects0 || tree(t, f.workflow) != workflowBefore {
		t.Fatal("the failed command registered a client or changed the workflow state")
	}
	// The way out the message names works, and leaves the preference alone.
	before := readFile(t, f.preferenceFile(dir))
	if code, _, stderr = f.fresh(dir, other, "cmd-stale", "--project", "feat/alpha", "--endpoint", f.endpoint); code != 0 {
		t.Fatalf("explicit project: exit %d %s", code, stderr)
	}
	if readFile(t, f.preferenceFile(dir)) != before {
		t.Fatal("the override changed the preference")
	}
}
