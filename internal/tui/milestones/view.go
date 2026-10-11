package milestones

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/edit"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/quick"
)

// Env is what the view needs from the browser: the gate (reads, and the
// commits of its quick actions), the project, and its loaded issues, which
// name the members and give their states.
type Env struct {
	Gate         quick.Gate
	Project      string // the project's UUID
	ProjectTitle string
	Issues       []protocol.IssueSummary
}

// Result tells the root model what happened.
type Result struct {
	Cmd    tea.Cmd
	Closed bool // the view closed
	// Edit asks the root model to open (or offer) this description draft,
	// in the editor or Detached.
	Edit     *edit.Target
	Detached bool
	Note     string // the outcome of a quick action, for the view and the footer
}

// action is one entry of a milestone's action list.
type action int

const (
	actRename action = iota
	actEdit
	actDetached
	actBack
)

var actionLabels = []string{
	actRename:   "Rename the milestone",
	actEdit:     "Edit the description in the editor",
	actDetached: "Edit the description detached (shows the draft path)",
	actBack:     "Back to the list",
}

// Model is the open Milestones view: the project's milestones and their
// progress, the selected one's description and members, and the actions
// that create, rename and describe milestones. It belongs to the UI
// goroutine.
type Model struct {
	env     Env
	items   []litclient.MilestoneView
	err     error
	loaded  bool
	loading bool
	at      time.Time
	gen     int
	cursor  int // 0 is "New milestone"; n is items[n-1]
	offset  int
	actions *int         // the selected milestone's action list, with its cursor
	qa      *quick.Model // a title field and its preview, when open
	note    string
}

type loadedMsg struct {
	m   *Model
	gen int
	l   Loaded
	at  time.Time
}

// New opens the view of env's project and starts loading its milestones.
func New(env Env) (*Model, tea.Cmd) {
	m := &Model{env: env}
	return m, m.Reload()
}

// Reload reads the milestones again; the selection follows its milestone.
func (m *Model) Reload() tea.Cmd {
	m.gen++
	m.loading = true
	gen, g, project := m.gen, m.env.Gate, m.env.Project
	return func() tea.Msg {
		return loadedMsg{m, gen, Load(context.Background(), g, project), time.Now()}
	}
}

// SetIssues replaces the loaded issues after the tree reloads.
func (m *Model) SetIssues(issues []protocol.IssueSummary) { m.env.Issues = issues }

// Project is the UUID of the view's project.
func (m *Model) Project() string { return m.env.Project }

// Typing reports whether typed letters belong to the title field.
func (m *Model) Typing() bool { return m.qa != nil && m.qa.Typing() }

// Paste inserts pasted text into the title field.
func (m *Model) Paste(s string) {
	if m.qa != nil {
		m.qa.Paste(s)
	}
}

// Update takes the view's own messages, including its quick actions'.
func (m *Model) Update(msg tea.Msg) (Result, bool) {
	if l, ok := msg.(loadedMsg); ok {
		if l.m != m {
			return Result{}, false
		}
		if l.gen == m.gen {
			m.set(l.l, l.at)
		}
		return Result{}, true
	}
	if m.qa == nil {
		return Result{}, false
	}
	r, ok := m.qa.Update(msg)
	if !ok {
		return Result{}, false
	}
	return m.quickResult(r), true
}

// set shows a load, keeping the selected milestone selected.
func (m *Model) set(l Loaded, at time.Time) {
	selected, had := m.selected()
	m.loading, m.loaded, m.err = false, true, l.Err
	if l.Err == nil || len(l.Milestones) > 0 {
		m.items, m.at = l.Milestones, at
	}
	if had {
		for n, it := range m.items {
			if it.ID == selected.ID {
				m.cursor = n + 1
			}
		}
	}
	m.cursor = min(m.cursor, len(m.items))
}

// selected is the selected milestone, if a milestone row is selected.
func (m *Model) selected() (litclient.MilestoneView, bool) {
	if m.cursor < 1 || m.cursor > len(m.items) {
		return litclient.MilestoneView{}, false
	}
	return m.items[m.cursor-1], true
}

// quickResult applies a quick action's result: a closed one is dropped,
// its note shown, and anything attempted reloads the milestones.
func (m *Model) quickResult(r quick.Result) Result {
	if r.Closed {
		m.qa = nil
	}
	if r.Note != "" {
		m.note = r.Note
	}
	out := Result{Cmd: r.Cmd, Note: r.Note}
	if r.Reload {
		out.Cmd = tea.Batch(r.Cmd, m.Reload())
	}
	return out
}

// Key handles a key press: the title field first, then the action list,
// then the list. Menu-context keys move and choose; Escape and the
// milestones key close.
func (m *Model) Key(k tea.KeyPressMsg, km keys.Keymap) Result {
	if m.qa != nil {
		return m.quickResult(m.qa.Key(k, km))
	}
	a, _ := km.Lookup(keys.Menu, k.Key())
	if m.actions != nil {
		return m.actionKey(a)
	}
	last := len(m.items)
	switch a {
	case keys.Cancel, keys.Milestones:
		return Result{Closed: true}
	case keys.Refresh:
		return Result{Cmd: m.Reload()}
	case keys.Up:
		m.cursor = max(m.cursor-1, 0)
	case keys.Down:
		m.cursor = min(m.cursor+1, last)
	case keys.Top, keys.PageUp:
		m.cursor = 0
	case keys.Bottom, keys.PageDown:
		m.cursor = last
	case keys.Open:
		m.note = ""
		if _, ok := m.selected(); ok {
			cursor := 0
			m.actions = &cursor
			return Result{}
		}
		m.qa = quick.Start(quick.NewMilestone, m.quickEnv())
	}
	return Result{}
}

// actionKey handles the selected milestone's action list.
func (m *Model) actionKey(a keys.Action) Result {
	switch a {
	case keys.Up:
		*m.actions = max(*m.actions-1, 0)
	case keys.Down:
		*m.actions = min(*m.actions+1, len(actionLabels)-1)
	case keys.Top, keys.PageUp:
		*m.actions = 0
	case keys.Bottom, keys.PageDown:
		*m.actions = len(actionLabels) - 1
	case keys.Cancel:
		m.actions = nil
	case keys.Open:
		chosen := action(*m.actions)
		m.actions = nil
		ms, ok := m.selected()
		switch {
		case !ok:
		case chosen == actRename:
			env := m.quickEnv()
			env.Milestone = ms.Milestone
			m.qa = quick.Start(quick.RenameMilestone, env)
		case chosen == actEdit, chosen == actDetached:
			t := Description(m.env.Project, ms.Milestone)
			return Result{Edit: &t, Detached: chosen == actDetached}
		}
	}
	return Result{}
}

// quickEnv is the environment of the view's quick actions.
func (m *Model) quickEnv() quick.Env {
	env := quick.Env{Gate: m.env.Gate, Project: m.env.Project, ProjectTitle: m.env.ProjectTitle, Issues: m.env.Issues}
	for _, it := range m.items {
		env.Milestones = append(env.Milestones, it.Milestone)
	}
	return env
}

// Title is a loaded milestone's title.
func (m *Model) Title(id string) (string, bool) {
	for _, it := range m.items {
		if it.ID == id {
			return it.Title, true
		}
	}
	return "", false
}
