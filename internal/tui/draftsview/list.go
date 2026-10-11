package draftsview

import (
	"fmt"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/sanitize"
)

// Item is one draft in the Drafts view.
type Item struct {
	Draft drafts.Draft
	// Project names the draft's project ("project “P”"), or is empty.
	Project string
	// Current is the record's current revision when Changed knows it.
	Current int64
	Changed bool
}

// line describes the item in one line: kind, title, project, revision, start
// and, for a changed revision, that it opens as a conflict.
func (it Item) line() string {
	d := it.Draft
	parts := []string{KindLabel(d.Kind), "“" + sanitize.Title(d.Title) + "”"}
	if it.Project != "" {
		parts = append(parts, sanitize.Title(it.Project))
	}
	if it.Changed {
		parts = append(parts, fmt.Sprintf("changed: revision %d → %d, opens as a conflict", d.Revision, it.Current))
	} else {
		parts = append(parts, fmt.Sprintf("revision %d", d.Revision))
	}
	if t := d.Created(); !t.IsZero() {
		parts = append(parts, "started "+t.Local().Format("2006-01-02 15:04"))
	}
	return strings.Join(parts, " · ")
}

// Model is the open Drafts view: the drafts, the selection and the scroll.
// It belongs to the UI goroutine.
type Model struct {
	items  []Item
	err    error
	cursor int
	offset int
}

// New returns an empty view; Set fills it.
func New() *Model { return &Model{} }

// Set replaces the listed drafts, keeping the selected draft selected while
// it is listed. err reports drafts that could not be read.
func (m *Model) Set(items []Item, err error) {
	prev, had := m.Selected()
	m.items, m.err = items, err
	if had {
		for i, it := range items {
			if Key(it.Draft) == Key(prev.Draft) && it.Draft.ProjectID == prev.Draft.ProjectID {
				m.cursor = i
				return
			}
		}
	}
	m.cursor = max(min(m.cursor, len(items)-1), 0)
}

// Len is how many drafts are listed.
func (m *Model) Len() int { return len(m.items) }

// Selected is the selected draft.
func (m *Model) Selected() (Item, bool) {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return Item{}, false
	}
	return m.items[m.cursor], true
}

// Do performs a menu-context action. Open chooses the selected draft
// (chosen); Cancel and the view's own action close the view (closed).
func (m *Model) Do(a keys.Action) (chosen *Item, closed bool) {
	last := max(len(m.items)-1, 0)
	switch a {
	case keys.Cancel, keys.Drafts:
		return nil, true
	case keys.Open:
		if it, ok := m.Selected(); ok {
			return &it, false
		}
	case keys.Up:
		m.cursor = max(m.cursor-1, 0)
	case keys.Down:
		m.cursor = min(m.cursor+1, last)
	case keys.Top, keys.PageUp:
		m.cursor = 0
	case keys.Bottom, keys.PageDown:
		m.cursor = last
	}
	return nil, false
}

// View draws the view as exactly h lines of at most w cells: a header, one
// line per draft, the selected draft's path, and the key hint.
func (m *Model) View(method cells.Method, w, h int, km keys.Keymap, plain bool) string {
	out := []string{fmt.Sprintf("Unapplied drafts · %d", len(m.items))}
	var tail []string
	if line := errorLine(m.err); line != "" {
		tail = append(tail, sanitize.Title(line))
	}
	if it, ok := m.Selected(); ok {
		tail = append(tail, "File: "+sanitize.Title(it.Draft.Path()))
	}
	tail = append(tail, km.Hint(keys.Menu, keys.Open, keys.Cancel))
	rows := max(h-len(out)-len(tail)-1, 1)
	if len(m.items) == 0 {
		out = append(out, "No unapplied drafts.")
	}
	m.offset = max(min(m.offset, m.cursor), m.cursor-rows+1, 0)
	for i := m.offset; i < len(m.items) && i < m.offset+rows; i++ {
		line := "  " + m.items[i].line()
		if i == m.cursor {
			line = "> " + m.items[i].line()
			if !plain {
				line = "\x1b[7m" + clip(method, line, w) + "\x1b[m"
			}
		}
		out = append(out, line)
	}
	for len(out) < h-len(tail) {
		out = append(out, "")
	}
	out = append(out, tail...)
	if h > 0 && len(out) > h {
		out = append(out[:h-1], out[len(out)-1])
	}
	for i, l := range out {
		out[i] = clip(method, l, w)
	}
	return strings.Join(out, "\n")
}

func clip(method cells.Method, s string, w int) string {
	if w <= 0 {
		return s
	}
	return cells.Clip(method, s, w, "…")
}
