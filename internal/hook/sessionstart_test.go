package hook

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/workflowsession"
)

func wantContext(t *testing.T, r reply, wants ...string) {
	t.Helper()
	if r.Code != 0 || r.Stderr != "" {
		t.Fatalf("a hook must exit 0 quietly: %d %q", r.Code, r.Stderr)
	}
	if r.Event != "SessionStart" || r.Context == "" {
		t.Fatalf("no SessionStart context: %q", r.Stdout)
	}
	for _, want := range wants {
		if !strings.Contains(r.Context, want) {
			t.Errorf("context lacks %q:\n%s", want, r.Context)
		}
	}
}

func noFailures(t *testing.T) {
	t.Helper()
	failures, e := RecentFailures(time.Now().Add(-time.Hour))
	if e != nil || len(failures) != 0 {
		t.Fatalf("unexpected hook failures: %+v %v", failures, e)
	}
}

func TestSessionStartCreatesTheSessionForNewRuntimes(t *testing.T) {
	for _, host := range hosts {
		for _, source := range []string{"startup", "clear", "fork"} {
			t.Run(host+"/"+source, func(t *testing.T) {
				g := newRig(t, nil)
				in, id := input(t, host, source)
				r := g.hook(host, "session-start", in, Options{})
				a, e := g.association(host, id)
				if e != nil || a.Status != "ready" || a.Endpoint != g.endpoint || a.Client == nil {
					t.Fatalf("association: %+v %v", a, e)
				}
				wantContext(t, r, "session: "+a.Session, "endpoint: "+g.endpoint, "runtime ID: "+id,
					"project: none selected", "claims: none",
					"lit workflow-session --host "+host+" --runtime-id "+id+" run -- ")
				if g.connects.Load() != 1 {
					t.Fatalf("registered %d clients", g.connects.Load())
				}
				me := g.tracker(host, id, "session", "get")[0]
				var runtime map[string]string
				json.Unmarshal(me["runtime"], &runtime)
				if jsonString(me["client_id"]) != *a.Client || runtime["vendor"] != host || runtime["session_id"] != id {
					t.Fatalf("the tracker session is not this runtime's: %s %v", me["client_id"], runtime)
				}
				noFailures(t)
			})
		}
	}
}

func TestSessionStartResumesTheKnownRuntime(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			start, id := input(t, host, "startup")
			g.hook(host, "session-start", start, Options{})
			first, _ := g.association(host, id)
			g.run(host, id, "projects", "create", "--project-title", "feat/resumed", "--content", "x")
			project := jsonString(g.tracker(host, id, "projects", "list")[0]["id"])
			g.run(host, id, "session", "set", "--project", project)
			g.run(host, id, "issues", "create", "--issue", "held work", "--content", "x")
			g.run(host, id, "claims", "acquire", "held work")
			claim := g.tracker(host, id, "claims", "list")[0]

			resume, same := input(t, host, "resume")
			if same != id {
				t.Fatalf("resume fixture must keep the session ID: %s != %s", same, id)
			}
			r := g.hook(host, "session-start", resume, Options{})
			second, e := g.association(host, id)
			if e != nil || *second.Client != *first.Client || second.Session != first.Session {
				t.Fatalf("resume changed the identity: %+v %v", second, e)
			}
			wantContext(t, r, "session: "+first.Session, "runtime ID: "+id, "project: feat/resumed ("+project+")",
				jsonString(claim["issue_id"]), "lease expires "+jsonString(claim["expires_at"]))
			noFailures(t)
		})
	}
}

func TestSessionStartWithoutAnAssociationStartsOneEvenOnResume(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			in, id := input(t, host, "resume")
			r := g.hook(host, "session-start", in, Options{})
			a, e := g.association(host, id)
			if e != nil || a.Status != "ready" {
				t.Fatalf("%+v %v", a, e)
			}
			wantContext(t, r, "session: "+a.Session)
		})
	}
}

