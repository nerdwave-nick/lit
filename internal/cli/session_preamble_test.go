package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/service"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/store"
)

// preambleService starts a real service and isolates CLI state, cwd caches and
// environment for one test.
func preambleService(t *testing.T, wrap func(http.Handler) http.Handler) (state string, server *httptest.Server) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	state = t.TempDir()
	t.Setenv("LIT_STATE_DIR", state)
	t.Setenv("LIT_SESSION", "")
	data, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { data.Close() })
	var handler http.Handler
	handler, err = service.New(data, service.Config{Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"test"}})
	if err != nil {
		t.Fatal(err)
	}
	if wrap != nil {
		handler = wrap(handler)
	}
	server = httptest.NewServer(handler)
	t.Cleanup(server.Close)
	t.Setenv("LIT_ENDPOINT", server.URL)
	return state, server
}

func jsonErrorCode(t *testing.T, stderr []byte) string {
	t.Helper()
	var v struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr, &v); err != nil {
		t.Fatalf("stderr is not a JSON error: %q", stderr)
	}
	return v.Error.Code
}

// Characterizes the session preamble: which error each failure produces, its
// exit code, and which output format renders it at that point.
func TestSessionPreambleErrorsAndFormats(t *testing.T) {
	state, _ := preambleService(t, nil)
	var out, stderr bytes.Buffer
	run := func(args ...string) int {
		out.Reset()
		stderr.Reset()
		return Run(args, &out, &stderr)
	}
	setCache := func(m Mapping, format string) {
		t.Helper()
		if err := WriteJSON(cachePath(m.ClientID), Cache{1, m.ClientID, m.ServiceID, format}); err != nil {
			t.Fatal(err)
		}
	}

	if code := run("session", "get", "--session", "ghost", "--format", "json"); code != 2 || out.Len() != 0 || jsonErrorCode(t, stderr.Bytes()) != "not_connected" {
		t.Fatalf("unmapped session: %d %s %s", code, &out, &stderr)
	}
	if _, err := os.Stat(mappingPath(state, "ghost")); !os.IsNotExist(err) {
		t.Fatalf("unmapped session created a mapping: %v", err)
	}

	if code := run("connect", "--session", "pref", "--output-format", "markdown", "--format", "json"); code != 0 {
		t.Fatalf("connect: %d %s", code, &stderr)
	}
	mapping, err := readMapping(mappingPath(state, "pref"))
	if err != nil {
		t.Fatal(err)
	}

	unlock, err := acquireLocal(mappingPath(state, "pref")+".lock", time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	code := run("session", "get", "--session", "pref", "--timeout", "50ms", "--format", "json")
	unlock()
	if code != 3 || jsonErrorCode(t, stderr.Bytes()) != "session_locked" {
		t.Fatalf("locked mapping: %d %s", code, &stderr)
	}

	// Before the client record is read, the cwd cache selects the format.
	setCache(mapping, "json")
	if code := run("session", "get", "--session", "pref", "--endpoint", "http://127.0.0.1:1"); code != 1 || jsonErrorCode(t, stderr.Bytes()) != "service_unavailable" {
		t.Fatalf("unreachable endpoint: %d %s", code, &stderr)
	}

	if code := run("disconnect", "--session", "pref", "--format", "json"); code != 0 {
		t.Fatalf("disconnect: %d %s", code, &stderr)
	}
	// After the client record is read, its stored preference selects the format,
	// including for the disconnected-client refusal.
	setCache(mapping, "cli")
	if code := run("session", "get", "--session", "pref"); code != 3 || out.Len() != 0 || !strings.HasPrefix(stderr.String(), "**Error [client&#95;disconnected]:**") {
		t.Fatalf("disconnected client: %d %q %q", code, &out, &stderr)
	}
	if code := run("disconnect", "--session", "pref", "--format", "json"); code != 0 || !strings.Contains(out.String(), `"outcome":"already_disconnected"`) {
		t.Fatalf("repeat disconnect: %d %s %s", code, &out, &stderr)
	}

	if err := os.WriteFile(mappingPath(state, "corrupt"), []byte(`{"schema_version":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	if code := run("session", "get", "--session", "corrupt", "--format", "json"); code != 1 || jsonErrorCode(t, stderr.Bytes()) != "invalid_local_state" {
		t.Fatalf("corrupt mapping: %d %s", code, &stderr)
	}

	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(protocol.Response{Data: protocol.Meta{ServiceID: protocol.UUID(), APIMajor: 2}})
	}))
	defer other.Close()
	if code := run("connect", "--session", "future", "--endpoint", other.URL, "--format", "json"); code != 1 || jsonErrorCode(t, stderr.Bytes()) != "incompatible_service" {
		t.Fatalf("incompatible service: %d %s", code, &stderr)
	}
	if _, err := os.Stat(mappingPath(state, "future")); !os.IsNotExist(err) {
		t.Fatalf("incompatible service created a mapping: %v", err)
	}
}

// A confirmed mutation whose pending record cannot be removed still succeeds,
// and the cleanup warning goes to the CLI's stderr.
func TestPendingCleanupWarningReachesStderr(t *testing.T) {
	var state string
	state, _ = preambleService(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" && r.URL.Path == "/v1/connect" {
				paths, err := filepath.Glob(filepath.Join(state, "pending", "*.json"))
				if err != nil || len(paths) != 1 {
					t.Errorf("pending evidence before send: %v %v", paths, err)
				} else if err = os.Remove(paths[0]); err == nil {
					// A non-empty directory makes the later removal fail without
					// depending on permissions or the running UID.
					if err = os.Mkdir(paths[0], 0700); err == nil {
						err = os.WriteFile(filepath.Join(paths[0], "keep"), nil, 0600)
					}
				}
				if err != nil {
					t.Error(err)
				}
			}
			next.ServeHTTP(w, r)
		})
	})
	var out, stderr bytes.Buffer
	if code := Run([]string{"connect", "--session", "warn", "--format", "json"}, &out, &stderr); code != 0 {
		t.Fatalf("connect: %d %s", code, &stderr)
	}
	if !strings.HasPrefix(stderr.String(), "warning: confirmed operation; pending record cleanup failed:") || strings.Count(stderr.String(), "\n") != 1 {
		t.Fatalf("stderr %q", &stderr)
	}
	var result Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || len(result.Items) != 1 {
		t.Fatalf("stdout %q: %v", &out, err)
	}
}
