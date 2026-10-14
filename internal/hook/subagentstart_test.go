package hook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/workflowsession"
)

// Subagent tests set up the root conversation's lit session first, as its
// SessionStart hook does, and then feed the recorded SubagentStart input.

// subagentOptions are generous: a loaded machine must not look like a stuck
// lock or a slow server. The budget test uses the real budget.
func subagentOptions() Options {
	return Options{Budget: 20 * time.Second, ProbeTimeout: 10 * time.Second, BusyWait: 10 * time.Second}
}

// subagentInput returns the recorded SubagentStart input of a host with the
// root conversation's session_id and the subagent's agent_id.
func subagentInput(t *testing.T, host string) (raw []byte, root, agent string) {
	t.Helper()
	for _, f := range loadFixtures(t).Fixtures {
		if f.Host != host || f.Event != "subagent-start" {
			continue
		}
		raw, e := os.ReadFile(filepath.Join("testdata", f.File))
		if e != nil {
			t.Fatal(e)
		}
		var in struct {
			SessionID string `json:"session_id"`
			AgentID   string `json:"agent_id"`
		}
		if e = json.Unmarshal(raw, &in); e != nil || in.SessionID == "" || in.AgentID == "" || in.SessionID == in.AgentID {
			t.Fatalf("%s: %+v %v", f.File, in, e)
		}
		return raw, in.SessionID, in.AgentID
	}
	t.Fatalf("no %s subagent-start fixture", host)
	return nil, "", ""
}

// startRoot sets up the root conversation's lit session.
func (g *rig) startRoot(host, root string) workflowsession.Association {
	g.t.Helper()
	a := workflowsession.Adapter{Host: host, RuntimeID: root, Binary: g.binary, WorkDir: g.t.TempDir()}
	r, e := a.Fresh(context.Background(), workflowsession.FreshOptions{Endpoint: g.endpoint})
	if e != nil {
		g.t.Fatal(e)
	}
	return r
}

// selectProject creates a project and selects it for the runtime's session.
func (g *rig) selectProject(host, runtime, title string) string {
	g.t.Helper()
	g.run(host, runtime, "projects", "create", "--project-title", title, "--content", "x")
	for _, p := range g.tracker(host, runtime, "projects", "list") {
		if jsonString(p["title"]) == title {
			id := jsonString(p["id"])
			g.run(host, runtime, "session", "set", "--project", id)
			return id
		}
	}
	g.t.Fatalf("project %s was not created", title)
	return ""
}

// sessionOf is the tracker's own view of a runtime's session.
func (g *rig) sessionOf(host, runtime string) map[string]json.RawMessage {
	g.t.Helper()
	return g.tracker(host, runtime, "session", "get")[0]
}

func wantSubagentContext(t *testing.T, r reply, wants ...string) {
	t.Helper()
	if r.Code != 0 || r.Stderr != "" {
		t.Fatalf("a hook must exit 0 quietly: %d %q", r.Code, r.Stderr)
	}
	if r.Event != "SubagentStart" || r.Context == "" {
		t.Fatalf("no SubagentStart context: %q", r.Stdout)
	}
	for _, want := range wants {
		if !strings.Contains(r.Context, want) {
			t.Errorf("context lacks %q:\n%s", want, r.Context)
		}
	}
}

func wantSilence(t *testing.T, r reply) {
	t.Helper()
	if r.Code != 0 || r.Stdout != "" || r.Stderr != "" {
		t.Fatalf("the hook must exit 0 and print nothing: %+v", r)
	}
}

