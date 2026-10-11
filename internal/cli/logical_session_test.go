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

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/service"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

func TestConnectSessionIDOnly(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("LIT_STATE_DIR", t.TempDir())
	t.Setenv("LIT_SESSION", "")
	data, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	handler, err := service.New(data, service.Config{Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"test"}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	t.Setenv("LIT_ENDPOINT", server.URL)
	var out, stderr bytes.Buffer
	run := func(args ...string) int {
		out.Reset()
		stderr.Reset()
		return Run(args, &out, &stderr)
	}
	if code := run("connect", "--session-id-only"); code != 0 {
		t.Fatalf("%d: %s", code, &stderr)
	}
	name := strings.TrimSuffix(out.String(), "\n")
	if !strings.HasPrefix(name, "throwaway-") || !protocol.ValidUUID(strings.TrimPrefix(name, "throwaway-")) || out.String() != name+"\n" || stderr.Len() != 0 {
		t.Fatalf("unexpected output: %q stderr %q", &out, &stderr)
	}
	t.Setenv("LIT_SESSION", name)
	if code := run("session", "get", "--format", "json"); code != 0 || !strings.Contains(out.String(), name) {
		t.Fatalf("returned name unusable: %s %s", &out, &stderr)
	}
	for _, format := range []string{"cli", "markdown", "json"} {
		if code := run("connect", "--session-id-only", "--format", format); code != 0 || out.String() != name+"\n" {
			t.Fatalf("%s: %s %s", format, &out, &stderr)
		}
	}
	if code := run("connect", "--session", "named", "--session-id-only", "--output-format", "markdown"); code != 0 || out.String() != "named\n" {
		t.Fatalf("named: %s %s", &out, &stderr)
	}
	if code := run("session", "get", "--session", "named", "--format", "json"); code != 0 || !strings.Contains(out.String(), `"output_format":"markdown"`) {
		t.Fatalf("preference: %s %s", &out, &stderr)
	}
	if code := run("connect", "--session-id-only", "--actor-kind", "invalid"); code == 0 || out.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("failure polluted stdout: %s %s", &out, &stderr)
	}
	if code := run("session", "get", "--session-id-only"); code == 0 || out.Len() != 0 {
		t.Fatalf("flag accepted outside connect: %s %s", &out, &stderr)
	}
	if code := run("connect", "--help"); code != 0 || !strings.Contains(out.String(), "--session-id-only") {
		t.Fatalf("help: %s %s", &out, &stderr)
	}
}

