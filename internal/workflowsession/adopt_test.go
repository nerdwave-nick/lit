package workflowsession

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/checkoutcontext"
	"github.com/nerdwave-nick/lit/internal/litclient"
)

var noEndpoint = checkoutcontext.Endpoint{}

// Two conversations in the same checkout adopt the remembered project, each
// with a client of its own, and adoption leaves the preference as it was.
func TestFreshAdoptsTheRememberedProject(t *testing.T) {
	f := newAdoption(t)
	alpha := f.project(f.endpoint, "feat/alpha")
	dir := f.checkout()
	saved := f.remember(dir, f.endpoint, "feat/alpha")
	before := readFile(t, f.preferenceFile(dir))
	ctx := context.Background()

	first, e := f.adapter("one").Fresh(ctx, FreshOptions{Endpoint: f.endpoint, Remembered: remembered(dir, noEndpoint)})
	if e != nil {
		t.Fatal(e)
	}
	second, e := f.adapter("two").Fresh(ctx, FreshOptions{Endpoint: f.endpoint, Remembered: remembered(dir, noEndpoint)})
	if e != nil {
		t.Fatal(e)
	}
	if *first.Client == *second.Client || first.Session == second.Session {
		t.Fatalf("runtimes share a client: %+v %+v", first, second)
	}
	for _, a := range []Association{first, second} {
		if a.Status != "ready" || a.Project == nil || *a.Project != alpha || a.Endpoint != f.endpoint {
			t.Fatalf("association: %+v", a)
		}
		want := Selection{Source: SourceRemembered, Checkout: dir, ProjectID: alpha, ProjectTitle: "feat/alpha", SavedAt: saved.SavedAt.UTC().Format(time.RFC3339Nano)}
		if a.Selection == nil || *a.Selection != want {
			t.Fatalf("selection: %+v, want %+v", a.Selection, want)
		}
	}
	if got := projectOf(t, f.adapter("one")); got == nil || *got != alpha {
		t.Fatalf("the tracker session selected %v", got)
	}
	if got := f.seen.count(connects); got != 3 { // the administrator and the two conversations
		t.Fatalf("connect requests: %d", got)
	}
	if after := readFile(t, f.preferenceFile(dir)); after != before {
		t.Fatalf("adoption rewrote the preference:\n%s\n%s", before, after)
	}
	// The selection is part of the saved association.
	if read, e := f.adapter("one").Read(); e != nil || read.Selection == nil || *read.Selection != *first.Selection {
		t.Fatalf("saved association: %+v %v", read.Selection, e)
	}
}

// A project renamed since it was remembered is adopted under its current title.
func TestFreshAnnouncesARenamedProject(t *testing.T) {
	f := newAdoption(t)
	f.project(f.endpoint, "feat/alpha")
	dir := f.checkout()
	f.remember(dir, f.endpoint, "feat/alpha")
	f.admin(f.endpoint, "projects", "update", "feat/alpha", "--title", "feat/renamed")
	before := readFile(t, f.preferenceFile(dir))

	r, e := f.adapter("one").Fresh(context.Background(), FreshOptions{Endpoint: f.endpoint, Remembered: remembered(dir, noEndpoint)})
	if e != nil {
		t.Fatal(e)
	}
	if r.Selection.ProjectTitle != "feat/renamed" || r.Selection.SavedTitle != "feat/alpha" {
		t.Fatalf("selection: %+v", r.Selection)
	}
	if after := readFile(t, f.preferenceFile(dir)); after != before {
		t.Fatal("a rename rewrote the preference")
	}
}