func TestSubagentStartGivesTheSubagentItsOwnSession(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			in, root, agent := subagentInput(t, host)
			parent := g.startRoot(host, root)
			g.selectProject(host, root, "feat/parent")
			g.run(host, root, "issues", "create", "--issue", "parent work", "--content", "x")
			g.run(host, root, "claims", "acquire", "parent work")

			r := g.hook(host, "subagent-start", in, subagentOptions())

			child, e := g.association(host, agent)
			if e != nil || child.Status != "ready" || child.Client == nil {
				t.Fatalf("association: %+v %v", child, e)
			}
			if child.Parent == nil || *child.Parent != root || child.Runtime != agent || child.Endpoint != parent.Endpoint {
				t.Fatalf("the child is not bound to its own runtime under the parent: %+v", child)
			}
			if child.Session == parent.Session || *child.Client == *parent.Client {
				t.Fatalf("the child shares the parent's session: %+v / %+v", child, parent)
			}
			wantSubagentContext(t, r, "session: "+child.Session, "endpoint: "+g.endpoint, "runtime ID: "+agent,
				"claims: none", "lit workflow-session --host "+host+" --runtime-id "+agent+" run -- ")
			for _, forbidden := range []string{root, parent.Session} {
				if strings.Contains(r.Context, forbidden) {
					t.Errorf("the subagent's context names the parent's %q:\n%s", forbidden, r.Context)
				}
			}

			me := g.sessionOf(host, agent)
			var runtime map[string]string
			json.Unmarshal(me["runtime"], &runtime)
			if jsonString(me["client_id"]) != *child.Client || runtime["vendor"] != host || runtime["session_id"] != agent {
				t.Fatalf("the tracker session is not the subagent's: %s %v", me["client_id"], runtime)
			}
			if claims := g.tracker(host, agent, "claims", "list"); len(claims) != 0 {
				t.Fatalf("a child session starts without claims: %v", claims)
			}
			if claims := g.tracker(host, root, "claims", "list"); len(claims) != 1 {
				t.Fatalf("the parent keeps its claim: %v", claims)
			}
			if g.connects.Load() != 2 {
				t.Fatalf("registered %d clients, want the parent's and the child's", g.connects.Load())
			}
			noFailures(t)
		})
	}
}

func TestSubagentStartPropagatesTheParentsProjectOnlyExplicitly(t *testing.T) {
	for _, host := range hosts {
		t.Run(host+"/selected", func(t *testing.T) {
			g := newRig(t, nil)
			in, root, agent := subagentInput(t, host)
			g.startRoot(host, root)
			project := g.selectProject(host, root, "feat/parent")

			r := g.hook(host, "subagent-start", in, subagentOptions())

			child, _ := g.association(host, agent)
			if child.Project == nil || *child.Project != project || jsonString(g.sessionOf(host, agent)["project_id"]) != project {
				t.Fatalf("the parent's project was not propagated: %+v", child)
			}
			wantSubagentContext(t, r, "project: "+project)

			// Later changes of the parent's project do not move the child.
			g.selectProject(host, root, "feat/elsewhere")
			again := g.hook(host, "subagent-start", in, subagentOptions())
			if again.Stdout != r.Stdout || jsonString(g.sessionOf(host, agent)["project_id"]) != project {
				t.Fatalf("the child followed the parent: %q", again.Stdout)
			}
			noFailures(t)
		})
		t.Run(host+"/none", func(t *testing.T) {
			g := newRig(t, nil)
			in, root, agent := subagentInput(t, host)
			g.startRoot(host, root)
			g.run(host, root, "projects", "create", "--project-title", "feat/unselected", "--content", "x")

			r := g.hook(host, "subagent-start", in, subagentOptions())

			child, _ := g.association(host, agent)
			if child.Project != nil || jsonString(g.sessionOf(host, agent)["project_id"]) != "" {
				t.Fatalf("a child must not pick a project the parent did not select: %+v", child)
			}
			wantSubagentContext(t, r, "project: none selected")
			noFailures(t)
		})
	}
}

