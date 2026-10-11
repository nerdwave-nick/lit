package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/milestones"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/quick"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
)

// Milestones: milestones (M) opens the Milestones view of the project
// (progress, creation, titles, descriptions); set_milestones (W) adds the
// selected issue to a milestone or removes it, as a quick action. A
// description is edited in a milestone_description TUI draft through the
// editor and the draft panel, like a body, but without a claim.

// milestonesState is the root model's Milestones view, when open. Model
// copies share it; it belongs to the UI goroutine.
type milestonesState struct {
	view *milestones.Model
}

// membershipMsg is the load of the milestones for set_milestones.
type membershipMsg struct {
	project string
	issue   protocol.IssueSummary
	l       milestones.Loaded
}

// milestonesAction opens the Milestones view, or set_milestones on the
// selected issue once the project's milestones are read.
func (m Model) milestonesAction(a keys.Action) (Model, tea.Cmd) {
	b := m.b
	if b == nil || b.project == nil || b.switching {
		if b != nil {
			b.qa.note = "Choose a project first."
		}
		return m, nil
	}
	b.menu, m.help = nil, false
	if a == keys.Milestones {
		if m.ms == nil {
			return m, nil
		}
		view, cmd := milestones.New(milestones.Env{Gate: b.src, Project: b.project.ID, ProjectTitle: b.project.Title, Issues: b.last.Items})
		m.ms.view = view
		return m, cmd
	}
	sel, ok := b.tree.Selected()
	if !ok {
		b.qa.note = "Select an issue first."
		return m, nil
	}
	b.qa.note = "Loading the milestones of " + titled(b.project.Title) + "…"
	src, project := b.src, b.project.ID
	return m, func() tea.Msg {
		return membershipMsg{project, sel, milestones.Load(context.Background(), src, project)}
	}
}

// milestonesOpen reports that the Milestones view is open.
func (m Model) milestonesOpen() bool { return m.ms != nil && m.ms.view != nil }

// milestonesWatch notes, without consuming the message, what changes the
// open view shows: a reloaded tree names the members, and an applied or
// discarded description draft reloads the milestones.
func (m Model) milestonesWatch(msg tea.Msg) tea.Cmd {
	if !m.milestonesOpen() {
		return nil
	}
	v := m.ms.view
	switch msg := msg.(type) {
	case issuesMsg:
		if msg.issues.Err == nil {
			v.SetIssues(msg.issues.Items)
		}
	case appliedMsg:
		if msg.a.Draft.Kind == drafts.MilestoneDescription {
			return v.Reload()
		}
	case discardedMsg:
		if msg.d.Kind == drafts.MilestoneDescription {
			return v.Reload()
		}
	}
	return nil
}

// milestonesUpdate handles the membership load and, while the view is open,
// its keys and messages. Quit, help, the uncertain-requests and Drafts views
// keep their keys unless the title field owns typed letters.
func (m Model) milestonesUpdate(msg tea.Msg) (tea.Cmd, bool) {
	if got, ok := msg.(membershipMsg); ok {
		return m.membership(got), true
	}
	if !m.milestonesOpen() {
		return nil, false
	}
	v := m.ms.view
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if keys.IsShutdown(msg.Key()) || m.help {
			return nil, false
		}
		if !v.Typing() {
			switch action, _ := m.prefs.Keymap.Lookup(keys.Menu, msg.Key()); action {
			case keys.Quit:
				return m.quit(), true
			case keys.Help, keys.UncertainRequests, keys.Drafts:
				return nil, false
			}
		}
		return m.milestonesResult(v.Key(msg, m.prefs.Keymap)), true
	case tea.PasteMsg:
		v.Paste(msg.Content)
		return nil, true
	case tea.MouseMsg:
		return nil, true
	}
	r, ok := v.Update(msg)
	if !ok {
		return nil, false
	}
	return m.milestonesResult(r), true
}

// milestonesResult applies the view's result: it closes, or a description
// draft opens (an existing draft is offered, never overwritten).
func (m Model) milestonesResult(r milestones.Result) tea.Cmd {
	switch {
	case r.Closed:
		m.ms.view = nil
	case r.Edit != nil && m.ed == nil:
		if m.b != nil {
			m.b.qa.note = "This lit-tui cannot edit milestone descriptions."
		}
	case r.Edit != nil && m.ed.busy:
		m.ed.note = "Wait for the running edit action to finish."
	case r.Edit != nil:
		return tea.Batch(r.Cmd, m.openTarget(*r.Edit, r.Detached))
	}
	return r.Cmd
}

// membership opens set_milestones on the issue with the loaded milestones.
func (m Model) membership(got membershipMsg) tea.Cmd {
	b := m.b
	if b == nil || b.project == nil || b.project.ID != got.project || b.qa.m != nil {
		return nil // switched away, or another quick action opened meanwhile
	}
	b.qa.note = ""
	if got.l.Err != nil {
		b.qa.note = "Cannot load the milestones: " + describe(got.l.Err) + "."
		return nil
	}
	env := quick.Env{Gate: b.src, Project: b.project.ID, ProjectTitle: b.project.Title, Issue: got.issue, Issues: b.last.Items}
	for _, ms := range got.l.Milestones {
		env.Milestones = append(env.Milestones, ms.Milestone)
	}
	b.menu, b.qa.m = nil, quick.Start(quick.SetMilestones, env)
	return nil
}

// milestonesView draws the view full screen, with the editing lines (a
// detached draft's path, the last edit note) under it.
func (m Model) milestonesView() string {
	method, plain := cells.Default, false
	if m.b != nil {
		method, plain = m.b.method, m.b.tree.Plain()
	}
	extra := m.editLines()
	lines := strings.Split(m.ms.view.View(method, m.width, max(m.height-len(extra), 1), m.prefs.Keymap, plain), "\n")
	lines = append(lines, extra...)
	for i, l := range lines {
		if !strings.HasPrefix(l, "\x1b[7m") {
			lines[i] = pad(method, l, m.width)
		}
	}
	return strings.Join(lines, "\n")
}

// milestoneName names a milestone record by its title when the open view
// knows it.
func (m Model) milestoneName(id string) string {
	if m.milestonesOpen() {
		if title, ok := m.ms.view.Title(id); ok {
			return "milestone " + titled(title)
		}
	}
	return "milestone " + short(id)
}
