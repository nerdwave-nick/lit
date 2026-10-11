package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

func fixture(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	v := openTestServer(t, t.TempDir(), Config{Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"feat"}})
	h := httptest.NewServer(v)
	t.Cleanup(h.Close)
	return v, h
}

// outside opens a second, independent connection to the service's database,
// as another process would.
func outside(t *testing.T, s *Server) *sql.DB {
	t.Helper()
	db, e := sql.Open("sqlite", "file:"+filepath.Join(s.DB.Dir(), storage.DatabaseFile)+"?_pragma=busy_timeout(5000)")
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func countClients(t *testing.T, s *Server) int {
	t.Helper()
	var n int
	if e := outside(t, s).QueryRow("SELECT count(*) FROM clients").Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

// waitInStack waits until some goroutine runs the named function.
func waitInStack(t *testing.T, function string) {
	t.Helper()
	buf := make([]byte, 1<<20)
	for deadline := time.Now().Add(4 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if bytes.Contains(buf[:runtime.Stack(buf, true)], []byte(function)) {
			return
		}
	}
	t.Fatalf("no goroutine reached %s", function)
}
func request(t *testing.T, h *httptest.Server, method, path, body, id, revision string) (int, map[string]any) {
	t.Helper()
	r, _ := http.NewRequest(method, h.URL+path, strings.NewReader(body))
	r.Header.Set("X-Lit-Client-ID", id)
	r.Header.Set("If-Match", revision)
	resp, e := http.DefaultClient.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	var v map[string]any
	if e = json.NewDecoder(resp.Body).Decode(&v); e != nil {
		t.Fatal(e)
	}
	return resp.StatusCode, v
}
func TestConditionalClientState(t *testing.T) {
	_, h := fixture(t)
	status, v := request(t, h, "POST", "/v1/connect", `{"actor":{"name":"A","kind":"agent"},"output_format":"json"}`, "", "")
	if status != 201 {
		t.Fatal(status, v)
	}
	c := v["data"].(map[string]any)["client"].(map[string]any)
	id := c["client_id"].(string)
	for _, x := range []struct {
		body, rev, code string
		status          int
	}{{`{"runtime":{"vendor":"codex"}}`, "", "precondition_required", 428}, {`{"runtime":{"vendor":"codex"}}`, `"client:2"`, "client_state_conflict", 409}, {`{"actor":{"name":"A","name":"B"}}`, `"client:1"`, "invalid_json", 400}, {`{"state_revision":99}`, `"client:1"`, "unknown_property", 400}} {
		status, v = request(t, h, "PATCH", "/v1/clients/"+id, x.body, id, x.rev)
		if status != x.status || v["error"].(map[string]any)["code"] != x.code {
			t.Fatal(status, v)
		}
	}
	status, v = request(t, h, "PATCH", "/v1/clients/"+id, `{"actor":{"name":"B"},"runtime":{"vendor":"codex","session_id":"s"}}`, id, `"client:1"`)
	if status != 200 || v["data"].(map[string]any)["state_revision"] != float64(2) {
		t.Fatal(status, v)
	}
	status, v = request(t, h, "PATCH", "/v1/clients/"+id, `{"actor":{"name":"B"}}`, id, `"client:2"`)
	if status != 200 || v["data"].(map[string]any)["state_revision"] != float64(2) {
		t.Fatal("noop", status, v)
	}
	status, v = request(t, h, "POST", "/v1/disconnect", `{}`, id, `"client:2"`)
	if status != 200 {
		t.Fatal(status, v)
	}
	status, v = request(t, h, "GET", "/v1/clients/"+id, "", id, "")
	if status != 200 || v["data"].(map[string]any)["status"] != "disconnected" {
		t.Fatal("bootstrap read", status, v)
	}
	status, v = request(t, h, "POST", "/v1/connect", `{"client_id":"`+id+`","actor":{"name":"C"}}`, "", `"client:2"`)
	if status != 409 {
		t.Fatal("stale resume accepted", status, v)
	}
	status, v = request(t, h, "POST", "/v1/connect", `{"client_id":"`+id+`"}`, "", "")
	if status != 200 {
		t.Fatal(status, v)
	}
	status, v = request(t, h, "POST", "/v1/disconnect", `{}`, id, `"client:3"`)
	if status != 409 {
		t.Fatal("delayed disconnect accepted", status, v)
	}
}
func TestSlowBodyDoesNotHoldStateLock(t *testing.T) {
	_, h := fixture(t)
	reader, writer := io.Pipe()
	r, _ := http.NewRequest("POST", h.URL+"/v1/connect", reader)
	r.ContentLength = 100
	done := make(chan struct{})
	go func() {
		resp, _ := http.DefaultClient.Do(r)
		if resp != nil {
			resp.Body.Close()
		}
		close(done)
	}()
	writer.Write([]byte(`{"actor":`))
	client := http.Client{Timeout: time.Second}
	resp, e := client.Get(h.URL + "/v1/meta")
	writer.Close()
	<-done
	if e != nil {
		t.Fatal("slow body blocked independent request", e)
	}
	resp.Body.Close()
}

type blockedWriter struct {
	entered, release chan struct{}
	h                http.Header
}

func (w *blockedWriter) Header() http.Header { return w.h }
func (w *blockedWriter) WriteHeader(int)     {}
func (w *blockedWriter) Write(b []byte) (int, error) {
	close(w.entered)
	<-w.release
	return len(b), nil
}
func TestSlowReaderDoesNotHoldStateLock(t *testing.T) {
	s, _ := fixture(t)
	w := &blockedWriter{make(chan struct{}), make(chan struct{}), make(http.Header)}
	done := make(chan struct{})
	go func() { s.ServeHTTP(w, httptest.NewRequest("GET", "/v1/meta", nil)); close(done) }()
	<-w.entered
	second := make(chan struct{})
	go func() {
		s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/meta", bytes.NewReader(nil)))
		close(second)
	}()
	select {
	case <-second:
	case <-time.After(time.Second):
		close(w.release)
		t.Fatal("slow response held serialization lock")
	}
	close(w.release)
	<-done
}
func TestResponseLimitRejectsBeforeRegistration(t *testing.T) {
	s, h := fixture(t)
	s.Config.Limits.SnapshotBytes = 64
	status, v := request(t, h, "POST", "/v1/connect", `{"actor":{"name":"A","kind":"human"}}`, "", "")
	if status != 413 {
		t.Fatal(status, v)
	}
	if n := countClients(t, s); n != 0 {
		t.Fatal("rejected registration committed", n)
	}
}

// An admitted request finishes its transaction after Stop, while new requests
// are refused. Another connection holds SQLite's write lock, so the admitted
// request waits inside its BEGIN IMMEDIATE until that lock is released.
func TestShutdownDrainsAdmittedTransaction(t *testing.T) {
	s, h := fixture(t)
	held, e := outside(t, s).Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = held.ExecContext(context.Background(), "BEGIN IMMEDIATE"); e != nil {
		t.Fatal(e)
	}
	released := false
	release := func() {
		if !released {
			released = true
			held.ExecContext(context.Background(), "ROLLBACK")
			held.Close()
		}
	}
	defer release()
	done := make(chan int, 1)
	go func() {
		r, e := http.Post(h.URL+"/v1/connect", "application/json", strings.NewReader(`{"actor":{"name":"A","kind":"human"}}`))
		if e != nil {
			done <- 0
			return
		}
		r.Body.Close()
		done <- r.StatusCode
	}()
	waitInStack(t, "storage.(*Writer).Begin")
	s.Stop()
	resp, e := http.Get(h.URL + "/v1/meta")
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatal("admitted request during shutdown", resp.StatusCode)
	}
	release()
	if status := <-done; status != 201 {
		t.Fatal("did not finish admitted transaction", status)
	}
	if n := countClients(t, s); n != 1 {
		t.Fatal("lost drained transaction", n)
	}
}
func TestResponseLimitRejectsBeforeSessionChanges(t *testing.T) {
	for _, operation := range []string{"patch", "disconnect"} {
		t.Run(operation, func(t *testing.T) {
			s, h := fixture(t)
			status, v := request(t, h, "POST", "/v1/connect", `{"actor":{"name":"A","kind":"human"}}`, "", "")
			if status != 201 {
				t.Fatal(status, v)
			}
			id := v["data"].(map[string]any)["client"].(map[string]any)["client_id"].(string)
			s.Config.Limits.SnapshotBytes = 64
			if operation == "patch" {
				status, v = request(t, h, "PATCH", "/v1/clients/"+id, `{"actor":{"name":"B"}}`, id, `"client:1"`)
			} else {
				status, v = request(t, h, "POST", "/v1/disconnect", `{}`, id, `"client:1"`)
			}
			if status != 413 {
				t.Fatal(status, v)
			}
			c, e := s.Client(id)
			if e != nil || c.StateRevision != 1 || c.Status != "connected" || c.Actor.Name != "A" {
				t.Fatal("rejected operation committed", c, e)
			}
		})
	}
}
func TestTransportErrorCodes(t *testing.T) {
	_, h := fixture(t)
	for _, tc := range []struct {
		method, path, body, code string
		status                   int
	}{{"GET", "/v1/clients/anything", "", "client_required", 400}, {"POST", "/v1/connect", `{"actor":`, "invalid_json", 400}, {"POST", "/v1/connect", `{"actor":{"name":"A","kind":"unsupported"}}`, "validation_failed", 422}, {"POST", "/v1/connect", `{"client_id":"00000000-0000-4000-8000-000000000000"}`, "unknown_client", 404}} {
		status, v := request(t, h, tc.method, tc.path, tc.body, "", "")
		if status != tc.status || v["error"].(map[string]any)["code"] != tc.code {
			t.Fatal(status, v)
		}
	}
}