func TestSubagentStartIsIdempotentPerAgent(t *testing.T) {
	for _, host := range hosts {
		t.Run(host+"/repeated", func(t *testing.T) {
			g := newRig(t, nil)
			in, root, agent := subagentInput(t, host)
			g.startRoot(host, root)
			first := g.hook(host, "subagent-start", in, subagentOptions())
			before, _ := g.association(host, agent)

			// Claude runs the hook again when a subagent is resumed, and for an
			// in-process teammate with every message. The hook injects only text it
			// has not injected before, so a repeat must print the same bytes.
			second := g.hook(host, "subagent-start", in, subagentOptions())
			g.server.Close() // a known subagent needs no server
			third := g.hook(host, "subagent-start", in, subagentOptions())

			wantSubagentContext(t, first, "session: "+before.Session)
			if second.Stdout != first.Stdout || third.Stdout != first.Stdout {
				t.Fatalf("repeated runs print other text:\n%s\n%s\n%s", first.Stdout, second.Stdout, third.Stdout)
			}
			after, _ := g.association(host, agent)
			if *after.Client != *before.Client || after.Session != before.Session {
				t.Fatalf("a repeat changed the identity: %+v", after)
			}
			if g.connects.Load() != 2 {
				t.Fatalf("registered %d clients", g.connects.Load())
			}
			noFailures(t)
		})
		t.Run(host+"/concurrent", func(t *testing.T) {
			g := newRig(t, nil)
			in, root, agent := subagentInput(t, host)
			g.startRoot(host, root)
			const runs = 8
			replies := make([]reply, runs)
			var wg sync.WaitGroup
			for i := range replies {
				wg.Add(1)
				go func() {
					defer wg.Done()
					replies[i] = g.hook(host, "subagent-start", in, subagentOptions())
				}()
			}
			wg.Wait()
			a, e := g.association(host, agent)
			if e != nil || a.Status != "ready" {
				t.Fatalf("%+v %v", a, e)
			}
			for i, r := range replies {
				if r.Code != 0 || r.Stdout != replies[0].Stdout {
					t.Errorf("run %d printed other text: %+v", i, r)
				}
			}
			wantSubagentContext(t, replies[0], "session: "+a.Session, "runtime ID: "+agent)
			if g.connects.Load() != 2 {
				t.Fatalf("registered %d clients", g.connects.Load())
			}
			noFailures(t)
		})
	}
}

func TestSubagentStartWaitsForABriefLock(t *testing.T) {
	g := newRig(t, nil)
	in, root, agent := subagentInput(t, "claude")
	g.startRoot("claude", root)
	_, lock, _ := workflowsession.Adapter{Host: "claude", RuntimeID: agent}.Paths()
	if e := os.MkdirAll(lock, 0o700); e != nil {
		t.Fatal(e)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = os.Remove(lock)
	}()
	r := g.hook("claude", "subagent-start", in, subagentOptions())
	a, e := g.association("claude", agent)
	if e != nil || a.Status != "ready" {
		t.Fatalf("%+v %v", a, e)
	}
	wantSubagentContext(t, r, "session: "+a.Session)
	noFailures(t)
}

func TestSubagentStartLeavesALockThatPersistsAlone(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			in, root, agent := subagentInput(t, host)
			g.startRoot(host, root)
			_, lock, _ := workflowsession.Adapter{Host: host, RuntimeID: agent}.Paths()
			if e := os.MkdirAll(lock, 0o700); e != nil {
				t.Fatal(e)
			}
			opt := subagentOptions()
			opt.BusyWait = 300 * time.Millisecond
			wantSilence(t, g.hook(host, "subagent-start", in, opt))
			if _, e := g.association(host, agent); !os.IsNotExist(e) {
				t.Fatalf("a held lock must not be bypassed: %v", e)
			}
			if _, e := os.Stat(lock); e != nil {
				t.Fatal("the hook removed a lock it does not own")
			}
			if g.connects.Load() != 1 {
				t.Fatal("registered a client past a held lock")
			}
			failures, _ := RecentFailures(time.Now().Add(-time.Hour))
			if len(failures) != 1 || failures[0].RuntimeID != agent || !strings.Contains(failures[0].Message, lock) {
				t.Fatalf("a lock that persists is a failure of this subagent: %+v", failures)
			}
		})
	}
}

