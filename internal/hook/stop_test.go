package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/workflowsession"
)

// stopReply is a Stop hook run's observable result. A block decision fills
// Decision and Reason; an allowed stop prints nothing.
type stopReply struct {
	Code             int
	Stdout, Stderr   string
	Decision, Reason string
}

func (r stopReply) blocked() bool { return r.Decision == "block" }

// stopInput returns the recorded Stop input of a host and its session_id.
func stopInput(t *testing.T, host string, active bool) ([]byte, string) {
	t.Helper()
	name := "stop.json"
	if active {
		name = "stop-active.json"
	}
	b, e := os.ReadFile(filepath.Join("testdata", host, name))
	if e != nil {
		t.Fatal(e)
	}
	var id struct {
		SessionID string `json:"session_id"`
	}
	if e = json.Unmarshal(b, &id); e != nil || id.SessionID == "" {
		t.Fatalf("%s: %v", name, e)
	}
	return b, id.SessionID
}

// stop runs `lit hook --host HOST stop` with in on stdin.
func (g *rig) stop(host string, in []byte, opt Options) stopReply {
	g.t.Helper()
	opt.Binary = g.binary
	var out, errOut bytes.Buffer
	code := Run(host, "stop", bytes.NewReader(in), &out, &errOut, opt)
	return decodeStop(g.t, code, out.String(), errOut.String())
}

func decodeStop(t *testing.T, code int, stdout, stderr string) stopReply {
	t.Helper()
	r := stopReply{Code: code, Stdout: stdout, Stderr: stderr}
	if stdout == "" {
		return r
	}
	var wire map[string]json.RawMessage
	if e := json.Unmarshal([]byte(stdout), &wire); e != nil {
		t.Fatalf("stdout is not one JSON object: %q: %v", stdout, e)
	}
	if len(wire) != 2 || wire["decision"] == nil || wire["reason"] == nil {
		t.Fatalf("a block decision carries exactly decision and reason: %s", stdout)
	}
	r.Decision, r.Reason = jsonString(wire["decision"]), jsonString(wire["reason"])
	return r
}

func wantAllowed(t *testing.T, r stopReply) {
	t.Helper()
	if r.Code != 0 || r.Stdout != "" || r.Stderr != "" {
		t.Fatalf("the stop must be allowed silently: %d %q %q", r.Code, r.Stdout, r.Stderr)
	}
}

// requests counts the requests whose method and path match.
func requests(n *atomic.Int32, method, path string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == method && r.URL.Path == path {
				n.Add(1)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// holding sets up the lit session of host's recorded Stop session and makes it
// claim the named issues. It returns the runtime ID and the claims.
func (g *rig) holding(host string, issues ...string) (string, []map[string]json.RawMessage) {
	g.t.Helper()
	start, id := input(g.t, host, "startup")
	g.hook(host, "session-start", start, Options{})
	g.run(host, id, "projects", "create", "--project-title", "feat/stopped", "--content", "x")
	project := jsonString(g.tracker(host, id, "projects", "list")[0]["id"])
	g.run(host, id, "session", "set", "--project", project)
	for _, title := range issues {
		g.run(host, id, "issues", "create", "--issue", title, "--content", "x")
		g.run(host, id, "claims", "acquire", title)
	}
	return id, g.tracker(host, id, "claims", "list")
}

func TestStopSessionMatchesTheRecordedStartupSession(t *testing.T) {
	for _, host := range hosts {
		_, startup := input(t, host, "startup")
		for _, active := range []bool{false, true} {
			if _, id := stopInput(t, host, active); id != startup {
				t.Errorf("%s: the Stop fixture is of another session than the startup fixture: %s != %s", host, id, startup)
			}
		}
	}
}

func TestStopBlocksOnceNamingTheHeldClaims(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			id, claims := g.holding(host, "first held work", "second held work")
			if len(claims) != 2 {
				t.Fatalf("setup: %d claims", len(claims))
			}
			in, same := stopInput(t, host, false)
			if same != id {
				t.Fatalf("setup: %s != %s", same, id)
			}
			r := g.stop(host, in, Options{})
			if r.Code != 0 || r.Stderr != "" || !r.blocked() {
				t.Fatalf("a session holding claims must be blocked once: %+v", r)
			}
			for _, claim := range claims {
				for _, want := range []string{jsonString(claim["issue_id"]), "lease expires " + jsonString(claim["expires_at"])} {
					if !strings.Contains(r.Reason, want) {
						t.Errorf("the reason does not name %q:\n%s", want, r.Reason)
					}
				}
				if strings.Contains(r.Reason, jsonString(claim["token"])) {
					t.Errorf("the reason leaks a claim token:\n%s", r.Reason)
				}
			}
			run := "lit workflow-session --host " + host + " --runtime-id " + id + " run -- "
			for _, want := range []string{"checkpoint", "handoff", "release", "continue", run + "comments create --issue", run + "claims release"} {
				if !strings.Contains(r.Reason, want) {
					t.Errorf("the reason lacks %q:\n%s", want, r.Reason)
				}
			}
			noFailures(t)
		})
	}
}

