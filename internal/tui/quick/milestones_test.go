package quick_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/keys"
	"github.com/nerdwave-nick/lit/internal/tui/quick"
	"github.com/nerdwave-nick/lit/internal/tui/render/cells"
	"github.com/nerdwave-nick/lit/internal/tui/tuitest"
)

// driver runs a quick model as the root model does: each key's command runs
// at once and its message goes back to the model.
type driver struct {
	t    *testing.T
	m    *quick.Model
	last quick.Result
}

func (d *driver) press(ks ...string) {
	d.t.Helper()
	for _, k := range ks {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		case "ctrl+u":
			msg = tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
		default:
			msg = tea.KeyPressMsg{Code: rune(k[0]), Text: k}
		}
		d.run(d.m.Key(msg, keys.Default()))
	}
}

func (d *driver) typeText(s string) {
	d.t.Helper()
	for _, r := range s {
		d.press(string(r))
	}
}

func (d *driver) run(r quick.Result) {
	d.t.Helper()
	d.last = r
	for r.Cmd != nil {
		next, ok := d.m.Update(r.Cmd())
		if !ok {
			d.t.Fatal("the command's message was not the model's")
		}
		d.last, r = next, next
	}
}

func (d *driver) screen() string {
	return strings.Join(d.m.Lines(cells.Default, 200, 30, true), "\n")
}

func (d *driver) expect(what string, ok bool) {
	d.t.Helper()
	if !ok {
		d.t.Fatalf("%s; screen:\n%s", what, d.screen())
	}
}

// milestone reads the milestone titled title through the agent.
func (f *fixture) milestone(title string) protocol.Milestone {
	f.t.Helper()
	got, err := litclient.Get[protocol.Milestone](context.Background(), f.agent, project, title)
	if err != nil || len(got) != 1 {
		f.t.Fatalf("milestone %q: %v %v", title, got, err)
	}
	return got[0]
}

func (f *fixture) env(issue string, milestones ...protocol.Milestone) quick.Env {
	env := quick.Env{Gate: f.l, Project: f.project(), ProjectTitle: project, Milestones: milestones}
	if issue != "" {
		env.Issue = f.summary(issue)
		for _, id := range f.ids {
			env.Issues = append(env.Issues, f.summaryByID(id))
		}
	}
	return env
}

// Adding the selected issue to a milestone and removing it again are each
// one previewed transaction that writes only the milestone, fenced at the
// revision it was loaded at. Membership is explicit: the issue's child does
// not follow it, and no blocker is created.
func TestMembershipAddsAndRemovesTheIssueAtTheMilestoneRevision(t *testing.T) {
	f := newFixture(t, "Work", "Other")
	f.ids["Child"] = tuitest.CreateIssues(t, f.agent, project, f.ids["Work"], "Child")[0]
	tuitest.Apply(t, f.agent, litclient.CreateMilestone(project, "M1", "goal", f.ids["Other"]))
	tuitest.Apply(t, f.agent, litclient.CreateMilestone(project, "Another", ""))
	m1 := f.milestone("M1")
	work := f.summary("Work")
	commits := f.srv.Count("POST /v1/transactions")

	d := &driver{t: t, m: quick.Start(quick.SetMilestones, f.env("Work", m1, f.milestone("Another")))}
	d.expect("the picker", strings.Contains(d.screen(), "[ ] “M1”") && strings.Contains(d.screen(), "[ ] “Another”"))
	d.typeText("M1")
	d.press("enter")
	d.expect("the preview", strings.Contains(d.screen(), "Add “Work” to the milestone “M1”") &&
		strings.Contains(d.screen(), "• milestone “M1” · revision 1") && strings.Contains(d.screen(), "Would write:"))
	if f.srv.Count("POST /v1/transactions") != commits {
		t.Fatal("the preview committed")
	}
	d.press("enter")
	if !d.last.Closed || d.last.Note != "Applied: Add “Work” to the milestone “M1”." {
		t.Fatalf("result %+v; screen:\n%s", d.last, d.screen())
	}
	if f.srv.Count("POST /v1/transactions")-commits != 1 {
		t.Fatalf("%d commits", f.srv.Count("POST /v1/transactions")-commits)
	}
	got := f.milestone("M1")
	if !slices.Equal(got.IssueIDs, []string{f.ids["Other"], work.ID}) && !slices.Equal(got.IssueIDs, []string{work.ID, f.ids["Other"]}) || got.Revision != 2 {
		t.Fatalf("after adding: %+v", got)
	}
	if slices.Contains(got.IssueIDs, f.ids["Child"]) {
		t.Fatal("the child followed its parent")
	}
	if after := f.summary("Work"); len(after.BlockedBy)+len(after.Blocks) != 0 || after.Revision != work.Revision {
		t.Fatalf("the issue changed: %+v", after)
	}

	d = &driver{t: t, m: quick.Start(quick.SetMilestones, f.env("Work", got))}
	d.expect("the member is checked", strings.Contains(d.screen(), "[x] “M1” (member: enter removes it)"))
	d.press("enter")
	d.expect("the removal preview", strings.Contains(d.screen(), "Remove “Work” from the milestone “M1”") && strings.Contains(d.screen(), "milestone “M1” · revision 2"))
	d.press("enter")
	if got = f.milestone("M1"); !slices.Equal(got.IssueIDs, []string{f.ids["Other"]}) || got.Revision != 3 {
		t.Fatalf("after removing: %+v", got)
	}
}

