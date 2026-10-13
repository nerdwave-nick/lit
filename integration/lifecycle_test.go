package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	_ "github.com/nerdwave-nick/lit/internal/cli"
	_ "github.com/nerdwave-nick/lit/internal/service"
	"github.com/nerdwave-nick/lit/internal/storage"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var litBin, cliBin string

func TestMain(m *testing.M) {
	d, e := os.MkdirTemp("", "lit-tests-")
	if e != nil {
		panic(e)
	}
	litBin = filepath.Join(d, "lit-server")
	cliBin = filepath.Join(d, "lit")
	for _, x := range []struct{ out, pkg string }{{litBin, "../cmd/lit-server"}, {cliBin, "../cmd/lit"}} {
		c := exec.Command("go", "build", "-race", "-o", x.out, x.pkg)
		if b, e := c.CombinedOutput(); e != nil {
			fmt.Printf("build: %s %v\n", b, e)
			os.RemoveAll(d)
			os.Exit(1)
		}
	}
	n := m.Run()
	os.RemoveAll(d)
	os.Exit(n)
}

type server struct {
	cmd      *exec.Cmd
	endpoint string
	log      *safeBuffer
}

func start(t *testing.T, data string, env ...string) *server {
	t.Helper()
	return startBinary(t, litBin, data, env...)
}

// startBinary starts the lit-server binary bin.
func startBinary(t *testing.T, bin, data string, env ...string) *server {
	t.Helper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	addr := l.Addr().String()
	l.Close()
	b := new(safeBuffer)
	c := exec.Command(bin, "--data-dir", data, "--listen", addr)
	c.Env = append(append(os.Environ(), "GORACE=atexit_sleep_ms=0"), env...)
	c.Stderr = b
	if e = c.Start(); e != nil {
		t.Fatal(e)
	}
	s := &server{c, "http://" + addr, b}
	t.Cleanup(func() {
		if c.ProcessState == nil {
			s.stop(t)
		}
	})
	for i := 0; i < 200; i++ {
		r, e := http.Get(s.endpoint + "/v1/meta")
		if e == nil {
			r.Body.Close()
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("service unavailable: %s", b)
	return nil
}
func (s *server) stop(t *testing.T) {
	t.Helper()
	// The test clients share http.DefaultTransport. A connection it dialed
	// but never used is new to the server, and Shutdown waits up to 5s for
	// new connections: close them so the drain is not held up.
	http.DefaultTransport.(*http.Transport).CloseIdleConnections()
	s.cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatalf("stop: %v: %s", e, s.log)
		}
	case <-time.After(3 * time.Second):
		s.cmd.Process.Kill()
		t.Fatal("did not drain")
	}
}
func run(t *testing.T, cwd, state string, want int, args ...string) map[string]any {
	t.Helper()
	c := exec.Command(cliBin, args...)
	c.Dir = cwd
	c.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0", "LIT_SESSION=default", "LIT_STATE_DIR="+state)
	var out, err bytes.Buffer
	c.Stdout = &out
	c.Stderr = &err
	e := c.Run()
	code := 0
	if e != nil {
		if x, ok := e.(*exec.ExitError); ok {
			code = x.ExitCode()
		} else {
			t.Fatal(e)
		}
	}
	if code != want {
		t.Fatalf("%v: code %d want %d stdout=%s stderr=%s", args, code, want, out.String(), err.String())
	}
	b := out.Bytes()
	if want != 0 {
		if out.Len() != 0 {
			t.Fatal("failure stdout", out.String())
		}
		b = err.Bytes()
	}
	var v map[string]any
	if e = json.Unmarshal(b, &v); e != nil {
		t.Fatalf("JSON: %v: %s", e, b)
	}
	return v
}
func item(v map[string]any) map[string]any { return v["items"].([]any)[0].(map[string]any) }
func TestLifecycle(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	data := filepath.Join(root, "data")
	s := start(t, data)
	a := item(run(t, root, state, 0, "connect", "--endpoint", s.endpoint, "--session", "a", "--actor-name", "Alpha", "--actor-kind", "agent", "--format", "json"))
	b := item(run(t, root, state, 0, "connect", "--endpoint", s.endpoint, "--session", "b", "--format", "json"))
	if a["client_id"] == b["client_id"] {
		t.Fatal("sessions shared identity")
	}
	id := a["client_id"]
	service := a["service_id"]
	run(t, root, state, 0, "session", "set", "--session", "a", "--runtime-vendor", "codex", "--runtime-session-id", "vendor-1")
	run(t, root, state, 0, "disconnect", "--session", "a")
	run(t, root, state, 3, "session", "get", "--session", "a")
	a = item(run(t, root, state, 0, "connect", "--session", "a"))
	if a["client_id"] != id {
		t.Fatal("resume replaced identity")
	}
	s.stop(t)
	s = start(t, data)
	a = item(run(t, root, state, 0, "session", "get", "--session", "a", "--endpoint", s.endpoint))
	if a["client_id"] != id || a["service_id"] != service {
		t.Fatal("restart changed identity")
	}
	if a["runtime"].(map[string]any)["session_id"] != "vendor-1" {
		t.Fatal("lost runtime")
	}
	run(t, root, state, 0, "session", "unset", "--session", "a", "--endpoint", s.endpoint, "--runtime-vendor", "--runtime-session-id")
	other := start(t, filepath.Join(root, "other"))
	v := run(t, root, state, 1, "session", "get", "--session", "a", "--endpoint", other.endpoint, "--format", "json")
	if v["error"].(map[string]any)["code"] != "wrong_service" {
		t.Fatal(v)
	}
}