func TestSubagentStartWithoutAReadyParentSessionCreatesNothing(t *testing.T) {
	for _, host := range hosts {
		t.Run(host+"/no session", func(t *testing.T) {
			g := newRig(t, nil)
			in, _, agent := subagentInput(t, host)
			wantSilence(t, g.hook(host, "subagent-start", in, subagentOptions()))
			if _, e := g.association(host, agent); !os.IsNotExist(e) {
				t.Fatalf("a subagent got a session although its parent has none: %v", e)
			}
			if g.connects.Load() != 0 {
				t.Fatal("registered a client")
			}
			noFailures(t) // a conversation without lit is not a hook failure
		})
		t.Run(host+"/pending", func(t *testing.T) {
			g := newRig(t, nil)
			in, root, agent := subagentInput(t, host)
			g.startRoot(host, root)
			record, _, _ := workflowsession.Adapter{Host: host, RuntimeID: root}.Paths()
			raw, _ := os.ReadFile(record)
			pending := strings.Replace(string(raw), `"status": "ready"`, `"status": "pending"`, 1)
			if pending == string(raw) || os.WriteFile(record, []byte(pending), 0o600) != nil {
				t.Fatalf("could not make the parent pending: %s", raw)
			}
			wantSilence(t, g.hook(host, "subagent-start", in, subagentOptions()))
			if _, e := g.association(host, agent); !os.IsNotExist(e) {
				t.Fatalf("a subagent got a session under a pending parent: %v", e)
			}
			noFailures(t)
		})
	}
}

func TestSubagentStartWhenTheServerIsUnreachable(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			in, root, agent := subagentInput(t, host)
			g.startRoot(host, root)
			project := g.selectProject(host, root, "feat/parent")
			g.server.Close()

			start := time.Now()
			r := g.hook(host, "subagent-start", in, Options{})
			if time.Since(start) > 2*time.Second {
				t.Fatalf("took %s", time.Since(start))
			}
			wantSubagentContext(t, r, "lit is unavailable", g.endpoint, "no lit session was created for this subagent",
				"lit workflow-session --host "+host+" --runtime-id "+agent+" subagent --parent-runtime-id "+root+
					" --endpoint "+g.endpoint+" --project "+project)
			if _, e := g.association(host, agent); !os.IsNotExist(e) {
				t.Fatalf("an unreachable server must create nothing: %v", e)
			}
			noFailures(t) // a server that is down is not a hook failure
		})
	}
}

func TestSubagentStartRefusesAServiceThatIsNotTheParents(t *testing.T) {
	g := newRig(t, nil)
	in, root, agent := subagentInput(t, "claude")
	g.startRoot("claude", root)
	record, _, _ := workflowsession.Adapter{Host: "claude", RuntimeID: root}.Paths()
	raw, _ := os.ReadFile(record)
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	fields["service_id"] = "00000000-0000-4000-8000-000000000000"
	changed, _ := json.Marshal(fields)
	if e := os.WriteFile(record, changed, 0o600); e != nil {
		t.Fatal(e)
	}

	wantSilence(t, g.hook("claude", "subagent-start", in, subagentOptions()))
	if _, e := g.association("claude", agent); !os.IsNotExist(e) {
		t.Fatalf("a subagent was registered on another service: %v", e)
	}
	if g.connects.Load() != 1 {
		t.Fatal("registered a client on a service that is not the parent's")
	}
	failures, _ := RecentFailures(time.Now().Add(-time.Hour))
	if len(failures) != 1 || failures[0].RuntimeID != agent || !strings.Contains(failures[0].Message, "not the one") {
		t.Fatalf("a changed service is a failure: %+v", failures)
	}
}

func TestSubagentStartKeepsThePendingAssociationWhenConnectTimesOut(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			var stall atomic.Bool
			release := make(chan struct{})
			g := newRig(t, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if stall.Load() && r.Method == http.MethodPost && r.URL.Path == "/v1/connect" {
						_, _ = io.Copy(io.Discard, r.Body) // the server notices a closed connection once the body is read
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
			in, root, agent := subagentInput(t, host)
			g.startRoot(host, root)
			stall.Store(true)

			start := time.Now()
			r := g.hook(host, "subagent-start", in, Options{}) // the real budget: about one second
			if time.Since(start) > 3*time.Second {
				t.Fatalf("took %s", time.Since(start))
			}
			a, e := g.association(host, agent)
			if e != nil || a.Status != "pending" {
				t.Fatalf("the pending association must be kept: %+v %v", a, e)
			}
			wantSubagentContext(t, r, "pending", a.Session, "lit workflow-session --host "+host+" --runtime-id "+agent+" reconcile")
			failures, _ := RecentFailures(time.Now().Add(-time.Hour))
			if len(failures) != 1 || failures[0].Event != "subagent-start" || failures[0].RuntimeID != agent {
				t.Fatalf("a connect timeout is a hook failure of this subagent: %+v", failures)
			}

			// A repeat does not start another client; it points at the same recovery.
			stall.Store(false)
			again := g.hook(host, "subagent-start", in, subagentOptions())
			wantSubagentContext(t, again, "pending", "reconcile")
			if g.connects.Load() != 2 {
				t.Fatalf("%d connect requests, want the parent's and the one stalled attempt", g.connects.Load())
			}
		})
	}
}

