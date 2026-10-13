package detail

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nerdwave-nick/lit/internal/tui/keys"
	"github.com/nerdwave-nick/lit/internal/tui/render/cells"
)

// Update handles the viewport's keys (detail context), mouse wheel, mode 2027
// reports and its own render and timer messages. Other messages are ignored.
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case renderedMsg:
		if msg.id == m.id {
			m.accept(msg)
		}
	case placeholderMsg:
		for _, s := range m.secs {
			if msg.id == m.id && s.serial == msg.serial && s.doc == nil && s.err == nil {
				s.placeholder = true
				m.relayout()
			}
		}
	case debounceMsg:
		if msg.id == m.id && msg.gen == m.resizeGen && m.debouncing {
			m.debouncing = false
			return m.sync()
		}
	case tea.ModeReportMsg:
		if msg.Mode == ansi.ModeUnicodeCore {
			m.SetMethod(cells.MethodForUnicodeCore(msg.Value))
		}
	case tea.KeyPressMsg:
		if a, ok := m.keymap.Lookup(keys.Detail, msg.Key()); ok {
			m.act(a)
		}
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			m.scroll(-WheelLines)
		case tea.MouseWheelDown:
			m.scroll(WheelLines)
		case tea.MouseWheelLeft:
			m.hscroll(-ScrollStep)
		case tea.MouseWheelRight:
			m.hscroll(ScrollStep)
		}
	}
	return nil
}

// accept shows a finished render if its section still wants exactly it.
func (m *Model) accept(msg renderedMsg) {
	for _, s := range m.secs {
		if s.slot != msg.slot {
			continue
		}
		if s.slot.Accept(msg.res) && msg.res.Key == s.want {
			s.show(msg.res.Key, msg.res.Doc, msg.res.Err)
			m.relayout()
		}
		return
	}
}

func (m *Model) act(a keys.Action) {
	page := max(1, m.height-1)
	switch a {
	case keys.Up:
		m.scroll(-1)
	case keys.Down:
		m.scroll(1)
	case keys.PageUp:
		m.scroll(-page)
	case keys.PageDown:
		m.scroll(page)
	case keys.Top:
		m.scroll(-len(m.lay.rows))
	case keys.Bottom:
		m.scroll(len(m.lay.rows))
	case keys.Left:
		m.hscroll(-ScrollStep)
	case keys.Right:
		m.hscroll(ScrollStep)
	case keys.NextBlock:
		m.cycle(1)
	case keys.PrevBlock:
		m.cycle(-1)
	}
}
