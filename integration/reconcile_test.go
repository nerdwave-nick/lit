package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func statusCLI(t *testing.T, cwd, state string, want int, args ...string) map[string]any {
	t.Helper()
	args = append([]string{"transactions", "status", "--format", "json"}, args...)
	cmd := exec.Command(cliBin, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0", "LIT_SESSION=default", "LIT_STATE_DIR="+state)
	var out, errs bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errs
	e := cmd.Run()
	code := 0
	if x, ok := e.(*exec.ExitError); ok {
		code = x.ExitCode()
	} else if e != nil {
		t.Fatal(e)
	}
	if code != want || errs.Len() != 0 {
		t.Fatalf("status code=%d want=%d out=%s err=%s", code, want, out.String(), errs.String())
	}
	var v map[string]any
	if json.Unmarshal(out.Bytes(), &v) != nil || v["items"] == nil {
		t.Fatal(out.String())
	}
	if _, ok := v["next_cursor"]; !ok {
		t.Fatal("missing next_cursor", v)
	}
	return v
}
func TestLostResponseAutomaticReadOnlyReconciliation(t *testing.T) {
	data, state, cwd := t.TempDir(), t.TempDir(), t.TempDir()
	s := start(t, data)
	var mutations atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, _ := http.NewRequest(r.Method, s.endpoint+r.URL.RequestURI(), r.Body)
		req.Header = r.Header.Clone()
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Error(e)
			return
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		if r.URL.Path == "/v1/transactions" {
			mutations.Add(1)
			conn, _, e := w.(http.Hijacker).Hijack()
			if e == nil {
				conn.Close()
			}
			return
		}
		for k, v := range res.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(res.StatusCode)
		w.Write(body)
	}))
	defer proxy.Close()
	run(t, cwd, state, 0, "connect", "--endpoint", proxy.URL, "--format", "json")
	result := run(t, cwd, state, 0, "projects", "create", "--project-title", "feat/auto", "--content", "original")
	if result["outcome"] != "applied" || mutations.Load() != 1 {
		t.Fatal(result, mutations.Load())
	}
	pending, _ := filepath.Glob(filepath.Join(state, "pending", "*.json"))
	if len(pending) != 0 {
		t.Fatal(pending)
	}
	// A lost no-op response validates the original revisions and remains no-op.
	result = run(t, cwd, state, 0, "projects", "update", "feat/auto", "--content", "original")
	if result["outcome"] != "already_satisfied" || mutations.Load() != 2 {
		t.Fatal(result, mutations.Load())
	}
	if n := len(run(t, cwd, state, 0, "projects", "history", "feat/auto")["items"].([]any)); n != 1 {
		t.Fatal("replayed mutation/history", n)
	}
}
func TestKilledCLIFullMutationReconciliation(t *testing.T) {
	for _, kind := range []string{"project-create", "project-update", "issue-create", "issue-parent", "issue-close", "issue-reopen", "comment-create", "comment-update", "link", "unlink", "noop", "acquire", "renew", "release", "release-noop", "disconnect", "session", "resume"} {
		t.Run(kind, func(t *testing.T) {
			data, state, cwd := t.TempDir(), t.TempDir(), t.TempDir()
			s := start(t, data)
			run(t, cwd, state, 0, "connect", "--endpoint", s.endpoint, "--format", "json")
			run(t, cwd, state, 0, "projects", "create", "--project-title", "feat/recovery")
			run(t, cwd, state, 0, "session", "set", "--project", "feat/recovery")
			run(t, cwd, state, 0, "issues", "create", "--issue", "Parent", "--issue", "Child")
			run(t, cwd, state, 0, "comments", "create", "--issue", "Child", "--content", "before")
			child := item(run(t, cwd, state, 0, "issues", "get", "Child"))["id"].(string)
			comment := item(run(t, cwd, state, 0, "comments", "list", "--issue", "Child"))["id"].(string)
			args := []string{}
			path := "/v1/transactions"
			want := "committed"
			switch kind {
			case "project-create":
				args = []string{"projects", "create", "--project-title", "feat/another", "--project-title", "feat/third"}
			case "project-update":
				args = []string{"projects", "update", "feat/recovery", "--content", "after"}
			case "issue-create":
				args = []string{"issues", "create", "--issue", "New", "--parent", "Parent"}
			case "issue-parent":
				args = []string{"issues", "update", "Child", "--parent", "Parent"}
			case "issue-close":
				run(t, cwd, state, 0, "claims", "acquire", "Child")
				args = []string{"issues", "close", "Child"}
			case "issue-reopen":
				run(t, cwd, state, 0, "issues", "close", "Child")
				args = []string{"issues", "reopen", "Child"}
			case "comment-create":
				args = []string{"comments", "create", "--issue", "Child", "--content", "append once"}
			case "comment-update":
				args = []string{"comments", "update", comment, "--content", "after"}
			case "link":
				args = []string{"issues", "link", "--from", "Parent", "--to", "Child", "--relation", "blocks"}
			case "unlink":
				run(t, cwd, state, 0, "issues", "link", "--from", "Parent", "--to", "Child", "--relation", "blocks")
				args = []string{"issues", "unlink", "--from", "Parent", "--to", "Child", "--relation", "blocks"}
			case "noop":
				args = []string{"issues", "update", "Child", "--title", "Child"}
				want = "already_satisfied"
			case "acquire":
				args = []string{"claims", "acquire", "Child"}
				path = "/v1/operations"
				want = "uncertain"
			case "renew":
				run(t, cwd, state, 0, "claims", "acquire", "Child")
				args = []string{"claims", "renew", "Child", "--for", "50m"}
				path = "/v1/operations"
				want = "already_satisfied"
			case "release":
				run(t, cwd, state, 0, "claims", "acquire", "Child")
				args = []string{"claims", "release", "Child"}
				path = "/v1/operations"
				want = "uncertain"
			case "release-noop":
				args = []string{"claims", "release", "Child"}
				path = "/v1/operations"
				want = "already_satisfied"
			case "disconnect":
				run(t, cwd, state, 0, "claims", "acquire", "Child")
				args = []string{"disconnect"}
				path = "/v1/disconnect"
				want = "already_satisfied"
			case "session":
				args = []string{"session", "set", "--actor-name", "changed"}
				path = "/v1/clients/" + item(run(t, cwd, state, 0, "session", "get"))["client_id"].(string)
				want = "already_satisfied"
			case "resume":
				run(t, cwd, state, 0, "disconnect")
				args = []string{"connect"}
				path = "/v1/connect"
				want = "already_satisfied"
			}
			type intercepted struct {
				body     []byte
				headers  http.Header
				response []byte
			}
			sent := make(chan intercepted, 1)
			committed := make(chan struct{}, 1)
			release := make(chan struct{})
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestBody, _ := io.ReadAll(r.Body)
				req, _ := http.NewRequest(r.Method, s.endpoint+r.URL.RequestURI(), bytes.NewReader(requestBody))
				req.Header = r.Header.Clone()
				res, e := http.DefaultClient.Do(req)
				if e != nil {
					t.Error(e)
					return
				}
				defer res.Body.Close()
				body, _ := io.ReadAll(res.Body)
				if r.Method != "GET" && r.URL.Path == path {
					if res.StatusCode >= 300 {
						t.Errorf("mutation rejected %d %s", res.StatusCode, body)
					}
					sent <- intercepted{requestBody, r.Header.Clone(), body}
					committed <- struct{}{}
					<-release
					return
				}
				for k, v := range res.Header {
					w.Header()[k] = v
				}
				w.WriteHeader(res.StatusCode)
				w.Write(body)
			}))
			defer proxy.Close()
			defer close(release)
			args = append(args, "--endpoint", proxy.URL)
			cmd := exec.Command(cliBin, args...)
			cmd.Dir = cwd
			cmd.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0", "LIT_SESSION=default", "LIT_STATE_DIR="+state)
			var out, errs bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &errs
			if e := cmd.Start(); e != nil {
				t.Fatal(e)
			}
			select {
			case <-committed:
			case <-time.After(5 * time.Second):
				cmd.Process.Kill()
				cmd.Wait()
				t.Fatalf("mutation never reached commit: %s %s", out.String(), errs.String())
			}
			cmd.Process.Kill()
			cmd.Wait()
			pending, _ := filepath.Glob(filepath.Join(state, "pending", "*.json"))
			if len(pending) != 1 {
				t.Fatal(pending)
			}
			original, _ := os.ReadFile(pending[0])
			captured := <-sent
			var retained litclient.Pending
			if e := json.Unmarshal(original, &retained); e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(retained.Body, captured.body) {
				t.Fatal("pending body differs from actual transmitted request")
			}
			for key, value := range retained.Headers {
				if captured.headers.Get(key) != value {
					t.Fatalf("pending header %s differs", key)
				}
			}
			if retained.Path != path || retained.RequestID == "" {
				t.Fatal(retained)
			}
			// Restart the real process before examining history/operational authority.
			s.cmd.Process.Kill()
			s.cmd.Wait()
			s = start(t, data)
			code := 0
			if want == "uncertain" {
				code = 4
			}
			v := statusCLI(t, cwd, state, code, "--endpoint", s.endpoint)
			if len(v["items"].([]any)) != 1 || item(v)["status"] != want {
				t.Fatal(v)
			}
			if want == "uncertain" {
				after, _ := os.ReadFile(pending[0])
				if !bytes.Equal(original, after) {
					t.Fatal("pending request changed")
				}
			} else if _, e := os.Stat(pending[0]); !os.IsNotExist(e) {
				t.Fatal("resolved pending remains", e)
			}
			if kind == "renew" {
				var q protocol.ClaimOperation
				json.Unmarshal(retained.Body, &q)
				var acknowledged struct {
					Data protocol.ClaimResult `json:"data"`
				}
				json.Unmarshal(captured.response, &acknowledged)
				live := item(run(t, cwd, state, 0, "claims", "get", child, "--endpoint", s.endpoint))["claim"].(map[string]any)
				if live["token"] != q.Items[0].Token || live["expires_at"] != q.Items[0].ExtendTo || live["expires_at"] != acknowledged.Data.Items[0].Claim.ExpiresAt {
					t.Fatal("renewal changed across status/restart", live, q)
				}
			}
			if kind == "disconnect" {
				run(t, cwd, state, 0, "connect", "--endpoint", s.endpoint)
				claims := run(t, cwd, state, 0, "claims", "get", child)
				if item(claims)["claim"] != nil {
					t.Fatal(claims)
				}
			}
		})
	}
}

