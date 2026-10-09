package projectadmin_test

import (
	"context"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/claims"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/edit"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/lifecycle"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/projectadmin"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/quick"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

var ctx = context.Background()

const previews = "POST /v1/transaction-previews"

// fixture is a temporary service (title prefix "test") with an agent
// client and a connected lit-tui lifecycle, over project test/admin.
type fixture struct {
	t     *testing.T
	srv   *tuitest.Server
	agent *litclient.Session
	l     *lifecycle.Lifecycle
	state string
	id    string // test/admin
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	srv := tuitest.Service(t)
	_, agent := tuitest.InstanceAs(t, t.TempDir(), srv.URL, protocol.Actor{Name: "Claude", Kind: "agent"})
	tuitest.Apply(t, agent, litclient.CreateProject("test/admin", "the spec\n", "https://a.example/one"))
	state := t.TempDir()
	l, err := lifecycle.New(lifecycle.Config{StateDir: state, Endpoint: srv.URL, HTTP: &http.Client{}, Actor: protocol.Actor{Name: "person", Kind: "human"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		l.Shutdown("test")
		<-l.Done()
	})
	f := &fixture{t: t, srv: srv, agent: agent, l: l, state: state}
	f.id = f.project("test/admin").ID
	return f
}

func (f *fixture) project(ref string) protocol.Project {
	f.t.Helper()
	got, err := litclient.Get[protocol.Project](ctx, f.agent, "", ref)
	if err != nil || len(got) != 1 {
		f.t.Fatalf("project %s: %v %v", ref, got, err)
	}
	return got[0]
}

func (f *fixture) env() quick.Env {
	p := f.project(f.id)
	return quick.Env{Gate: f.l, Project: p.ID, ProjectTitle: p.Title}
}

// form drives one quick model the way the browser does: keys, then the
// commands they return, until the model has no more of its own messages.
type form struct {
	t    *testing.T
	m    *quick.Model
	km   keys.Keymap
	last quick.Result
	out  []tea.Msg // messages that are not the model's own
}

func (x *form) run(r quick.Result) {
	x.t.Helper()
	x.last = r
	for cmd := r.Cmd; cmd != nil; {
		msg := cmd()
		next, own := x.m.Update(msg)
		if !own {
			x.out = append(x.out, msg)
			return
		}
		x.last, cmd = next, next.Cmd
	}
}

func (x *form) press(ks ...string) {
	x.t.Helper()
	for _, k := range ks {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "home":
			msg = tea.KeyPressMsg{Code: tea.KeyHome}
		case "ctrl+u":
			msg = tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
		default:
			msg = tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
		}
		x.run(x.m.Key(msg, x.km))
	}
}

func (x *form) typeText(s string) {
	x.t.Helper()
	for _, r := range s {
		x.press(string(r))
	}
}

func (x *form) view() string {
	return strings.Join(x.m.Lines(cells.Default, 200, 40, true), "\n")
}

func (x *form) expect(what string, ok bool) {
	x.t.Helper()
	if !ok {
		x.t.Fatalf("%s; form:\n%s", what, x.view())
	}
}

func (x *form) has(s ...string) bool {
	v := x.view()
	for _, want := range s {
		if !strings.Contains(v, want) {
			return false
		}
	}
	return true
}

// The local title check agrees with lit-server's: for listed and random
// titles, ValidTitle accepts exactly the titles whose creation the service
// previews, and refuses the others as validation_failed.
func TestValidTitleAgreesWithTheService(t *testing.T) {
	f := newFixture(t)
	titles := []string{
		"test/a", "test/a/b", "test/a-b_c", "test/1a", "test/a_", "test/" + strings.Repeat("a", projectadmin.MaxTitle-5),
		"", "test", "test/", "/a", "test//a", "test/a/", "test/-a", "test/_a", "Test/a", "nope/a", "test/a b", "test/é",
		"test/" + strings.Repeat("a", projectadmin.MaxTitle-4), "test/a\x1b", "a/test", "test/a.b",
	}
	r := rand.New(rand.NewPCG(1, 2))
	alphabet := []rune("test/ab-_9Zé .")
	for range 150 {
		n := r.IntN(14)
		var b strings.Builder
		if r.IntN(2) == 0 {
			b.WriteString("test/")
		}
		for range n {
			b.WriteRune(alphabet[r.IntN(len(alphabet))])
		}
		titles = append(titles, b.String())
	}
	for _, title := range slices.Compact(slices.Sorted(slices.Values(titles))) {
		local := projectadmin.ValidTitle(title, []string{"test"})
		_, err := f.agent.Prepare(ctx, litclient.CreateProject(title, ""))
		if (local == nil) != (err == nil) {
			t.Errorf("%q: local check %v, service %v", title, local, err)
		}
		if local != nil && litclient.Code(local) != "validation_failed" {
			t.Errorf("%q: local code %q", title, litclient.Code(local))
		}
	}
}

// An invalid title is refused with the syntax before anything is
// previewed; a valid one previews the creation, and applying commits
// exactly that preview and delivers the new project.
func TestNewProjectChecksTheTitleBeforeThePreviewThenCreatesIt(t *testing.T) {
	f := newFixture(t)
	prefixes, err := projectadmin.Prefixes(ctx, f.l)
	if err != nil || !slices.Equal(prefixes, []string{"test"}) {
		t.Fatalf("prefixes %v %v", prefixes, err)
	}
	x := &form{t: t, m: projectadmin.NewProject(quick.Env{Gate: f.l}, prefixes), km: keys.Default()}
	x.expect("the field", x.has("New project", "allowed prefixes test", "Title: "))
	before := f.srv.Count(previews)
	for _, bad := range []struct{ title, note string }{
		{"nope/x", "“nope” is not an allowed prefix"},
		{"test", "prefix/name"},
		{"test/-x", "“-x”"},
	} {
		x.press("ctrl+u")
		x.typeText(bad.title)
		x.press("enter")
		x.expect(bad.title+" refused", x.last.Cmd == nil && x.m.Typing() && x.has(bad.note))
	}
	if n := f.srv.Count(previews); n != before {
		t.Fatalf("an invalid title was previewed: %d previews", n-before)
	}
	x.press("ctrl+u")
	x.typeText("test/new-project")
	x.press("enter")
	x.expect("the preview", f.srv.Count(previews) == before+1 && x.has("Create the project “test/new-project”", "project “test/new-project”", "Nothing is written until you apply it"))
	x.press("enter")
	x.expect("applied", x.last.Closed && x.last.Note == "Applied: Create the project “test/new-project”.")
	if len(x.out) != 1 {
		t.Fatalf("after the commit: %#v", x.out)
	}
	created, ok := x.out[0].(projectadmin.Created)
	got := f.project("test/new-project")
	if !ok || created.Err != nil || created.Project.ID != got.ID || got.Description != "" || len(got.RepositoryRefs) != 0 {
		t.Fatalf("created %+v, service %+v", x.out[0], got)
	}
}

// A title the service refuses for another reason (it exists already) is
// refused by the preview: nothing is written.
func TestNewProjectWithATakenTitleIsRefusedByThePreview(t *testing.T) {
	f := newFixture(t)
	x := &form{t: t, m: projectadmin.NewProject(quick.Env{Gate: f.l}, []string{"test"}), km: keys.Default()}
	x.typeText("test/admin")
	x.press("enter")
	x.expect("refused", x.has("Refused (validation_failed)") && x.m.Hint(x.km) == "esc close")
	x.press("enter")
	x.expect("still refused", !x.last.Closed && len(x.out) == 0)
}

// The repository list changes through an exact preview fenced at the
// revision it was read at: the current repositories are checked, typing
// adds one, and the commit writes exactly the previewed change.
func TestRepositoriesChangeThroughAnExactPreview(t *testing.T) {
	f := newFixture(t)
	p, err := projectadmin.Read(ctx, f.l, f.id)
	if err != nil {
		t.Fatal(err)
	}
	x := &form{t: t, m: projectadmin.Repositories(f.env(), p), km: keys.Default()}
	x.expect("the picker", x.has("Repositories of “test/admin”", "[x] https://a.example/one"))
	x.press("enter") // uncheck the current one
	x.typeText("https://b.example/two")
	x.expect("the new value offered", x.has("add the repository “https://b.example/two”"))
	x.press("enter", "home", "enter")
	x.expect("the preview", x.has("Change the repositories of “test/admin”: +https://b.example/two −https://a.example/one",
		"project “test/admin” · revision "+strconv.FormatInt(p.Revision, 10)))
	x.press("enter")
	x.expect("applied", x.last.Closed && strings.HasPrefix(x.last.Note, "Applied: Change the repositories"))
	got := f.project(f.id)
	if !slices.Equal(got.RepositoryRefs, []string{"https://b.example/two"}) || got.Revision != p.Revision+1 || got.Description != "the spec\n" {
		t.Fatalf("project %+v", got)
	}
}

// Without a change there is nothing to preview; a change of the project
// after the preview refuses the commit, and nothing is written.
func TestARepositoryChangeAfterARevisionChangeIsRefused(t *testing.T) {
	f := newFixture(t)
	p, err := projectadmin.Read(ctx, f.l, f.id)
	if err != nil {
		t.Fatal(err)
	}
	x := &form{t: t, m: projectadmin.Repositories(f.env(), p), km: keys.Default()}
	x.press("home", "enter")
	x.expect("nothing to change", x.last.Cmd == nil && x.has("Nothing changes"))
	x.typeText("https://c.example/three")
	x.press("enter", "home", "enter")
	x.expect("the preview", x.has("+https://c.example/three"))
	desc := "the agent's spec\n"
	tuitest.Apply(t, f.agent, litclient.UpdateProject(f.id, 0, litclient.ProjectChange{Description: &desc}))
	x.press("enter")
	x.expect("refused", !x.last.Closed && x.has("Refused (revision_conflict)"))
	if got := f.project(f.id); !slices.Equal(got.RepositoryRefs, []string{"https://a.example/one"}) || got.Description != desc {
		t.Fatalf("project %+v", got)
	}
}

// The specification is edited through a project_spec draft: no claim, the
// draft holds exactly the description at the project's revision, applying
// commits it at that revision, and a change meanwhile keeps the draft.
func TestTheSpecificationIsEditedThroughAnOptimisticDraft(t *testing.T) {
	f := newFixture(t)
	h := claims.Start(f.l, claims.Config{})
	e := edit.New(f.l, h, drafts.New(f.state))
	projectadmin.Register(e)
	p := f.project(f.id)
	target := projectadmin.Spec(p)
	if target.Kind != drafts.ProjectSpec || target.Record != p.ID || target.Project != p.ID || target.Issue != "" {
		t.Fatalf("target %+v", target)
	}
	s := e.Open(ctx, target)
	if s.Err != nil || s.Kind != drafts.ClaimNone || s.Draft.Base != "the spec\n" || s.Draft.Revision != p.Revision || s.Draft.Title != "test/admin" ||
		!strings.HasPrefix(filepath.Base(s.Draft.Path()), "spec-test-admin--") {
		t.Fatalf("open %+v (%s)", s, s.Draft.Path())
	}
	if len(h.Snapshot().Holds) != 0 {
		t.Fatalf("a specification draft took a claim: %+v", h.Snapshot())
	}
	write(t, s.Draft, "my spec\n")
	pv := e.Preview(ctx, s.Draft)
	if !pv.Ready() || !pv.Exempt || pv.Record.Revision != p.Revision {
		t.Fatalf("preview %+v", pv)
	}
	desc := "the agent's spec\n"
	tuitest.Apply(t, f.agent, litclient.UpdateProject(f.id, 0, litclient.ProjectChange{Description: &desc}))
	if a := e.Apply(ctx, pv); a.Err == nil {
		t.Fatalf("applied over a newer revision: %+v", a)
	}
	if c := e.Preview(ctx, s.Draft); !c.Conflict || c.Ready() {
		t.Fatalf("conflict %+v", c)
	}
	if d, ok, err := e.ExistingFor(target); err != nil || !ok || read(t, d) != "my spec\n" {
		t.Fatalf("the draft was not kept: %+v %v %v", d, ok, err)
	}
	d, err := e.Reload(ctx, s.Draft)
	if err != nil {
		t.Fatal(err)
	}
	if a := e.Apply(ctx, e.Preview(ctx, d)); a.Err != nil {
		t.Fatalf("apply %+v", a)
	}
	if got := f.project(f.id); got.Description != "my spec\n" || got.Revision != p.Revision+2 {
		t.Fatalf("project %+v", got)
	}
	if _, ok, _ := e.ExistingFor(target); ok {
		t.Fatal("the applied draft was kept")
	}
}

func write(t *testing.T, d drafts.Draft, s string) {
	t.Helper()
	if err := os.WriteFile(d.Path(), []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, d drafts.Draft) string {
	t.Helper()
	b, err := os.ReadFile(d.Path())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The list is fenced at the revision it was read at: a change of the
// project before the preview refuses the preview, and nothing is written.
func TestARepositoryChangeIsFencedAtTheReadRevision(t *testing.T) {
	f := newFixture(t)
	p, err := projectadmin.Read(ctx, f.l, f.id)
	if err != nil {
		t.Fatal(err)
	}
	x := &form{t: t, m: projectadmin.Repositories(f.env(), p), km: keys.Default()}
	desc := "the agent's spec\n"
	tuitest.Apply(t, f.agent, litclient.UpdateProject(f.id, 0, litclient.ProjectChange{Description: &desc}))
	x.press("enter", "home", "enter")
	x.expect("refused by the preview", x.has("−https://a.example/one", "Refused (revision_conflict)") && x.m.Hint(x.km) == "esc close")
	if got := f.project(f.id); !slices.Equal(got.RepositoryRefs, []string{"https://a.example/one"}) {
		t.Fatalf("project %+v", got)
	}
}
