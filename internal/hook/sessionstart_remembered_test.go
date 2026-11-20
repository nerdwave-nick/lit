package hook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/checkoutcontext"
	"github.com/nerdwave-nick/lit/internal/workflowsession"
)

const (
	noService = "0b5c3a4e-9d2f-4a6b-8c1e-7f3d2a1b0c9d" // a service ID no rig has
	noProject = "5d1e8f2a-3b4c-4d6e-9f0a-1b2c3d4e5f60" // a project ID no rig has
)

func TestSessionStartAnnouncesTheAdoptedRememberedProject(t *testing.T) {
	for _, host := range hosts {
		for _, source := range []string{"startup", "clear", "fork", "resume"} {
			t.Run(host+"/"+source, func(t *testing.T) {
				g := newRig(t, nil)
				id := g.project("feat/remembered")
				g.project("feat/other")
				dir := checkoutDir(t)
				g.remember(dir, "feat/remembered")
				before := readFile(t, g.preferenceFile(dir))
				// A subdirectory of the checkout shares the checkout's preference.
				in, runtime := inputIn(t, host, source, filepath.Join(dir, "src", "deep"))

				r := g.hook(host, "session-start", in, Options{})

				a, e := g.association(host, runtime)
				if e != nil || a.Status != "ready" || a.Project == nil || *a.Project != id {
					t.Fatalf("the remembered project must be selected: %+v %v", a, e)
				}
				if s := a.Selection; s == nil || s.Source != workflowsession.SourceRemembered || s.Checkout != dir {
					t.Fatalf("selection: %+v", a.Selection)
				}
				wantContext(t, r, "session: "+a.Session, "project: feat/remembered ("+id+")",
					"remembered project: this project was remembered for this checkout ("+dir+") and adopted when this session started")
				if strings.Contains(r.Context, "none selected") || strings.Contains(r.Context, "saved as") {
					t.Errorf("an adopted, unrenamed project is selected:\n%s", r.Context)
				}
				if got := jsonString(g.tracker(host, runtime, "session", "get")[0]["project_id"]); got != id {
					t.Errorf("the tracker session selected %q, not the remembered project", got)
				}
				if g.connects.Load() != 1 {
					t.Errorf("registered %d clients", g.connects.Load())
				}
				if readFile(t, g.preferenceFile(dir)) != before {
					t.Error("session start rewrote the preference")
				}
				noFailures(t)
			})
		}
	}
}

func TestSessionStartSaysWhenTheRememberedProjectWasRenamed(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			id := g.project("feat/old-name")
			dir := checkoutDir(t)
			g.remember(dir, "feat/old-name")
			g.admin("projects", "update", "feat/old-name", "--title", "feat/new-name")
			before := readFile(t, g.preferenceFile(dir))
			in, runtime := inputIn(t, host, "startup", dir)

			r := g.hook(host, "session-start", in, Options{})

			wantContext(t, r, "project: feat/new-name ("+id+")",
				"remembered project: this project was remembered for this checkout ("+dir+") and adopted when this session started",
				"saved as feat/old-name and has been renamed since")
			if a, _ := g.association(host, runtime); a.Project == nil || *a.Project != id {
				t.Fatalf("a renamed project is still the remembered one: %+v", a)
			}
			if readFile(t, g.preferenceFile(dir)) != before {
				t.Error("the preference must not follow the rename")
			}
			noFailures(t)
		})
	}
}

func TestSessionStartWithoutARememberedProjectSelectsNone(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			g.project("feat/remembered-elsewhere")
			elsewhere, dir := checkoutDir(t), checkoutDir(t)
			g.remember(elsewhere, "feat/remembered-elsewhere") // another checkout's choice is not this one's
			in, runtime := inputIn(t, host, "startup", dir)

			r := g.hook(host, "session-start", in, Options{})

			a, e := g.association(host, runtime)
			if e != nil || a.Status != "ready" || a.Project != nil {
				t.Fatalf("an unselected session was expected: %+v %v", a, e)
			}
			if s := a.Selection; s == nil || s.Source != workflowsession.SourceNone || s.Checkout != dir {
				t.Fatalf("selection: %+v", a.Selection)
			}
			wantContext(t, r, "session: "+a.Session, "project: none selected",
				"remembered project: none for this checkout ("+dir+")", "lit context set --project REF")
			if strings.Contains(r.Context, "adopted") {
				t.Errorf("nothing was adopted:\n%s", r.Context)
			}
			noFailures(t)
		})
	}
}

