package tui

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/claims"
	"github.com/nerdwave-nick/lit/internal/tui/drafts"
	"github.com/nerdwave-nick/lit/internal/tui/milestones"
	"github.com/nerdwave-nick/lit/internal/tui/tuitest"
)

// startMilestoning is startEditing with milestone descriptions registered
// and milestone M1 ("The goal.", no members) created by the agent.
func startMilestoning(t *testing.T) *editingUI {
	t.Helper()
	u := startEditing(t, claims.Config{})
	milestones.Register(u.e)
	tuitest.Apply(t, u.agent, litclient.CreateMilestone("test/one", "M1", "The goal.\n"))
	return u
}

func (u *editingUI) milestone(title string) protocol.Milestone {
	u.t.Helper()
	got, err := litclient.Get[protocol.Milestone](context.Background(), u.agent, "test/one", title)
	if err != nil || len(got) != 1 {
		u.t.Fatalf("milestone %q: %v %v", title, got, err)
	}
	return got[0]
}

// M opens the Milestones view: progress per milestone (an empty one is an
// empty scope, not complete). A new milestone is created from its title,
// previewed and applied once; typed letters are text; Escape returns to
// the tree.
func TestTheMilestonesViewShowsProgressAndCreatesMilestones(t *testing.T) {
	u := startMilestoning(t)
	commits := u.srv.Count("POST /v1/transactions")
	u.press("M")
	u.expect("the view", u.line("Milestones of “test/one” · 1") != "" && u.line("“M1” · no members: an empty scope, not complete") != "")
	u.press("enter")
	u.expect("the title field", u.line("New milestone in “test/one”") != "")
	u.typeText("Sprint q")
	u.press("enter")
	u.expect("the preview", u.line("Create the milestone “Sprint q” in “test/one”") != "" && u.line("Would write:") != "")
	if u.srv.Count("POST /v1/transactions") != commits || u.l.Started() {
		t.Fatal("the preview committed, or q quit")
	}
	u.press("enter")
	u.expect("created and listed", u.line("Applied: Create the milestone “Sprint q” in “test/one”.") != "" && u.line("“Sprint q” · no members") != "")
	if got := u.milestone("Sprint q"); len(got.IssueIDs) != 0 || u.srv.Count("POST /v1/transactions")-commits != 1 {
		t.Fatalf("created %+v", got)
	}
	u.press("esc")
	u.expect("back in the tree", u.line("Milestones of") == "" && u.treeLine("Root issue") != "")
}

// W adds the selected issue to a milestone and removes it again, each one
// previewed transaction writing only the milestone. Membership is explicit
// (the child does not follow its parent) and milestones take no claims, so
// an issue the agent claims is added without a refusal and the agent's
// claim stays. The view shows the members' states.
func TestSetMilestonesAddsAndRemovesTheSelectedIssue(t *testing.T) {
	u := startMilestoning(t)
	u.press("W")
	u.expect("the picker", u.line("Milestones of “Root issue” (enter adds or removes it)") != "" && u.line("[ ] “M1”") != "")
	u.press("enter")
	u.expect("the preview", u.line("Add “Root issue” to the milestone “M1”") != "" && u.line("milestone “M1” · revision 1") != "")
	u.press("enter")
	u.expect("applied", u.line("Applied: Add “Root issue” to the milestone “M1”.") != "")
	if got := u.milestone("M1"); !slices.Equal(got.IssueIDs, []string{u.root}) {
		t.Fatalf("members %v", got.IssueIDs)
	}

	u.press("right", "down") // the child, claimed by the agent
	u.press("W", "enter")
	u.expect("no refusal", u.line("Add “Child issue” to the milestone “M1”") != "" && u.line("Refused") == "")
	u.press("enter")
	u.expect("applied", u.line("Applied: Add “Child issue” to the milestone “M1”.") != "")
	if got := u.milestone("M1"); len(got.IssueIDs) != 2 || slices.Contains(got.IssueIDs, u.grand) {
		t.Fatalf("members %v", got.IssueIDs)
	}
	if c := u.claim(u.child); c == nil || c.OwnerClientID == u.l.Status().Client {
		t.Fatalf("the agent's claim changed: %+v", c)
	}

	u.press("M")
	u.expect("the progress", u.line("“M1” · 2 members: 0 closed, 2 open (0 blocked, 1 claimed)") != "")
	u.press("down")
	u.expect("the members", u.line("• “Child issue” · open · claimed by Claude (agent)") != "" && u.line("• “Root issue” · open") != "")
	u.press("esc", "W")
	u.expect("checked", u.line("[x] “M1” (member: enter removes it)") != "")
	u.press("enter")
	u.expect("the removal", u.line("Remove “Child issue” from the milestone “M1”") != "")
	u.press("enter")
	if got := u.milestone("M1"); !slices.Equal(got.IssueIDs, []string{u.root}) {
		t.Fatalf("members after removing %v", got.IssueIDs)
	}
}

