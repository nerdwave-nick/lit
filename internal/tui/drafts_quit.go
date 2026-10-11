package tui

import (
	"sort"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/draftsview"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
)

// quit is the quit action: with drafts open in this session it asks first,
// once; otherwise it starts the orderly shutdown.
func (m Model) quit() tea.Cmd {
	if m.dv != nil {
		if open := m.openDrafts(); len(open) > 0 {
			if m.b != nil {
				m.b.menu = nil
			}
			m.dv.prompt = draftsview.NewPrompt(open)
			return nil
		}
	}
	return m.life.Quit()
}

// openDrafts are the drafts opened in this session that still exist (not
// applied or discarded), oldest first.
func (m Model) openDrafts() []drafts.Draft {
	var out []drafts.Draft
	for key, d := range m.dv.opened {
		got, ok, err := m.dv.store.Find(d.ProjectID, d.Kind, d.RecordID)
		switch {
		case err != nil: // cannot tell: ask rather than quit silently
			out = append(out, d)
		case ok:
			out = append(out, got)
		default:
			delete(m.dv.opened, key)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

// promptUpdate gives the quit prompt its keys before anything below it.
// Ctrl-C closes it and shuts down as usual.
func (m Model) promptUpdate(msg tea.Msg) (tea.Cmd, bool) {
	if m.dv == nil || m.dv.prompt == nil {
		return nil, false
	}
	switch msg := msg.(type) {
	case tea.MouseMsg:
		return nil, true
	case tea.KeyPressMsg:
		if keys.IsShutdown(msg.Key()) {
			m.dv.prompt = nil
			return nil, false
		}
		action, _ := m.prefs.Keymap.Lookup(keys.Menu, msg.Key())
		choice, ok := m.dv.prompt.Do(action)
		if !ok {
			return nil, true
		}
		open := m.dv.prompt.Drafts()
		m.dv.prompt = nil
		switch choice {
		case draftsview.Quit:
			return m.life.Quit(), true
		case draftsview.ApplyNow:
			return m.applyNow(open), true
		}
		return nil, true
	}
	return nil, false
}

// applyNow cancels quitting and previews applying the first open draft.
func (m Model) applyNow(open []drafts.Draft) tea.Cmd {
	d := open[0]
	if _, ok := draftOpeners[d.Kind]; !ok {
		return m.openDraft(d)
	}
	if m.ed.busy {
		m.ed.note = "Wait for the running edit action to finish."
		return nil
	}
	cmd := m.ed.previewCmd(d)
	if n := len(open) - 1; n > 0 {
		m.ed.note += " " + plural(n, "more open draft") + " after this one."
	}
	return cmd
}

func (m Model) promptView() string {
	method, plain := cells.Default, false
	if m.b != nil {
		method, plain = m.b.method, m.b.tree.Plain()
	}
	return m.dv.prompt.View(method, m.width, m.prefs.Keymap, plain)
}
