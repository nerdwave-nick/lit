package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/detail"
	"github.com/nerdwave-nick/lit/internal/tui/history"
	"github.com/nerdwave-nick/lit/internal/tui/keys"
	"github.com/nerdwave-nick/lit/internal/tui/render/cells"
	"github.com/nerdwave-nick/lit/internal/tui/render/sanitize"
)

// excerpt is how many cells of a comment's first line its chooser entry
// shows (Proposal).
const excerpt = 40

// openHistory opens the read-only history view for the selected issue: from
// the tree its history at once; from the detail a chooser of the issue and
// each loaded comment. Closing it returns to the same selection and scroll.
func (m Model) openHistory() (Model, tea.Cmd) {
	b := m.b
	if b == nil || b.switching || b.project == nil {
		return m, nil
	}
	b.menu = nil
	sel, ok := b.tree.Selected()
	if !ok {
		return m, nil
	}
	targets := []history.Target{{Type: "issues", ID: sel.ID, Label: `issue "` + sel.Title + `"`}}
	if m.view() == keys.Detail && b.full.got != nil && b.full.id == sel.ID {
		for _, c := range b.full.got.Comments {
			targets = append(targets, history.Target{Type: "comments", ID: c.ID, Label: commentLabel(c)})
		}
	}
	var cmd tea.Cmd
	m.hv, cmd = history.New(b.src, targets...)
	return m, cmd
}

// commentLabel names a comment in the chooser: its author, creation time
// and the start of its first non-empty line.
func commentLabel(c protocol.Comment) string {
	label := "comment by " + c.Author + " · " + c.CreatedAt
	for _, l := range strings.Split(c.Body, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return label + " · " + cells.Clip(cells.Default, sanitize.Title(l), excerpt, "…")
		}
	}
	return label
}

// historyKey gives the open history view its messages and key presses after
// the prompts, so they still come first. Quit and Ctrl-C close it and go on
// as usual; the uncertain-requests view opens over it.
func (m Model) historyKey(msg tea.Msg) (Model, tea.Cmd, bool) {
	if m.hv == nil {
		return m, nil, false
	}
	if cmd, ok := m.hv.Update(msg); ok {
		return m, cmd, true
	}
	switch msg := msg.(type) {
	case tea.MouseWheelMsg: // nothing behind the view takes the mouse
		var cmds []tea.Cmd
		for range detail.WheelLines {
			switch msg.Mouse().Button {
			case tea.MouseWheelUp:
				cmd, _ := m.hv.Do(keys.Up)
				cmds = append(cmds, cmd)
			case tea.MouseWheelDown:
				cmd, _ := m.hv.Do(keys.Down)
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...), true
	case tea.MouseClickMsg:
		return m, nil, true
	}
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil, false
	}
	km := m.prefs.Keymap
	action, _ := km.Lookup(keys.Menu, k.Key())
	if a, _ := km.Lookup(m.view(), k.Key()); a == keys.History { // its own key closes it
		action = a
	}
	switch {
	case keys.IsShutdown(k.Key()) || action == keys.Quit:
		m.hv.Close()
		m.hv = nil
		return m, nil, false
	case action == keys.UncertainRequests:
		return m, nil, false
	}
	cmd, closed := m.hv.Do(action)
	if closed {
		m.hv = nil
	}
	return m, cmd, true
}

func (m Model) historyView() string {
	method, plain := cells.Default, false
	if m.b != nil {
		method, plain = m.b.method, m.b.tree.Plain()
	}
	return m.hv.View(method, m.width, m.height, m.prefs.Keymap, plain)
}