func TestConnectLogicalSessions(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	state := t.TempDir()
	t.Setenv("LIT_STATE_DIR", state)
	t.Setenv("LIT_SESSION", "")
	data, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	handler, err := service.New(data, service.Config{Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"test"}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	t.Setenv("LIT_ENDPOINT", server.URL)
	run := func(args ...string) (string, string) {
		t.Helper()
		var out, stderr bytes.Buffer
		if code := Run(args, &out, &stderr); code != 0 {
			t.Fatalf("%v: %d %s", args, code, &stderr)
		}
		var result struct {
			Items []struct {
				Name string `json:"logical_session"`
				ID   string `json:"client_id"`
			} `json:"items"`
		}
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Items) != 1 || result.Items[0].Name == "" {
			t.Fatalf("missing logical session: %s", &out)
		}
		return result.Items[0].Name, result.Items[0].ID
	}
	first, firstID := run("connect", "--format", "json")
	second, secondID := run("connect", "--format", "json")
	if first == second || firstID == secondID || !strings.HasPrefix(first, "throwaway-") {
		t.Fatalf("sessions not isolated: %s %s / %s %s", first, second, firstID, secondID)
	}
	if !protocol.ValidUUID(strings.TrimPrefix(first, "throwaway-")) {
		t.Fatal(first)
	}
	if _, err := litclient.ReadMapping(litclient.MappingPath(state, "default")); !os.IsNotExist(err) {
		t.Fatalf("implicit default mapping: %v", err)
	}
	t.Setenv("LIT_SESSION", first)
	name, id := run("connect", "--format", "json")
	if name != first || id != firstID {
		t.Fatal("environment did not resume")
	}
	name, id = run("connect", "--session", second, "--format", "json")
	if name != second || id != secondID {
		t.Fatal("explicit session did not override environment")
	}
	name, id = run("session", "get", "--format", "json")
	if name != first || id != firstID {
		t.Fatal("session get did not report environment-selected logical name and client")
	}
	name, id = run("session", "get", "--session", second, "--format", "json")
	if name != second || id != secondID {
		t.Fatal("session get did not report explicitly selected logical name and client")
	}
	for _, format := range []string{"cli", "markdown"} {
		var out, stderr bytes.Buffer
		if code := Run([]string{"session", "get", "--session", second, "--format", format}, &out, &stderr); code != 0 || !strings.Contains(out.String(), second) || !strings.Contains(out.String(), secondID) || !strings.Contains(out.String(), "Logical session") || !strings.Contains(out.String(), "Client ID") {
			t.Fatalf("%s session identities not distinct: %s %s", format, &out, &stderr)
		}
		if format == "cli" && strings.Index(out.String(), "Logical session") > strings.Index(out.String(), "Client ID") {
			t.Fatalf("logical name must precede client UUID: %s", &out)
		}
	}
	for _, selector := range []string{"session-id", "status"} {
		var out, stderr bytes.Buffer
		if code := Run([]string{"session", "get", "--session", second, "--" + selector, "--format", "json"}, &out, &stderr); code != 0 {
			t.Fatalf("filtered session: %d %s", code, &stderr)
		}
		var result struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Items) != 1 || len(result.Items[0]) != 1 {
			t.Fatalf("unexpected filtered fields: %s", &out)
		}
		if selector == "session-id" && result.Items[0]["client_id"] != secondID {
			t.Fatalf("session-id changed semantics: %s", &out)
		}
		if selector == "status" && result.Items[0]["status"] != "connected" {
			t.Fatalf("status changed semantics: %s", &out)
		}
	}
	legacy, legacyID := run("connect", "--session", "default", "--format", "json")
	name, id = run("connect", "--session", "default", "--format", "json")
	if legacy != "default" || name != legacy || id != legacyID {
		t.Fatal("explicit legacy mapping did not resume")
	}
	for _, format := range []string{"cli", "markdown"} {
		var out, stderr bytes.Buffer
		if code := Run([]string{"connect", "--session", first, "--format", format}, &out, &stderr); code != 0 || !strings.Contains(out.String(), first) {
			t.Fatalf("%s missing reusable name: %s %s", format, &out, &stderr)
		}
	}
}

func TestMissingSessionDoesNotUseDefaultOrContactService(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	state := t.TempDir()
	t.Setenv("LIT_STATE_DIR", state)
	t.Setenv("LIT_SESSION", "")
	m := Mapping{SchemaVersion: 1, ClientID: protocol.UUID(), ServiceID: protocol.UUID(), Endpoint: "http://127.0.0.1:7411"}
	if err := litclient.WriteJSON(litclient.MappingPath(state, "default"), m); err != nil {
		t.Fatal(err)
	}
	if err := litclient.WriteJSON(cachePath(m.ClientID), Cache{1, m.ClientID, m.ServiceID, "markdown"}); err != nil {
		t.Fatal(err)
	}
	if got := offlineFormat([]string{"projects", "list"}); got != "" {
		t.Fatalf("implicitly read default preference: %q", got)
	}
	original := http.DefaultTransport
	http.DefaultTransport = endpointTransport(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("missing session contacted %s", r.URL)
		return nil, nil
	})
	defer func() { http.DefaultTransport = original }()
	for _, args := range [][]string{{"projects", "list"}, {"projects", "create", "--project-title", "test/accidental"}, {"disconnect"}, {"session", "get"}} {
		var out, stderr bytes.Buffer
		if code := Run(args, &out, &stderr); code == 0 || !strings.Contains(stderr.String(), "missing_session") || !strings.Contains(stderr.String(), "LIT_SESSION") {
			t.Fatalf("%v: %d %s", args, code, &stderr)
		}
	}
	var out, stderr bytes.Buffer
	if code := Run([]string{"transactions", "status", "--format", "json"}, &out, &stderr); code != 0 {
		t.Fatalf("local transaction status: %d %s", code, &stderr)
	}
	out.Reset()
	stderr.Reset()
	if code := Run([]string{"version"}, &out, &stderr); code != 0 {
		t.Fatalf("version: %d %s", code, &stderr)
	}
}

