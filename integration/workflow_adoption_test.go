package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// adoptionCLI runs the built lit's workflow-session against real services,
// with isolated workflow and client state. LIT_ENDPOINT is always set: the
// caller picks the service explicitly, never the developer's own default.
type adoptionCLI struct {
	t                       *testing.T
	base, workflow, private string
}

// workflow runs `lit workflow-session --host HOST --runtime-id ID ARGS...` in
// cwd with LIT_ENDPOINT=endpoint and returns the exit code, the JSON printed on
// success, and stderr.
func (a *adoptionCLI) workflowSession(cwd, endpoint, host, runtime string, args ...string) (int, map[string]any, string) {
	a.t.Helper()
	argv := append([]string{"workflow-session", "--host", host, "--runtime-id", runtime}, args...)
	cmd := exec.Command(cliBin, argv...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0", "HOME="+a.base, "LIT_WORKFLOW_STATE_DIR="+a.workflow,
		"LIT_STATE_DIR="+a.private, "LIT_SESSION=no-such-session", "LIT_ENDPOINT="+endpoint)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	code := 0
	if err := cmd.Run(); err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			a.t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	var printed map[string]any
	if code == 0 {
		if err := json.Unmarshal(out.Bytes(), &printed); err != nil {
			a.t.Fatalf("lit %v: %v\n%s", argv, err, &out)
		}
	}
	return code, printed, errOut.String()
}

// fresh creates a session and requires success.
func (a *adoptionCLI) fresh(cwd, endpoint, host, runtime string, args ...string) map[string]any {
	a.t.Helper()
	code, printed, stderr := a.workflowSession(cwd, endpoint, host, runtime, append([]string{"fresh"}, args...)...)
	if code != 0 {
		a.t.Fatalf("fresh %s: exit %d: %s", runtime, code, stderr)
	}
	return printed
}

// selected is the project a runtime's tracker session has selected right now.
func (a *adoptionCLI) selected(cwd, endpoint, host, runtime string) any {
	a.t.Helper()
	code, printed, stderr := a.workflowSession(cwd, endpoint, host, runtime, "run", "--", "session", "get", "--project")
	if code != 0 {
		a.t.Fatalf("session get: exit %d: %s", code, stderr)
	}
	return item(printed)["project_id"]
}

func (a *adoptionCLI) vendorRecords() int {
	records, _ := filepath.Glob(filepath.Join(a.workflow, "vendor-sessions", "*.json"))
	return len(records)
}

