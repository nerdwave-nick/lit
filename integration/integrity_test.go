package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/storage"
)

// errorCode asks the server for path and returns the status and error code.
func errorCode(t *testing.T, s *server, method, path string) (int, string, string) {
	t.Helper()
	req, e := http.NewRequest(method, s.endpoint+path, strings.NewReader("{}"))
	if e != nil {
		t.Fatal(e)
	}
	r, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	return r.StatusCode, body.Error.Code, body.Error.Message
}

// verifyBinary runs lit-server verify on data.
func verifyBinary(t *testing.T, data string) (int, string) {
	t.Helper()
	c := exec.Command(litBin, "verify", "--data-dir", data)
	c.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0")
	out, e := c.CombinedOutput()
	code := 0
	if x, ok := e.(*exec.ExitError); ok {
		code = x.ExitCode()
	} else if e != nil {
		t.Fatal(e)
	}
	return code, string(out)
}

// A damaged database keeps lit-server up in degraded mode: every request,
// /v1/meta included, gets 503 recovery_required with the reason, and the CLI
// reports it.
func TestDamagedDatabaseServesDegradedMode(t *testing.T) {
	root := t.TempDir()
	data, state := filepath.Join(root, "data"), filepath.Join(root, "state")
	if e := os.MkdirAll(data, 0o700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(data, storage.DatabaseFile), bytes.Repeat([]byte("not a database"), 1000), 0o600); e != nil {
		t.Fatal(e)
	}
	s := start(t, data)
	for _, r := range [][2]string{{"GET", "/v1/meta"}, {"POST", "/v1/connect"}, {"GET", "/v1/projects"}} {
		if code, errCode, msg := errorCode(t, s, r[0], r[1]); code != 503 || errCode != "recovery_required" || !strings.Contains(msg, "file is not a database") {
			t.Fatal(r, code, errCode, msg)
		}
	}
	v := run(t, root, state, 1, "connect", "--endpoint", s.endpoint, "--format", "json")
	if v["error"].(map[string]any)["code"] != "recovery_required" {
		t.Fatal(v)
	}
	if !strings.Contains(s.log.String(), "degraded mode") {
		t.Fatal(s.log.String())
	}
	t.Log(s.log.String())
	s.stop(t)
}

// Corruption met while serving makes lit-server exit non-zero; restarted on
// the damaged database, it serves degraded mode.
func TestCorruptionWhileServingExits(t *testing.T) {
	root := t.TempDir()
	data, state := filepath.Join(root, "data"), filepath.Join(root, "state")
	s := start(t, data)
	client := item(run(t, root, state, 0, "connect", "--endpoint", s.endpoint, "--format", "json"))
	// Make the stored schema unparsable and bump the schema cookie, so the
	// server's next statement reloads it and meets SQLITE_CORRUPT.
	db := database(t, data)
	var version int
	if e := db.QueryRow("PRAGMA schema_version").Scan(&version); e != nil {
		t.Fatal(e)
	}
	for _, q := range []string{
		"PRAGMA writable_schema = ON",
		"UPDATE sqlite_schema SET sql = 'CREATE INDEX issues_parent ON' WHERE name = 'issues_parent'",
		"PRAGMA writable_schema = OFF",
		fmt.Sprintf("PRAGMA schema_version = %d", version+1),
	} {
		if _, e := db.Exec(q); e != nil {
			t.Fatal(q, e)
		}
	}
	db.Close()
	// Any request reading storage meets it; this one reads its client.
	req, e := http.NewRequest("GET", s.endpoint+"/v1/projects", nil)
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("X-Lit-Client-ID", client["client_id"].(string))
	if r, e := http.DefaultClient.Do(req); e == nil {
		r.Body.Close()
	}
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case e := <-done:
		if x, ok := e.(*exec.ExitError); !ok || x.ExitCode() != 1 || !strings.Contains(s.log.String(), "database failure while serving") {
			t.Fatalf("exit %v: %s", e, s.log)
		}
		t.Log(s.log.String())
	case <-time.After(10 * time.Second):
		s.cmd.Process.Kill()
		t.Fatalf("still running: %s", s.log)
	}
	if cleanMarker(t, data) {
		t.Fatal("a database failure recorded a clean shutdown")
	}
	s = start(t, data)
	if code, errCode, msg := errorCode(t, s, "GET", "/v1/meta"); code != 503 || errCode != "recovery_required" || !strings.Contains(msg, "malformed") {
		t.Fatal(code, errCode, msg)
	}
	s.stop(t)
}

// lit-server verify passes on a tracker written through the API, fails while
// lit-server runs, and reports a change made behind the history.
func TestVerifyCommand(t *testing.T) {
	root := t.TempDir()
	data, state := filepath.Join(root, "data"), filepath.Join(root, "state")
	s := start(t, data)
	run(t, root, state, 0, "connect", "--endpoint", s.endpoint, "--format", "json", "--actor-name", "Tester")
	run(t, root, state, 0, "projects", "create", "--project-title", "feat/verify")
	run(t, root, state, 0, "session", "set", "--project", "feat/verify")
	run(t, root, state, 0, "issues", "create", "--issue", "Parent", "--content", "one\r\ntwo", "--label", "x")
	run(t, root, state, 0, "issues", "create", "--issue", "Child", "--parent", "Parent")
	run(t, root, state, 0, "issues", "update", "title:Parent", "--content", "one\nthree\n", "--add-label", "y")
	run(t, root, state, 0, "comments", "create", "--issue", "title:Child", "--content", "a comment")
	run(t, root, state, 0, "issues", "close", "title:Child")
	if code, out := verifyBinary(t, data); code != 1 || !strings.Contains(out, "already owned") {
		t.Fatal(code, out)
	}
	s.stop(t)
	if code, out := verifyBinary(t, data); code != 0 || !strings.Contains(out, "verified") || !strings.Contains(out, "4 records") {
		t.Fatal(code, out)
	}
	db := database(t, data)
	if _, e := db.Exec("UPDATE issues SET state = 'open' WHERE title = 'Child'"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	code, out := verifyBinary(t, data)
	if code != 1 || !strings.Contains(out, `state is "open", history replays "closed"`) {
		t.Fatal(code, out)
	}
	t.Log(out)
}