func TestConnectMappingSaveFailureCanRecoverRegisteredClient(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	state := t.TempDir()
	t.Setenv("LIT_STATE_DIR", state)
	t.Setenv("LIT_SESSION", "")
	t.Setenv("LIT_ENDPOINT", "http://127.0.0.1:7411")
	data, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	handler, err := service.New(data, service.Config{Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"test"}})
	if err != nil {
		t.Fatal(err)
	}
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	blockSave := true
	blockedPath := ""
	http.DefaultTransport = endpointTransport(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if blockSave && r.Method == "POST" && r.URL.Path == "/v1/connect" && w.Code >= 200 && w.Code < 300 {
			paths, err := filepath.Glob(filepath.Join(state, "sessions", "*.json.lock"))
			if err != nil || len(paths) != 1 {
				t.Fatalf("session locks %v: %v", paths, err)
			}
			blockedPath = strings.TrimSuffix(paths[0], ".lock")
			// Registration has committed. A directory at the mapping destination makes
			// its atomic rename fail, without depending on permissions or running UID.
			if err := os.Mkdir(blockedPath, 0700); err != nil {
				t.Fatal(err)
			}
		}
		return w.Result(), nil
	})
	var out, stderr bytes.Buffer
	code := Run([]string{"connect", "--format", "json"}, &out, &stderr)
	if code == 0 || out.Len() != 0 {
		t.Fatalf("save failure: code=%d stdout=%s stderr=%s", code, &out, &stderr)
	}
	var failure struct {
		Error struct {
			Code    string            `json:"code"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	details := failure.Error.Details
	if failure.Error.Code != "local_state_error" || !protocol.ValidUUID(details["client_id"]) || !protocol.ValidUUID(details["service_id"]) || !strings.HasPrefix(details["logical_session"], "throwaway-") || details["endpoint"] != "http://127.0.0.1:7411" {
		t.Fatalf("missing recovery identity: %s", &stderr)
	}
	if litclient.MappingPath(state, details["logical_session"]) != blockedPath {
		t.Fatalf("wrong recovery name: %v", details)
	}
	if err := os.Remove(blockedPath); err != nil {
		t.Fatal(err)
	}
	blockSave = false
	out.Reset()
	stderr.Reset()
	if code := Run([]string{"connect", "--session", details["logical_session"], "--client-id", details["client_id"], "--endpoint", details["endpoint"], "--format", "json"}, &out, &stderr); code != 0 {
		t.Fatalf("recover: %d %s", code, &stderr)
	}
	mapping, err := litclient.ReadMapping(blockedPath)
	if err != nil || mapping.ClientID != details["client_id"] || mapping.ServiceID != details["service_id"] || mapping.Endpoint != details["endpoint"] {
		t.Fatalf("recovered wrong identity: %+v %v", mapping, err)
	}
	var result struct {
		Items []struct {
			Name string `json:"logical_session"`
			ID   string `json:"client_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Name != details["logical_session"] || result.Items[0].ID != details["client_id"] {
		t.Fatalf("recovered output: %s", &out)
	}
}

func TestExplicitEmptySessionRejectedBeforeSideEffects(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	state := filepath.Join(t.TempDir(), "uncreated-state")
	t.Setenv("LIT_STATE_DIR", state)
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = endpointTransport(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("empty session contacted %s", r.URL)
		return nil, nil
	})
	for _, env := range []string{"", "existing-session"} {
		t.Setenv("LIT_SESSION", env)
		for _, command := range [][]string{{"connect"}, {"disconnect"}, {"session", "get"}, {"projects", "list"}, {"issues", "list"}, {"comments", "list", "--issue", "example"}, {"grep", "needle"}, {"claims", "list"}, {"transactions", "status"}, {"version"}} {
			for _, flag := range [][]string{{"--session", ""}, {"--session="}} {
				args := append(append([]string{}, command...), flag...)
				parsed, err := Parse(args)
				if err == nil || !strings.Contains(err.Error(), "--session must not be empty") {
					t.Fatalf("Parse(%v): %+v %v", args, parsed, err)
				}
				var out, stderr bytes.Buffer
				code := Run(args, &out, &stderr)
				if code == 0 || out.Len() != 0 || !strings.Contains(stderr.String(), "invalid_arguments") || !strings.Contains(stderr.String(), "--session must not be empty") {
					t.Fatalf("%v env=%q: %d out=%s err=%s", args, env, code, &out, &stderr)
				}
				if _, err := os.Stat(state); !os.IsNotExist(err) {
					t.Fatalf("empty session created state: %v", err)
				}
			}
		}
	}
	parsed, err := Parse([]string{"grep", "--", "--session="})
	if err != nil || len(parsed.Positionals) != 1 || parsed.Positionals[0] != "--session=" {
		t.Fatalf("literal session flag was rejected: %+v %v", parsed, err)
	}
}
