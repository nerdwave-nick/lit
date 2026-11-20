package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// contextCLI runs the built lit with isolated client and workflow state, an
// ambient session that does not exist, and the given endpoint environment.
type contextCLI struct {
	t                                  *testing.T
	workflow, private, home, ambientEP string
}

func (c *contextCLI) run(cwd string, want int, args ...string) map[string]any {
	c.t.Helper()
	cmd := exec.Command(cliBin, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0", "HOME="+c.home, "LIT_WORKFLOW_STATE_DIR="+c.workflow,
		"LIT_STATE_DIR="+c.private, "LIT_SESSION=no-such-session", "LIT_ENDPOINT="+c.ambientEP)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	code := 0
	if err := cmd.Run(); err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			c.t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	if code != want {
		c.t.Fatalf("lit %v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, &out, &errOut)
	}
	raw := out.Bytes()
	if want != 0 {
		raw = errOut.Bytes()
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		c.t.Fatalf("lit %v: %v\n%s", args, err, raw)
	}
	return v
}

// failure is an error's code and details.
func failure(v map[string]any) (code string, details map[string]any) {
	e := v["error"].(map[string]any)
	details, _ = e["details"].(map[string]any)
	return e["code"].(string), details
}

// The whole remembered-project flow against real binaries: set resolves and
// saves a validated project without a session or client; show stays offline;
// show --validate follows a rename, refuses a replacement service at the same
// address, reports a stopped one, and none of it touches the preference, the
// client state or the services. Only clear removes it, even while the service
// is down, and ordinary project and session commands never rewrite it.
func TestContextSetAndValidateAgainstBinaries(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := &contextCLI{t: t, workflow: filepath.Join(base, "workflow"), private: filepath.Join(base, "private"), home: base, ambientEP: "http://127.0.0.1:1"}
	repo := filepath.Join(base, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd := exec.Command("git", "init", "-q", "-b", "main")
	gitCmd.Dir = repo
	gitCmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + base, "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_SYSTEM=" + os.DevNull}
	if out, err := gitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	dataA, dataB := filepath.Join(base, "data-a"), filepath.Join(base, "data-b")
	a := start(t, dataA)
	b := start(t, dataB)
	admin := func(want int, args ...string) map[string]any {
		return c.run(base, want, append(args, "--session", "admin", "--endpoint", a.endpoint, "--format", "json")...)
	}
	admin(0, "connect")
	admin(0, "projects", "create", "--project-title", "feat/alpha", "--content", "PRIVATE_PROJECT_CONTENT")
	// A project of the same title on the other service must never stand in for it.
	c.run(base, 0, "connect", "--session", "other-admin", "--endpoint", b.endpoint, "--format", "json")
	c.run(base, 0, "projects", "create", "--session", "other-admin", "--endpoint", b.endpoint, "--project-title", "feat/alpha", "--content", "x", "--format", "json")
	clientState := func() string {
		var b strings.Builder
		_ = filepath.WalkDir(c.private, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				content, _ := os.ReadFile(path)
				rel, _ := filepath.Rel(c.private, path)
				b.WriteString(rel + "=" + string(content) + "\n")
			}
			return nil
		})
		return b.String()
	}
	// The client state is the admin session's; context commands must not add to it.
	countsA, countsB, stateBefore := tableCounts(t, dataA), tableCounts(t, dataB), clientState()
	record := func() []byte {
		files, _ := filepath.Glob(filepath.Join(c.workflow, "checkout-context", "*.json"))
		if len(files) != 1 {
			t.Fatalf("expected one preference file, found %v", files)
		}
		raw, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	// Nothing remembered: success, no network request (the ambient endpoint is dead).
	empty := item(c.run(repo, 0, "context", "show", "--validate", "--format", "json"))
	if empty["remembered"] != false || empty["validated"] != false || empty["preference"] != nil {
		t.Fatalf("empty: %v", empty)
	}

	// set: resolves and saves, with no session, no client and nothing written to the services.
	set := c.run(filepath.Join(repo, "pkg"), 0, "context", "set", "--project", "FEAT/ALPHA", "--endpoint", a.endpoint, "--format", "json")
	pref := item(set)["preference"].(map[string]any)
	if set["outcome"] != "applied" || pref["project_title"] != "feat/alpha" || pref["endpoint"] != a.endpoint || item(set)["validated"] != true {
		t.Fatalf("set: %v", set)
	}
	saved := record()
	if strings.Contains(string(saved), "client") || strings.Contains(string(saved), "PRIVATE_PROJECT_CONTENT") {
		t.Errorf("the record holds more than project context: %s", saved)
	}
	assertQuiet := func(step string) {
		t.Helper()
		if got := tableCounts(t, dataA); !sameCounts(got, countsA) {
			t.Fatalf("%s changed service A: %v, was %v", step, got, countsA)
		}
		if got := tableCounts(t, dataB); !sameCounts(got, countsB) {
			t.Fatalf("%s changed service B: %v, was %v", step, got, countsB)
		}
		if clientState() != stateBefore {
			t.Fatalf("%s changed the client state", step)
		}
		if string(record()) != string(saved) {
			t.Fatalf("%s rewrote the preference", step)
		}
	}
	assertQuiet("set")

	// show is offline (the ambient endpoint is dead); show --validate uses the saved endpoint.
	if shown := item(c.run(repo, 0, "context", "show", "--format", "json")); shown["remembered"] != true || shown["validated"] != false {
		t.Fatalf("show: %v", shown)
	}
	v := item(c.run(repo, 0, "context", "show", "--validate", "--endpoint", a.endpoint, "--format", "json"))
	if v["validated"] != true || v["renamed"] != false || v["current"].(map[string]any)["project_id"] != pref["project_id"] {
		t.Fatalf("validate: %v", v)
	}
	c.ambientEP = a.endpoint
	if v = item(c.run(repo, 0, "context", "show", "--validate", "--format", "json")); v["validated"] != true {
		t.Fatalf("validate through LIT_ENDPOINT: %v", v)
	}
	c.ambientEP = "http://127.0.0.1:1"
	if e, _ := failure(c.run(repo, 1, "context", "show", "--validate", "--endpoint", "http://127.0.0.1:1", "--timeout", "2s", "--format", "json")); e != "service_unavailable" {
		t.Fatalf("an unreachable endpoint: %v", e)
	}
	assertQuiet("validate")

	// Ordinary project and session commands never rewrite the preference.
	admin(0, "projects", "get", "feat/alpha")
	admin(0, "session", "set", "--project", "feat/alpha")
	c.run(base, 0, "connect", "--session", "second", "--endpoint", a.endpoint, "--project", "feat/alpha", "--format", "json")
	c.run(base, 0, "workflow-session", "--host", "claude", "--runtime-id", "context-runtime", "fresh", "--endpoint", a.endpoint, "--project", "feat/alpha")
	stateBefore = clientState() // these commands legitimately wrote their own session files
	countsA = tableCounts(t, dataA)
	assertQuiet("project and session commands")

	// A rename is followed; the saved title is not rewritten.
	admin(0, "projects", "update", "feat/alpha", "--title", "feat/renamed")
	countsA, stateBefore = tableCounts(t, dataA), clientState() // the administrator's own mutation
	v = item(c.run(repo, 0, "context", "show", "--validate", "--endpoint", a.endpoint, "--format", "json"))
	if v["renamed"] != true || v["current"].(map[string]any)["project_title"] != "feat/renamed" || v["preference"].(map[string]any)["project_title"] != "feat/alpha" {
		t.Fatalf("after rename: %v", v)
	}
	assertQuiet("validate after a rename")

	// The other service has a project of the same title but is not the remembered service.
	code, details := failure(c.run(repo, 1, "context", "show", "--validate", "--endpoint", b.endpoint, "--format", "json"))
	if code != "wrong_service" || details["answered_by"] == nil || details["remembered"].(map[string]any)["service_id"] != pref["service_id"] {
		t.Fatalf("wrong service: %v %v", code, details)
	}
	assertQuiet("a wrong-service answer")

	// Stopping the service: show and clear still work, validation says so, and nothing changes.
	a.stop(t)
	if shown := item(c.run(repo, 0, "context", "show", "--format", "json")); shown["remembered"] != true {
		t.Fatalf("offline show: %v", shown)
	}
	if code, _ = failure(c.run(repo, 1, "context", "show", "--validate", "--endpoint", a.endpoint, "--timeout", "3s", "--format", "json")); code != "service_unavailable" {
		t.Fatalf("stopped service: %v", code)
	}
	if string(record()) != string(saved) || clientState() != stateBefore {
		t.Fatal("a stopped service changed local state")
	}
	cleared := c.run(repo, 0, "context", "clear", "--format", "json")
	if cleared["outcome"] != "applied" || item(cleared)["removed"] != "preference" {
		t.Fatalf("clear: %v", cleared)
	}
	if clientState() != stateBefore {
		t.Error("clear changed the client state")
	}
	if again := item(c.run(repo, 0, "context", "show", "--validate", "--format", "json")); again["remembered"] != false {
		t.Fatalf("after clear: %v", again)
	}
}
