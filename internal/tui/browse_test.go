package tui

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/detail"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/lifecycle"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/prefs"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tree"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// timers records the browser's refresh timers instead of sleeping, so tests
// fire them when they choose.
type timers struct {
	mu      sync.Mutex
	pending []timer
}

type timer struct {
	d   time.Duration
	msg tea.Msg
}

func (tm *timers) after(d time.Duration, msg tea.Msg) tea.Cmd {
	tm.mu.Lock()
	tm.pending = append(tm.pending, timer{d, msg})
	tm.mu.Unlock()
	return nil
}

// last returns the most recently scheduled timer.
func (tm *timers) last(t *testing.T) timer {
	t.Helper()
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if len(tm.pending) == 0 {
		t.Fatal("no timer scheduled")
	}
	return tm.pending[len(tm.pending)-1]
}

type browsing struct {
	t   *testing.T
	m   tea.Model
	l   *lifecycle.Lifecycle
	tm  *timers
	srv *tuitest.Server
}

// startBrowsing starts the root model with a connected lifecycle against srv at
// w×h and runs its start-up commands.
func startBrowsing(t *testing.T, srv *tuitest.Server, w, h int, mouse bool) *browsing {
	t.Helper()
	l, err := lifecycle.New(lifecycle.Config{StateDir: t.TempDir(), Endpoint: srv.URL, HTTP: &http.Client{}, Actor: protocol.Actor{Name: "person", Kind: "human"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		l.Shutdown("test")
		<-l.Done()
	})
	p := prefs.Default()
	p.Theme, p.Mouse = prefs.ThemeDark, mouse
	tm := &timers{}
	m := NewModel(srv.URL).WithPrefs(p).WithLifecycle(l).withTimers(tm.after)
	b := &browsing{t: t, l: l, tm: tm, srv: srv}
	b.m = drive(t, m, m.Init(), tea.WindowSizeMsg{Width: w, Height: h})
	return b
}

func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	}
	return tea.KeyPressMsg{Code: rune(k[0]), Text: k}
}

func (b *browsing) press(ks ...string) {
	b.t.Helper()
	for _, k := range ks {
		b.m = drive(b.t, b.m, nil, keyMsg(k))
	}
}

func (b *browsing) send(msgs ...tea.Msg) { b.t.Helper(); b.m = drive(b.t, b.m, nil, msgs...) }

func (b *browsing) view() string { return plainView(b.m) }

// line returns the first screen line containing s, or "".
func (b *browsing) line(s string) string {
	for _, l := range strings.Split(b.view(), "\n") {
		if strings.Contains(l, s) {
			return l
		}
	}
	return ""
}

func (b *browsing) header() string { return strings.Split(b.view(), "\n")[0] }

// crumb is the breadcrumb line under the header.
func (b *browsing) crumb() string { return strings.Split(b.view(), "\n")[1] }

// body is the screen without the header and breadcrumb.
func (b *browsing) body() []string { return strings.Split(b.view(), "\n")[2:] }

// bodyLine returns the first body line containing s, or "".
func (b *browsing) bodyLine(s string) string {
	for _, l := range b.body() {
		if strings.Contains(l, s) {
			return l
		}
	}
	return ""
}

// treeLine returns the first tree-pane line containing s, or "".
func (b *browsing) treeLine(s string) string {
	for _, l := range strings.Split(b.tree(), "\n") {
		if strings.Contains(l, s) {
			return l
		}
	}
	return ""
}

// tree is the text of the tree pane: the body left of the separator.
func (b *browsing) tree() string {
	var out []string
	for _, l := range b.body() {
		left, _, _ := strings.Cut(l, "│")
		out = append(out, left)
	}
	return strings.Join(out, "\n")
}

func (b *browsing) expect(what string, ok bool) {
	b.t.Helper()
	if !ok {
		b.t.Fatalf("%s; screen:\n%s", what, b.view())
	}
}

// seed creates project with a root, a child, a grandchild and a second root,
// and an agent client that claims the child.
func seed(t *testing.T, srv *tuitest.Server, project string) (s *litclient.Session, root, child, grand string) {
	t.Helper()
	_, s = tuitest.InstanceAs(t, t.TempDir(), srv.URL, protocol.Actor{Name: "Claude", Kind: "agent"})
	tuitest.Apply(t, s, litclient.CreateProject(project, "spec"))
	root = tuitest.CreateIssues(t, s, project, "", "Root issue")[0]
	child = tuitest.CreateIssues(t, s, project, root, "Child issue")[0]
	grand = tuitest.CreateIssues(t, s, project, child, "Grandchild issue")[0]
	tuitest.CreateIssues(t, s, project, "", "Second root")
	if _, err := s.AcquireClaims(context.Background(), litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{child}}}); err != nil {
		t.Fatal(err)
	}
	return s, root, child, grand
}

