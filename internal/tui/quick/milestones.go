package quick

import (
	"slices"
	"strings"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// Milestone quick actions. Milestones have no claims: each change is one
// optimistic transaction that writes only the milestone, fenced at the
// revision it was loaded at (a new milestone is unfenced), and commits
// exactly its preview. Membership is explicit: adding an issue does not add
// its children and creates no blocker.

// Membership adds issue i to milestone ms (add) or removes it.
func Membership(project string, i protocol.IssueSummary, ms protocol.Milestone, add bool) Change {
	c := litclient.MilestoneChange{RemoveIssues: []string{i.ID}}
	summary := "Remove " + quoted(i.Title) + " from the milestone " + quoted(ms.Title)
	if add {
		c = litclient.MilestoneChange{AddIssues: []string{i.ID}}
		summary = "Add " + quoted(i.Title) + " to the milestone " + quoted(ms.Title)
	}
	return Change{i.ID, summary, litclient.UpdateMilestone(project, ms.ID, ms.Revision, c), []string{i.ID}}
}

// Creation creates a milestone titled title in the project, with no
// members and an empty description.
func Creation(project, projectTitle, title string) Change {
	return Change{"", "Create the milestone " + quoted(title) + " in " + quoted(projectTitle), litclient.CreateMilestone(project, title, ""), nil}
}

// Rename renames milestone ms, fenced at its revision.
func Rename(project string, ms protocol.Milestone, title string) Change {
	return Change{"", "Rename the milestone " + quoted(ms.Title) + " to " + quoted(title),
		litclient.UpdateMilestone(project, ms.ID, ms.Revision, litclient.MilestoneChange{Title: &title}), nil}
}

// startMilestone opens a milestone action.
func (m *Model) startMilestone() {
	switch m.kind {
	case SetMilestones:
		m.pick = milestonesPicker(m.env.Issue, m.env.Milestones)
		if len(m.pick.options) == 0 {
			m.note = "The project has no milestones yet: create one in the Milestones view."
		}
	case NewMilestone:
		m.field = &field{}
	case RenameMilestone:
		m.field = &field{text: []rune(m.env.Milestone.Title)}
	}
}

// milestonesPicker lists the project's milestones by title, the ones i is a
// member of checked; choosing one adds i or removes it.
func milestonesPicker(i protocol.IssueSummary, milestones []protocol.Milestone) *picker {
	p := &picker{title: "Milestones of " + quoted(i.Title) + " (enter adds or removes it)"}
	sorted := slices.Clone(milestones)
	slices.SortStableFunc(sorted, func(a, b protocol.Milestone) int {
		return strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
	})
	for _, ms := range sorted {
		label := "[ ] " + quoted(ms.Title)
		if slices.Contains(ms.IssueIDs, i.ID) {
			label = "[x] " + quoted(ms.Title) + " (member: enter removes it)"
		}
		p.options = append(p.options, option{label: label, value: ms.ID})
	}
	return p
}

// chosenMilestone previews adding or removing the issue for the chosen
// milestone.
func (m *Model) chosenMilestone(o option) Result {
	for _, ms := range m.env.Milestones {
		if ms.ID == o.value {
			return m.prepare(Membership(m.env.Project, m.env.Issue, ms, !slices.Contains(ms.IssueIDs, m.env.Issue.ID)))
		}
	}
	return Result{}
}

// fieldHeading is the line above the title field.
func (m *Model) fieldHeading() string {
	switch m.kind {
	case NewMilestone:
		return "New milestone in " + quoted(m.env.ProjectTitle)
	case RenameMilestone:
		return "Rename the milestone " + quoted(m.env.Milestone.Title)
	}
	return "Edit the title of " + quoted(m.env.Issue.Title)
}

// titleChange is the change the title field submits, or a note why there
// is nothing to send.
func (m *Model) titleChange(title string) (Change, string) {
	switch {
	case m.kind != EditTitle && strings.TrimSpace(title) == "":
		return Change{}, "Type a title first."
	case m.kind == NewMilestone:
		return Creation(m.env.Project, m.env.ProjectTitle, title), ""
	case m.kind == RenameMilestone && title == m.env.Milestone.Title, m.kind == EditTitle && title == m.env.Issue.Title:
		return Change{}, "The title is unchanged."
	case m.kind == RenameMilestone:
		return Rename(m.env.Project, m.env.Milestone, title), ""
	}
	return Title(m.env.Project, m.env.Issue, title), ""
}

// nameMilestones titles the previewed milestones: loaded ones by ID, a new
// one by the typed title.
func (m *Model) nameMilestones() {
	for n, a := range m.preview.Affected {
		if a.Type != "milestones" || a.Title != "" {
			continue
		}
		for _, ms := range append([]protocol.Milestone{m.env.Milestone}, m.env.Milestones...) {
			if ms.ID == a.ID {
				m.preview.Affected[n].Title = ms.Title
			}
		}
		if m.preview.Affected[n].Title == "" && m.kind == NewMilestone {
			m.preview.Affected[n].Title = m.field.value()
		}
	}
}