// The announcement is read back from the association, so a repeated or resumed
// start says the same without consulting the preference again.
func TestSessionStartRepeatsTheAnnouncementFromTheAssociation(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			id := g.project("feat/remembered")
			dir := checkoutDir(t)
			g.remember(dir, "feat/remembered")
			start, runtime := inputIn(t, host, "startup", dir)
			g.hook(host, "session-start", start, Options{})
			first, _ := g.association(host, runtime)

			// A later change of the preference, here a damaged record, neither
			// redirects nor disturbs the session.
			if e := os.WriteFile(g.preferenceFile(dir), []byte("{broken"), 0o600); e != nil {
				t.Fatal(e)
			}
			resume, _ := inputIn(t, host, "resume", dir)
			again, _ := inputIn(t, host, "startup", dir)
			for name, in := range map[string][]byte{"resume": resume, "duplicate startup": again} {
				r := g.hook(host, "session-start", in, Options{})
				wantContext(t, r, "project: feat/remembered ("+id+")",
					"remembered project: this project was remembered for this checkout ("+dir+") and adopted when this session started")
				if strings.Contains(r.Context, "cannot be used") {
					t.Errorf("%s: a known runtime never consults the preference:\n%s", name, r.Context)
				}
			}
			if a, _ := g.association(host, runtime); a.Project == nil || *a.Project != id || *a.Client != *first.Client {
				t.Fatalf("the session keeps its project and client: %+v", a)
			}
			noFailures(t)
		})
	}
}

