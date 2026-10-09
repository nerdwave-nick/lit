package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/claims"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/create"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// startCreating is startEditing with issue creation registered.
func startCreating(t *testing.T) *editingUI {
	t.Helper()
	u := startEditing(t, claims.Config{})
	create.Register(u.e)
	return u
}

// titled lists test/one's issues titled title, as the agent reads them.
func (u *editingUI) titled(title string) []protocol.Issue {
	u.t.Helper()
	page, err := litclient.List[protocol.Issue](context.Background(), u.agent, litclient.Query{ProjectID: u.project(), Title: title, All: true})
	if err != nil {
		u.t.Fatal(err)
	}
	return page.Items
}

// N asks for the title inline (typed letters such as q are text), opens the
// editor on the body's draft, previews the creation and creates exactly
// that issue on apply; the new issue is then selected in the reloaded tree.
func TestNewIssueCreatesATopLevelIssueAndSelectsIt(t *testing.T) {
	u := startCreating(t)
	u.press("N")
	u.expect("the title prompt", u.line("New issue in “test/one”") != "" && u.line("Title: ▏") != "")
	u.typeText("Quick question")
	u.expect("typed q is text", u.line("Title: Quick question▏") != "")
	if len(u.ran) != 0 || len(u.drafts(drafts.IssueNew)) != 0 {
		t.Fatal("a draft was started before the title was submitted")
	}
	u.content = "Its body.\n"
	u.press("enter")
	if len(u.ran) != 1 || !strings.Contains(u.ran[0][2], "quick-question--") {
		t.Fatalf("editor commands %q", u.ran)
	}
	u.expect("the preview", u.line("Create the new issue “Quick question”?") != "" && u.line("+Its body.") != "" &&
		u.line("issue “Quick question” new (changed)") != "" && u.line("project “test/one”") != "" && u.line("New issues take no claim") != "")
	commits := u.srv.Count("POST /v1/transactions")
	u.choose("Apply")
	if u.srv.Count("POST /v1/transactions") != commits+1 {
		t.Fatal("not exactly one commit")
	}
	got := u.titled("Quick question")
	if len(got) != 1 || got[0].Body != "Its body.\n" || got[0].ParentID != nil {
		t.Fatalf("created %+v", got)
	}
	u.expect("created note", u.line("Created “Quick question”.") != "")
	u.expect("selected in the reloaded tree", strings.HasPrefix(u.crumb(), "test/one › Quick question") && strings.Contains(u.header(), "5 issues"))
	if left := u.drafts(drafts.IssueNew); len(left) != 0 {
		t.Fatalf("drafts kept: %+v", left)
	}
}

// C creates a child of the selection: the preview lists the parent, whose
// children change, and the new child is selected under its parent.
func TestNewChildIsCreatedUnderTheSelection(t *testing.T) {
	u := startCreating(t)
	before := u.issue(u.root)
	u.press("C")
	u.expect("the child prompt", u.line("New child of “Root issue”") != "")
	u.typeText("Another child")
	u.press("enter")
	u.expect("the preview lists the parent", u.line("Create the new issue “Another child”?") != "" &&
		u.line(fmt.Sprintf("issue “Root issue” at revision %d (changed)", before.Revision)) != "" &&
		u.line("Creating it changes the children of “Root issue”") != "")
	u.choose("Apply")
	got := u.titled("Another child")
	if len(got) != 1 || got[0].ParentID == nil || *got[0].ParentID != u.root || got[0].Body != "" {
		t.Fatalf("created %+v", got)
	}
	if !slices.Contains(u.issue(u.root).ChildIDs, got[0].ID) {
		t.Fatal("the parent does not list the child")
	}
	u.expect("selected under its parent", strings.HasPrefix(u.crumb(), "test/one › Root issue › Another child"))
	u.expect("created note", u.line("Created “Another child” under “Root issue”.") != "")
}

// A child of an issue another client claims is refused before anything is
// written, naming the owner; the prompt warns about the claim first. The
// draft is kept and listed in the Drafts view.
func TestNewChildUnderAForeignClaimIsRefusedNamingTheOwner(t *testing.T) {
	u := startCreating(t)
	u.press("right", "down") // the child, claimed by the agent
	u.press("C")
	u.expect("the claim warning", u.line("“Child issue” is claimed by Claude (agent)") != "")
	u.typeText("Blocked child")
	u.content = "kept text\n"
	commits := u.srv.Count("POST /v1/transactions")
	u.press("enter")
	u.expect("refused", u.line("Cannot apply the new issue “Blocked child”") != "" &&
		u.line("“Child issue” is claimed by Claude (agent) until") != "" && u.line("Nothing was written; the draft is kept") != "")
	if u.srv.Count("POST /v1/transactions") != commits || len(u.titled("Blocked child")) != 0 {
		t.Fatal("something was written")
	}
	kept := u.drafts(drafts.IssueNew)
	if len(kept) != 1 || kept[0].ParentID != u.child {
		t.Fatalf("drafts %+v", kept)
	}
	if text, _ := u.store.Read(kept[0]); text != "kept text\n" {
		t.Fatalf("draft %q", text)
	}
	u.press("esc")
	u.expect("kept note", u.line("Kept the new issue “Blocked child”") != "")
	u.press("D")
	u.expect("listed in the Drafts view", u.line("new issue · “Blocked child” · project “test/one”") != "")
}