func TestStopOutputMatchesTheCodexSchema(t *testing.T) {
	schema := loadSchema(t, "stop.output.schema.json")
	g := newRig(t, nil)
	g.holding("codex", "held work")
	in, _ := stopInput(t, "codex", false)
	r := g.stop("codex", in, Options{})
	var out map[string]any
	if e := json.Unmarshal([]byte(r.Stdout), &out); e != nil || !r.blocked() {
		t.Fatalf("%+v %v", r, e)
	}
	if problems := check(schema, schema, out, "output"); len(problems) != 0 {
		t.Fatalf("the schema rejects %s: %v", r.Stdout, problems)
	}
}

func TestStopNeverChangesWhatTheSessionHolds(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			id, before := g.holding(host, "kept work")
			in, _ := stopInput(t, host, false)
			if r := g.stop(host, in, Options{}); !r.blocked() {
				t.Fatalf("%+v", r)
			}
			after := g.tracker(host, id, "claims", "list")
			if len(after) != 1 || string(after[0]["token"]) != string(before[0]["token"]) || string(after[0]["expires_at"]) != string(before[0]["expires_at"]) {
				t.Fatalf("the guard changed the claim: %s != %s", after[0], before[0])
			}
			issue := g.tracker(host, id, "issues", "get", jsonString(before[0]["issue_id"]))[0]
			if jsonString(issue["state"]) != "open" {
				t.Fatalf("the guard closed the issue: %s", issue["state"])
			}
		})
	}
}

func TestStopAllowsWhenAStopHookAlreadyContinuedTheTurn(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			var listed atomic.Int32
			g := newRig(t, requests(&listed, http.MethodGet, "/v1/claims"))
			g.holding(host, "held work")
			listed.Store(0)
			in, _ := stopInput(t, host, true)
			wantAllowed(t, g.stop(host, in, Options{}))
			if listed.Load() != 0 {
				t.Fatal("an active stop hook needs no tracker call")
			}
			noFailures(t)
		})
	}
}

func TestStopAllowsWithoutClaims(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			in, _ := stopInput(t, host, false)
			start, _ := input(t, host, "startup")
			g.hook(host, "session-start", start, Options{})
			wantAllowed(t, g.stop(host, in, Options{}))

			id, claims := g.holding(host, "released work")
			if r := g.stop(host, in, Options{}); !r.blocked() {
				t.Fatalf("setup: %+v", r)
			}
			g.run(host, id, "claims", "release", jsonString(claims[0]["issue_id"]))
			wantAllowed(t, g.stop(host, in, Options{}))
			noFailures(t)
		})
	}
}