// Concurrent runs of the same start (the same runtime ID) adopt once: one client,
// and every run, the one that registered and those that only read, announces it.
func TestSessionStartConcurrentRunsAdoptOnce(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			id := g.project("feat/remembered")
			dir := checkoutDir(t)
			g.remember(dir, "feat/remembered")
			in, runtime := inputIn(t, host, "startup", dir)
			const runs = 6
			replies := make([]reply, runs)
			var wg sync.WaitGroup
			for i := range replies {
				wg.Add(1)
				go func() {
					defer wg.Done()
					replies[i] = g.hook(host, "session-start", in, Options{Budget: 20 * time.Second, BusyWait: 15 * time.Second})
				}()
			}
			wg.Wait()
			a, e := g.association(host, runtime)
			if e != nil || a.Status != "ready" || a.Project == nil || *a.Project != id {
				t.Fatalf("%+v %v", a, e)
			}
			for i, r := range replies {
				if r.Code != 0 || !strings.Contains(r.Context, "this project was remembered for this checkout ("+dir+") and adopted") {
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

func mustCheckout(t *testing.T, dir string) checkoutcontext.Checkout {
	t.Helper()
	c, e := checkoutcontext.Resolve(dir)
	if e != nil {
		t.Fatal(e)
	}
	return c
}

// staleCases are the preferences that cannot be adopted. Each saves its record
// for dir on the rig's service and says what the note must name.
var staleCases = []struct {
	name string
	set  func(g *rig, dir string)
	want []string
}{
	{
		"another service",
		func(g *rig, dir string) {
			g.save(dir, checkoutcontext.Preference{ServiceID: noService, ProjectID: noProject, ProjectTitle: "feat/foreign", Endpoint: g.endpoint})
		},
		[]string{"feat/foreign", "belongs to service " + noService, "wrong_service"},
	},
	{
		"project gone",
		func(g *rig, dir string) {
			g.save(dir, checkoutcontext.Preference{ServiceID: g.serviceID(), ProjectID: noProject, ProjectTitle: "feat/gone", Endpoint: g.endpoint})
		},
		[]string{"feat/gone", "no longer exists", "not_found"},
	},
	{
		"damaged record",
		func(g *rig, dir string) {
			g.remember(dir, "feat/remembered")
			if e := os.WriteFile(g.preferenceFile(dir), []byte("{broken"), 0o600); e != nil {
				g.t.Fatal(e)
			}
		},
		[]string{"is damaged", "invalid_local_state"},
	},
	{
		"unsupported record",
		func(g *rig, dir string) {
			g.remember(dir, "feat/remembered")
			if e := os.WriteFile(g.preferenceFile(dir), []byte(`{"schema_version":99}`), 0o600); e != nil {
				g.t.Fatal(e)
			}
		},
		[]string{"unsupported format", "invalid_local_state"},
	},
}

func TestSessionStartExplainsAStalePreferenceAndCreatesNoSession(t *testing.T) {
	for _, host := range hosts {
		for _, stale := range staleCases {
			for _, source := range []string{"startup", "resume"} {
				t.Run(host+"/"+stale.name+"/"+source, func(t *testing.T) {
					g := newRig(t, nil)
					g.project("feat/remembered")
					dir := checkoutDir(t)
					stale.set(g, dir)
					before := readFile(t, g.preferenceFile(dir))
					in, runtime := inputIn(t, host, source, dir)

					r := g.hook(host, "session-start", in, Options{})

					wantContext(t, r, append([]string{
						"no lit session was created", "remembered for this checkout", dir,
						"lit context clear",
						"lit workflow-session --host " + host + " --runtime-id " + runtime + " fresh --endpoint " + g.endpoint,
						"lit workflow-session --host " + host + " --runtime-id " + runtime + " fresh --endpoint " + g.endpoint + " --project REF",
					}, stale.want...)...)
					if _, e := g.association(host, runtime); !os.IsNotExist(e) {
						t.Fatalf("a stale preference must create no association: %v", e)
					}
					if names := g.sessionFiles(); len(names) != 0 {
						t.Errorf("a stale preference must leave no session record: %v", names)
					}
					if g.connects.Load() != 0 {
						t.Error("a stale preference must register no client")
					}
					if readFile(t, g.preferenceFile(dir)) != before {
						t.Error("session start must leave the preference as it is")
					}
					noFailures(t) // a stale preference is not a hook failure
					logged := warnings(t)
					if len(logged) != 1 || logged[0].RuntimeID != runtime || !strings.Contains(logged[0].Message, dir) {
						t.Errorf("a stale preference is logged once, as a warning: %+v", logged)
					}
				})
			}
		}
	}
}

// Both commands the note names work for the runtime that got it.
func TestSessionStartStaleNoteLeavesTheRuntimeFreeToSetUp(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			id := g.project("feat/explicit")
			dir := checkoutDir(t)
			g.save(dir, checkoutcontext.Preference{ServiceID: noService, ProjectID: noProject, ProjectTitle: "feat/foreign", Endpoint: g.endpoint})
			in, runtime := inputIn(t, host, "startup", dir)
			g.hook(host, "session-start", in, Options{})

			a := workflowsession.Adapter{Host: host, RuntimeID: runtime, Binary: g.binary, WorkDir: t.TempDir()}
			got, e := a.Fresh(context.Background(), workflowsession.FreshOptions{Endpoint: g.endpoint, Project: "feat/explicit"})
			if e != nil || got.Project == nil || *got.Project != id || got.Selection == nil || got.Selection.Source != workflowsession.SourceExplicit {
				t.Fatalf("an explicit project sets the session up despite the stale preference: %+v %v", got, e)
			}
			// The same hook run again now finds the association and reports it ready.
			r := g.hook(host, "session-start", in, Options{})
			wantContext(t, r, "session: "+got.Session, "project: feat/explicit ("+id+")")
			noFailures(t)
		})
	}
}