func TestSubagentStartFailsSoftOnBadInput(t *testing.T) {
	g := newRig(t, nil)
	_, root, _ := subagentInput(t, "claude")
	g.startRoot("claude", root)
	for name, in := range map[string]string{
		"empty":         "",
		"not json":      "subagent start",
		"array":         "[]",
		"no session":    `{"hook_event_name":"SubagentStart","agent_id":"a1"}`,
		"no agent":      `{"hook_event_name":"SubagentStart","session_id":"` + root + `"}`,
		"agent is root": `{"hook_event_name":"SubagentStart","session_id":"` + root + `","agent_id":"` + root + `"}`,
		"other event":   `{"hook_event_name":"Stop","session_id":"` + root + `","agent_id":"a1"}`,
	} {
		t.Run(name, func(t *testing.T) {
			wantSilence(t, g.hook("claude", "subagent-start", []byte(in), subagentOptions()))
		})
	}
	failures, _ := RecentFailures(time.Now().Add(-time.Hour))
	if len(failures) != 7 {
		t.Fatalf("every bad input is logged: %+v", failures)
	}
	if g.connects.Load() != 1 {
		t.Fatal("bad input registered a client")
	}
}

func TestSubagentStartInputsAndOutputsMatchTheirHosts(t *testing.T) {
	set := loadFixtures(t)
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			in, root, agent := subagentInput(t, host)
			var fields map[string]any
			if e := json.Unmarshal(in, &fields); e != nil {
				t.Fatal(e)
			}
			if fields["hook_event_name"] != "SubagentStart" || fields["agent_type"] == "" || fields["cwd"] == nil || fields["transcript_path"] == nil {
				t.Fatalf("not a SubagentStart input: %v", fields)
			}
			for _, f := range set.Fixtures {
				if f.Host == host && f.Event == "subagent-start" {
					text := set.Provenance[f.Provenance]
					if !strings.Contains(text, "Not captured") && !strings.Contains(text, "Captured") {
						t.Errorf("the provenance must say whether the input was captured: %q", text)
					}
				}
			}
			if host == "codex" {
				schema := loadSchema(t, "subagent-start.input.schema.json")
				if problems := check(schema, schema, fields, "input"); len(problems) != 0 {
					t.Fatalf("the Codex schema rejects the input: %v", problems)
				}
			}

			g := newRig(t, nil)
			g.startRoot(host, root)
			r := g.hook(host, "subagent-start", in, subagentOptions())
			wantSubagentContext(t, r, "runtime ID: "+agent)
			if host == "codex" {
				schema := loadSchema(t, "subagent-start.output.schema.json")
				var out map[string]any
				if e := json.Unmarshal([]byte(r.Stdout), &out); e != nil {
					t.Fatal(e)
				}
				if problems := check(schema, schema, out, "output"); len(problems) != 0 {
					t.Fatalf("the schema rejects %s: %v", r.Stdout, problems)
				}
			}
		})
	}
}

func TestLitHookSubagentStartEndToEnd(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			in, root, agent := subagentInput(t, host)
			g.startRoot(host, root)
			r := litHook(t, g, in, "hook", "--host", host, "subagent-start")
			a, e := g.association(host, agent)
			if e != nil || a.Status != "ready" {
				t.Fatalf("%+v %v", a, e)
			}
			wantSubagentContext(t, r, "session: "+a.Session, "runtime ID: "+agent)
			if again := litHook(t, g, in, "hook", "--host", host, "subagent-start"); again.Stdout != r.Stdout {
				t.Fatalf("a repeat prints other text: %q", again.Stdout)
			}
			if g.connects.Load() != 2 {
				t.Fatalf("registered %d clients", g.connects.Load())
			}
		})
	}
}