// files lists every file below dir with its content.
func files(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			content, _ := os.ReadFile(path)
			rel, _ := filepath.Rel(dir, path)
			lines = append(lines, rel+"="+string(content))
		}
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// The demonstration of remembered-project adoption with the built binaries: two
// real Git worktrees remember different projects of one service; fresh runtimes
// adopt each worktree's project with clients of their own; a temporary
// override, a later change of the preference, a subagent and a resume leave the
// preference and the active sessions alone; clearing gives unselected sessions;
// and another service, or none, at the remembered address stops fresh before a
// client or association exists.
func TestFreshAdoptsRememberedProjectsPerWorktreeAgainstBinaries(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &adoptionCLI{t: t, base: base, workflow: filepath.Join(base, "workflow"), private: filepath.Join(base, "private")}
	c := &contextCLI{t: t, workflow: a.workflow, private: a.private, home: base, ambientEP: "http://127.0.0.1:1"}
	main, linked := filepath.Join(base, "main"), filepath.Join(base, "linked")
	gitIn := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + base, "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_SYSTEM=" + os.DevNull,
			"GIT_AUTHOR_NAME=lit", "GIT_AUTHOR_EMAIL=lit@example.org", "GIT_COMMITTER_NAME=lit", "GIT_COMMITTER_EMAIL=lit@example.org"}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(main, "init", "-q", "-b", "main")
	gitIn(main, "commit", "-q", "--allow-empty", "-m", "initial")
	gitIn(main, "worktree", "add", "-q", "-b", "other", linked)
	mainSub, linkedSub := filepath.Join(main, "pkg", "deep"), filepath.Join(linked, "pkg")
	for _, dir := range []string{mainSub, linkedSub} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	dataA, dataB := filepath.Join(base, "data-a"), filepath.Join(base, "data-b")
	svcA, svcB := start(t, dataA), start(t, dataB)
	admin := func(args ...string) map[string]any {
		return c.run(base, 0, append(args, "--session", "admin", "--endpoint", svcA.endpoint, "--format", "json")...)
	}
	admin("connect")
	alpha := item(admin("projects", "create", "--project-title", "feat/alpha", "--content", "x"))["id"].(string)
	beta := item(admin("projects", "create", "--project-title", "feat/beta", "--content", "x"))["id"].(string)
	c.run(base, 0, "connect", "--session", "other-admin", "--endpoint", svcB.endpoint, "--format", "json")
	c.run(base, 0, "projects", "create", "--session", "other-admin", "--endpoint", svcB.endpoint, "--project-title", "feat/alpha", "--content", "x", "--format", "json")

	remember := func(dir, ref string) {
		t.Helper()
		c.run(dir, 0, "context", "set", "--project", ref, "--endpoint", svcA.endpoint, "--format", "json")
	}
	remember(main, "feat/alpha")
	remember(linked, "feat/beta")
	preferences := func() string { return files(t, filepath.Join(a.workflow, "checkout-context")) }
	clients := func() int { return tableCounts(t, dataA)["clients"] }
	selection := func(r map[string]any) map[string]any { return r["project_selection"].(map[string]any) }

	// Two runtimes in one worktree: each adopts alpha with a client of its own. A
	// subdirectory shares the worktree's preference; the other worktree has its own.
	before := clients()
	r1 := a.fresh(mainSub, svcA.endpoint, "claude", "r1")
	r2 := a.fresh(main, svcA.endpoint, "codex", "r2")
	r3 := a.fresh(linkedSub, svcA.endpoint, "claude", "r3")
	if got := clients(); got != before+3 {
		t.Fatalf("clients: %d, want %d", got, before+3)
	}
	for _, tc := range []struct {
		r        map[string]any
		project  string
		title    string
		checkout string
	}{{r1, alpha, "feat/alpha", main}, {r2, alpha, "feat/alpha", main}, {r3, beta, "feat/beta", linked}} {
		s := selection(tc.r)
		if tc.r["project_id"] != tc.project || tc.r["status"] != "ready" || tc.r["endpoint"] != svcA.endpoint ||
			s["source"] != "remembered" || s["project_id"] != tc.project || s["project_title"] != tc.title || s["checkout"] != tc.checkout || s["saved_at"] == nil {
			t.Fatalf("adopted: %v", tc.r)
		}
	}
	if r1["client_id"] == r2["client_id"] || r1["client_id"] == r3["client_id"] || r1["local_session"] == r2["local_session"] {
		t.Fatal("runtimes share a client")
	}
	if got := a.selected(main, svcA.endpoint, "claude", "r1"); got != alpha {
		t.Fatalf("the tracker session of r1 selected %v", got)
	}
	saved := preferences()

	// A temporary override leaves the preference as it was; the next runtime adopts it again.
	r4 := a.fresh(main, svcA.endpoint, "claude", "r4", "--project", "feat/beta")
	if r4["project_id"] != beta || selection(r4)["source"] != "explicit" || selection(r4)["project_id"] != nil {
		t.Fatalf("explicit: %v", r4)
	}
	if preferences() != saved {
		t.Fatal("an explicit project changed the preferences")
	}
	if r5 := a.fresh(main, svcA.endpoint, "claude", "r5"); r5["project_id"] != alpha || selection(r5)["source"] != "remembered" {
		t.Fatalf("after the override: %v", r5)
	}

	// Remembering another project redirects new runtimes, never active ones, and resume works.
	remember(main, "feat/beta")
	if r6 := a.fresh(main, svcA.endpoint, "claude", "r6"); r6["project_id"] != beta {
		t.Fatalf("a new runtime after set: %v", r6)
	}
	code, resumed, stderr := a.workflowSession(base, svcB.endpoint, "claude", "r1", "resume") // another cwd and ambient endpoint change nothing
	if code != 0 || resumed["client_id"] != r1["client_id"] || resumed["project_id"] != alpha || selection(resumed)["project_id"] != alpha || resumed["endpoint"] != svcA.endpoint {
		t.Fatalf("resume: exit %d %v %s", code, resumed, stderr)
	}
	if got := a.selected(base, svcB.endpoint, "claude", "r1"); got != alpha {
		t.Fatalf("the active session of r1 was redirected to %v", got)
	}
	remember(main, "feat/alpha")
	saved = preferences()

	// A subagent never adopts: its scope is the coordinator's explicit project.
	code, child, stderr := a.workflowSession(main, svcA.endpoint, "claude", "r1-child", "subagent", "--parent-runtime-id", "r1")
	if code != 0 || child["project_id"] != nil || selection(child)["source"] != "none" || child["parent_runtime_id"] != "r1" {
		t.Fatalf("subagent: exit %d %v %s", code, child, stderr)
	}
	code, scoped, stderr := a.workflowSession(main, svcA.endpoint, "claude", "r1-scoped", "subagent", "--parent-runtime-id", "r1", "--project", "feat/beta")
	if code != 0 || scoped["project_id"] != beta || selection(scoped)["source"] != "explicit" {
		t.Fatalf("scoped subagent: exit %d %v %s", code, scoped, stderr)
	}
	if preferences() != saved {
		t.Fatalf("a runtime changed the preferences:\n%s\n%s", saved, preferences())
	}

	// Clearing gives unselected sessions; the other worktree and existing sessions are untouched.
	c.run(main, 0, "context", "clear", "--format", "json")
	r7 := a.fresh(main, svcA.endpoint, "claude", "r7")
	if r7["project_id"] != nil || r7["status"] != "ready" || selection(r7)["source"] != "none" || selection(r7)["checkout"] != main {
		t.Fatalf("after clear: %v", r7)
	}
	if r8 := a.fresh(linked, svcA.endpoint, "claude", "r8"); r8["project_id"] != beta {
		t.Fatalf("the other worktree after clearing main: %v", r8)
	}
	if got := a.selected(main, svcA.endpoint, "claude", "r1"); got != alpha {
		t.Fatalf("clearing redirected r1 to %v", got)
	}

	// Another service at the remembered address, or none, stops fresh before any client or association.
	remember(main, "feat/alpha")
	saved = preferences()
	records, countsA, countsB, privateBefore := a.vendorRecords(), tableCounts(t, dataA), tableCounts(t, dataB), files(t, a.private)
	for _, tc := range []struct{ name, endpoint, want string }{
		{"wrong service", svcB.endpoint, "answered as service"},
		{"nothing listening", "http://127.0.0.1:1", "cannot reach"},
	} {
		code, printed, stderr := a.workflowSession(main, tc.endpoint, "claude", "refused-"+strings.ReplaceAll(tc.name, " ", "-"), "fresh")
		if code != 1 || printed != nil {
			t.Fatalf("%s: exit %d %v", tc.name, code, printed)
		}
		for _, word := range []string{tc.want, "no session was created", "lit context clear", "--project REF"} {
			if !strings.Contains(stderr, word) {
				t.Errorf("%s: stderr lacks %q:\n%s", tc.name, word, stderr)
			}
		}
	}
	if got := a.vendorRecords(); got != records {
		t.Fatalf("a refused fresh left an association: %d records, was %d", got, records)
	}
	if got := tableCounts(t, dataA); !sameCounts(got, countsA) {
		t.Fatalf("a refused fresh changed service A: %v, was %v", got, countsA)
	}
	if got := tableCounts(t, dataB); !sameCounts(got, countsB) {
		t.Fatalf("a refused fresh changed service B: %v, was %v", got, countsB)
	}
	if got := files(t, a.private); got != privateBefore {
		t.Fatal("a refused fresh changed the client state")
	}
	if preferences() != saved {
		t.Fatal("a refused fresh changed the preferences")
	}
	// The explicit way out works, and the preference still stands.
	if r9 := a.fresh(main, svcA.endpoint, "claude", "r9", "--project", "feat/alpha"); r9["project_id"] != alpha {
		t.Fatalf("explicit project after a refusal: %v", r9)
	}
	if preferences() != saved {
		t.Fatal("the override changed the preferences")
	}
}