func TestInterruptedFirstConnectRemainsUnidentifiable(t *testing.T) {
	data, state, cwd := t.TempDir(), t.TempDir(), t.TempDir()
	s := start(t, data)
	committed := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, _ := http.NewRequest(r.Method, s.endpoint+r.URL.RequestURI(), r.Body)
		req.Header = r.Header.Clone()
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Error(e)
			return
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		if r.URL.Path == "/v1/connect" {
			calls.Add(1)
			if res.StatusCode != 201 {
				t.Errorf("connect: %d %s", res.StatusCode, body)
			}
			committed <- struct{}{}
			<-release
			return
		}
		w.WriteHeader(res.StatusCode)
		w.Write(body)
	}))
	defer proxy.Close()
	defer close(release)
	cmd := exec.Command(cliBin, "connect", "--endpoint", proxy.URL, "--format", "json", "--actor-name", "unidentified")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0", "LIT_SESSION=default", "LIT_STATE_DIR="+state)
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	select {
	case <-committed:
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal("connect did not commit")
	}
	cmd.Process.Kill()
	cmd.Wait()
	files, _ := filepath.Glob(filepath.Join(state, "pending", "*.json"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	before, _ := os.ReadFile(files[0])
	var p litclient.Pending
	json.Unmarshal(before, &p)
	if p.ClientID != "" {
		t.Fatal("invented client ID", p)
	}
	for attempt := 0; attempt < 2; attempt++ {
		v := statusCLI(t, cwd, state, 4, "--endpoint", s.endpoint)
		if item(v)["status"] != "uncertain" || item(v)["proof"] != "registration_identity_not_received" {
			t.Fatal(v)
		}
	}
	after, _ := os.ReadFile(files[0])
	if !bytes.Equal(before, after) {
		t.Fatal("lost first registration evidence")
	}
	mappings, _ := filepath.Glob(filepath.Join(state, "sessions", "*.json"))
	clients := clientCount(t, data)
	if len(mappings) != 0 || clients != 1 || calls.Load() != 1 {
		t.Fatal("replayed or invented registration", mappings, clients, calls.Load())
	}
}

func TestTransactionStatusExplicitSessionAndEndpointBinding(t *testing.T) {
	data, state, cwd := t.TempDir(), t.TempDir(), t.TempDir()
	s := start(t, data)
	a := item(run(t, cwd, state, 0, "connect", "--session", "a", "--endpoint", s.endpoint, "--format", "json"))
	b := item(run(t, cwd, state, 0, "connect", "--session", "b", "--endpoint", s.endpoint, "--format", "json"))
	write := func(client map[string]any) string {
		p := litclient.Pending{SchemaVersion: 1, RequestID: protocol.UUID(), ServiceID: client["service_id"].(string), ClientID: client["client_id"].(string), Endpoint: s.endpoint, Method: "POST", Path: "/v1/disconnect", Body: json.RawMessage(`{}`), Headers: map[string]string{"If-Match": "\"client:1\""}, CreatedAt: protocol.Now()}
		path := filepath.Join(state, "pending", p.RequestID+".json")
		if e := litclient.WriteJSON(path, p); e != nil {
			t.Fatal(e)
		}
		return path
	}
	first, second := write(a), write(b)
	v := statusCLI(t, cwd, state, 0, "--session", "a")
	if len(v["items"].([]any)) != 1 || item(v)["status"] != "uncommitted" {
		t.Fatal(v)
	}
	if _, e := os.Stat(first); !os.IsNotExist(e) {
		t.Fatal("a unresolved", e)
	}
	if _, e := os.Stat(second); e != nil {
		t.Fatal("other session evidence deleted", e)
	}
	run(t, cwd, state, 1, "transactions", "status", "--session", "a", "--file", second, "--format", "json")
	other := start(t, t.TempDir())
	run(t, cwd, state, 1, "transactions", "status", "--endpoint", other.endpoint, "--format", "json")
	if _, e := os.Stat(second); e != nil {
		t.Fatal("retargeted evidence", e)
	}
}

func TestRetainedAcquireAcceptsActualServiceCap(t *testing.T) {
	data, state, cwd := t.TempDir(), t.TempDir(), t.TempDir()
	s := start(t, data)
	client := item(run(t, cwd, state, 0, "connect", "--endpoint", s.endpoint, "--format", "json"))
	run(t, cwd, state, 0, "projects", "create", "--project-title", "feat/cap")
	issue := item(run(t, cwd, state, 0, "issues", "create", "--project", "feat/cap", "--issue", "Capped"))["id"].(string)
	// The public argument layer rejects >1h durations. Exercise its retained-call
	// validator against the direct HTTP contract's legitimate acquisition cap.
	cid, sid := client["client_id"].(string), client["service_id"].(string)
	a := &litclient.Session{StateDir: state, Endpoint: s.endpoint, Mapping: litclient.Mapping{ClientID: cid, ServiceID: sid}, Meta: protocol.Meta{ServiceID: sid}, HTTP: http.DefaultClient, Context: context.Background()}
	op := protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: cid, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: issue, ExtendTo: time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339Nano)}}}
	var result protocol.ClaimResult
	if e := a.Call("POST", "/v1/operations", op, nil, &result, true); e != nil {
		t.Fatal(e)
	}
	claim := result.Items[0].Claim
	start, _ := time.Parse(time.RFC3339Nano, claim.AcquiredAt)
	expiry, _ := time.Parse(time.RFC3339Nano, claim.ExpiresAt)
	if !expiry.Equal(start.Add(time.Hour)) {
		t.Fatal("service grant not capped", claim)
	}
	files, _ := filepath.Glob(filepath.Join(state, "pending", "*.json"))
	if len(files) != 0 {
		t.Fatal("legitimate capped receipt rejected", files)
	}
}