// An explicit project wins without reading the preference: even a stale or
// damaged one neither blocks it nor is changed by it.
func TestFreshExplicitProjectLeavesThePreferenceAlone(t *testing.T) {
	f := newAdoption(t)
	f.project(f.endpoint, "feat/alpha")
	beta := f.project(f.endpoint, "feat/beta")
	via := proxy(t, f.endpoint) // an address other than the one the preference was saved with
	for _, tc := range []struct {
		name   string
		break_ func(dir string)
	}{
		{"valid", func(string) {}},
		{"stale", func(dir string) {
			f.save(dir, checkoutcontext.Preference{ServiceID: "22222222-2222-4222-8222-222222222222", ProjectID: "33333333-3333-4333-8333-333333333333", ProjectTitle: "feat/gone", Endpoint: f.endpoint})
		}},
		{"damaged", func(dir string) {
			if e := os.WriteFile(f.preferenceFile(dir), []byte("{not json"), 0o600); e != nil {
				t.Fatal(e)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := f.checkout()
			f.remember(dir, f.endpoint, "feat/alpha")
			tc.break_(dir)
			before, contexts0 := readFile(t, f.preferenceFile(dir)), f.seen.count(contexts)

			r, e := f.adapter("explicit-"+tc.name).Fresh(context.Background(), FreshOptions{Endpoint: via, Project: "feat/beta", Remembered: remembered(dir, noEndpoint)})
			if e != nil {
				t.Fatal(e)
			}
			if r.Endpoint != via {
				t.Fatalf("an explicit project adopted the preference's endpoint: %s", r.Endpoint)
			}
			if r.Project == nil || *r.Project != beta || r.Selection == nil || *r.Selection != (Selection{Source: SourceExplicit}) {
				t.Fatalf("association: project %v selection %+v", r.Project, r.Selection)
			}
			if after := readFile(t, f.preferenceFile(dir)); after != before {
				t.Fatal("an explicit project changed the preference")
			}
			if f.seen.count(contexts) != contexts0 {
				t.Fatal("an explicit project validated the preference")
			}
		})
	}
}

// With neither an explicit project nor a preference the session is unselected,
// as before, and the selection says where the preference was looked for.
func TestFreshWithoutAPreferenceIsUnselected(t *testing.T) {
	f := newAdoption(t)
	f.project(f.endpoint, "feat/alpha")
	dir := f.checkout()

	r, e := f.adapter("none").Fresh(context.Background(), FreshOptions{Endpoint: f.endpoint, Remembered: remembered(dir, noEndpoint)})
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != "ready" || r.Project != nil || r.Selection == nil || *r.Selection != (Selection{Source: SourceNone, Checkout: dir}) {
		t.Fatalf("association: %+v selection %+v", r, r.Selection)
	}
	if _, e = os.Stat(filepath.Join(f.workflow, "checkout-context")); !errors.Is(e, fs.ErrNotExist) {
		t.Fatalf("looking for a preference created state: %v", e)
	}
	// Not asking for adoption is the same as before: nothing is looked up.
	plain, e := f.adapter("plain").Fresh(context.Background(), FreshOptions{Endpoint: f.endpoint})
	if e != nil || plain.Project != nil || *plain.Selection != (Selection{Source: SourceNone}) {
		t.Fatalf("plain fresh: %+v %v", plain, e)
	}
}

// A preference that cannot be adopted stops fresh before it creates anything:
// no pending association, no client, no change to the preference, and the
// error names how to fix it.
func TestFreshRefusesAnInvalidPreferenceBeforeRegistering(t *testing.T) {
	f := newAdoption(t)
	f.project(f.endpoint, "feat/alpha")
	other := testService(t, nil)
	f.project(other, "feat/alpha")
	identity, e := checkoutcontext.ResolveProject(context.Background(), "feat/alpha", checkoutcontext.Endpoint{Explicit: f.endpoint, Supplied: true}, nil)
	if e != nil {
		t.Fatal(e)
	}
	service, project := identity.ServiceID, identity.ProjectID
	const stranger = "44444444-4444-4444-8444-444444444444"

	for _, tc := range []struct {
		name     string
		prepare  func(dir string)
		inputs   checkoutcontext.Endpoint
		code     string
		mentions []string
	}{
		{"another service answers", func(dir string) {
			f.save(dir, checkoutcontext.Preference{ServiceID: stranger, ProjectID: project, ProjectTitle: "feat/alpha", Endpoint: f.endpoint})
		}, noEndpoint, "wrong_service", []string{"lit context clear", "--project"}},
		{"the environment names another service", func(dir string) {
			f.remember(dir, f.endpoint, "feat/alpha")
		}, checkoutcontext.Endpoint{Environment: other}, "wrong_service", []string{"lit context clear"}},
		{"the project is gone", func(dir string) {
			f.save(dir, checkoutcontext.Preference{ServiceID: service, ProjectID: stranger, ProjectTitle: "feat/gone", Endpoint: f.endpoint})
		}, noEndpoint, "not_found", []string{"lit context clear", "no longer exists"}},
		{"the service is not running", func(dir string) {
			f.save(dir, checkoutcontext.Preference{ServiceID: service, ProjectID: project, ProjectTitle: "feat/alpha", Endpoint: "http://127.0.0.1:1"})
		}, noEndpoint, "service_unavailable", []string{"lit context clear", "cannot reach"}},
		{"the record is damaged", func(dir string) {
			f.remember(dir, f.endpoint, "feat/alpha")
			if e := os.WriteFile(f.preferenceFile(dir), []byte("{not json"), 0o600); e != nil {
				t.Fatal(e)
			}
		}, noEndpoint, "invalid_local_state", []string{"lit context clear", "damaged"}},
		{"the record is unsupported", func(dir string) {
			f.remember(dir, f.endpoint, "feat/alpha")
			if e := os.WriteFile(f.preferenceFile(dir), []byte(`{"schema_version":2,"future":true}`), 0o600); e != nil {
				t.Fatal(e)
			}
		}, noEndpoint, "invalid_local_state", []string{"lit context clear", "unsupported"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := f.checkout()
			tc.prepare(dir)
			a := f.adapter("refused")
			connects0, privateBefore, workflowBefore := f.seen.count(connects), tree(t, f.private), tree(t, f.workflow)

			r, e := a.Fresh(context.Background(), FreshOptions{Endpoint: f.endpoint, Remembered: remembered(dir, tc.inputs)})
			var refusal *AdoptionError
			if !errors.As(e, &refusal) {
				t.Fatalf("want an AdoptionError, got %v", e)
			}
			if refusal.Code != tc.code || refusal.Checkout != dir {
				t.Fatalf("refusal: %+v", refusal)
			}
			if tc.code != "invalid_local_state" && litclient.Code(e) != tc.code {
				t.Fatalf("the service's code is not reachable through errors.As: %q", litclient.Code(e))
			}
			for _, word := range append(tc.mentions, "no session was created") {
				if !strings.Contains(e.Error(), word) {
					t.Errorf("the message lacks %q:\n%s", word, e)
				}
			}
			if r.Session != "" || r.Status != "" {
				t.Fatalf("an association was returned: %+v", r)
			}
			if _, e = a.Read(); !errors.Is(e, fs.ErrNotExist) {
				t.Fatalf("an association was saved: %v", e)
			}
			if f.seen.count(connects) != connects0 {
				t.Fatal("a client was registered")
			}
			if got := tree(t, f.private); got != privateBefore {
				t.Fatalf("the client state changed:\n%s\n%s", privateBefore, got)
			}
			if got := tree(t, f.workflow); got != workflowBefore {
				t.Fatalf("the workflow state changed:\n%s\n%s", workflowBefore, got)
			}
		})
	}
}

// The endpoint of an adopted session is the first of the flag, LIT_ENDPOINT,
// the preference's and the default; the service must be the remembered one.
func TestFreshChoosesTheEndpointOfAnAdoptedProject(t *testing.T) {
	f := newAdoption(t)
	f.project(f.endpoint, "feat/alpha")
	viaOne, viaTwo := proxy(t, f.endpoint), proxy(t, f.endpoint)
	for _, tc := range []struct {
		name   string
		inputs checkoutcontext.Endpoint
		want   string
	}{
		{"saved", noEndpoint, f.endpoint},
		{"environment over saved", checkoutcontext.Endpoint{Environment: viaOne}, viaOne},
		{"flag over saved", checkoutcontext.Endpoint{Explicit: viaTwo, Supplied: true}, viaTwo},
		{"flag over environment", checkoutcontext.Endpoint{Explicit: viaTwo, Supplied: true, Environment: viaOne}, viaTwo},
		{"an address with a trailing slash", checkoutcontext.Endpoint{Environment: viaOne + "/"}, viaOne},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := f.checkout()
			f.remember(dir, f.endpoint, "feat/alpha")
			r, e := f.adapter("ep-"+strings.ReplaceAll(tc.name, " ", "-")).Fresh(context.Background(), FreshOptions{Endpoint: "http://127.0.0.1:1", Remembered: remembered(dir, tc.inputs)})
			if e != nil {
				t.Fatal(e)
			}
			if r.Endpoint != tc.want || r.Project == nil {
				t.Fatalf("endpoint %s, want %s (project %v)", r.Endpoint, tc.want, r.Project)
			}
			// The preference keeps the address it was saved with.
			if rec, e := checkoutcontext.OpenAt(f.workflow).Load(mustResolve(t, dir)); e != nil || rec.Endpoint != f.endpoint {
				t.Fatalf("saved endpoint: %+v %v", rec, e)
			}
		})
	}
	t.Run("a flag that is not an origin is refused", func(t *testing.T) {
		dir := f.checkout()
		f.remember(dir, f.endpoint, "feat/alpha")
		a := f.adapter("bad-origin")
		_, e := a.Fresh(context.Background(), FreshOptions{Endpoint: f.endpoint, Remembered: remembered(dir, checkoutcontext.Endpoint{Explicit: "not-an-origin", Supplied: true})})
		var refusal *AdoptionError
		if !errors.As(e, &refusal) || refusal.Code != "invalid_endpoint" {
			t.Fatalf("got %v", e)
		}
		if _, e = a.Read(); !errors.Is(e, fs.ErrNotExist) {
			t.Fatal("an association was saved")
		}
	})
}