func TestSessionStartWhenTheServerIsUnreachable(t *testing.T) {
	for _, host := range hosts {
		t.Run(host+"/new", func(t *testing.T) {
			g := newRig(t, nil)
			t.Setenv("LIT_ENDPOINT", "http://127.0.0.1:1")
			in, id := input(t, host, "startup")
			start := time.Now()
			r := g.hook(host, "session-start", in, Options{})
			if time.Since(start) > 3*time.Second {
				t.Fatalf("took %s", time.Since(start))
			}
			wantContext(t, r, "lit is unavailable", "http://127.0.0.1:1",
				"lit workflow-session --host "+host+" --runtime-id "+id+" fresh --endpoint http://127.0.0.1:1")
			if _, e := g.association(host, id); !os.IsNotExist(e) {
				t.Fatalf("an unreachable server must create nothing: %v", e)
			}
			noFailures(t)
		})
		t.Run(host+"/known", func(t *testing.T) {
			g := newRig(t, nil)
			in, id := input(t, host, "startup")
			g.hook(host, "session-start", in, Options{})
			before, _ := g.association(host, id)
			record, _, _ := workflowsession.Adapter{Host: host, RuntimeID: id}.Paths()
			saved, _ := os.ReadFile(record)
			g.server.Close()
			resume, _ := input(t, host, "resume")
			r := g.hook(host, "session-start", resume, Options{})
			wantContext(t, r, "lit is unavailable", "session: "+before.Session,
				"lit workflow-session --host "+host+" --runtime-id "+id+" resume")
			if after, _ := os.ReadFile(record); string(after) != string(saved) {
				t.Fatal("an unreachable server changed the association")
			}
			noFailures(t)
		})
	}
}

func TestSessionStartKeepsThePendingAssociationWhenConnectTimesOut(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			release := make(chan struct{})
			g := newRig(t, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost && r.URL.Path == "/v1/connect" {
						io.Copy(io.Discard, r.Body) // the server notices a closed connection once the body is read
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
			in, id := input(t, host, "startup")
			start := time.Now()
			r := g.hook(host, "session-start", in, Options{Budget: 1500 * time.Millisecond})
			if time.Since(start) > 4*time.Second {
				t.Fatalf("took %s", time.Since(start))
			}
			a, e := g.association(host, id)
			if e != nil || a.Status != "pending" {
				t.Fatalf("the pending association must be kept: %+v %v", a, e)
			}
			wantContext(t, r, "pending", a.Session,
				"lit workflow-session --host "+host+" --runtime-id "+id+" reconcile")
			failures, _ := RecentFailures(time.Now().Add(-time.Hour))
			if len(failures) != 1 || failures[0].Host != host || failures[0].Event != "session-start" || failures[0].RuntimeID != id {
				t.Fatalf("a connect timeout is a hook failure: %+v", failures)
			}
		})
	}
}

func TestSessionStartDuplicatesNeverCreateASecondClient(t *testing.T) {
	for _, host := range hosts {
		t.Run(host+"/sequential", func(t *testing.T) {
			g := newRig(t, nil)
			in, id := input(t, host, "startup")
			first := g.hook(host, "session-start", in, Options{})
			second := g.hook(host, "session-start", in, Options{})
			a, _ := g.association(host, id)
			wantContext(t, first, "session: "+a.Session)
			wantContext(t, second, "session: "+a.Session, "runtime ID: "+id)
			if g.connects.Load() != 1 {
				t.Fatalf("registered %d clients", g.connects.Load())
			}
			noFailures(t)
		})
		t.Run(host+"/concurrent", func(t *testing.T) {
			g := newRig(t, nil)
			in, id := input(t, host, "startup")
			const runs = 8
			replies := make([]reply, runs)
			var wg sync.WaitGroup
			for i := range replies {
				wg.Add(1)
				go func() {
					defer wg.Done()
					// Generous waits: contention must not look like a stuck lock.
					replies[i] = g.hook(host, "session-start", in, Options{Budget: 20 * time.Second, BusyWait: 15 * time.Second})
				}()
			}
			wg.Wait()
			a, e := g.association(host, id)
			if e != nil || a.Status != "ready" {
				t.Fatalf("%+v %v", a, e)
			}
			for i, r := range replies {
				if r.Code != 0 || !strings.Contains(r.Context, "session: "+a.Session) {
					t.Errorf("run %d: %+v", i, r)
				}
			}
			if g.connects.Load() != 1 {
				t.Fatalf("registered %d clients", g.connects.Load())
			}
			noFailures(t)
		})
	}
}

