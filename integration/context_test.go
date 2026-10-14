package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/checkoutcontext"
)

// TestContextShowAndClearWorkOfflineInRealCheckouts runs the built lit against
// real Git worktrees with no service listening anywhere, an ambient session and
// an unreachable endpoint, and a private client state directory that must stay
// empty.
func TestContextShowAndClearWorkOfflineInRealCheckouts(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workflow, private := filepath.Join(base, "workflow"), filepath.Join(base, "private")
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
	for _, dir := range []string{filepath.Join(main, "a", "b"), filepath.Join(linked, "a")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// No set command exists in this slice: save fixture preferences through the module.
	store := checkoutcontext.OpenAt(workflow)
	for dir, pref := range map[string]checkoutcontext.Preference{
		main:   {ServiceID: "5e5e5e5e-1111-4111-8111-abcdefabcdef", ProjectID: "22220001-0000-4000-8000-000000000001", ProjectTitle: "feat/main-project", Endpoint: "http://127.0.0.1:7411"},
		linked: {ServiceID: "5e5e5e5e-1111-4111-8111-abcdefabcdef", ProjectID: "22220002-0000-4000-8000-000000000002", ProjectTitle: "feat/linked-project", Endpoint: "http://127.0.0.1:7411/"},
	} {
		c, err := checkoutcontext.Resolve(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Save(c, pref); err != nil {
			t.Fatal(err)
		}
	}

	lit := func(cwd string, want int, args ...string) map[string]any {
		t.Helper()
		cmd := exec.Command(cliBin, args...)
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0", "HOME="+base, "LIT_WORKFLOW_STATE_DIR="+workflow, "LIT_STATE_DIR="+private,
			"LIT_SESSION=ambient", "LIT_ENDPOINT=http://127.0.0.1:1")
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		code := 0
		if err := cmd.Run(); err != nil {
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		if code != want {
			t.Fatalf("lit %v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, &out, &errOut)
		}
		raw := out.Bytes()
		if want != 0 {
			raw = errOut.Bytes()
		}
		var v map[string]any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("lit %v: %v\n%s", args, err, raw)
		}
		return v
	}
	first := func(v map[string]any) map[string]any { return v["items"].([]any)[0].(map[string]any) }
	title := func(cwd string) any {
		pref, _ := first(lit(cwd, 0, "context", "show", "--format", "json"))["preference"].(map[string]any)
		return pref["project_title"]
	}

	if got := title(filepath.Join(main, "a", "b")); got != "feat/main-project" {
		t.Fatalf("main worktree subdirectory remembers %v", got)
	}
	if got := title(filepath.Join(linked, "a")); got != "feat/linked-project" {
		t.Fatalf("linked worktree remembers %v", got)
	}
	if shown := first(lit(main, 0, "context", "show", "--format", "json")); shown["validated"] != false || shown["remembered"] != true {
		t.Fatalf("show: %v", shown)
	}

	cleared := lit(filepath.Join(main, "a"), 0, "context", "clear", "--format", "json")
	if cleared["outcome"] != "applied" || first(cleared)["removed"] != "preference" {
		t.Fatalf("clear: %v", cleared)
	}
	if again := lit(main, 0, "context", "clear", "--format", "json"); again["outcome"] != "already_satisfied" {
		t.Fatalf("second clear: %v", again)
	}
	if shown := first(lit(main, 0, "context", "show", "--format", "json")); shown["remembered"] != false || shown["preference"] != nil {
		t.Fatalf("show after clear: %v", shown)
	}
	if got := title(linked); got != "feat/linked-project" {
		t.Fatalf("clearing the main worktree changed the linked one: %v", got)
	}

	// Argument errors keep the JSON error contract and exit 2.
	bad := lit(main, 2, "context", "show", "--session", "ambient", "--format", "json")
	if msg := bad["error"].(map[string]any)["message"]; !strings.Contains(msg.(string), "--session does not apply") {
		t.Fatalf("session flag: %v", bad)
	}
	if _, err := os.Stat(private); !os.IsNotExist(err) {
		t.Fatalf("offline context commands created the client state directory: %v", err)
	}
}