// A milestone's description is edited in a TUI draft: no claim is taken,
// the preview is optimistic and fenced at the milestone's revision, and
// applying commits exactly that preview and removes the draft.
func TestAMilestoneDescriptionIsEditedThroughADraft(t *testing.T) {
	u := startMilestoning(t)
	m := u.milestone("M1")
	u.content = "The new goal.\n"
	u.press("M", "down", "enter")
	u.expect("the actions", u.line("Actions for the milestone “M1”:") != "")
	u.press("down", "enter")
	if len(u.ran) != 1 || !strings.HasSuffix(u.ran[0][len(u.ran[0])-1], "--"+m.ID[:8]+".md") {
		t.Fatalf("editor commands %q", u.ran)
	}
	u.expect("the preview", u.line("Apply the draft of the description of the milestone “M1”?") != "" && u.line("+The new goal.") != "" &&
		u.line("-The goal.") != "" && u.line("milestone “M1” at revision 1 (changed)") != "" &&
		u.line("Applying is optimistic: it is refused if the description of the milestone “M1” changed after revision 1.") != "")
	if len(u.h.Snapshot().Holds) != 0 {
		t.Fatalf("a milestone draft took a claim: %+v", u.h.Snapshot())
	}
	u.choose("Apply")
	u.expect("applied in the view", u.line("Applied the draft of the description of the milestone “M1”: revision 1 → 2.") != "" &&
		u.line("Milestones of “test/one”") != "" && u.line("  The new goal.") != "")
	if got := u.milestone("M1"); got.Body != "The new goal.\n" || got.Revision != 2 {
		t.Fatalf("milestone %+v", got)
	}
	if left := u.drafts(drafts.MilestoneDescription); len(left) != 0 {
		t.Fatalf("drafts kept: %+v", left)
	}
}

// A revision conflict keeps the description draft: a detached draft the
// agent overtook is listed in the Drafts view, opens from the view as a
// conflict, diffs against the current description, and after a reload
// applies over the new revision.
func TestAMilestoneDescriptionConflictKeepsTheDraft(t *testing.T) {
	u := startMilestoning(t)
	m := u.milestone("M1")
	u.press("M", "down", "enter", "down", "down", "enter")
	u.expect("detached", u.line("Editing the description of the milestone “M1” detached") != "" && u.line("then M applies it") != "")
	left := u.drafts(drafts.MilestoneDescription)
	if len(left) != 1 || left[0].IssueID != "" || left[0].RecordID != m.ID || left[0].Revision != 1 {
		t.Fatalf("drafts %+v", left)
	}
	if err := os.WriteFile(left[0].Path(), []byte("My goal.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := "The agent's goal.\n"
	tuitest.Apply(t, u.agent, litclient.UpdateMilestone("test/one", m.ID, 0, litclient.MilestoneChange{Body: &body}))

	u.press("D")
	u.expect("listed", strings.Contains(u.line("“M1”"), "milestone description · “M1” · project “test/one” · revision 1"))
	u.press("esc")
	u.press("enter", "down", "enter") // edit the description again: the draft is offered
	u.expect("a conflict", u.line("The description of the milestone “M1” changed since the draft was started: revision 1 → 2") != "" &&
		u.line("the draft is kept") != "")
	if got, err := u.store.Read(left[0]); err != nil || got != "My goal.\n" {
		t.Fatalf("draft %q %v", got, err)
	}
	u.expect("the diff choice names the description", u.line("Diff against the current description") != "")
	u.choose("Diff against the current")
	u.expect("the diff", u.line("+My goal.") != "" && u.line("-The agent's goal.") != "")
	u.choose("Reload")
	u.expect("the preview at the new revision", u.line("Apply the draft of the description of the milestone “M1”?") != "" &&
		u.line("milestone “M1” at revision 2 (changed)") != "")
	u.choose("Apply")
	if got := u.milestone("M1"); got.Body != "My goal.\n" || got.Revision != 3 {
		t.Fatalf("milestone %+v", got)
	}
	if left := u.drafts(drafts.MilestoneDescription); len(left) != 0 {
		t.Fatalf("drafts kept: %+v", left)
	}
}

// The action menu offers both milestone commands with their keys, and runs
// them.
func TestTheMenuOffersTheMilestoneCommands(t *testing.T) {
	u := startMilestoning(t)
	u.press("m")
	u.expect("the entries", strings.HasSuffix(strings.TrimSpace(u.line("add to or remove from a milestone")), "W") &&
		strings.HasSuffix(strings.TrimSpace(u.line("show the project's milestones")), "M"))
	m := u.m.(Model)
	for m.b.menu != nil && m.b.menu.items[m.b.menu.cursor].Action != "milestones" {
		u.press("down")
		m = u.m.(Model)
	}
	u.press("enter")
	u.expect("the view", u.line("Milestones of “test/one” · 1") != "")
}
