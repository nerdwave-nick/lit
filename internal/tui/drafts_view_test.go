package tui

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/claims"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/edit"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/lifecycle"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/prefs"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// drafted is a seeded service, with the agent's project test/one, and the
// state directory lit-tui instances share across launches.
type drafted struct {
	t                  *testing.T
	srv                *tuitest.Server
	agent              *litclient.Session
	state              string
	project            string
	root, child, grand string
}

func newDrafted(t *testing.T) *drafted {
	t.Helper()
	srv := tuitest.Service(t)
	agent, root, child, grand := seed(t, srv, "test/one")
	x := &drafted{t: t, srv: srv, agent: agent, state: t.TempDir(), root: root, child: child, grand: grand}
	x.project = x.issue(root).ProjectID
	return x
}

func (x *drafted) issue(id string) protocol.Issue {
	x.t.Helper()
	got, err := litclient.Get[protocol.Issue](context.Background(), x.agent, "test/one", id)
	if err != nil || len(got) != 1 {
		x.t.Fatalf("read %s: %v", id, err)
	}
	return got[0]
}

// draft records a draft of kind for record (owned by issue) as an earlier
// launch left it, at the issue's current revision.
func (x *drafted) draft(kind drafts.Kind, record, issue, title, content string) drafts.Draft {
	x.t.Helper()
	got := x.issue(issue)
	d, err := drafts.New(x.state).Create(drafts.Draft{Kind: kind, ServiceID: x.agent.Meta.ServiceID, ProjectID: x.project, RecordID: record, IssueID: issue,
		Title: title, Revision: got.Revision, Base: got.Body, Claim: drafts.ClaimEdit}, content)
	if err != nil {
		x.t.Fatal(err)
	}
	return d
}

// launch starts a lit-tui instance on the shared state directory, as a new
// launch does, with the simulated editor of editingUI.
func (x *drafted) launch() *editingUI {
	t := x.t
	t.Helper()
	l, err := lifecycle.New(lifecycle.Config{StateDir: x.state, Endpoint: x.srv.URL, HTTP: &http.Client{}, Actor: protocol.Actor{Name: "person", Kind: "human"}})
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
	h := claims.Start(l, claims.Config{})
	store := drafts.New(x.state)
	e := edit.New(l, h, store)
	tm := &timers{}
	b := &browsing{t: t, l: l, tm: tm, srv: x.srv}
	u := &editingUI{claiming: &claiming{browsing: b, h: h, agent: x.agent, root: x.root, child: x.child, grand: x.grand}, e: e, store: store}
	p := prefs.Default()
	p.Theme = prefs.ThemeDark
	m := NewModel(x.srv.URL).WithPrefs(p).WithLifecycle(l).withTimers(tm.after).WithClaims(h).WithEditor(e)
	m.ed.getenv = func(string) string { return "" }
	m.ed.exec = func(cmd *exec.Cmd, done tea.ExecCallback) tea.Cmd {
		return func() tea.Msg {
			u.ran = append(u.ran, cmd.Args)
			return done(os.WriteFile(cmd.Args[len(cmd.Args)-1], []byte(u.content), 0o600))
		}
	}
	m.ed.post = func(string, tea.Msg) {}
	b.m = drive(t, m, m.Init(), tea.WindowSizeMsg{Width: 160, Height: 30})
	return u
}

func (x *drafted) agentEdits(id, body string) {
	x.t.Helper()
	tuitest.Apply(x.t, x.agent, litclient.UpdateIssue("test/one", id, 0, litclient.IssueChange{Body: &body}))
}

