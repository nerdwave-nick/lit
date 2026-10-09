// Package tree is lit-tui's issue hierarchy: a collapsible tree of a
// project's issue summaries, of any depth, navigated with the tree context's
// actions. The selection is an issue UUID, so it survives refreshes, moves
// and reordering.
//
// The model owns no data loading. Its owner sets the issues (SetIssues), the
// size and the styling, passes actions and mouse events, and draws View.
package tree

import (
	"maps"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
)

// Change reports what an update did.
type Change struct {
	Selection bool // the selected issue changed
	Open      bool // the user asked to open the selected issue
	Missing   bool // the previously selected issue is no longer listed
}

// State is a project's tree state, kept while the user looks at another
// project.
type State struct {
	Selected string
	Expanded map[string]bool
	Row      int // the selection's row on screen
}

// Model is the issue tree. It belongs to the UI goroutine.
type Model struct {
	f        *forest
	rows     []row
	index    map[string]int // row of each visible issue
	expanded map[string]bool
	selected string
	cursor   int // row of the selection; kept when the selection vanishes
	offset   int // first row shown

	width, height  int
	plain, focused bool
	method         cells.Method
}

// New returns an empty tree.
func New() *Model {
	m := &Model{f: build(nil), expanded: map[string]bool{}, method: cells.Default, focused: true}
	m.reindex()
	return m
}

// SetIssues replaces the issues, keeping the selection by UUID: it stays on
// its screen row when it can, and its ancestors are expanded if it moved
// under a collapsed parent. A selection that is no longer listed moves to the
// row in its place and is reported as Missing.
func (m *Model) SetIssues(items []protocol.IssueSummary) Change {
	prev, screen := m.selected, m.cursor-m.offset
	m.f = build(items)
	var ch Change
	if prev != "" && m.f.nodes[prev] != nil {
		for _, a := range m.f.path(prev) {
			if a.ID != prev {
				m.expanded[a.ID] = true
			}
		}
		m.reindex()
		m.cursor = m.index[prev]
	} else {
		m.reindex()
		m.cursor = min(max(m.cursor, 0), len(m.rows)-1)
		m.selected = ""
		if m.cursor >= 0 {
			m.selected = m.rows[m.cursor].id
		}
		ch.Selection = m.selected != prev
		ch.Missing = prev != ""
	}
	m.offset = m.cursor - screen
	m.clamp()
	return ch
}

// SetSize sets the pane size in cells.
func (m *Model) SetSize(w, h int) {
	m.width, m.height = max(w, 0), max(h, 0)
	m.clamp()
}

// SetPlain marks the selection with ">" instead of styling (no colours).
func (m *Model) SetPlain(plain bool) { m.plain = plain }

// Plain reports whether the selection is marked without styling.
func (m *Model) Plain() bool { return m.plain }

// SetFocused says whether the tree has the keyboard focus; the selection is
// highlighted less strongly without it.
func (m *Model) SetFocused(focused bool) { m.focused = focused }

// SetMethod sets how cells are measured (mode 2027).
func (m *Model) SetMethod(method cells.Method) { m.method = method }

// Do performs a tree-context action.
func (m *Model) Do(a keys.Action) Change {
	if len(m.rows) == 0 {
		return Change{}
	}
	page := max(m.height-1, 1)
	switch a {
	case keys.Up:
		return m.move(m.cursor - 1)
	case keys.Down:
		return m.move(m.cursor + 1)
	case keys.PageUp:
		return m.move(m.cursor - page)
	case keys.PageDown:
		return m.move(m.cursor + page)
	case keys.Top:
		return m.move(0)
	case keys.Bottom:
		return m.move(len(m.rows) - 1)
	case keys.Expand:
		n := m.f.nodes[m.selected]
		if len(n.children) == 0 {
			return Change{}
		}
		if !m.expanded[m.selected] {
			m.setExpanded(m.selected, true)
			return Change{}
		}
		return m.move(m.cursor + 1)
	case keys.Collapse:
		if m.expanded[m.selected] && len(m.f.nodes[m.selected].children) > 0 {
			m.setExpanded(m.selected, false)
			return Change{}
		}
		if p := m.f.nodes[m.selected].parent; p != "" {
			return m.move(m.index[p])
		}
	case keys.Open:
		return Change{Open: true}
	}
	return Change{}
}