// The only project opens at once: its tree loads with a consistent count,
// deep branches expand, the breadcrumb names the ancestors and claims name
// their owner.
func TestBrowseTheTreeWithBreadcrumbsAndClaimOwners(t *testing.T) {
	srv := tuitest.Service(t)
	seed(t, srv, "test/one")
	b := startBrowsing(t, srv, 120, 30, false)
	b.expect("tree and count", strings.Contains(b.tree(), "Root issue") && strings.Contains(b.tree(), "Second root") &&
		strings.Contains(b.header(), "test/one · 4 issues") && !strings.Contains(b.tree(), "Child issue"))
	b.press("right", "down")
	b.expect("claim owner in the tree", strings.Contains(b.treeLine("Child issue"), "⚑ Claude"))
	b.expect("breadcrumb", strings.HasPrefix(b.crumb(), "test/one › Root issue › Child issue"))
	b.expect("claim owner in the detail", b.bodyLine("Claim: Claude (agent), until") != "")
	b.press("right", "right")
	b.expect("deep breadcrumb", strings.Contains(b.crumb(), "Root issue › Child issue › Grandchild issue"))
}

// Wide terminals show the detail beside the tree; narrow ones open it on
// Enter and return to the same selection on Escape.
func TestAdaptiveLayout(t *testing.T) {
	srv := tuitest.Service(t)
	seed(t, srv, "test/one")
	b := startBrowsing(t, srv, 120, 30, false)
	b.expect("wide: tree and detail side by side", strings.Contains(b.bodyLine("Root issue"), "│") && strings.Contains(b.bodyLine("State: open"), "│"))
	b.press("enter")
	b.expect("wide: detail focused, tree still shown", strings.Contains(b.tree(), "Second root") && strings.Contains(b.view(), "esc back"))
	b.press("esc", "down")
	b.expect("wide: tree focused again", strings.HasSuffix(strings.TrimSpace(b.crumb()), "Second root"))

	b.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	b.expect("narrow: tree only", b.bodyLine("Second root") != "" && b.bodyLine("State: open") == "" && !strings.Contains(b.view(), "│"))
	b.press("enter")
	b.expect("narrow: detail full screen", b.bodyLine("State: open") != "" && b.bodyLine("Root issue") == "" &&
		strings.HasSuffix(strings.TrimSpace(b.crumb()), "Second root"))
	b.press("esc")
	b.expect("narrow: back to the same selection", b.bodyLine("Root issue") != "" && strings.HasSuffix(strings.TrimSpace(b.crumb()), "Second root"))
}

// The switcher lists every project; switching back to a project keeps its
// previous selection.
func TestSwitchingProjectsKeepsEachSelection(t *testing.T) {
	srv := tuitest.Service(t)
	_, s := tuitest.Instance(t, t.TempDir(), srv.URL)
	tuitest.Apply(t, s, litclient.CreateProject("test/a", "spec"))
	tuitest.Apply(t, s, litclient.CreateProject("test/b", "spec"))
	tuitest.CreateIssues(t, s, "test/a", "", "A one", "A three", "A two")
	tuitest.CreateIssues(t, s, "test/b", "", "B one")
	b := startBrowsing(t, srv, 100, 24, false)
	b.expect("the switcher opens with several projects", b.line("Switch project") != "" && b.line("test/a") != "" && b.line("test/b") != "")
	b.press("enter")
	b.expect("test/a chosen", b.bodyLine("A one") != "" && strings.Contains(b.header(), "test/a"))
	b.press("down", "down")
	b.expect("A two selected", strings.HasSuffix(strings.TrimSpace(b.crumb()), "A two"))
	b.press("p")
	b.expect("the current project is marked", strings.Contains(b.bodyLine("test/a"), "(current)"))
	b.press("down", "enter")
	b.expect("test/b loaded", b.bodyLine("B one") != "" && b.bodyLine("A one") == "")
	b.press("p", "up", "enter")
	b.expect("A two selected again", b.bodyLine("A one") != "" && strings.HasSuffix(strings.TrimSpace(b.crumb()), "A two"))
	b.press("p", "esc")
	b.expect("cancel keeps the project", b.line("A one") != "")
}