func rawCLI(t *testing.T, cwd, state string, args ...string) (int, string, string) {
	t.Helper()
	c := exec.Command(cliBin, args...)
	c.Dir = cwd
	c.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0", "LIT_SESSION=default", "LIT_STATE_DIR="+state)
	var out, err bytes.Buffer
	c.Stdout = &out
	c.Stderr = &err
	e := c.Run()
	code := 0
	if e != nil {
		if x, ok := e.(*exec.ExitError); ok {
			code = x.ExitCode()
		} else {
			t.Fatal(e)
		}
	}
	return code, out.String(), err.String()
}
func TestOutputOfflineAndSessionProperties(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	s := start(t, filepath.Join(root, "data"))
	v := item(run(t, root, state, 0, "connect", "--endpoint", s.endpoint, "--format", "json", "--actor-name", "First"))
	id := v["client_id"].(string)
	v = item(run(t, root, state, 0, "session", "set", "--actor-name", "Second", "--actor-kind", "agent", "--runtime-vendor", "codex", "--runtime-session-id", "vendor"))
	if v["state_revision"] != float64(2) {
		t.Fatal(v)
	}
	v = item(run(t, root, state, 0, "session", "get", "--actor-name", "--runtime-session-id", "--revision"))
	if len(v) != 3 || v["actor_name"] != "Second" || v["runtime_session_id"] != "vendor" {
		t.Fatal(v)
	}
	code, out, err := rawCLI(t, root, state, "session", "set", "--output-format", "cli")
	if code != 0 || !strings.Contains(out, "Client ID: "+id) || !strings.Contains(out, "Output format: cli") || !strings.Contains(out, "Name: Second") || strings.Contains(out, "```") || err != "" {
		t.Fatal(code, out, err)
	}
	s.stop(t)
	code, out, err = rawCLI(t, root, state, "session", "get")
	if code != 1 || out != "" || !strings.HasPrefix(err, "Error [service_unavailable]") {
		t.Fatal(code, out, err)
	}
	run(t, root, state, 1, "session", "get", "--format", "json")
	cwd := t.TempDir()
	run(t, cwd, state, 1, "session", "get")
	os.WriteFile(filepath.Join(root, ".lit", id, "state.json"), []byte(`{"schema_version":99}`), 0600)
	run(t, root, state, 1, "session", "get")
	run(t, root, state, 2, "session", "get", "--format", "json", "--unknown")
	code, out, err = rawCLI(t, root, state, "--help")
	if code != 0 || !strings.Contains(out, "connect") || err != "" {
		t.Fatal(code, out, err)
	}
}
func TestConcurrentSameNameConnect(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	s := start(t, filepath.Join(root, "data"))
	type result struct {
		out []byte
		err error
	}
	ch := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			c := exec.Command(cliBin, "connect", "--endpoint", s.endpoint, "--session", "one", "--format", "json")
			c.Dir = root
			c.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0", "LIT_SESSION=default", "LIT_STATE_DIR="+state)
			b, e := c.CombinedOutput()
			ch <- result{b, e}
		}()
	}
	var ids []any
	for i := 0; i < 2; i++ {
		r := <-ch
		if r.err != nil {
			t.Fatal(r.err, string(r.out))
		}
		var v map[string]any
		json.Unmarshal(r.out, &v)
		ids = append(ids, item(v)["client_id"])
	}
	if ids[0] != ids[1] {
		t.Fatal("same name acquired multiple identities", ids)
	}
	if n := clientCount(t, filepath.Join(root, "data")); n != 1 {
		t.Fatal("clients stored:", n)
	}
}
func TestExclusiveStoreAndPortAndVersionRejection(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	s := start(t, data)
	c := exec.Command(litBin, "--data-dir", data, "--listen", "127.0.0.1:0")
	b, e := c.CombinedOutput()
	if e == nil || !bytes.Contains(b, []byte("already owned")) {
		t.Fatal(e, string(b))
	}
	c = exec.Command(litBin, "--data-dir", filepath.Join(root, "other"), "--listen", strings.TrimPrefix(s.endpoint, "http://"))
	b, e = c.CombinedOutput()
	if e == nil || !bytes.Contains(b, []byte("cannot listen")) {
		t.Fatal(e, string(b))
	}
	state := filepath.Join(root, "state")
	run(t, root, state, 0, "connect", "--endpoint", s.endpoint, "--format", "json")
	s.stop(t)
	// A database whose schema is newer than this lit-server knows is refused
	// before anything is written to it.
	db := database(t, data)
	if _, e := db.Exec("UPDATE service SET schema_version = schema_version + 1"); e != nil {
		t.Fatal(e)
	}
	db.Close() // the last connection checkpoints its change into the database file
	p := filepath.Join(data, storage.DatabaseFile)
	b, e = os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	c = exec.Command(litBin, "--data-dir", data, "--listen", "127.0.0.1:0")
	got, e := c.CombinedOutput()
	if e == nil || !bytes.Contains(got, []byte("newer than this lit-server supports")) {
		t.Fatal(e, string(got))
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(after, b) {
		t.Fatal("rewrote unsupported database")
	}
}

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *safeBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }
func TestKilledServicePreservesAcknowledgedState(t *testing.T) {
	root := t.TempDir()
	data, state := filepath.Join(root, "data"), filepath.Join(root, "state")
	s := start(t, data)
	before := item(run(t, root, state, 0, "connect", "--endpoint", s.endpoint, "--format", "json"))
	run(t, root, state, 0, "session", "set", "--actor-name", "Durable")
	if e := s.cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	if e := s.cmd.Wait(); e == nil {
		t.Fatal("expected killed process")
	}
	s = start(t, data)
	after := item(run(t, root, state, 0, "session", "get", "--endpoint", s.endpoint))
	if after["client_id"] != before["client_id"] || after["service_id"] != before["service_id"] || after["state_revision"] != float64(2) || after["actor"].(map[string]any)["name"] != "Durable" {
		t.Fatal("lost acknowledged state", after)
	}
}
