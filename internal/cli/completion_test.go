package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

func TestDynamicCompletionRoutingAndNoLocalWrites(t *testing.T) {
	state, cwd := t.TempDir(), t.TempDir()
	t.Setenv("LIT_STATE_DIR", state)
	t.Setenv("LIT_SESSION", "")
	t.Chdir(cwd)
	serviceID := protocol.UUID()
	var calls atomic.Int32
	var path, project, prefix, client string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != path || r.URL.Query().Get("project") != project || r.URL.Query().Get("prefix") != prefix || r.Header.Get("X-Lit-Client-ID") != client {
			t.Errorf("unexpected completion request: %s %s client=%s", r.Method, r.URL, r.Header.Get("X-Lit-Client-ID"))
		}
		json.NewEncoder(w).Encode(map[string]any{"data": protocol.Completions{APIMajor: 1, ServiceID: serviceID, Items: []protocol.Completion{{ID: protocol.UUID(), Value: "feat/sample", Title: "Sample"}}}})
	}))
	defer srv.Close()
	t.Setenv("LIT_ENDPOINT", srv.URL)
	for _, tc := range []struct {
		args []string
		kind string
	}{
		{[]string{"projects", "get"}, "projects"},
		{[]string{"projects", "history"}, "projects"},
		{[]string{"projects", "update"}, "projects"},
		{[]string{"projects", "update", "--project-id"}, "projects"},
		{[]string{"issues", "get"}, "issues"},
		{[]string{"issues", "close"}, "issues"},
		{[]string{"issues", "update", "--issue"}, "issues"},
		{[]string{"issues", "close", "--issue"}, "issues"},
		{[]string{"issues", "link", "--from"}, "issues"},
		{[]string{"issues", "link", "--to"}, "issues"},
		{[]string{"issues", "create", "--parent"}, "issues"},
		{[]string{"issues", "list", "--milestone"}, "milestones"},
		{[]string{"milestones", "get"}, "milestones"},
		{[]string{"milestones", "update"}, "milestones"},
		{[]string{"milestones", "create", "--issue"}, "issues"},
		{[]string{"comments", "get"}, "comments"},
		{[]string{"comments", "create", "--issue"}, "issues"},
		{[]string{"comments", "update", "--comment"}, "comments"},
		{[]string{"claims", "acquire"}, "issues"},
		{[]string{"session", "set", "--project"}, "projects"},
		{[]string{"issues", "get", "--project"}, "projects"},
		{[]string{"grep", "--project"}, "projects"},
		{[]string{"workflow-session", "fresh", "--project"}, "projects"},
	} {
		path, prefix = "/v1/completions/"+tc.kind, "fe"
		before := calls.Load()
		out := runCompletionTest(t, append(tc.args, prefix)...)
		if calls.Load() != before+1 || !strings.Contains(out, "feat/sample\tSample") || !strings.HasSuffix(out, ":4\n") {
			t.Fatal(tc.args, out, calls.Load()-before)
		}
	}
	for _, dir := range []string{state, cwd} {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatal("completion wrote local state", dir, entries, err)
		}
	}
	// Missing optional mappings do not prevent sessionless completion.
	path, prefix = "/v1/completions/issues", ""
	runCompletionTest(t, "issues", "get", "--session", "missing", "")
	// Existing mappings supply identity and endpoint without touching a lock or cache.
	mapping := Mapping{SchemaVersion: 1, ClientID: protocol.UUID(), ServiceID: serviceID, Endpoint: srv.URL}
	if err := litclient.WriteJSON(litclient.MappingPath(state, "saved"), mapping); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIT_ENDPOINT", "")
	t.Setenv("LIT_SESSION", "saved")
	client, project = mapping.ClientID, "feat/explicit"
	runCompletionTest(t, "issues", "get", "--project", project, "")
	entries, _ := os.ReadDir(filepath.Join(state, "sessions"))
	if len(entries) != 1 {
		t.Fatal("completion created mapping lock", entries)
	}
	entries, _ = os.ReadDir(cwd)
	if len(entries) != 0 {
		t.Fatal("completion wrote output cache", entries)
	}
}