// Refresh keeps the selection by UUID, is coalesced while a load runs, and
// background refresh pauses while a mutation runs.
func TestRefreshIsCoalescedAndPausesForMutations(t *testing.T) {
	srv := tuitest.Service(t)
	s, _, _, _ := seed(t, srv, "test/one")
	b := startBrowsing(t, srv, 120, 30, false)
	b.press("down")
	b.expect("Second root selected", strings.HasSuffix(strings.TrimSpace(b.crumb()), "Second root"))
	reads := srv.Count("POST /v1/issue-summaries")
	tuitest.CreateIssues(t, s, "test/one", "", "Added later")

	// Two presses while the first refresh runs give one more load, not two.
	m, first := b.m.Update(keyMsg("r"))
	m, second := m.Update(keyMsg("r"))
	m, third := m.Update(keyMsg("r"))
	if first == nil || second != nil || third != nil {
		t.Fatalf("refresh commands: %v %v %v", first != nil, second != nil, third != nil)
	}
	b.m = drive(t, m, first)
	if got := srv.Count("POST /v1/issue-summaries") - reads; got != 2 {
		t.Fatalf("%d reads for three refresh presses, want 2", got)
	}
	b.expect("the new issue is shown and the selection kept", strings.Contains(b.tree(), "Added later") &&
		strings.HasSuffix(strings.TrimSpace(b.crumb()), "Second root") && strings.Contains(b.header(), "5 issues"))

	// The background timer is rare and refreshes the project.
	tick := b.tm.last(t)
	if tick.d < 30*time.Second {
		t.Fatalf("background refresh every %v", tick.d)
	}
	reads = srv.Count("POST /v1/issue-summaries")
	b.send(tick.msg)
	if srv.Count("POST /v1/issue-summaries") != reads+1 {
		t.Fatal("the background timer did not refresh")
	}

	// While a mutation runs, the timer does not load and says it waits.
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- b.l.Mutate(context.Background(), func(context.Context, *litclient.Session) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	reads = srv.Count("POST /v1/issue-summaries")
	b.send(b.tm.last(t).msg)
	b.press("r")
	if srv.Count("POST /v1/issue-summaries") != reads {
		t.Fatal("refreshed during a mutation")
	}
	b.expect("waiting note", strings.Contains(b.header(), "waits"))
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	retry := b.tm.last(t)
	if retry.d > 2*time.Second {
		t.Fatalf("retry after %v", retry.d)
	}
	b.send(retry.msg)
	if srv.Count("POST /v1/issue-summaries") != reads+1 || strings.Contains(b.header(), "waits") {
		t.Fatal("the deferred refresh did not run after the mutation")
	}
}

// A project too large for one bounded read loads in pages, says the result
// is partial and shows no count.
func TestPartialLoadsShowAnIndicatorAndNoTotal(t *testing.T) {
	limits := protocol.DefaultLimits()
	limits.SnapshotBytes, limits.MaxPage, limits.DefaultPage = 8000, 8, 8
	srv := tuitest.ServiceWithLimits(t, limits)
	_, s := tuitest.Instance(t, t.TempDir(), srv.URL)
	tuitest.Apply(t, s, litclient.CreateProject("test/big", "spec"))
	for i := 0; i < 15; i++ {
		tuitest.CreateIssues(t, s, "test/big", "", "Issue "+string(rune('a'+i))+"1", "Issue "+string(rune('a'+i))+"2")
	}
	b := startBrowsing(t, srv, 120, 40, false)
	header := b.header()
	b.expect("partial indicator without a total", strings.Contains(header, "partial") && strings.Contains(header, "may have changed between pages") &&
		!strings.Contains(header, "issues") && strings.Contains(b.tree(), "Issue o2"))
}

// With the mouse preference, clicks select rows and the wheel moves the
// selection.
func TestMouseSelectsAndScrollsTheTree(t *testing.T) {
	srv := tuitest.Service(t)
	seed(t, srv, "test/one")
	b := startBrowsing(t, srv, 120, 30, true)
	row := -1
	for i, l := range strings.Split(b.view(), "\n") {
		if strings.Contains(l, "Second root") {
			row = i
		}
	}
	b.send(tea.MouseClickMsg{X: 4, Y: row, Button: tea.MouseLeft})
	b.expect("click selects", strings.HasSuffix(strings.TrimSpace(b.crumb()), "Second root"))
	b.send(tea.MouseWheelMsg{X: 4, Y: row, Button: tea.MouseWheelUp})
	b.expect("wheel moves the selection", strings.HasSuffix(strings.TrimSpace(b.crumb()), "Root issue"))
}

// The action menu lists the context's commands with their effective keys
// and runs the chosen one.
func TestActionMenuRunsCommands(t *testing.T) {
	srv := tuitest.Service(t)
	seed(t, srv, "test/one")
	b := startBrowsing(t, srv, 120, 30, false)
	b.press("m")
	b.expect("menu entries with keys", b.line("switch project") != "" && strings.Contains(b.line("switch project"), "p") && b.line("refresh") != "")
	for b.line("switch project") != "" && !strings.HasPrefix(strings.TrimSpace(b.line("switch project")), ">") && !selectedRow(b.m, "switch project") {
		b.press("down")
	}
	b.press("enter")
	b.expect("the switcher opened", b.line("Switch project") != "")
	b.press("esc", "m", "esc")
	b.expect("menu closed", strings.Contains(b.tree(), "Root issue") && b.bodyLine("Actions") == "")
}

// selectedRow reports whether the line containing s is drawn selected
// (reverse video).
func selectedRow(m tea.Model, s string) bool {
	for _, l := range strings.Split(m.View().Content, "\n") {
		if strings.Contains(l, s) {
			return strings.Contains(l, "\x1b[7m")
		}
	}
	return false
}

// Tracker text in the metadata is escaped: it renders literally, never as
// Markdown.
func TestIssueMetadataRendersTrackerTextLiterally(t *testing.T) {
	i := protocol.IssueSummary{ID: "11111111-2222-4333-8444-555555555555", Revision: 3, Title: "*bold* [link](http://x) `code` # h | t\nnext",
		State: "open", Labels: []string{"a_b_c", "x*y*z"}, ChildIDs: []string{}, UpdatedAt: "2026-10-09T10:00:00Z"}
	md := issueMarkdown(i, &protocol.Project{Title: "test/p"}, nil)
	p := prefs.Default()
	p.Theme = prefs.ThemeDark
	m := NewModel("http://127.0.0.1:1").WithPrefs(p).WithDocument(detail.Section{RecordID: i.ID, Revision: 3, Content: md})
	view := plainView(drive(t, m, m.Init(), tea.WindowSizeMsg{Width: 100, Height: 20}))
	for _, want := range []string{"*bold* [link](http://x) `code` # h | t next", "Labels: a_b_c, x*y*z", "Updated: 2026-10-09T10:00:00Z (revision 3)", i.ID, "in test/p"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
}

// The metadata names children in tree order (not UUID order), at most
// eight, with a count, and children that are not loaded by short ID.
func TestIssueMetadataListsChildrenInTreeOrder(t *testing.T) {
	parent := "p0000000-0000-4000-8000-000000000000"
	sum := func(id, title, created string) protocol.IssueSummary {
		return protocol.IssueSummary{ID: id, Title: title, State: "open", CreatedAt: created, ParentID: &parent, ChildIDs: []string{}, Labels: []string{}}
	}
	var items []protocol.IssueSummary
	var ids []string
	for n := 0; n < 10; n++ {
		id := string(rune('z'-n)) + "0000000-0000-4000-8000-000000000000" // UUID order is the reverse of creation order
		items = append(items, sum(id, "Child "+string(rune('A'+n)), "2026-10-09T10:00:0"+string(rune('0'+n))+"Z"))
		ids = append([]string{id}, ids...)
	}
	p := protocol.IssueSummary{ID: parent, Title: "Parent", State: "open", ChildIDs: append(ids, "00000000-0000-4000-8000-00000000beef"), Labels: []string{}}
	tr := tree.New()
	tr.SetIssues(append(items, p))
	md := issueMarkdown(p, nil, tr)
	want := "**Children:** 11: Child A, Child B, Child C, Child D, Child E, Child F, Child G, Child H, and 3 more"
	if !strings.Contains(md, want) {
		t.Fatalf("children:\n%s", md)
	}
	p.ChildIDs = []string{"00000000-0000-4000-8000-00000000beef", items[1].ID}
	if md := issueMarkdown(p, nil, tr); !strings.Contains(md, "**Children:** 2: Child B, `00000000`") {
		t.Fatalf("unloaded child:\n%s", md)
	}
}
