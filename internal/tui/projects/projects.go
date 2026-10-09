// Package projects is lit-tui's project switcher: a list of every project,
// read page by page as the selection reaches the end, from which the user
// chooses the project to browse. It uses the menu context's actions.
//
// The model owns no data loading. Its owner resets it, loads the page a
// Result asks for (More) and passes it to AddPage.
package projects

import (
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/recovery"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/sanitize"
)

// Heading is the switcher's first line.
const Heading = "Switch project"

// Result reports what an update asks the owner to do.
type Result struct {
	Chosen *protocol.Project // browse this project
	More   *string           // load the page after this cursor ("" is the first page)
	Cancel bool              // close the switcher
}

// Model is the switcher. It belongs to the UI goroutine.
type Model struct {
	items   []protocol.Project
	next    *string // cursor of the next page; nil when the list is complete
	loading bool
	err     error
	current string // the project being browsed

	cursor, offset int
	width, height  int
	plain          bool
	method         cells.Method
}

// New returns an empty switcher.
func New() *Model { return &Model{method: cells.Default} }

// SetSize sets the pane size in cells.
func (m *Model) SetSize(w, h int) { m.width, m.height = max(w, 0), max(h, 0); m.follow() }

// SetPlain marks the selection with ">" instead of styling.
func (m *Model) SetPlain(plain bool) { m.plain = plain }

// SetMethod sets how cells are measured (mode 2027).
func (m *Model) SetMethod(method cells.Method) { m.method = method }

// Reset starts a fresh list whose first page is loading; current is the
// project being browsed, preselected once it is listed.
func (m *Model) Reset(current string) {
	*m = Model{width: m.width, height: m.height, plain: m.plain, method: m.method, current: current, loading: true}
}

// AddPage appends a loaded page, or records the error that ended loading.
func (m *Model) AddPage(p litclient.Page[protocol.Project], err error) {
	m.loading, m.err = false, err
	if err != nil {
		return
	}
	first := len(m.items) == 0
	m.items = append(m.items, p.Items...)
	m.next = p.NextCursor
	if first {
		for i, it := range m.items {
			if it.ID == m.current {
				m.cursor = i
			}
		}
	}
	m.follow()
}

// Selected is the selected row: a project's index, or len(projects) for the
// trailing loading, more or error row.
func (m *Model) Selected() int { return m.cursor }

// Do performs a menu-context action.
func (m *Model) Do(a keys.Action) Result {
	page := max(m.height-2, 1)
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
		return m.move(m.rows() - 1)
	case keys.Open:
		if m.cursor < len(m.items) {
			p := m.items[m.cursor]
			return Result{Chosen: &p}
		}
		return m.more()
	case keys.Cancel:
		return Result{Cancel: true}
	}
	return Result{}
}

// Click selects the row at pane line y (line 0 is the heading); clicking the
// selected row chooses it.
func (m *Model) Click(y int) Result {
	r := m.offset + y - 1
	if y < 1 || r >= m.rows() {
		return Result{}
	}
	if r == m.cursor {
		return m.Do(keys.Open)
	}
	return m.move(r)
}

// Wheel moves the selection by n rows.
func (m *Model) Wheel(n int) Result { return m.move(m.cursor + n) }

// rows counts the project rows plus the trailing status row, if any.
func (m *Model) rows() int {
	if m.trailer() != "" {
		return len(m.items) + 1
	}
	return len(m.items)
}

func (m *Model) trailer() string {
	switch {
	case m.loading:
		return "Loading projects…"
	case m.err != nil:
		return "Loading stopped: " + sanitize.Title(recovery.Describe(m.err)) + " (Enter retries)"
	case m.next != nil:
		return "More projects…"
	case len(m.items) == 0:
		return "No projects. Create one with lit projects create."
	}
	return ""
}

// move selects row r; reaching the "more" row asks for the next page.
func (m *Model) move(r int) Result {
	if m.rows() == 0 {
		return Result{}
	}
	m.cursor = min(max(r, 0), m.rows()-1)
	m.follow()
	if m.cursor == len(m.items) {
		return m.more()
	}
	return Result{}
}

func (m *Model) more() Result {
	if m.loading || m.next == nil && m.err == nil {
		return Result{}
	}
	cursor := ""
	if m.next != nil {
		cursor = *m.next
	}
	m.loading, m.err = true, nil
	return Result{More: &cursor}
}

func (m *Model) follow() {
	body := max(m.height-1, 1)
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+body {
		m.offset = m.cursor - body + 1
	}
	m.offset = max(min(m.offset, m.rows()-body), 0)
}

// View draws exactly the pane's height in lines of exactly its width.
func (m *Model) View() string {
	if m.height == 0 {
		return ""
	}
	lines := []string{m.pad(Heading, false)}
	for r := m.offset; r < m.rows() && len(lines) < m.height; r++ {
		text := m.trailer()
		if r < len(m.items) {
			text = sanitize.Title(m.items[r].Title)
			if m.items[r].ID == m.current {
				text += "  (current)"
			}
		}
		lines = append(lines, m.pad(text, r == m.cursor))
	}
	for len(lines) < m.height {
		lines = append(lines, strings.Repeat(" ", m.width))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) pad(text string, sel bool) string {
	marker := "  "
	if sel && m.plain {
		marker = "> "
	}
	s := cells.Clip(m.method, marker+text, m.width, "…")
	s += strings.Repeat(" ", max(m.width-cells.Width(m.method, s), 0))
	if sel && !m.plain {
		return "\x1b[7m" + s + "\x1b[m"
	}
	return s
}