func isProject(got *string, want string) bool { return got != nil && *got == want }

func mustResolve(t *testing.T, dir string) checkoutcontext.Checkout {
	t.Helper()
	c, e := checkoutcontext.Resolve(dir)
	if e != nil {
		t.Fatal(e)
	}
	return c
}

// A subagent's project scope comes from its coordinator: it never adopts the
// checkout's preference, and never reads it.
func TestSubagentNeverAdoptsARememberedProject(t *testing.T) {
	f := newAdoption(t)
	f.project(f.endpoint, "feat/alpha")
	beta := f.project(f.endpoint, "feat/beta")
	dir := f.checkout()
	f.remember(dir, f.endpoint, "feat/alpha")
	ctx := context.Background()
	parent, e := f.adapter("parent").Fresh(ctx, FreshOptions{Endpoint: f.endpoint, Project: "feat/beta"})
	if e != nil {
		t.Fatal(e)
	}
	contexts0 := f.seen.count(contexts)

	bare, e := f.adapter("bare").Subagent(ctx, "parent", FreshOptions{Endpoint: f.endpoint, Remembered: remembered(dir, noEndpoint)})
	if e != nil {
		t.Fatal(e)
	}
	if bare.Project != nil || *bare.Selection != (Selection{Source: SourceNone}) || *bare.Parent != "parent" {
		t.Fatalf("subagent without a project: %+v selection %+v", bare, bare.Selection)
	}
	scoped, e := f.adapter("scoped").Subagent(ctx, "parent", FreshOptions{Endpoint: f.endpoint, Project: "feat/beta", Remembered: remembered(dir, noEndpoint)})
	if e != nil {
		t.Fatal(e)
	}
	if !isProject(scoped.Project, beta) || *scoped.Selection != (Selection{Source: SourceExplicit}) {
		t.Fatalf("subagent with a project: %+v selection %+v", scoped, scoped.Selection)
	}
	if *bare.Client == *parent.Client || *scoped.Client == *parent.Client || *bare.Client == *scoped.Client {
		t.Fatal("subagents share a client")
	}
	if f.seen.count(contexts) != contexts0 {
		t.Fatal("a subagent validated the preference")
	}
	// Not even a damaged preference is read.
	if e = os.WriteFile(f.preferenceFile(dir), []byte("{not json"), 0o600); e != nil {
		t.Fatal(e)
	}
	if _, e = f.adapter("damaged").Subagent(ctx, "parent", FreshOptions{Endpoint: f.endpoint, Remembered: remembered(dir, noEndpoint)}); e != nil {
		t.Fatalf("a damaged preference blocked a subagent: %v", e)
	}
}

