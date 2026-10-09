package quick

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
)

// Kind is a quick action.
type Kind int

const (
	EditTitle Kind = iota
	SetState
	SetLabels
	SetAssignee
	SetParent
	AddLink
	RemoveLink
)

// Env is what an action needs from the browser: the gate, the project's
// UUID, the issue as last loaded and the loaded issues (for pickers and
// titles).
type Env struct {
	Gate         Gate
	Project      string // the project's UUID
	ProjectTitle string
	Issue        protocol.IssueSummary
	Issues       []protocol.IssueSummary
}

type stage int

const (
	choosing   stage = iota // the title field or a picker
	previewing              // the preview request runs
	previewed               // the preview is shown; confirm or cancel
	committing              // the commit runs; keys wait
	failed                  // the commit was refused or failed
)

// Model is one open quick action. It belongs to the UI goroutine.
type Model struct {
	env      Env
	kind     Kind
	stage    stage
	field    *field  // EditTitle
	pick     *picker // the other kinds
	relation string  // AddLink: the chosen relation
	note     string  // a problem with the input, shown under it
	preview  Preview
	outcome  Outcome
	gen      int
}

// Result tells the browser what happened.
type Result struct {
	Cmd    tea.Cmd
	Closed bool   // drop the model
	Reload bool   // records may have changed: reload the project
	Note   string // for the footer, for example "Applied: Close “X”."
}

type (
	previewMsg struct {
		m   *Model
		gen int
		p   Preview
	}
	commitMsg struct {
		m   *Model
		gen int
		out Outcome
	}
)

// Start opens action k on env.Issue.
func Start(k Kind, env Env) *Model {
	m := &Model{env: env, kind: k}
	i := env.Issue
	switch k {
	case EditTitle:
		m.field = &field{text: []rune(i.Title)}
	case SetState:
		m.pick = statePicker(i)
	case SetLabels:
		m.pick = labelsPicker(i, env.Issues)
	case SetAssignee:
		m.pick = assigneePicker(i, env.Issues)
	case SetParent:
		m.pick = parentPicker(i, env.Issues)
	case AddLink:
		m.pick = relationPicker(i)
	case RemoveLink:
		m.pick = unlinkPicker(i, m.title)
		if len(m.pick.options) == 0 {
			m.note = quoted(i.Title) + " has no links."
		}
	}
	return m
}

// lookup finds a loaded issue.
func (m *Model) lookup(id string) (protocol.IssueSummary, bool) {
	for _, i := range m.env.Issues {
		if i.ID == id {
			return i, true
		}
	}
	return protocol.IssueSummary{}, false
}

// title is a loaded issue's quoted title, or its short UUID.
func (m *Model) title(id string) string {
	if i, ok := m.lookup(id); ok {
		return quoted(i.Title)
	}
	return short(id)
}

func short(id string) string { return id[:min(len(id), 8)] }

// Typing reports whether typed letters belong to the model (its field or
// picker filter) rather than to the keymap.
func (m *Model) Typing() bool { return m.stage == choosing }

// Paste inserts pasted text into the field or the picker's filter.
func (m *Model) Paste(s string) {
	switch {
	case m.stage != choosing:
	case m.field != nil:
		m.field.paste(s)
	case m.pick != nil:
		m.pick.filter.paste(s)
	}
}

// Key handles a key press.
func (m *Model) Key(k tea.KeyPressMsg, km keys.Keymap) Result {
	action, _ := km.Lookup(keys.Menu, k.Key())
	switch m.stage {
	case choosing:
		return m.choose(k, km)
	case previewing:
		if action == keys.Cancel {
			return Result{Closed: true}
		}
	case previewed:
		switch {
		case action == keys.Cancel:
			return Result{Closed: true}
		case action == keys.Open && !m.preview.Refused():
			return m.commit()
		}
	case failed:
		if action == keys.Cancel || action == keys.Open {
			return Result{Closed: true}
		}
	}
	return Result{}
}

