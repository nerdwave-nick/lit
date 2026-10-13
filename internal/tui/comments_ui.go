package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/lit/internal/tui/comments"
	"github.com/nerdwave-nick/lit/internal/tui/issue"
	"github.com/nerdwave-nick/lit/internal/tui/keys"
	"github.com/nerdwave-nick/lit/internal/tui/render/cells"
)

// Comment editing: new_comment writes a new comment on the selected issue
// in a draft (claim-exempt); edit_comment loads the issue's comments,
// chooses one and edits it (claim-on-edit). Both then run like a body edit:
// the draft panel previews, applies, reloads, diffs and discards.

// commentsMsg is the load of an issue's comments for the chooser.
type commentsMsg struct {
	id, title string
	got       issue.Detail
}

// commentAction runs new_comment or edit_comment on the selected issue.
func (m Model) commentAction(a keys.Action) tea.Cmd {
	e, b := m.ed, m.b
	if b == nil || b.project == nil || b.switching {
		return nil
	}
	sel, ok := b.tree.Selected()
	switch {
	case !ok:
		e.note = "Select an issue first."
		return nil
	case e.busy:
		e.note = "Wait for the running edit action to finish."
		return nil
	case a == keys.NewComment:
		return m.openTarget(comments.New(b.project.ID, sel.ID, sel.Title), false)
	}
	e.busy, e.note = true, "Loading the comments of "+titled(sel.Title)+"…"
	src, project := b.src, b.project.ID
	return func() tea.Msg {
		return commentsMsg{id: sel.ID, title: sel.Title, got: issue.Load(context.Background(), src, project, sel.ID)}
	}
}

// commentUpdate handles the comment load: it opens the chooser.
func (m Model) commentUpdate(msg tea.Msg) (tea.Cmd, bool) {
	e := m.ed
	got, ok := msg.(commentsMsg)
	if !ok {
		return nil, false
	}
	e.busy, e.note = false, ""
	switch {
	case got.got.Err != nil && len(got.got.Comments) == 0:
		e.note = "Cannot load the comments of " + titled(got.title) + ": " + describe(got.got.Err) + "."
	case len(got.got.Comments) == 0:
		e.note = titled(got.title) + " has no comments to edit; " + firstKey(m, keys.Tree, keys.NewComment) + " writes a new one."
	default:
		e.chooser = comments.NewChooser(got.id, got.title, got.got.Comments)
	}
	return nil, true
}

// chooserKey handles a menu-context action in the comment chooser: Enter
// edits the chosen comment.
func (m Model) chooserKey(a keys.Action) tea.Cmd {
	e := m.ed
	ch := e.chooser
	chosen, closed := ch.Do(a)
	if closed {
		e.chooser = nil
	}
	if chosen == nil || m.b == nil || m.b.project == nil {
		return nil
	}
	return m.openTarget(comments.Edit(m.b.project.ID, *chosen, ch.Title), false)
}

// chooserView draws the comment chooser full screen.
func (m Model) chooserView() string {
	method, plain := cells.Default, false
	if m.b != nil {
		method, plain = m.b.method, m.b.tree.Plain()
	}
	lines := m.ed.chooser.Lines(method, m.width, m.height, plain, m.prefs.Keymap)
	for i, l := range lines {
		lines[i] = pad(method, l, m.width)
	}
	return strings.Join(lines, "\n")
}