// Resume keeps the session as it was: a later change to the preference, or its
// loss, neither redirects the active conversation nor makes resume fail.
func TestResumeIgnoresLaterPreferenceChanges(t *testing.T) {
	f := newAdoption(t)
	alpha := f.project(f.endpoint, "feat/alpha")
	f.project(f.endpoint, "feat/beta")
	dir := f.checkout()
	f.remember(dir, f.endpoint, "feat/alpha")
	ctx := context.Background()
	a := f.adapter("active")
	first, e := a.Fresh(ctx, FreshOptions{Endpoint: f.endpoint, Remembered: remembered(dir, noEndpoint)})
	if e != nil {
		t.Fatal(e)
	}

	f.remember(dir, f.endpoint, "feat/beta") // the user remembers another project
	resumed, e := a.Resume(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if *resumed.Client != *first.Client || resumed.Session != first.Session || !isProject(resumed.Project, alpha) || *resumed.Selection != *first.Selection {
		t.Fatalf("resume changed the session: %+v selection %+v", resumed, resumed.Selection)
	}
	if got := projectOf(t, a); got == nil || *got != alpha {
		t.Fatalf("the active conversation was redirected to %v", got)
	}

	if e = os.WriteFile(f.preferenceFile(dir), []byte("{not json"), 0o600); e != nil {
		t.Fatal(e)
	}
	contexts0, connects0 := f.seen.count(contexts), f.seen.count(connects)
	if again, e := a.Resume(ctx); e != nil || *again.Client != *first.Client || !isProject(again.Project, alpha) {
		t.Fatalf("resume with a damaged preference: %+v %v", again, e)
	}
	if f.seen.count(contexts) != contexts0 {
		t.Fatal("resume validated the preference")
	}
	if f.seen.count(connects) != connects0+1 { // resume reconnects the recorded client
		t.Fatalf("connect requests: %d, was %d", f.seen.count(connects), connects0)
	}
	// Fresh for a runtime that has a session is still refused, whatever the preference says.
	if _, e = a.Fresh(ctx, FreshOptions{Endpoint: f.endpoint, Remembered: remembered(dir, noEndpoint)}); !errors.Is(e, ErrAlreadyAssociated) {
		t.Fatalf("fresh for an associated runtime: %v", e)
	}
}

// A pending association from an interrupted connect keeps its provenance, and
// is neither validated again nor replaced by a second fresh, even when the
// preference has since gone stale.
func TestPendingAssociationIsPreserved(t *testing.T) {
	f := newAdoption(t)
	alpha := f.project(f.endpoint, "feat/alpha")
	dir := f.checkout()
	f.remember(dir, f.endpoint, "feat/alpha")
	f.seen.stall.Store(true)
	a := f.adapter("interrupted")
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()

	pending, e := a.Fresh(ctx, FreshOptions{Endpoint: f.endpoint, Remembered: remembered(dir, noEndpoint)})
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("want the deadline, got %v", e)
	}
	if pending.Status != "pending" || pending.Selection == nil || pending.Selection.Source != SourceRemembered || pending.Selection.ProjectID != alpha {
		t.Fatalf("pending association: %+v selection %+v", pending, pending.Selection)
	}
	record, _, _ := a.Paths()
	kept := readFile(t, record)
	if !strings.Contains(kept, `"project_selection"`) || !strings.Contains(kept, `"remembered"`) {
		t.Fatalf("the pending record lacks its provenance:\n%s", kept)
	}

	if e = os.WriteFile(f.preferenceFile(dir), []byte("{not json"), 0o600); e != nil {
		t.Fatal(e)
	}
	contexts0 := f.seen.count(contexts)
	if _, e = a.Fresh(context.Background(), FreshOptions{Endpoint: f.endpoint, Remembered: remembered(dir, noEndpoint)}); !errors.Is(e, ErrAlreadyAssociated) {
		t.Fatalf("a second fresh must report the association, got %v", e)
	}
	var refusal *AdoptionError
	if errors.As(e, &refusal) {
		t.Fatal("a pending association was judged by the preference")
	}
	if readFile(t, record) != kept {
		t.Fatal("the pending association changed")
	}
	if f.seen.count(contexts) != contexts0 {
		t.Fatal("the preference was validated for a runtime that is already associated")
	}
}