func (m *Model) choose(k tea.KeyPressMsg, km keys.Keymap) Result {
	m.note = ""
	if m.field != nil {
		submit, cancel := m.field.key(k, km)
		switch {
		case cancel:
			return Result{Closed: true}
		case !submit:
			return Result{}
		case m.field.value() == m.env.Issue.Title:
			m.note = "The title is unchanged."
			return Result{}
		}
		return m.prepare(Title(m.env.Project, m.env.Issue, m.field.value()))
	}
	chosen, cancel := m.pick.key(k, km)
	switch {
	case cancel:
		return Result{Closed: true}
	case chosen == nil:
		return Result{}
	}
	return m.chosen(*chosen)
}

// chosen turns the picked option into a change, or moves to the next picker.
func (m *Model) chosen(o option) Result {
	i, p := m.env.Issue, m.env.Project
	switch m.kind {
	case SetState:
		if o.value == i.State {
			m.note = "The issue is already " + o.value + "."
			return Result{}
		}
		return m.prepare(State(p, i, o.value))
	case SetLabels:
		add, remove := m.pick.diff()
		if len(add)+len(remove) == 0 {
			m.note = "Nothing changes: toggle labels with enter first."
			return Result{}
		}
		return m.prepare(Labels(p, i, add, remove))
	case SetAssignee:
		if cur := i.Assignee; cur == nil && o.value == "" || cur != nil && *cur == o.value {
			m.note = "That is the current assignee."
			return Result{}
		}
		return m.prepare(Assignee(p, i, o.value))
	case SetParent:
		if cur := i.ParentID; cur != nil && *cur == o.value {
			m.note = "That is the current parent."
			return Result{}
		}
		return m.prepare(Parent(p, i, o.value, m.titleText(o.value)))
	case AddLink:
		if m.relation == "" {
			m.relation = o.value
			m.pick = linkTargetPicker(i, o.value, m.env.Issues)
			return Result{}
		}
		return m.prepare(Link(p, i, m.relation, o.value, m.titleText(o.value)))
	case RemoveLink:
		relation, id, _ := strings.Cut(o.value, " ")
		return m.prepare(Unlink(p, i, relation, id, m.titleText(id)))
	}
	return Result{}
}

// titleText is a loaded issue's title, or its short UUID.
func (m *Model) titleText(id string) string {
	if i, ok := m.lookup(id); ok {
		return i.Title
	}
	return short(id)
}

// prepare starts previewing c.
func (m *Model) prepare(c Change) Result {
	m.stage, m.preview = previewing, Preview{Change: c}
	m.gen++
	gen, g, lookup := m.gen, m.env.Gate, m.lookup
	return Result{Cmd: func() tea.Msg { return previewMsg{m, gen, Prepare(context.Background(), g, c, lookup)} }}
}

// commit submits the shown preview, exactly once.
func (m *Model) commit() Result {
	m.stage = committing
	m.gen++
	gen, g, p := m.gen, m.env.Gate, m.preview
	return Result{Cmd: func() tea.Msg { return commitMsg{m, gen, Commit(context.Background(), g, p)} }}
}

// Update takes this model's own messages; it reports whether msg was one.
func (m *Model) Update(msg tea.Msg) (Result, bool) {
	switch msg := msg.(type) {
	case previewMsg:
		if msg.m != m || msg.gen != m.gen {
			return Result{}, msg.m == m
		}
		m.stage, m.preview = previewed, msg.p
		for n, a := range m.preview.Affected {
			if a.Type == "projects" && a.ID == m.env.Project {
				m.preview.Affected[n].Title = m.env.ProjectTitle
			}
		}
		// A stale or refused preview may mean the tree is out of date.
		return Result{Reload: msg.p.Err != nil}, true
	case commitMsg:
		if msg.m != m || msg.gen != m.gen {
			return Result{}, msg.m == m
		}
		m.outcome = msg.out
		if msg.out.Err == nil {
			return Result{Closed: true, Reload: true, Note: "Applied: " + m.preview.Change.Summary + "."}, true
		}
		m.stage = failed
		return Result{Reload: true}, true
	}
	return Result{}, false
}
