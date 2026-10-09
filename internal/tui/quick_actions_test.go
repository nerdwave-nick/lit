package tui

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/claims"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

func (b *browsing) ctrlU() { b.t.Helper(); b.send(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}) }

// summaryOf reads issue id's current summary through the agent session.
func summaryOf(t *testing.T, s *litclient.Session, id string) protocol.IssueSummary {
	t.Helper()
	issues, err := litclient.Get[protocol.Issue](context.Background(), s, "", id)
	if err != nil || len(issues) != 1 {
		t.Fatalf("issue %s: %v", id, err)
	}
	return issues[0].Summarize(nil)
}

// select moves the tree selection to the root titled title.
func (b *browsing) selectRoot(title string) {
	b.t.Helper()
	for range 10 {
		if strings.HasSuffix(strings.TrimSpace(b.crumb()), title) {
			return
		}
		b.press("down")
	}
	b.t.Fatalf("no root %q:\n%s", title, b.view())
}

// Title, state, labels, assignee and parent each preview their change, list
// what it writes and commit exactly that preview once; typed letters belong
// to the inputs.
func TestQuickActionsChangeMetadataThroughAnExactPreview(t *testing.T) {
	srv := tuitest.Service(t)
	agent, root, _, _ := seed(t, srv, "test/one")
	second := tuitest.CreateIssues(t, agent, "test/one", "", "Third root")[0]
	b := startBrowsing(t, srv, 120, 30, false)
	b.selectRoot("Third root")
	commits := srv.Count("POST /v1/transactions")

	b.press("t")
	b.expect("the title field", b.line("Title: Third root") != "")
	b.ctrlU()
	b.typeText("Quick third root")
	b.expect("q is typed, not quit", b.line("Title: Quick third root") != "")
	b.press("enter")
	b.expect("the preview lists what it writes", b.line("Rename “Third root” to “Quick third root”") != "" && b.line("Would write:") != "" &&
		b.line("“Third root” (this issue) · revision 1") != "")
	if srv.Count("POST /v1/transactions") != commits {
		t.Fatal("the preview committed")
	}
	b.press("enter")
	b.expect("applied and reloaded", b.line("Applied: Rename “Third root” to “Quick third root”.") != "" && strings.Contains(b.tree(), "Quick third root"))
	if got := srv.Count("POST /v1/transactions") - commits; got != 1 {
		t.Fatalf("%d commits, want 1", got)
	}

	b.press("s")
	b.expect("the state picker", b.line("open (current)") != "" && b.line("closed") != "")
	b.press("enter", "enter")
	b.expect("closed", b.line("Applied: Close “Quick third root”.") != "" && summaryOf(t, agent, second).State == "closed")

	b.press("#")
	b.typeText("urgent")
	b.expect("a new label is offered", b.line("add the new label “urgent”") != "")
	b.press("enter")
	b.send(tea.KeyPressMsg{Code: tea.KeyHome})
	b.press("enter")
	b.expect("the label preview", b.line("Change the labels of “Quick third root”: +urgent") != "")
	b.press("enter")
	if got := summaryOf(t, agent, second).Labels; !slices.Equal(got, []string{"urgent"}) {
		t.Fatalf("labels %v", got)
	}

	b.press("A")
	b.typeText("Ada")
	b.press("enter")
	b.expect("the assignee preview", b.line("Assign “Quick third root” to Ada") != "")
	b.press("enter")
	if a := summaryOf(t, agent, second).Assignee; a == nil || *a != "Ada" {
		t.Fatalf("assignee %v", a)
	}

	b.press("P")
	b.typeText("Root issue")
	b.press("enter")
	b.expect("the parent preview lists the new parent", b.line("Move “Quick third root” under “Root issue”") != "" && b.line("“Root issue” · revision") != "")
	b.press("enter")
	if p := summaryOf(t, agent, second).ParentID; p == nil || *p != root {
		t.Fatalf("parent %v", p)
	}
}

// User story 7: adding a blocker lists the other issue it affects; another
// client's claim on it refuses the action before anything is written, and
// the owner is named.
func TestABlockerOnAnIssueClaimedByAnotherClientIsRefusedNamingTheOwner(t *testing.T) {
	srv := tuitest.Service(t)
	agent, _, child, _ := seed(t, srv, "test/one")
	b := startBrowsing(t, srv, 120, 30, false)
	b.selectRoot("Second root")
	before, blocker := summaryOf(t, agent, b.selectedID()), summaryOf(t, agent, child)
	commits := srv.Count("POST /v1/transactions")
	b.press("b")
	b.expect("the relation picker", b.line("“Second root” is blocked by …") != "")
	b.press("enter")
	b.typeText("Child")
	b.press("enter")
	b.expect("the preview names the affected issue and its owner", strings.Contains(b.line("• “Child issue”"), "claimed by Claude (agent) until") &&
		b.line("Refused (claim_conflict): “Child issue” is claimed by Claude (agent) until") != "" && b.line("Nothing was written.") != "")
	b.press("enter")
	if n := srv.Count("POST /v1/transactions") - commits; n != 0 {
		t.Fatalf("%d commits after a refusal", n)
	}
	if entries, _ := os.ReadDir(filepath.Join(b.l.Entry().Root, "..", "..", "pending")); len(entries) != 0 {
		t.Fatalf("pending evidence written: %v", entries)
	}
	if a, c := summaryOf(t, agent, before.ID), summaryOf(t, agent, child); a.Revision != before.Revision || c.Revision != blocker.Revision {
		t.Fatal("an issue changed")
	}
	b.press("esc")
	b.expect("closed", b.line("Refused") == "" && strings.Contains(b.tree(), "Second root"))
}