func TestStopAllowsAnExpiredClaim(t *testing.T) {
	g := newRig(t, nil)
	start, id := input(t, "claude", "startup")
	g.hook("claude", "session-start", start, Options{})
	g.run("claude", id, "projects", "create", "--project-title", "feat/stopped", "--content", "x")
	g.run("claude", id, "session", "set", "--project", jsonString(g.tracker("claude", id, "projects", "list")[0]["id"]))
	g.run("claude", id, "issues", "create", "--issue", "short lease", "--content", "x")
	g.run("claude", id, "claims", "acquire", "short lease", "--for", "1s")
	in, _ := stopInput(t, "claude", false)
	deadline := time.Now().Add(10 * time.Second)
	for len(g.tracker("claude", id, "claims", "list")) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the lease did not expire")
		}
		time.Sleep(100 * time.Millisecond)
	}
	wantAllowed(t, g.stop("claude", in, Options{}))
	noFailures(t)
}

func TestStopAllowsARuntimeWithoutALitSession(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			var requested atomic.Int32
			g := newRig(t, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requested.Add(1)
					next.ServeHTTP(w, r)
				})
			})
			in, id := stopInput(t, host, false)
			wantAllowed(t, g.stop(host, in, Options{}))
			if requested.Load() != 0 {
				t.Fatal("a runtime without a session needs no tracker call")
			}
			if _, e := g.association(host, id); !os.IsNotExist(e) {
				t.Fatalf("the guard created an association: %v", e)
			}
			noFailures(t)
		})
	}
}

func TestStopAllowsWhenTheServerDoesNotAnswer(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			g.holding(host, "held work")
			g.server.Close()
			in, _ := stopInput(t, host, false)
			start := time.Now()
			wantAllowed(t, g.stop(host, in, Options{}))
			if time.Since(start) > 2*time.Second {
				t.Fatalf("took %s", time.Since(start))
			}
			noFailures(t) // a server that is down is not a hook failure
		})
	}
}

func TestStopAllowsAPendingAssociation(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			in, id := stopInput(t, host, false)
			record, _, _ := workflowsession.Adapter{Host: host, RuntimeID: id}.Paths()
			pending, _ := json.Marshal(workflowsession.Association{Version: 1, Host: host, Runtime: id, Session: "agent-pending", Endpoint: g.endpoint, Status: "pending"})
			if e := os.MkdirAll(filepath.Dir(record), 0o700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(record, pending, 0o600); e != nil {
				t.Fatal(e)
			}
			wantAllowed(t, g.stop(host, in, Options{}))
			noFailures(t)
		})
	}
}