// After a relaunch the banner counts this service's unapplied drafts of
// every kind and opens the Drafts view. Drafts are marked in the tree, and
// an issue that changed since its draft is marked and opens as a conflict.
// The view diffs and discards; a kind this version cannot resume is still
// listed and can be discarded.
func TestTheBannerOpensTheDraftsViewAndAChangedRevisionOpensAsAConflict(t *testing.T) {
	x := newDrafted(t)
	root := x.draft(drafts.IssueBody, x.root, x.root, "Root issue", "my root text\n")
	x.draft(drafts.IssueBody, x.child, x.child, "Child issue", "my child text\n")
	x.draft("comment", protocol.UUID(), x.grand, "On the grandchild", "a comment\n")
	other := drafts.Draft{Kind: drafts.IssueBody, ServiceID: protocol.UUID(), ProjectID: x.project, RecordID: x.grand, IssueID: x.grand, Title: "Elsewhere", Revision: 1}
	if _, err := drafts.New(x.state).Create(other, "x"); err != nil {
		t.Fatal(err)
	}
	x.agentEdits(x.root, "The agent's body.\n")
	u := x.launch()
	u.expect("the banner", u.line("3 unapplied drafts · D opens the Drafts view") != "")
	u.press("right", "down", "right")
	u.expect("marks", strings.Contains(u.treeLine("Root issue"), "draft: changed") && strings.Contains(u.treeLine("Child issue"), "draft") &&
		!strings.Contains(u.treeLine("Child issue"), "changed") && strings.Contains(u.treeLine("Grandchild issue"), "comment draft"))
	u.press("D")
	u.expect("the view", u.line("Unapplied drafts · 3") != "" && u.line("Elsewhere") == "" &&
		strings.Contains(u.line("“Root issue”"), "changed: revision") && strings.Contains(u.line("“Root issue”"), "opens as a conflict") &&
		strings.Contains(u.line("“Child issue”"), "issue body · “Child issue” · project “test/one” · revision") &&
		strings.HasPrefix(strings.TrimSpace(u.line("“On the grandchild”")), "comment · “On the grandchild”"))
	u.press("enter") // the root's draft, oldest first
	u.expect("a conflict", u.line("“Root issue” changed since the draft was started: revision") != "" && u.line("the draft is kept") != "")
	u.choose("Diff against the current body")
	u.expect("the diff", u.line("+my root text") != "" && u.line("-The agent's body.") != "")
	u.choose("Keep")
	u.expect("back in the view", u.line("Unapplied drafts · 3") != "")
	u.press("down", "down", "enter")
	u.expect("the comment draft", u.line("The comment draft “On the grandchild”") != "" && u.line("cannot resume or diff comment drafts") != "")
	u.choose("Discard the draft")
	u.choose("Discard")
	u.expect("two left", u.line("Unapplied drafts · 2") != "" && u.line("On the grandchild") == "")
	u.press("esc")
	u.expect("closed, no banner", u.line("Unapplied drafts") == "" && u.line("unapplied drafts ·") == "")
	if _, ok, _ := u.store.Find(x.project, drafts.IssueBody, root.RecordID); !ok {
		t.Fatal("the root's draft was removed without a discard")
	}
	if got := x.issue(x.root); got.Body != "The agent's body.\n" {
		t.Fatalf("the agent's body was overwritten: %q", got.Body)
	}
}

// Story 8 in the model: quitting with an open draft shows one prompt naming
// it. Cancel keeps running, Apply now previews it, and Quit releases the
// claims and keeps the draft; the next launch shows "1 unapplied draft".
func TestQuitWithAnOpenDraftAsksOnceAndKeepsIt(t *testing.T) {
	x := newDrafted(t)
	u := x.launch()
	u.content = "unsaved work\n"
	u.press("e")
	u.expect("the preview", u.line("Apply the draft of “Root issue”?") != "")
	prompt := "1 open draft (“Root issue”). Drafts are kept; claims will be released."
	u.press("q")
	u.expect("the prompt", u.line(prompt) != "" && u.line("[Apply now] [Quit] [Cancel]") != "")
	if u.l.Started() {
		t.Fatal("quit before the answer")
	}
	u.press("esc") // cancel
	u.expect("still running", u.line(prompt) == "" && u.line("Apply the draft of “Root issue”?") != "" && !u.l.Started())
	u.press("esc") // keep the draft for later
	u.press("q")
	u.expect("asked again", u.line(prompt) != "")
	u.press("enter") // apply now
	u.expect("the apply preview", u.line("Apply the draft of “Root issue”?") != "" && u.line("+unsaved work") != "" && !u.l.Started())
	u.press("esc")
	u.press("q", "down", "enter") // quit
	select {
	case <-u.l.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	if r := u.l.Result(); r.Outcome != lifecycle.Clean {
		t.Fatalf("result %+v", r)
	}
	if u.claim(x.root) != nil {
		t.Fatal("the edit claim was not released")
	}
	d, ok, err := u.store.Find(x.project, drafts.IssueBody, x.root)
	if err != nil || !ok {
		t.Fatalf("the draft was not kept: %v", err)
	}
	if got, _ := u.store.Read(d); got != "unsaved work\n" {
		t.Fatalf("draft %q", got)
	}
	next := x.launch()
	next.expect("the banner", next.line("1 unapplied draft · D opens the Drafts view") != "")
}

// Without open drafts quit does not ask: an applied or discarded draft is
// no longer open, and a draft left by an earlier launch was not opened.
func TestQuitWithoutOpenDraftsDoesNotAsk(t *testing.T) {
	x := newDrafted(t)
	x.draft(drafts.IssueBody, x.child, x.child, "Child issue", "left from before\n")
	u := x.launch()
	u.content = "applied\n"
	u.press("e")
	u.choose("Apply")
	u.expect("applied", u.line("Applied the draft of “Root issue”") != "")
	u.press("q")
	if u.line("open draft") != "" || !u.l.Started() {
		t.Fatalf("asked, or did not quit; screen:\n%s", u.view())
	}
}

// Drafts survive a crash and recovery: the next launch cleans up the
// orphan and still lists the draft.
func TestDraftsSurviveACrashAndRecovery(t *testing.T) {
	x := newDrafted(t)
	_, orphan := tuitest.Instance(t, x.state, x.srv.URL)
	d := x.draft(drafts.IssueBody, x.root, x.root, "Root issue", "before the crash\n")
	tuitest.Crash(orphan)
	u := x.launch()
	u.expect("recovered and listed", u.line("Cleaned up 1 orphaned lit-tui session") != "" && u.line("1 unapplied draft") != "")
	if got, err := u.store.Read(d); err != nil || got != "before the crash\n" {
		t.Fatalf("draft %q %v", got, err)
	}
}
