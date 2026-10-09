package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/claims"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/edit"
)

// editingUI is a claiming root model with a body editor whose editor
// process is simulated: it writes the next content into the draft file.
type editingUI struct {
	*claiming
	e       *edit.Editor
	store   drafts.Store
	ran     [][]string // the editor commands run
	content string     // what the simulated editor saves
	mu      sync.Mutex
	posted  []tea.Msg // what the draft watcher posted
}

func (u *editingUI) lastPosted() (tea.Msg, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.posted) == 0 {
		return nil, false
	}
	return u.posted[len(u.posted)-1], true
}

func startEditing(t *testing.T, cfg claims.Config) *editingUI {
	t.Helper()
	c := startClaiming(t, cfg)
	store := drafts.New(t.TempDir())
	e := edit.New(c.l, c.h, store)
	u := &editingUI{claiming: c, e: e, store: store}
	m := c.m.(Model).WithEditor(e)
	m.ed.getenv = func(k string) string { return map[string]string{"VISUAL": `'/opt/My Editor/nvim' --clean`}[k] }
	m.ed.exec = func(cmd *exec.Cmd, done tea.ExecCallback) tea.Cmd {
		return func() tea.Msg {
			u.ran = append(u.ran, cmd.Args)
			err := os.WriteFile(cmd.Args[len(cmd.Args)-1], []byte(u.content), 0o600)
			return done(err)
		}
	}
	m.ed.post = func(_ string, msg tea.Msg) {
		u.mu.Lock()
		u.posted = append(u.posted, msg)
		u.mu.Unlock()
	}
	c.m = m
	return u
}

func (u *editingUI) draft(id string) (drafts.Draft, bool) {
	u.t.Helper()
	d, ok, err := u.store.Find(u.project(), drafts.IssueBody, id)
	if err != nil {
		u.t.Fatal(err)
	}
	return d, ok
}

func (u *editingUI) project() string { return u.m.(Model).b.project.ID }

func (u *editingUI) issue(id string) protocol.Issue {
	u.t.Helper()
	got, err := litclient.Get[protocol.Issue](context.Background(), u.agent, "test/one", id)
	if err != nil || len(got) != 1 {
		u.t.Fatalf("read %s: %v", id, err)
	}
	return got[0]
}

// choose moves the panel's cursor to the choice containing label and runs it.
func (u *editingUI) choose(label string) {
	u.t.Helper()
	p := u.m.(Model).ed.panel
	if p == nil {
		u.t.Fatalf("no panel to choose %q from; screen:\n%s", label, u.view())
	}
	for i, c := range p.choices {
		if strings.Contains(c.label, label) {
			for range i - p.cursor {
				u.press("down")
			}
			for range p.cursor - i {
				u.press("up")
			}
			u.press("enter")
			return
		}
	}
	u.t.Fatalf("no choice %q; screen:\n%s", label, u.view())
}

// Story 4 in the model: e claims the issue, suspends into the editor (run
// from parsed arguments, the draft path last), then previews the diff and
// the affected records; Enter commits exactly that preview at the recorded
// revision, releases the edit claim and removes the draft.
func TestEditClaimsEditsPreviewsAndApplies(t *testing.T) {
	u := startEditing(t, claims.Config{})
	before := u.issue(u.root)
	u.content = "A new body.\n"
	u.press("e")
	if len(u.ran) != 1 || !slices.Equal(u.ran[0][:2], []string{"/opt/My Editor/nvim", "--clean"}) || !strings.HasSuffix(u.ran[0][2], "--"+u.root[:8]+".md") {
		t.Fatalf("editor commands %q", u.ran)
	}
	u.expect("claimed while editing", u.ours(u.root))
	u.expect("the preview", u.line("Apply the draft of “Root issue”?") != "" && u.line("+A new body.") != "" &&
		u.line("-body of Root issue") != "" && u.line(fmt.Sprintf("issue “Root issue” at revision %d (changed)", before.Revision)) != "" && u.line("project “test/one”") != "")
	u.expect("the claim line", u.line("The edit claim on “Root issue” is held") != "")
	u.choose("Apply")
	u.expect("applied note", u.line(fmt.Sprintf("Applied the draft of “Root issue”: revision %d → %d. Released the edit claim.", before.Revision, before.Revision+1)) != "")
	if got := u.issue(u.root); got.Body != "A new body.\n" || got.Revision != before.Revision+1 {
		t.Fatalf("issue %+v", got)
	}
	u.expect("edit claim released", u.claim(u.root) == nil)
	if _, ok := u.draft(u.root); ok {
		t.Fatal("the draft was kept after apply")
	}
}

// Another client's claim refuses the edit: the owner is named and no
// editor starts.
func TestEditRefusedByAForeignClaimNamesTheOwner(t *testing.T) {
	u := startEditing(t, claims.Config{})
	u.press("right", "down")
	u.press("e")
	u.expect("owner named", u.line("Cannot edit “Child issue”: claimed by Claude (agent) until") != "")
	if len(u.ran) != 0 {
		t.Fatalf("the editor ran: %q", u.ran)
	}
	if _, ok := u.draft(u.child); ok {
		t.Fatal("a refused edit left a draft")
	}
}