// A claim taken after the preview refuses the commit and names the owner;
// unlinking lists the issue's links.
func TestAClaimTakenAfterThePreviewRefusesTheCommit(t *testing.T) {
	srv := tuitest.Service(t)
	agent, root, _, _ := seed(t, srv, "test/one")
	b := startBrowsing(t, srv, 120, 30, false)
	b.selectRoot("Second root")
	second := b.selectedID()
	b.press("b", "down", "down", "enter")
	b.typeText("Root issue")
	b.press("enter")
	b.expect("the related preview", b.line("Link: “Second root” is related to “Root issue”") != "" && b.line("Refused") == "")
	if _, err := agent.AcquireClaims(context.Background(), litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{root}}}); err != nil {
		t.Fatal(err)
	}
	b.press("enter")
	b.expect("refused at commit, owner named", b.line("Refused (claim_conflict): “Root issue” is claimed by Claude (agent) until") != "")
	if len(summaryOf(t, agent, second).Related) != 0 {
		t.Fatal("linked despite the claim")
	}
	b.press("esc")

	tuitest.Apply(t, agent, litclient.LinkIssues("test/one", second, "blocked-by", root))
	b.press("r", "B")
	b.expect("the links are listed", b.line("is blocked by “Root issue”") != "")
}

// A claim this client holds (taken with c) is passed on with the change.
func TestOwnClaimsArePassedOnWithAQuickAction(t *testing.T) {
	c := startClaiming(t, claims.Config{})
	c.selectRoot("Second root")
	id := c.selectedID()
	c.press("c")
	c.await("held", func(m claims.Msg) bool { _, ok := m.Hold(id); return ok })
	c.press("t")
	c.typeText(" (mine)")
	c.press("enter")
	c.expect("the claim is passed on", strings.Contains(c.line("(this issue)"), "claimed by you: your claim is passed on"))
	c.press("enter")
	c.expect("applied", c.line("Applied: Rename “Second root” to “Second root (mine)”.") != "")
	if !c.ours(id) {
		t.Fatal("the claim was lost")
	}
}

// The action menu offers the quick actions with their keys.
func TestTheActionMenuOffersQuickActions(t *testing.T) {
	srv := tuitest.Service(t)
	seed(t, srv, "test/one")
	b := startBrowsing(t, srv, 120, 30, false)
	b.press("m")
	for _, want := range []string{"edit the title", "close or reopen the issue", "change the labels", "change the assignee", "change the parent", "add a link", "remove a link"} {
		b.expect("menu entry "+want, b.line(want) != "")
	}
	for !selectedRow(b.m, "edit the title") {
		b.press("down")
	}
	b.press("enter")
	b.expect("the title field", b.line("Title: Root issue") != "")
}

// selectedID is the UUID of the selected issue.
func (b *browsing) selectedID() string {
	b.t.Helper()
	m := b.m.(Model)
	sel, ok := m.b.tree.Selected()
	if !ok {
		b.t.Fatal("nothing selected")
	}
	return sel.ID
}

// A commit whose answer is lost is shown as uncertain with its request ID
// and is never sent again.
func TestAnUncertainCommitIsShownAndNeverReplayed(t *testing.T) {
	srv := tuitest.Service(t)
	seed(t, srv, "test/one")
	b := startBrowsing(t, srv, 120, 30, false)
	b.selectRoot("Second root")
	b.press("s", "enter")
	b.expect("the close preview", b.line("Close “Second root”") != "" && b.line("enter apply") != "")
	// The commit's answer is lost and so is every read after it, so
	// reconciliation cannot establish the outcome.
	var lost atomic.Bool
	srv.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/v1/transactions" {
			lost.Store(true)
		}
		if lost.Load() {
			tuitest.HangUp(w)
			return true
		}
		return false
	})
	commits := srv.Count("POST /v1/transactions")
	b.press("enter")
	b.expect("uncertain, with its request", b.line("Outcome uncertain (outcome_uncertain)") != "" && b.line("never replayed") != "" &&
		strings.Contains(b.view(), "Request "))
	b.press("enter", "s", "esc")
	if got := srv.Count("POST /v1/transactions") - commits; got != 1 {
		t.Fatalf("%d commits sent, want exactly 1", got)
	}
}