// Without a directory the process's working directory is the checkout, and an
// explicit project does not need one.
func TestFreshUsesTheWorkingDirectoryWhenNoneIsGiven(t *testing.T) {
	f := newAdoption(t)
	alpha := f.project(f.endpoint, "feat/alpha")
	dir := f.checkout()
	f.remember(dir, f.endpoint, "feat/alpha")
	t.Chdir(dir)

	r, e := f.adapter("cwd").Fresh(context.Background(), FreshOptions{Endpoint: f.endpoint, Remembered: &Remembered{}})
	if e != nil || !isProject(r.Project, alpha) || r.Selection.Checkout != dir {
		t.Fatalf("working directory: %+v %v", r, e)
	}
	gone := f.checkout()
	t.Chdir(gone)
	if e = os.Remove(gone); e != nil {
		t.Fatal(e)
	}
	if _, e = f.adapter("cwd-gone").Fresh(context.Background(), FreshOptions{Endpoint: f.endpoint, Remembered: &Remembered{}}); e == nil {
		t.Fatal("a checkout that cannot be found was treated as having no preference")
	}
	if _, e = f.adapter("cwd-gone").Read(); !errors.Is(e, fs.ErrNotExist) {
		t.Fatalf("an association was saved: %v", e)
	}
	explicit, e := f.adapter("cwd-gone-explicit").Fresh(context.Background(), FreshOptions{Endpoint: f.endpoint, Project: "feat/alpha", Remembered: &Remembered{}})
	if e != nil || !isProject(explicit.Project, alpha) {
		t.Fatalf("an explicit project needs no checkout: %+v %v", explicit, e)
	}
}
