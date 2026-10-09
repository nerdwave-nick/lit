package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/quick"
)

// quickActions is the open quick action, if any, and the last one's result
// for the footer.
type quickActions struct {
	m    *quick.Model
	note string
}

// quickKinds maps the tree and detail actions to quick actions.
var quickKinds = map[keys.Action]quick.Kind{
	keys.EditTitle:   quick.EditTitle,
	keys.SetState:    quick.SetState,
	keys.SetLabels:   quick.SetLabels,
	keys.SetAssignee: quick.SetAssignee,
	keys.SetParent:   quick.SetParent,
	keys.Link:        quick.AddLink,
	keys.Unlink:      quick.RemoveLink,
}

// quickAction opens the quick action for action on the selected issue, if
// action is one.
func (b *browser) quickAction(action keys.Action) (tea.Cmd, bool) {
	kind, ok := quickKinds[action]
	if !ok {
		return nil, false
	}
	sel, selected := b.tree.Selected()
	if !selected || b.project == nil {
		b.qa.note = "Select an issue first."
		return nil, true
	}
	b.menu, b.qa.note = nil, ""
	b.qa.m = quick.Start(kind, quick.Env{Gate: b.src, Project: b.project.ID, ProjectTitle: b.project.Title, Issue: sel, Issues: b.last.Items})
	return nil, true
}

// quickKey gives a key press to the open quick action.
func (b *browser) quickKey(msg tea.KeyPressMsg) tea.Cmd {
	return b.quickResult(b.qa.m.Key(msg, b.km))
}

// quickUpdate takes the quick actions' own messages and pastes.
func (b *browser) quickUpdate(msg tea.Msg) (tea.Cmd, bool) {
	if b.qa.m == nil {
		return nil, false
	}
	if paste, ok := msg.(tea.PasteMsg); ok && b.qa.m.Typing() {
		b.qa.m.Paste(paste.Content)
		return nil, true
	}
	r, ok := b.qa.m.Update(msg)
	if !ok {
		return nil, false
	}
	return b.quickResult(r), true
}

// quickResult applies a quick action's result: close it, note its outcome,
// and reload the project after anything was attempted.
func (b *browser) quickResult(r quick.Result) tea.Cmd {
	if r.Closed {
		b.qa.m = nil
	}
	if r.Note != "" {
		b.qa.note = r.Note
	}
	if r.Reload {
		return tea.Batch(r.Cmd, b.refresh())
	}
	return r.Cmd
}