// failures lists the logged hook failures.
func failures(t *testing.T) []Failure {
	t.Helper()
	list, e := RecentFailures(time.Now().Add(-time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	return list
}

func TestStopAllowsAndLogsInternalErrors(t *testing.T) {
	for _, host := range hosts {
		in, _ := stopInput(t, host, false)
		withoutField := func(field string) string {
			var fields map[string]json.RawMessage
			json.Unmarshal(in, &fields)
			delete(fields, field)
			b, _ := json.Marshal(fields)
			return string(b)
		}
		for name, bad := range map[string]string{
			"empty":                       "",
			"not json":                    "stop",
			"array":                       "[]",
			"no session":                  withoutField("session_id"),
			"no stop_hook_active":         withoutField("stop_hook_active"),
			"stop_hook_active not a bool": strings.Replace(string(in), `"stop_hook_active": false`, `"stop_hook_active": "false"`, 1),
			"another event":               strings.Replace(string(in), `"Stop"`, `"SessionStart"`, 1),
		} {
			t.Run(host+"/"+name, func(t *testing.T) {
				g := newRig(t, nil)
				g.holding(host, "held work")
				wantAllowed(t, g.stop(host, []byte(bad), Options{}))
				if got := failures(t); len(got) != 1 || got[0].Event != "stop" || got[0].Host != host {
					t.Fatalf("an input the guard cannot trust is a logged failure: %+v", got)
				}
			})
		}
	}
}

func TestStopAllowsAndLogsAnUnreadableAssociation(t *testing.T) {
	g := newRig(t, nil)
	g.holding("claude", "held work")
	in, id := stopInput(t, "claude", false)
	record, _, _ := workflowsession.Adapter{Host: "claude", RuntimeID: id}.Paths()
	if e := os.WriteFile(record, []byte("{broken"), 0o600); e != nil {
		t.Fatal(e)
	}
	wantAllowed(t, g.stop("claude", in, Options{}))
	if got := failures(t); len(got) != 1 || got[0].RuntimeID != id {
		t.Fatalf("%+v", got)
	}
	if raw, _ := os.ReadFile(record); string(raw) != "{broken" {
		t.Fatal("the guard rewrote evidence")
	}
}

func TestStopAllowsAndLogsAServerThatFailsTheClaimsRead(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			var broken atomic.Bool
			g := newRig(t, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if broken.Load() && r.Method == http.MethodGet && r.URL.Path == "/v1/claims" {
						http.Error(w, "boom", http.StatusInternalServerError)
						return
					}
					next.ServeHTTP(w, r)
				})
			})
			g.holding(host, "held work")
			broken.Store(true)
			in, _ := stopInput(t, host, false)
			wantAllowed(t, g.stop(host, in, Options{}))
			got := failures(t)
			if len(got) != 1 || !strings.Contains(got[0].Message, "claims") {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestStopAllowsWhenTheClaimsReadOutlastsTheBudget(t *testing.T) {
	var stall atomic.Bool
	release := make(chan struct{})
	g := newRig(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if stall.Load() && r.Method == http.MethodGet && r.URL.Path == "/v1/claims" {
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	})
	t.Cleanup(func() { close(release) })
	g.holding("claude", "held work")
	stall.Store(true)
	in, _ := stopInput(t, "claude", false)
	start := time.Now()
	wantAllowed(t, g.stop("claude", in, Options{Budget: 500 * time.Millisecond}))
	if time.Since(start) > 3*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
	if got := failures(t); len(got) != 1 {
		t.Fatalf("a read that outlasts the budget is a logged failure: %+v", got)
	}
}

func TestStopHasABudgetOfAboutOneSecond(t *testing.T) {
	for _, e := range Events() {
		if e.Name == "stop" {
			if e.Budget != time.Second || e.HostName != "Stop" {
				t.Fatalf("%+v", e)
			}
			return
		}
	}
	t.Fatal("no stop event")
}

func TestLitHookStopEndToEnd(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			g.holding(host, "held work")
			in, _ := stopInput(t, host, false)
			blocked := litStop(t, g, in, host)
			if blocked.Code != 0 || !blocked.blocked() || !strings.Contains(blocked.Reason, "lease expires") {
				t.Fatalf("%+v", blocked)
			}
			active, _ := stopInput(t, host, true)
			if r := litStop(t, g, active, host); r.Code != 0 || r.Stdout != "" {
				t.Fatalf("%+v", r)
			}
		})
	}
}

// litStop runs the built lit executable as a host would.
func litStop(t *testing.T, g *rig, stdin []byte, host string) stopReply {
	t.Helper()
	code, out, errOut := execLit(t, g, stdin, "hook", "--host", host, "stop")
	return decodeStop(t, code, out, errOut)
}

// execLit runs the built lit executable in an empty directory.
func execLit(t *testing.T, g *rig, stdin []byte, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(g.binary, args...)
	cmd.Dir = t.TempDir()
	cmd.Stdin = bytes.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if e := cmd.Run(); e != nil {
		var exit *exec.ExitError
		if !errors.As(e, &exit) {
			t.Fatal(e)
		}
		code = exit.ExitCode()
	}
	return code, out.String(), errOut.String()
}
