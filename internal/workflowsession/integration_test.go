package workflowsession

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/service"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

func TestRealServiceLifecycle(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "lit")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "../../cmd/lit")
	if output, e := build.CombinedOutput(); e != nil {
		t.Fatalf("build: %s %v", output, e)
	}
	data, e := storage.Open(filepath.Join(root, "data"))
	if e != nil {
		t.Fatal(e)
	}
	defer data.Close()
	server, e := service.New(data, service.Config{Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"feat"}})
	if e != nil {
		t.Fatal(e)
	}
	http := httptest.NewServer(server)
	defer http.Close()
	workflow := filepath.Join(root, "workflow")
	t.Setenv("LIT_WORKFLOW_STATE_DIR", workflow)
	t.Setenv("LIT_STATE_DIR", filepath.Join(root, "private"))
	t.Setenv("LIT_SESSION", "inherited-parent")
	t.Setenv("LIT_ENDPOINT", "http://127.0.0.1:1")
	repo1 := filepath.Join(root, "repo1")
	repo2 := filepath.Join(root, "repo2")
	os.Mkdir(repo1, 0700)
	os.Mkdir(repo2, 0700)
	cwd := repo1
	call := func(ok bool, host, id string, args ...string) map[string]any {
		t.Helper()
		argv := append([]string{"workflow-session", "--host", host, "--runtime-id", id}, args...)
		cmd := exec.Command(binary, argv...)
		cmd.Dir = cwd
		output, e := cmd.CombinedOutput()
		if ok && e != nil {
			t.Fatalf("%v: %s %v", args, output, e)
		}
		if !ok {
			if e == nil {
				t.Fatalf("accepted %v: %s", args, output)
			}
			return nil
		}
		var r map[string]any
		if e = json.Unmarshal(output, &r); e != nil {
			t.Fatalf("%v: %s %v", args, output, e)
		}
		return r
	}
	ids := map[any]bool{}
	for _, host := range []string{"codex", "claude"} {
		first := call(true, host, "main", "fresh", "--endpoint", http.URL, "--discussion-id", "ticket:03")
		ids[first["client_id"]] = true
		p := call(true, host, "main", "run", "--", "projects", "create", "--project-title", "feat/"+host, "--content", "-literal --session")["items"].([]any)[0].(map[string]any)["id"].(string)
		call(true, host, "main", "run", "--", "session", "set", "--project", p)
		cwd = repo2
		resumed := call(true, host, "main", "resume")
		if resumed["client_id"] != first["client_id"] || resumed["project_id"] != p {
			t.Fatal("lost identity/project")
		}
		child := call(true, host, "child", "subagent", "--parent-runtime-id", "main", "--endpoint", http.URL, "--project", p)
		if ids[child["client_id"]] {
			t.Fatal("shared child")
		}
		ids[child["client_id"]] = true
		call(false, host, "main", "fresh", "--endpoint", http.URL)
		call(false, host, "missing", "resume")
		call(false, host, "main", "subagent", "--parent-runtime-id", "main", "--endpoint", http.URL)
		call(true, host, "main", "checkout", "--repository", "https://example.org/"+host, "--path", repo2)
		for _, pattern := range []string{"-literal", "--session"} {
			r := call(true, host, "main", "run", "--", "grep", "--", pattern)
			if len(r["items"].([]any)) != 1 {
				t.Fatal("literal search", r)
			}
		}
		for _, args := range [][]string{{"run", "--", "session", "get", "--session", "other"}, {"run", "--", "grep", "--endpoint=x"}, {"run", "--", "connect"}, {"run", "--", "session", "set", "--runtime-session-id", "other"}, {"run", "--", "session", "get", "--format", "human"}} {
			call(false, host, "main", args...)
		}
		call(true, host, "main", "run", "--", "disconnect")
		resumed = call(true, host, "main", "resume")
		if resumed["client_id"] != first["client_id"] {
			t.Fatal("disconnect changed identity")
		}
		filename := filepath.Join(workflow, "vendor-sessions", key(host, "main")+".json")
		var saved record
		if e = read(filename, &saved); e != nil {
			t.Fatal(e)
		}
		saved.Status = "pending"
		saved.Client = nil
		saved.Service = nil
		if e = save(filename, saved); e != nil {
			t.Fatal(e)
		}
		reconciled := call(true, host, "main", "resume")
		if reconciled["client_id"] != first["client_id"] || reconciled["local_session"] != first["local_session"] {
			t.Fatal("pending replaced identity")
		}
		// An existing mapping whose host runtime was changed must not be adopted.
		cmd := exec.Command(binary, "--session", saved.Session, "--endpoint", http.URL, "--format", "json", "session", "set", "--runtime-session-id", "other")
		cmd.Dir = cwd
		if output, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("change runtime %s %v", output, e)
		}
		before, _ := os.ReadFile(filename)
		call(false, host, "main", "reconcile")
		after, _ := os.ReadFile(filename)
		if string(before) != string(after) {
			t.Fatal("mismatch rewrote state")
		}
		saved.Version = 2
		save(filename, saved)
		before, _ = os.ReadFile(filename)
		call(false, host, "main", "resume")
		after, _ = os.ReadFile(filename)
		if string(before) != string(after) {
			t.Fatal("corruption rewritten")
		}
	}
	if len(ids) != 4 {
		t.Fatal(ids)
	}
	mappings, _ := filepath.Glob(filepath.Join(workflow, "checkouts", "*.json"))
	if len(mappings) != 2 {
		t.Fatal(mappings)
	}
	for _, path := range mappings {
		var r map[string]any
		if e = read(path, &r); e != nil || r["discussion_id"] != "ticket:03" {
			t.Fatal(r, e)
		}
	}
	call(false, "codex", "unavailable", "fresh", "--endpoint", "http://127.0.0.1:1")
	filename := filepath.Join(workflow, "vendor-sessions", key("codex", "unavailable")+".json")
	before, _ := os.ReadFile(filename)
	call(false, "codex", "unavailable", "resume")
	after, _ := os.ReadFile(filename)
	if string(before) != string(after) || !strings.Contains(string(after), `"status": "pending"`) {
		t.Fatal("lost pending")
	}
	// A persisted lock blocks concurrent use without touching the association.
	lock := strings.TrimSuffix(filename, ".json") + ".lock"
	os.Mkdir(lock, 0700)
	call(false, "codex", "unavailable", "reconcile")
	after, _ = os.ReadFile(filename)
	if string(before) != string(after) {
		t.Fatal("lock changed record")
	}
}

func TestPlatformStateDefaults(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	for _, tc := range []struct{ platform, xdg, local, want string }{{"linux", "relative", "", filepath.Join(home, ".local", "state", "lit", "local")}, {"linux", home, "", filepath.Join(home, "lit", "local")}, {"darwin", "", "", filepath.Join(home, "Library", "Application Support", "lit", "local")}, {"windows", "", home, filepath.Join(home, "lit", "local")}} {
		got, e := statePath(tc.platform, home, tc.xdg, tc.local)
		if e != nil || got != tc.want {
			t.Fatal(got, e, tc)
		}
	}
	if _, e := statePath("windows", home, "", ""); e == nil {
		t.Fatal("missing LOCALAPPDATA accepted")
	}
}