func TestStaticAndNewTitleCompletionRemainOffline(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "unexpected", 500) }))
	defer srv.Close()
	t.Setenv("LIT_ENDPOINT", srv.URL)
	t.Setenv("LIT_SESSION", "")
	for _, args := range [][]string{
		{"issues", "list", "--state", ""}, {"issues", "create", "--issue", ""},
		{"projects", "create", "--project-title", ""}, {"milestones", "create", "--milestone", ""},
		{"issues", "history", "already-selected", ""}, {"claims", "release", "--all", ""},
	} {
		runCompletionTest(t, args...)
	}
	var out, stderr bytes.Buffer
	if code := Run([]string{"completion", "fish"}, &out, &stderr); code != 0 {
		t.Fatal(code, &stderr)
	}
	if calls.Load() != 0 {
		t.Fatal("static completion accessed server")
	}
}

func TestDynamicCompletionFailsQuietly(t *testing.T) {
	for _, scenario := range []string{"unavailable", "wrong-service", "slow", "invalid-json"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("LIT_STATE_DIR", t.TempDir())
			t.Setenv("LIT_SESSION", "")
			id := protocol.UUID()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch scenario {
				case "unavailable":
					http.Error(w, "down", 503)
				case "slow":
					<-r.Context().Done()
				case "invalid-json":
					w.Write([]byte("oops"))
				default:
					json.NewEncoder(w).Encode(map[string]any{"data": protocol.Completions{APIMajor: 1, ServiceID: id, Items: []protocol.Completion{{Value: "must-not-appear"}}}})
				}
			}))
			defer srv.Close()
			t.Setenv("LIT_ENDPOINT", srv.URL)
			if scenario == "wrong-service" {
				dir, _ := litclient.StateDir()
				if err := litclient.WriteJSON(litclient.MappingPath(dir, "saved"), Mapping{SchemaVersion: 1, ClientID: protocol.UUID(), ServiceID: protocol.UUID(), Endpoint: srv.URL}); err != nil {
					t.Fatal(err)
				}
				t.Setenv("LIT_SESSION", "saved")
			}
			start := time.Now()
			out := runCompletionTest(t, "projects", "get", "")
			if out != ":4\n" {
				t.Fatal("failure leaked output", out)
			}
			if time.Since(start) > 2*completionTimeout {
				t.Fatal("completion exceeded deadline")
			}
		})
	}
}

func runCompletionTest(t *testing.T, args ...string) string {
	t.Helper()
	var out, stderr bytes.Buffer
	if code := Run(append([]string{"__complete"}, args...), &out, &stderr); code != 0 {
		t.Fatalf("%v: %d %s", args, code, &stderr)
	}
	// Cobra itself writes a directive diagnostic on stderr; callbacks must not
	// add application errors. Shell adapters discard this diagnostic.
	if strings.Contains(stderr.String(), "error") || strings.Contains(stderr.String(), "warning") {
		t.Fatal(&stderr)
	}
	return out.String()
}

func TestCompletionHelperProcess(t *testing.T) {
	if os.Getenv("LIT_COMPLETION_TEST_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Exit(Run(os.Args[i+1:], os.Stdout, os.Stderr))
		}
	}
	os.Exit(2)
}

func TestFishDynamicCompletionQuoting(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is not installed")
	}
	t.Setenv("LIT_SESSION", "")
	t.Setenv("LIT_STATE_DIR", t.TempDir())
	const value = "feat/api:Quotes 'and' \"spaces\" $HOME; (echo nope)"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": protocol.Completions{APIMajor: 1, ServiceID: protocol.UUID(), Items: []protocol.Completion{{ID: protocol.UUID(), Value: value, Title: "Display title"}}}})
	}))
	defer srv.Close()
	t.Setenv("LIT_ENDPOINT", srv.URL)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIT_COMPLETION_TEST_BINARY", executable)
	var generated, stderr bytes.Buffer
	if code := Run([]string{"completion", "fish"}, &generated, &stderr); code != 0 {
		t.Fatal(code, &stderr)
	}
	file := filepath.Join(t.TempDir(), "lit.fish")
	if err := os.WriteFile(file, generated.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIT_COMPLETION_TEST_SCRIPT", file)
	cmd := exec.Command(fish, "--no-config", "-c", `
function lit
    command "$LIT_COMPLETION_TEST_BINARY" -test.run=TestCompletionHelperProcess -- $argv
end
source "$LIT_COMPLETION_TEST_SCRIPT"
for line in (complete -C 'lit issues get feat/api:')
    set parts (string split -m 1 \t -- $line)
    set escaped (string escape -- $parts[1])
    eval "printf '%s\\n' $escaped"
end
`)
	cmd.Env = append(os.Environ(), "LIT_COMPLETION_TEST_HELPER=1")
	output, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != value {
		t.Fatalf("fish failed round trip: %q %v", output, err)
	}
}
