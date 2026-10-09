package tui

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/claims"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/projectadmin"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// startAdministering is startEditing with project specifications
// registered, at a width that shows whole notes.
func startAdministering(t *testing.T) *editingUI {
	t.Helper()
	u := startEditing(t, claims.Config{})
	projectadmin.Register(u.e)
	u.send(tea.WindowSizeMsg{Width: 320, Height: 30}) // room for paths
	return u
}

func (u *editingUI) projectRecord(ref string) protocol.Project {
	u.t.Helper()
	got, err := litclient.Get[protocol.Project](context.Background(), u.agent, "", ref)
	if err != nil || len(got) != 1 {
		u.t.Fatalf("project %s: %v %v", ref, got, err)
	}
	return got[0]
}

func (u *editingUI) typeText(s string) {
	u.t.Helper()
	for _, r := range s {
		u.press(string(r))
	}
}

// n in the switcher creates a project: an invalid title is refused with
// the allowed syntax before anything is previewed; a valid one previews the
// creation, Enter commits exactly that, and the new project is browsed and
// listed in the switcher.
func TestANewProjectIsCreatedFromTheSwitcherAndBrowsed(t *testing.T) {
	u := startAdministering(t)
	u.press("p")
	u.expect("the switcher hint", u.line("n new project") != "")
	u.press("n")
	u.expect("the form", u.line("New project") != "" && u.line("allowed prefixes test") != "" && u.line("Title: ▏") != "")
	before := u.srv.Count("POST /v1/transaction-previews")
	u.typeText("nope/x")
	u.press("enter")
	u.expect("refused before the preview", u.line("“nope” is not an allowed prefix; the service allows test.") != "" &&
		u.srv.Count("POST /v1/transaction-previews") == before)
	u.send(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	u.typeText("test/fresh")
	u.press("enter")
	u.expect("the preview", u.line("Create the project “test/fresh”") != "" && u.line("• project “test/fresh”") != "")
	u.press("enter")
	u.expect("browsing the new project", strings.Contains(u.crumb(), "test/fresh") && u.line("Created “test/fresh”: S writes its specification, R its repositories.") != "")
	if got := u.projectRecord("test/fresh"); got.Description != "" || u.project() != got.ID {
		t.Fatalf("project %+v, browsing %s", got, u.project())
	}
	u.press("p")
	u.expect("listed in the switcher", u.line("test/fresh  (current)") != "" && u.line("test/one") != "")
	u.press("esc")
	u.press("n") // only the switcher creates projects
	u.expect("no form in the tree", u.line("New project") == "")
}

// Cancelling the form returns to the switcher; nothing is created.
func TestCancellingANewProjectKeepsTheSwitcher(t *testing.T) {
	u := startAdministering(t)
	u.press("p", "n")
	u.typeText("test/never")
	u.press("esc")
	u.expect("the switcher", u.line("Switch project") != "" && u.line("New project") == "")
	if _, err := litclient.Get[protocol.Project](context.Background(), u.agent, "", "test/never"); err == nil {
		t.Fatal("a cancelled project was created")
	}
}

// S edits the project's specification in the editor through a
// project_spec draft: no claim, the preview diffs against the recorded
// revision and fences the project, and applying commits exactly it.
func TestTheSpecificationIsEditedAndApplied(t *testing.T) {
	u := startAdministering(t)
	rev := u.projectRecord("test/one").Revision
	at := func(n int64) string { return strconv.FormatInt(n, 10) }
	u.content = "the new spec\n"
	u.press("S")
	if len(u.ran) != 1 || !strings.Contains(u.ran[0][len(u.ran[0])-1], "spec-test-one--") {
		t.Fatalf("editor commands %q", u.ran)
	}
	u.expect("the preview", u.line("Apply the draft of the specification of “test/one”?") != "" && u.line("-spec") != "" &&
		u.line("+the new spec") != "" && u.line("project “test/one” at revision "+at(rev)+" (changed)") != "" &&
		u.line("Applying is optimistic: it is refused if the specification of “test/one” changed after revision "+at(rev)+".") != "")
	if len(u.h.Snapshot().Holds) != 0 {
		t.Fatalf("editing a specification took a claim: %+v", u.h.Snapshot())
	}
	u.choose("Apply")
	u.expect("applied", u.line("Applied the draft of the specification of “test/one”: revision "+at(rev)+" → "+at(rev+1)+".") != "")
	if got := u.projectRecord("test/one"); got.Description != "the new spec\n" || got.Revision != rev+1 {
		t.Fatalf("project %+v", got)
	}
	if left := u.drafts(drafts.ProjectSpec); len(left) != 0 {
		t.Fatalf("drafts kept: %+v", left)
	}
}

// A change of the project after the draft was started keeps the draft: S
// opens it as a conflict, the diff compares with the current
// specification, and reload applies it over the new revision.
func TestASpecificationConflictKeepsTheDraft(t *testing.T) {
	u := startAdministering(t)
	rev := u.projectRecord("test/one").Revision
	at := func(n int64) string { return strconv.FormatInt(n, 10) }
	u.content = "my spec\n"
	u.press("S")
	u.press("esc")
	u.expect("kept", u.line("Kept the draft of the specification of “test/one”") != "" && u.line("S in the project resumes") != "")
	desc := "the agent's spec\n"
	tuitest.Apply(t, u.agent, litclient.UpdateProject("test/one", 0, litclient.ProjectChange{Description: &desc}))
	u.press("S")
	u.expect("the conflict", u.line("The specification of “test/one” changed since the draft was started: revision "+at(rev)+" → "+at(rev+1)) != "" &&
		u.line("the draft is kept") != "" && u.line("Diff against the current specification") != "")
	if len(u.ran) != 1 || len(u.drafts(drafts.ProjectSpec)) != 1 {
		t.Fatalf("a second draft was started: %q", u.ran)
	}
	u.choose("Diff against")
	u.expect("the diff", u.line("-the agent's spec") != "" && u.line("+my spec") != "")
	u.choose("Reload")
	u.expect("the preview at the new revision", u.line("project “test/one” at revision "+at(rev+1)+" (changed)") != "")
	u.choose("Apply")
	if got := u.projectRecord("test/one"); got.Description != "my spec\n" || got.Revision != rev+2 {
		t.Fatalf("project %+v", got)
	}
}

// R changes the repository list through an exact quick preview fenced at
// the revision read when it opened.
func TestRepositoriesChangeFromTheTree(t *testing.T) {
	u := startAdministering(t)
	rev := u.projectRecord("test/one").Revision
	u.press("R")
	u.expect("the picker", u.line("Repositories of “test/one”") != "")
	u.typeText("https://x.example/repo")
	u.press("enter")
	u.send(tea.KeyPressMsg{Code: tea.KeyHome})
	u.press("enter")
	u.expect("the preview", u.line("Change the repositories of “test/one”: +https://x.example/repo") != "" &&
		u.line("project “test/one” · revision "+strconv.FormatInt(rev, 10)) != "")
	u.press("enter")
	u.expect("applied", u.line("Applied: Change the repositories of “test/one”: +https://x.example/repo.") != "")
	if got := u.projectRecord("test/one"); !slices.Equal(got.RepositoryRefs, []string{"https://x.example/repo"}) || got.Revision != rev+1 {
		t.Fatalf("project %+v", got)
	}
}

// The action menu offers the project's specification and repositories.
func TestTheMenuOffersProjectAdministration(t *testing.T) {
	u := startAdministering(t)
	u.press("m", "G") // the menu is longer than the pane: its end scrolls into view
	u.expect("the menu", u.line("edit the project's specification, or resume its draft  S") != "" &&
		u.line("change the project's repositories") != "")
}

// A kept specification draft is listed in the Drafts view and opens there
// as from S.
func TestASpecificationDraftOpensFromTheDraftsView(t *testing.T) {
	u := startAdministering(t)
	u.content = "later\n"
	u.press("S")
	u.press("esc")
	u.press("D")
	u.expect("listed", u.line("project spec · “test/one”") != "")
	u.press("enter")
	u.expect("offered", u.line("The specification of “test/one” has a draft") != "" && u.line("Resume in the editor") != "")
}