func TestSessionStartWaitsForABriefLock(t *testing.T) {
	g := newRig(t, nil)
	in, id := input(t, "claude", "startup")
	_, lock, _ := workflowsession.Adapter{Host: "claude", RuntimeID: id}.Paths()
	if e := os.MkdirAll(lock, 0o700); e != nil {
		t.Fatal(e)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		os.Remove(lock)
	}()
	r := g.hook("claude", "session-start", in, Options{Budget: 10 * time.Second, BusyWait: 5 * time.Second})
	a, e := g.association("claude", id)
	if e != nil || a.Status != "ready" {
		t.Fatalf("%+v %v", a, e)
	}
	wantContext(t, r, "session: "+a.Session)
	noFailures(t)
}

func TestSessionStartReportsALockThatPersists(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			in, id := input(t, host, "startup")
			_, lock, _ := workflowsession.Adapter{Host: host, RuntimeID: id}.Paths()
			if e := os.MkdirAll(lock, 0o700); e != nil {
				t.Fatal(e)
			}
			start := time.Now()
			r := g.hook(host, "session-start", in, Options{BusyWait: 400 * time.Millisecond})
			if time.Since(start) > 3*time.Second {
				t.Fatalf("took %s", time.Since(start))
			}
			wantContext(t, r, lock, "adapter lock")
			if strings.Count(r.Context, "\n") > 1 {
				t.Errorf("an error note is one line:\n%s", r.Context)
			}
			if _, e := g.association(host, id); !os.IsNotExist(e) {
				t.Fatalf("a held lock must not be bypassed: %v", e)
			}
			if g.connects.Load() != 0 {
				t.Fatal("registered a client past a held lock")
			}
			if _, e := os.Stat(lock); e != nil {
				t.Fatal("the hook removed a lock it does not own")
			}
			failures, _ := RecentFailures(time.Now().Add(-time.Hour))
			if len(failures) != 1 || !strings.Contains(failures[0].Message, lock) {
				t.Fatalf("a lock that persists is a failure: %+v", failures)
			}
		})
	}
}

func TestSessionStartFailsSoftOnBadInput(t *testing.T) {
	g := newRig(t, nil)
	for name, in := range map[string]string{
		"empty":       "",
		"not json":    "session start",
		"array":       "[]",
		"no session":  `{"hook_event_name":"SessionStart","source":"startup"}`,
		"other event": `{"session_id":"x","hook_event_name":"Stop","source":"startup"}`,
	} {
		t.Run(name, func(t *testing.T) {
			r := g.hook("claude", "session-start", []byte(in), Options{})
			wantContext(t, r, "lit session setup failed")
			if strings.Count(r.Context, "\n") > 1 {
				t.Errorf("an error note is one line:\n%s", r.Context)
			}
		})
	}
	failures, _ := RecentFailures(time.Now().Add(-time.Hour))
	if len(failures) != 5 {
		t.Fatalf("every bad input is logged: %+v", failures)
	}
	if g.connects.Load() != 0 {
		t.Fatal("bad input registered a client")
	}
}

func TestSessionStartReadsAnUnreadableAssociationAsAFailure(t *testing.T) {
	g := newRig(t, nil)
	in, id := input(t, "claude", "startup")
	g.hook("claude", "session-start", in, Options{})
	record, _, _ := workflowsession.Adapter{Host: "claude", RuntimeID: id}.Paths()
	if e := os.WriteFile(record, []byte("{broken"), 0o600); e != nil {
		t.Fatal(e)
	}
	r := g.hook("claude", "session-start", in, Options{})
	wantContext(t, r, "lit session setup failed")
	if raw, _ := os.ReadFile(record); string(raw) != "{broken" {
		t.Fatal("the hook rewrote evidence")
	}
	if g.connects.Load() != 1 {
		t.Fatal("registered a second client")
	}
}

func TestWorkflowStateStaysOutOfTheProject(t *testing.T) {
	g := newRig(t, nil)
	in, _ := input(t, "claude", "startup")
	project := t.TempDir()
	t.Chdir(project)
	g.hook("claude", "session-start", in, Options{})
	if entries, _ := os.ReadDir(project); len(entries) != 0 {
		t.Fatalf("the hook wrote into the agent's working directory: %v", entries)
	}
}
