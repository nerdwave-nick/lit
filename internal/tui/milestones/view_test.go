package milestones

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// viewer drives the Milestones view as the root model does: commands run
// at once and their messages go back to the view.
type viewer struct {
	t    *testing.T
	f    *fixture
	m    *Model
	last Result
}

func (f *fixture) open() *viewer {
	f.t.Helper()
	page, err := f.agent.IssueSummaries(ctx, map[string]any{"project_id": f.projectID, "all": true})
	if err != nil {
		f.t.Fatal(err)
	}
	m, cmd := New(Env{Gate: f.l, Project: f.projectID, ProjectTitle: project, Issues: page.Items})
	v := &viewer{t: f.t, f: f, m: m}
	v.run(Result{Cmd: cmd})
	return v
}

func (v *viewer) run(r Result) {
	v.t.Helper()
	v.last = r
	for queue := []tea.Cmd{r.Cmd}; len(queue) > 0; queue = queue[1:] {
		if queue[0] == nil {
			continue
		}
		switch msg := queue[0]().(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, msg...)
		default:
			next, ok := v.m.Update(msg)
			if !ok {
				v.t.Fatalf("not the view's message: %T", msg)
			}
			if next.Closed || next.Note != "" || next.Edit != nil {
				v.last = next
			}
			queue = append(queue, next.Cmd)
		}
	}
}

func (v *viewer) press(ks ...string) {
	v.t.Helper()
	for _, k := range ks {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		case "up":
			msg = tea.KeyPressMsg{Code: tea.KeyUp}
		case "ctrl+u":
			msg = tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
		default:
			msg = tea.KeyPressMsg{Code: rune(k[0]), Text: k}
		}
		v.run(v.m.Key(msg, keys.Default()))
	}
}

func (v *viewer) typeText(s string) {
	v.t.Helper()
	for _, r := range s {
		v.press(string(r))
	}
}

func (v *viewer) screen() string {
	return ansi.Strip(v.m.View(cells.Default, 160, 40, keys.Default(), false))
}

func (v *viewer) expect(what string, ok bool) {
	v.t.Helper()
	if !ok {
		v.t.Fatalf("%s; screen:\n%s", what, v.screen())
	}
}

func (v *viewer) has(s string) bool { return strings.Contains(v.screen(), s) }

// The view lists the project's milestones with their progress, read-only:
// an empty milestone is an empty scope, not complete; the selected one
// shows its description and each member's state, and open blockers that
// are not members. Titles and text are sanitized.
func TestTheViewListsMilestonesWithTheirProgress(t *testing.T) {
	f := start(t)
	ids := tuitest.CreateIssues(t, f.agent, project, "", "Closed member", "Blocker", "Claimed member")
	tuitest.Apply(t, f.agent, litclient.CloseIssue(project, ids[0], 0))
	tuitest.Apply(t, f.agent, litclient.LinkIssues(project, f.issue, "blocked-by", ids[1]))
	if _, err := f.agent.AcquireClaims(ctx, litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{ids[2]}}}); err != nil {
		t.Fatal(err)
	}
	m := f.milestone("M1")
	tuitest.Apply(t, f.agent, litclient.UpdateMilestone(project, m.ID, 0, litclient.MilestoneChange{
		AddIssues: []string{ids[0], ids[2]}, Body: ptr("Ship it.\nSecond \x1b[2J\x07 line\n")}))
	tuitest.Apply(t, f.agent, litclient.CreateMilestone(project, "Empty", ""))

	v := f.open()
	v.expect("the header", v.has("Milestones of “test/milestones” · 2"))
	v.expect("the new row", v.has("+ New milestone"))
	v.expect("M1's progress", v.has("“M1” · 3 members: 1 closed, 2 open (1 blocked, 1 claimed)"))
	v.expect("the empty milestone", v.has("“Empty” · no members: an empty scope, not complete"))
	v.press("down")
	v.expect("the description", v.has("Ship it.") && v.has("Second ␛[2J␇ line"))
	v.expect("the members", v.has("• “Member issue” · open · blocked") && v.has("• “Closed member” · closed") &&
		v.has("• “Claimed member” · open · claimed by Claude (agent)"))
	v.expect("the external blocker", v.has("Open blockers outside the milestone: “Blocker”"))
	v.press("down")
	v.expect("no members", v.has("No members yet."))
	v.press("esc")
	if !v.last.Closed {
		t.Fatalf("escape did not close: %+v", v.last)
	}
}

// New milestone opens a title field; the creation is previewed and applied
// once, and the list reloads with it.
func TestANewMilestoneIsCreatedInTheView(t *testing.T) {
	f := start(t)
	v := f.open()
	v.press("enter")
	v.expect("the field", v.has("New milestone in “test/milestones”"))
	v.typeText("Next quarter")
	v.press("enter")
	v.expect("the preview", v.has("Create the milestone “Next quarter” in “test/milestones”") && v.has("milestone “Next quarter”"))
	v.press("enter")
	v.expect("the applied note and the reload", v.has("Applied: Create the milestone “Next quarter”") && v.has("“Next quarter” · no members"))
	if got := f.milestone("Next quarter"); len(got.IssueIDs) != 0 {
		t.Fatalf("created %+v", got)
	}
}

// A milestone's actions rename it (previewed, fenced at its revision) and
// hand its description draft to the root model, in the editor or detached.
func TestAMilestonesActionsRenameItAndEditItsDescription(t *testing.T) {
	f := start(t)
	m := f.milestone("M1")
	v := f.open()
	v.press("down", "enter")
	v.expect("the actions", v.has("Actions for the milestone “M1”") && v.has("Rename the milestone") && v.has("Edit the description in the editor"))
	v.press("enter")
	v.expect("the field", v.has("Rename the milestone “M1”") && v.has("Title: M1"))
	v.press("ctrl+u")
	v.typeText("M2")
	v.press("enter")
	v.expect("the preview", v.has("Rename the milestone “M1” to “M2”") && v.has("milestone “M1” · revision 1"))
	v.press("enter")
	v.expect("renamed", v.has("Applied: Rename the milestone “M1” to “M2”.") && v.has("“M2” · 1 member"))
	m.Title, m.Revision = "M2", 2

	v.press("enter", "down", "enter")
	if v.last.Edit == nil || *v.last.Edit != Description(f.projectID, m) || v.last.Detached {
		t.Fatalf("edit the description: %+v", v.last)
	}
	v.press("enter", "down", "down", "enter")
	if v.last.Edit == nil || !v.last.Detached {
		t.Fatalf("edit detached: %+v", v.last)
	}
	v.press("enter", "esc")
	v.expect("escape leaves the actions", !v.has("Actions for the milestone") && v.has("+ New milestone"))
	v.press("M")
	if !v.last.Closed {
		t.Fatal("the milestones key did not close the view")
	}
}

// Typed letters belong to the title field, not to the keymap.
func TestTypedLettersBelongToTheTitleField(t *testing.T) {
	f := start(t)
	v := f.open()
	v.press("enter")
	v.typeText("qMr")
	v.expect("typed", v.has("Title: qMr") && !v.last.Closed)
	if !v.m.Typing() {
		t.Fatal("the field does not own typed keys")
	}
	v.press("esc")
	v.expect("the field closed, the view stays", v.has("+ New milestone") && !v.last.Closed)
}