// A milestone changed since it was loaded refuses the preview with
// revision_conflict: nothing is written.
func TestAStaleMilestoneRefusesTheMembershipPreview(t *testing.T) {
	f := newFixture(t, "Work")
	tuitest.Apply(t, f.agent, litclient.CreateMilestone(project, "M1", ""))
	stale := f.milestone("M1")
	tuitest.Apply(t, f.agent, litclient.UpdateMilestone(project, stale.ID, 0, litclient.MilestoneChange{Body: ptr("changed by the agent")}))
	commits := f.srv.Count("POST /v1/transactions")
	d := &driver{t: t, m: quick.Start(quick.SetMilestones, f.env("Work", stale))}
	d.press("enter")
	d.expect("the refusal", strings.Contains(d.screen(), "Refused (revision_conflict)") && strings.Contains(d.screen(), "Nothing was written"))
	d.press("enter") // a refused preview cannot be applied
	if f.srv.Count("POST /v1/transactions") != commits || len(f.pending()) != 0 || len(f.milestone("M1").IssueIDs) != 0 {
		t.Fatal("something was written")
	}
}

// Without milestones the picker says how to create one.
func TestMembershipWithoutMilestonesSaysSo(t *testing.T) {
	f := newFixture(t, "Work")
	d := &driver{t: t, m: quick.Start(quick.SetMilestones, f.env("Work"))}
	d.expect("the note", strings.Contains(d.screen(), "The project has no milestones yet: create one in the Milestones view."))
}

// A new milestone is created from an inline title field, previewed first
// and committed exactly once, with no members and an empty description; an
// empty title is not sent.
func TestANewMilestoneIsCreatedFromItsTitle(t *testing.T) {
	f := newFixture(t, "Work")
	commits := f.srv.Count("POST /v1/transactions")
	d := &driver{t: t, m: quick.Start(quick.NewMilestone, f.env(""))}
	d.expect("the field", strings.Contains(d.screen(), "New milestone in “test/quick”") && strings.Contains(d.screen(), "Title: ▏"))
	d.press("enter")
	d.expect("an empty title is refused locally", strings.Contains(d.screen(), "Type a title first."))
	d.typeText("Sprint q")
	d.press("enter")
	d.expect("the preview", strings.Contains(d.screen(), "Create the milestone “Sprint q” in “test/quick”") && strings.Contains(d.screen(), "• milestone “Sprint q”"))
	if f.srv.Count("POST /v1/transactions") != commits {
		t.Fatal("the preview committed")
	}
	d.press("enter")
	if !d.last.Closed || !strings.HasPrefix(d.last.Note, "Applied: Create the milestone “Sprint q”") {
		t.Fatalf("result %+v; screen:\n%s", d.last, d.screen())
	}
	if got := f.milestone("Sprint q"); len(got.IssueIDs) != 0 || got.Body != "" || got.Revision != 1 {
		t.Fatalf("created %+v", got)
	}
	if f.srv.Count("POST /v1/transactions")-commits != 1 {
		t.Fatal("not exactly one commit")
	}
}

// Renaming a milestone starts from its title and is fenced at its revision.
func TestRenamingAMilestoneIsFencedAtItsRevision(t *testing.T) {
	f := newFixture(t, "Work")
	tuitest.Apply(t, f.agent, litclient.CreateMilestone(project, "Old name", "goal"))
	m := f.milestone("Old name")
	env := f.env("")
	env.Milestone = m
	d := &driver{t: t, m: quick.Start(quick.RenameMilestone, env)}
	d.expect("prefilled", strings.Contains(d.screen(), "Rename the milestone “Old name”") && strings.Contains(d.screen(), "Title: Old name▏"))
	d.press("enter")
	d.expect("unchanged", strings.Contains(d.screen(), "The title is unchanged."))
	d.press("ctrl+u")
	d.typeText("New name")
	d.press("enter")
	d.expect("the preview", strings.Contains(d.screen(), "Rename the milestone “Old name” to “New name”") && strings.Contains(d.screen(), "milestone “Old name” · revision 1"))
	d.press("enter")
	if got := f.milestone("New name"); got.ID != m.ID || got.Body != "goal" || got.Revision != 2 {
		t.Fatalf("renamed %+v", got)
	}
}

func ptr(s string) *string { return &s }