// Click selects the issue on pane row y; clicking the selected issue expands
// or collapses it.
func (m *Model) Click(y int) Change {
	r := m.offset + y
	if y < 0 || r >= len(m.rows) {
		return Change{}
	}
	if r == m.cursor {
		m.setExpanded(m.selected, !m.expanded[m.selected])
		return Change{}
	}
	return m.move(r)
}

// Wheel moves the selection by lines (negative is up).
func (m *Model) Wheel(lines int) Change {
	if len(m.rows) == 0 {
		return Change{}
	}
	return m.move(m.cursor + lines)
}

// Selected returns the selected issue.
func (m *Model) Selected() (protocol.IssueSummary, bool) { return m.Issue(m.selected) }

// Issue returns a loaded issue.
func (m *Model) Issue(id string) (protocol.IssueSummary, bool) {
	if n := m.f.nodes[id]; n != nil {
		return n.issue, true
	}
	return protocol.IssueSummary{}, false
}

// Path lists id's ancestors from the root, then the issue itself.
func (m *Model) Path(id string) []protocol.IssueSummary {
	if m.f.nodes[id] == nil {
		return nil
	}
	return m.f.path(id)
}

// Children lists id's loaded children in tree order.
func (m *Model) Children(id string) []string {
	if n := m.f.nodes[id]; n != nil {
		return append([]string{}, n.children...)
	}
	return nil
}

// Len is the number of loaded issues.
func (m *Model) Len() int { return len(m.f.nodes) }

// Rows is the number of visible rows.
func (m *Model) Rows() int { return len(m.rows) }

// State returns the selection, expansion and scroll to remember.
func (m *Model) State() State {
	return State{Selected: m.selected, Expanded: maps.Clone(m.expanded), Row: m.cursor - m.offset}
}

// Restore resets the tree to s; the next SetIssues applies it.
func (m *Model) Restore(s State) {
	m.f = build(nil)
	m.selected, m.offset, m.cursor = s.Selected, 0, max(s.Row, 0)
	m.expanded = maps.Clone(s.Expanded)
	if m.expanded == nil {
		m.expanded = map[string]bool{}
	}
	m.reindex()
}

func (m *Model) move(r int) Change {
	r = min(max(r, 0), len(m.rows)-1)
	prev := m.selected
	m.cursor, m.selected = r, m.rows[r].id
	m.follow()
	return Change{Selection: m.selected != prev}
}

func (m *Model) setExpanded(id string, open bool) {
	if len(m.f.nodes[id].children) == 0 {
		return
	}
	m.expanded[id] = open
	m.reindex()
	m.cursor = m.index[m.selected]
	m.clamp()
}

func (m *Model) reindex() {
	m.rows = m.f.visible(m.expanded)
	m.index = make(map[string]int, len(m.rows))
	for i, r := range m.rows {
		m.index[r.id] = i
	}
}

// follow scrolls just enough to show the selection.
func (m *Model) follow() {
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.height > 0 && m.cursor >= m.offset+m.height {
		m.offset = m.cursor - m.height + 1
	}
	m.clamp()
}

func (m *Model) clamp() {
	m.offset = min(m.offset, len(m.rows)-m.height)
	m.offset = max(m.offset, 0)
	if m.height > 0 && m.cursor >= 0 && len(m.rows) > 0 {
		m.offset = min(m.offset, m.cursor)
		m.offset = max(m.offset, m.cursor-m.height+1)
	}
}