// The prompt refuses a title a loaded issue uses, and Escape cancels
// without starting a draft.
func TestTheTitlePromptRefusesATakenTitleAndCancels(t *testing.T) {
	u := startCreating(t)
	u.press("N")
	u.typeText("root issue")
	u.press("enter")
	u.expect("refused", u.line("An issue titled “Root issue” exists in this project already") != "")
	u.press("esc")
	u.expect("closed", u.line("Title:") == "")
	if len(u.ran) != 0 || len(u.drafts(drafts.IssueNew)) != 0 {
		t.Fatal("a draft was started")
	}
	if v := u.menu(); !strings.Contains(v, "create a top-level issue") || !strings.Contains(v, "create a child of the issue") {
		t.Fatalf("the action menu:\n%s", v)
	}
}

// A kept draft whose title another client took meanwhile is refused at
// preview; changing its title from the panel creates the issue with the
// draft's body under the new title.
func TestAKeptNewIssueIsRetitledAfterItsTitleWasTaken(t *testing.T) {
	u := startCreating(t)
	u.press("N")
	u.typeText("Racing")
	u.content = "race body\n"
	u.press("enter")
	u.expect("the preview", u.line("Create the new issue “Racing”?") != "")
	u.press("esc")
	tuitest.CreateIssues(t, u.agent, "test/one", "", "Racing")
	u.press("r")
	u.press("D", "enter")
	u.expect("offered", u.line("The new issue “Racing” is not created yet") != "")
	u.choose("Preview")
	u.expect("refused", u.line("Cannot preview the new issue “Racing”") != "")
	u.choose("Change the title")
	u.expect("the retitle prompt", u.line("Change the title of the new issue “Racing”") != "" && u.line("Title: Racing▏") != "")
	u.press("enter")
	u.expect("taken", u.line("An issue titled “Racing” exists in this project already") != "")
	u.typeText(" again")
	u.press("enter")
	u.expect("the preview", u.line("Create the new issue “Racing again”?") != "" && u.line("+race body") != "")
	u.choose("Apply")
	if got := u.titled("Racing again"); len(got) != 1 || got[0].Body != "race body\n" {
		t.Fatalf("created %+v", got)
	}
	u.expect("the Drafts view closes on the new issue", u.line("Unapplied drafts") == "" && u.line("Created “Racing again”.") != "" &&
		strings.HasPrefix(u.crumb(), "test/one › Racing again"))
}

// This session's claim on the parent is shown as passed on in the preview
// and is still held after the child is created.
func TestNewChildPassesOnOwnClaimOnTheParent(t *testing.T) {
	u := startCreating(t)
	u.press("c")
	u.expect("claimed", u.ours(u.root))
	rev := u.issue(u.root).Revision
	u.press("C")
	u.typeText("Claimed parent's child")
	u.press("enter")
	u.expect("passed on", u.line(fmt.Sprintf("issue “Root issue” at revision %d (changed) · claimed by you: your claim is passed on", rev)) != "")
	u.choose("Apply")
	u.expect("created", u.line("Created “Claimed parent's child” under “Root issue”.") != "")
	u.expect("still claimed", u.ours(u.root))
}

// A kept draft whose issue exists already (an apply whose answer was lost
// went through) opens from the Drafts view as created already and only
// offers to discard it.
func TestAKeptNewIssueThatExistsOpensAsCreatedAlready(t *testing.T) {
	u := startCreating(t)
	u.press("N")
	u.typeText("Lost answer")
	u.press("enter")
	u.press("esc")
	kept := u.drafts(drafts.IssueNew)
	if len(kept) != 1 {
		t.Fatalf("drafts %+v", kept)
	}
	req := litclient.CreateIssue("test/one", "Lost answer", "", "")
	req.Items[0].ID = kept[0].RecordID
	tuitest.Apply(t, u.agent, req)
	u.press("D", "enter")
	u.expect("created already", u.line("The new issue “Lost answer” was created already") != "" &&
		u.line("An issue with this draft's ID exists") != "" && u.line("Apply this change") == "")
	u.choose("Discard")
	u.expect("discarded", len(u.drafts(drafts.IssueNew)) == 0 && len(u.titled("Lost answer")) == 1)
}