func TestSessionStartProbesTheEndpointAdoptionWillChoose(t *testing.T) {
	for _, host := range hosts {
		t.Run(host+"/saved endpoint answers", func(t *testing.T) {
			g := newRig(t, nil)
			id := g.project("feat/remembered")
			dir := checkoutDir(t)
			g.remember(dir, "feat/remembered") // saved with the rig's endpoint
			t.Setenv("LIT_ENDPOINT", "")       // no environment: the saved endpoint is next
			in, runtime := inputIn(t, host, "startup", dir)

			r := g.hook(host, "session-start", in, Options{})

			a, e := g.association(host, runtime)
			if e != nil || a.Status != "ready" || a.Endpoint != g.endpoint || a.Project == nil || *a.Project != id {
				t.Fatalf("adoption registers at the saved endpoint, not the default: %+v %v", a, e)
			}
			wantContext(t, r, "endpoint: "+g.endpoint, "adopted when this session started")
			noFailures(t)
		})
		t.Run(host+"/saved endpoint is down", func(t *testing.T) {
			g := newRig(t, nil)
			g.project("feat/remembered")
			dir := checkoutDir(t)
			g.save(dir, checkoutcontext.Preference{ServiceID: g.serviceID(), ProjectID: noProject, ProjectTitle: "feat/remembered", Endpoint: "http://127.0.0.1:1"})
			t.Setenv("LIT_ENDPOINT", "")
			in, runtime := inputIn(t, host, "startup", dir)

			r := g.hook(host, "session-start", in, Options{})

			wantContext(t, r, "lit is unavailable", "nothing answered at http://127.0.0.1:1",
				"lit workflow-session --host "+host+" --runtime-id "+runtime+" fresh --endpoint http://127.0.0.1:1")
			if names := g.sessionFiles(); len(names) != 0 {
				t.Errorf("an unreachable server creates nothing: %v", names)
			}
			noFailures(t)
		})
		t.Run(host+"/environment wins", func(t *testing.T) {
			g := newRig(t, nil)
			id := g.project("feat/remembered")
			dir := checkoutDir(t)
			g.remember(dir, "feat/remembered")
			pref := mustLoad(t, g, dir)
			pref.Endpoint = "http://127.0.0.1:1" // saved at an address that no longer answers
			g.save(dir, pref.Preference)
			in, runtime := inputIn(t, host, "startup", dir) // LIT_ENDPOINT is the rig's

			g.hook(host, "session-start", in, Options{})

			a, e := g.association(host, runtime)
			if e != nil || a.Status != "ready" || a.Endpoint != g.endpoint || a.Project == nil || *a.Project != id {
				t.Fatalf("LIT_ENDPOINT outranks the saved endpoint: %+v %v", a, e)
			}
			noFailures(t)
		})
	}
}

func mustLoad(t *testing.T, g *rig, dir string) checkoutcontext.Record {
	t.Helper()
	rec, e := checkoutcontext.OpenAt(g.workflow).Load(mustCheckout(t, dir))
	if e != nil || rec == nil {
		t.Fatalf("%v %v", rec, e)
	}
	return *rec
}

// A damaged record is refused before anything is contacted, so the note names
// it even when no server answers.
func TestSessionStartReportsADamagedPreferenceWithoutAServer(t *testing.T) {
	g := newRig(t, nil)
	g.project("feat/remembered")
	dir := checkoutDir(t)
	g.remember(dir, "feat/remembered")
	if e := os.WriteFile(g.preferenceFile(dir), []byte("{broken"), 0o600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("LIT_ENDPOINT", "http://127.0.0.1:1")
	in, _ := inputIn(t, "claude", "startup", dir)

	r := g.hook("claude", "session-start", in, Options{})

	wantContext(t, r, "is damaged", "lit context clear", "no lit session was created")
	if strings.Contains(r.Context, "lit is unavailable") {
		t.Errorf("the damaged record is the problem, not the server:\n%s", r.Context)
	}
	noFailures(t)
}

// The built command prints the same note, exits 0 and writes nothing to stderr.
func TestLitHookCommandReportsAStalePreference(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			dir := checkoutDir(t)
			g.save(dir, checkoutcontext.Preference{ServiceID: noService, ProjectID: noProject, ProjectTitle: "feat/foreign", Endpoint: g.endpoint})
			in, runtime := inputIn(t, host, "startup", dir)

			r := litHook(t, g, in, "hook", "--host", host, "session-start")

			wantContext(t, r, "no lit session was created", "lit context clear", "fresh --endpoint "+g.endpoint+" --project REF")
			if _, e := g.association(host, runtime); !os.IsNotExist(e) {
				t.Fatalf("a stale preference must create no association: %v", e)
			}
			noFailures(t)
		})
	}
}
