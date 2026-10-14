package hook

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/checkoutcontext"
)

// admin runs the lit CLI against the rig's service as a session of its own and
// returns the items of its JSON answer. The rig's private client state keeps
// it away from every other service.
func (g *rig) admin(args ...string) []map[string]json.RawMessage {
	g.t.Helper()
	run := func(args ...string) []byte {
		cmd := exec.Command(g.binary, append([]string{"--session", "admin", "--endpoint", g.endpoint, "--format", "json"}, args...)...)
		cmd.Dir = g.t.TempDir()
		out, e := cmd.CombinedOutput()
		if e != nil {
			g.t.Fatalf("lit %v: %v\n%s", args, e, out)
		}
		return out
	}
	if !g.adminConnected {
		g.adminConnected = true
		run("connect")
		g.connects.Add(-1) // the admin's own client is not one the hook registered
	}
	var answer struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if e := json.Unmarshal(run(args...), &answer); e != nil {
		g.t.Fatalf("lit %v: %v", args, e)
	}
	return answer.Items
}

// project creates a project on the service and returns its ID.
func (g *rig) project(title string) string {
	g.t.Helper()
	return jsonString(g.admin("projects", "create", "--project-title", title, "--content", "x")[0]["id"])
}

// serviceID is the identity of the rig's service.
func (g *rig) serviceID() string {
	g.t.Helper()
	id, e := probeService(context.Background(), g.endpoint, 5*time.Second)
	if e != nil {
		g.t.Fatal(e)
	}
	return id
}

// checkoutDir is a physical directory that is a Git worktree root (the files
// that make a directory one), so that its subdirectories share its preference.
func checkoutDir(t *testing.T) string {
	t.Helper()
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	for _, d := range []string{".git/objects", ".git/refs", "src/deep"} {
		if e = os.MkdirAll(filepath.Join(dir, d), 0o755); e != nil {
			t.Fatal(e)
		}
	}
	if e = os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	return dir
}

// remember saves the project ref of the rig's service as dir's preference, the
// way `lit context set --project ref` does.
func (g *rig) remember(dir, ref string) {
	g.t.Helper()
	pref, e := checkoutcontext.ResolveProject(context.Background(), ref, checkoutcontext.Endpoint{Explicit: g.endpoint, Supplied: true}, nil)
	if e != nil {
		g.t.Fatal(e)
	}
	g.save(dir, pref)
}

// save stores pref, valid or not, as dir's preference.
func (g *rig) save(dir string, pref checkoutcontext.Preference) {
	g.t.Helper()
	c, e := checkoutcontext.Resolve(dir)
	if e != nil {
		g.t.Fatal(e)
	}
	if _, e = checkoutcontext.OpenAt(g.workflow).Save(c, pref); e != nil {
		g.t.Fatal(e)
	}
}

// preferenceFile is where dir's preference is stored.
func (g *rig) preferenceFile(dir string) string {
	g.t.Helper()
	c, e := checkoutcontext.Resolve(dir)
	if e != nil {
		g.t.Fatal(e)
	}
	sum := sha256.Sum256([]byte(c.Path))
	return filepath.Join(g.workflow, "checkout-context", hex.EncodeToString(sum[:])+".json")
}

// sessionFiles lists the association records and lock directories that exist.
func (g *rig) sessionFiles() []string {
	g.t.Helper()
	entries, e := os.ReadDir(filepath.Join(g.workflow, "vendor-sessions"))
	if e != nil && !os.IsNotExist(e) {
		g.t.Fatal(e)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// warnings returns the hook log's warn entries.
func warnings(t *testing.T) []entry {
	t.Helper()
	state, e := os.ReadFile(filepath.Join(os.Getenv("LIT_WORKFLOW_STATE_DIR"), "hooks", logName))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		t.Fatal(e)
	}
	var out []entry
	for _, line := range bytes.Split(state, []byte("\n")) {
		var en entry
		if json.Unmarshal(line, &en) == nil && en.Level == "warn" {
			out = append(out, en)
		}
	}
	return out
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
