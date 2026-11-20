package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nerdwave-nick/lit/internal/checkoutcontext"
)

// contextWorld is an isolated machine: an empty workflow state directory, a
// private client state directory with recovery evidence, an ambient logical
// session and endpoint, and a service that fails the test on any request.
type contextWorld struct {
	workflow, private string
	requests          atomic.Int32
}

func newContextWorld(t *testing.T) *contextWorld {
	t.Helper()
	w := &contextWorld{workflow: filepath.Join(physicalDir(t), "workflow"), private: filepath.Join(physicalDir(t), "private")}
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.requests.Add(1)
		t.Errorf("context commands must work offline, but %s %s reached the service", r.Method, r.URL)
		http.Error(rw, "offline", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	home := physicalDir(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("LIT_WORKFLOW_STATE_DIR", w.workflow)
	t.Setenv("LIT_STATE_DIR", w.private)
	t.Setenv("LIT_SESSION", "ambient-session")
	t.Setenv("LIT_ENDPOINT", srv.URL)
	return w
}

// seedNeighbors writes records of the other workflow state around preferences:
// adapter associations, checkout-location maps, pending requests and sessions.
func (w *contextWorld) seedNeighbors(t *testing.T) {
	t.Helper()
	for dir, files := range map[string][]string{
		w.workflow: {"vendor-sessions/claude-1.json", "checkouts/location.json", "consultations/c.json"},
		w.private:  {"pending/0a0a0a0a-0000-4000-8000-000000000001.json", "sessions/ambient.json", "locks/pending/0a0a0a0a.lock"},
	} {
		for _, f := range files {
			path := filepath.Join(dir, f)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(`{"schema_version":1,"file":"`+f+`"}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// neighbors fingerprints everything except the preferences themselves.
func (w *contextWorld) neighbors(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, dir := range []string{w.workflow, w.private} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			rel, _ := filepath.Rel(dir, path)
			switch {
			case err != nil:
				return err
			case rel == "checkout-context":
				return filepath.SkipDir
			case d.IsDir():
				fmt.Fprintf(&b, "%s/%s/\n", filepath.Base(dir), rel)
			default:
				content, err := os.ReadFile(path)
				sum := sha256.Sum256(content)
				fmt.Fprintf(&b, "%s/%s %s\n", filepath.Base(dir), rel, hex.EncodeToString(sum[:]))
				return err
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	return b.String()
}

func (w *contextWorld) store() *checkoutcontext.Store { return checkoutcontext.OpenAt(w.workflow) }

func (w *contextWorld) record(t *testing.T, dir string) string {
	t.Helper()
	c, err := checkoutcontext.Resolve(dir)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(c.Path))
	return filepath.Join(w.workflow, "checkout-context", hex.EncodeToString(sum[:])+".json")
}

func physicalDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func fixtureGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_SYSTEM=" + os.DevNull,
		"GIT_AUTHOR_NAME=lit", "GIT_AUTHOR_EMAIL=lit@example.org", "GIT_COMMITTER_NAME=lit", "GIT_COMMITTER_EMAIL=lit@example.org"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// fixtureRepository creates a repository and a linked worktree, each with a
// nested directory.
func fixtureRepository(t *testing.T) (root, linked string) {
	t.Helper()
	root, linked = physicalDir(t), filepath.Join(physicalDir(t), "linked")
	fixtureGit(t, root, "init", "-q", "-b", "main")
	fixtureGit(t, root, "commit", "-q", "--allow-empty", "-m", "initial")
	fixtureGit(t, root, "worktree", "add", "-q", "-b", "other", linked)
	for _, dir := range []string{filepath.Join(root, "pkg", "deep"), filepath.Join(linked, "pkg", "deep")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root, linked
}

func samplePreference(n int) checkoutcontext.Preference {
	return checkoutcontext.Preference{
		ServiceID:    "5e5e5e5e-1111-4111-8111-abcdefabcdef",
		ProjectID:    fmt.Sprintf("%08x-0000-4000-8000-%012x", 0x22220000+n, n),
		ProjectTitle: fmt.Sprintf("feat/project-%d", n),
		Endpoint:     "http://127.0.0.1:7411",
	}
}

func (w *contextWorld) save(t *testing.T, dir string, n int) checkoutcontext.Record {
	t.Helper()
	c, err := checkoutcontext.Resolve(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := w.store().Save(c, samplePreference(n))
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// runLit runs the command from dir and returns its exit code and output.
func runLit(t *testing.T, dir string, args ...string) (code int, out, errOut string) {
	t.Helper()
	t.Chdir(dir)
	var o, e bytes.Buffer
	code = Run(args, &o, &e)
	return code, o.String(), e.String()
}

// contextItem runs the command with --format json and returns the single result item.
func contextItem(t *testing.T, dir string, args ...string) map[string]any {
	t.Helper()
	code, out, errOut := runLit(t, dir, append(args, "--format", "json")...)
	if code != 0 || errOut != "" {
		t.Fatalf("lit %v: exit %d\nstdout: %s\nstderr: %s", args, code, out, errOut)
	}
	var envelope struct {
		Items   []map[string]any `json:"items"`
		Outcome string           `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil || len(envelope.Items) != 1 {
		t.Fatalf("lit %v: %v\n%s", args, err, out)
	}
	envelope.Items[0]["outcome"] = envelope.Outcome
	return envelope.Items[0]
}

// contextFailure runs a command expected to fail and returns its exit code and error.
func contextFailure(t *testing.T, dir string, args ...string) (int, map[string]any) {
	t.Helper()
	if !slices.Contains(args, "--format") {
		args = append(args, "--format", "json")
	}
	code, out, errOut := runLit(t, dir, args...)
	var envelope struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal([]byte(errOut), &envelope); err != nil || envelope.Error == nil || out != "" {
		t.Fatalf("lit %v: exit %d\nstdout: %s\nstderr: %s", args, code, out, errOut)
	}
	return code, envelope.Error
}