// An existing draft is offered (resume, diff, discard) instead of being
// overwritten; discarding asks first and then releases the edit claim.
func TestAnExistingDraftIsOfferedNeverOverwritten(t *testing.T) {
	u := startEditing(t, claims.Config{})
	u.content = "draft one\n"
	u.press("e")
	u.press("esc") // keep the draft for later
	u.expect("kept note", u.line("Kept the draft of “Root issue”") != "")
	u.press("e")
	if len(u.ran) != 1 {
		t.Fatalf("e on a drafted issue ran the editor: %q", u.ran)
	}
	u.expect("the offer", u.line("“Root issue” has a draft") != "" && u.line("Resume in the editor") != "" &&
		u.line("Diff against the current body") != "" && u.line("Discard the draft") != "")
	u.choose("Diff against the current body")
	u.expect("the diff", u.line("+draft one") != "" && u.line("-body of Root issue") != "")
	u.choose("Discard the draft")
	u.expect("asked first", u.line("Discard the draft of “Root issue”?") != "")
	if d, ok := u.draft(u.root); !ok {
		t.Fatal("discarded before confirming")
	} else if got, _ := u.store.Read(d); got != "draft one\n" {
		t.Fatalf("draft overwritten: %q", got)
	}
	u.choose("Discard")
	u.expect("discarded note", u.line("Discarded the draft of “Root issue”") != "")
	u.expect("edit claim released", u.claim(u.root) == nil)
	if _, ok := u.draft(u.root); ok {
		t.Fatal("the draft is still there")
	}
}

// E shows the draft's path and keeps the TUI live; the watcher reports file
// changes (and counts as activity); e then applies it.
func TestEditDetachedShowsThePathAndWatchesTheFile(t *testing.T) {
	u := startEditing(t, claims.Config{})
	u.send(tea.WindowSizeMsg{Width: 250, Height: 30}) // room for the path
	u.press("E")
	if len(u.ran) != 0 {
		t.Fatalf("detached edit ran an editor: %q", u.ran)
	}
	d, ok := u.draft(u.root)
	if !ok {
		t.Fatal("no draft")
	}
	u.expect("the path", strings.Contains(u.view(), d.File) && u.line("Draft open: “Root issue”") != "")
	if err := os.WriteFile(d.Path(), []byte("detached edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the watcher", func() bool { _, ok := u.lastPosted(); return ok })
	msg, _ := u.lastPosted()
	u.send(msg)
	u.expect("change shown", u.line("changed ") != "")
	u.press("e")
	u.choose("Preview and apply")
	u.expect("preview", u.line("+detached edit") != "")
	u.choose("Apply")
	if got := u.issue(u.root); got.Body != "detached edit\n" {
		t.Fatalf("body %q", got.Body)
	}
	u.expect("no open draft", u.line("Draft open:") == "")
}

// Story 6 in the model: with the edit claim lost, the preview warns before
// confirming; an agent's edit meanwhile makes the apply a revision conflict
// that keeps the draft; reload records the new revision and the optimistic
// apply then succeeds. A foreign claim refuses the apply, naming its owner.
func TestApplyAfterALostClaimWarnsAndConflictsKeepTheDraft(t *testing.T) {
	u := startEditing(t, claims.Config{})
	u.content = "mine\n"
	u.press("e")
	u.press("esc")
	u.press("u") // the claim is gone: applying is optimistic
	u.expect("released", u.claim(u.root) == nil)
	body, rev := "the agent's\n", u.issue(u.root).Revision
	if _, err := litclient.Get[protocol.Issue](context.Background(), u.agent, "test/one", u.root); err != nil {
		t.Fatal(err)
	}
	p, err := u.agent.Prepare(context.Background(), litclient.UpdateIssue("test/one", u.root, 0, litclient.IssueChange{Body: &body}))
	if err == nil {
		_, err = u.agent.Commit(context.Background(), p)
	}
	if err != nil {
		t.Fatal(err)
	}
	u.press("e")
	u.choose("Preview and apply")
	u.expect("conflict", u.line(fmt.Sprintf("“Root issue” changed since the draft was started: revision %d → %d", rev, rev+1)) != "" && u.line("the draft is kept") != "")
	u.choose("Reload")
	u.expect("warning before confirming", u.line("Warning: this session holds no claim on “Root issue”") != "" && u.line("Apply the draft of “Root issue”?") != "")
	if _, err := u.agent.AcquireClaims(context.Background(), litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{u.root}}}); err != nil {
		t.Fatal(err)
	}
	u.choose("Apply")
	u.expect("owner named", u.line("claimed by Claude (agent) until") != "" && u.line("the draft is kept") != "")
	if got := u.issue(u.root); got.Body != body {
		t.Fatalf("the refused apply wrote: %q", got.Body)
	}
	if d, ok := u.draft(u.root); !ok {
		t.Fatal("the draft was dropped")
	} else if got, _ := u.store.Read(d); got != "mine\n" {
		t.Fatalf("draft %q", got)
	}
	if _, err := u.agent.ReleaseClaims(context.Background(), litclient.ClaimSelection{Issues: []string{u.root}}); err != nil {
		t.Fatal(err)
	}
	u.choose("Keep")
	u.press("e")
	u.choose("Preview and apply")
	u.choose("Apply")
	if got := u.issue(u.root); got.Body != "mine\n" {
		t.Fatalf("optimistic apply: %q", got.Body)
	}
}

// Without an editor the menu offers no edit actions; with one it offers
// them at the end.
func TestTheMenuOffersEditing(t *testing.T) {
	c := startClaiming(t, claims.Config{})
	if strings.Contains(c.menu(), "edit the body") {
		t.Fatalf("edit offered without an editor:\n%s", c.menu())
	}
	u := startEditing(t, claims.Config{})
	menu := u.menu()
	for _, want := range []struct{ desc, key string }{{"edit the body, or resume, apply or discard its draft", "e"}, {"edit the body in any editor (shows the draft path)", "E"}} {
		found := false
		for _, l := range strings.Split(menu, "\n") {
			found = found || strings.Contains(l, want.desc) && strings.HasSuffix(strings.TrimSpace(l), " "+want.key)
		}
		if !found {
			t.Fatalf("menu lacks %q (%s):\n%s", want.desc, want.key, menu)
		}
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; !ok(); i++ {
		if i > 500 {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
