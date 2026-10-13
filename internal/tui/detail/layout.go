package detail

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/nerdwave-nick/lit/internal/tui/render/cells"
	"github.com/nerdwave-nick/lit/internal/tui/render/sanitize"
)

// ref locates a block in the current layout: a section index and a block
// index, or block -1 for a section without a render.
type ref struct{ sec, block int }

// bref names a block across layouts: its section's record and block index.
// Focus and horizontal offsets are kept by bref.
type bref struct {
	rec   string
	block int
}

// row is one display line.
type row struct {
	text  string
	ref   ref
	wide  bool // a line of an unwrapped block (top-level code or table)
	runes int  // visible non-space runes before this row within its block
}

// layout is the flattened view of every section.
type layout struct {
	rows   []row
	starts map[ref]int // first row of each block
	ends   map[ref]int // one past its last row, including its separator
	widths map[ref]int // widest line of each unwrapped block, without trailing blanks
}

// anchor is a scroll position by content: the top row is in block (rec,
// block), after runes visible runes of it, plus extra rows without runes
// (blank lines) that start at the same offset.
type anchor struct {
	rec          string
	block        int
	runes, extra int
}

// relayout rebuilds the rows after a render, resize or style change and
// restores the scroll position from the anchor.
func (m *Model) relayout() {
	l := layout{starts: map[ref]int{}, ends: map[ref]int{}, widths: map[ref]int{}}
	margin := strings.Repeat(" ", m.style.Margin())
	for si, s := range m.secs {
		last := si == len(m.secs)-1
		if s.doc != nil && len(s.doc.Blocks) > 0 {
			for bi, b := range s.doc.Blocks {
				r := ref{si, bi}
				l.starts[r] = len(l.rows)
				n := 0
				for _, line := range b.Lines {
					l.rows = append(l.rows, row{text: line, ref: r, wide: !b.Kind.Wraps(), runes: n})
					n += visibleRunes(line)
					if !b.Kind.Wraps() {
						l.widths[r] = max(l.widths[r], contentWidth(m.method, line))
					}
				}
				if bi < len(s.doc.Blocks)-1 || !last {
					l.rows = append(l.rows, row{ref: r, runes: n})
				}
				l.ends[r] = len(l.rows)
			}
			continue
		}
		text := ""
		switch {
		case s.err != nil:
			text = margin + "could not render: " + sanitize.Title(s.err.Error())
		case s.placeholder && s.doc == nil:
			text = margin + Placeholder
		}
		r := ref{si, -1}
		l.starts[r] = len(l.rows)
		l.rows = append(l.rows, row{text: text, ref: r})
		if !last {
			l.rows = append(l.rows, row{ref: r})
		}
		l.ends[r] = len(l.rows)
	}
	m.lay = l
	m.y = m.restore(m.anc)
	m.clampY()
	m.fixFocus()
}

func visibleRunes(s string) int {
	n := 0
	for _, r := range ansi.Strip(s) {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}

// contentWidth is the width of s without trailing blanks, which Glamour may
// pad lines with.
func contentWidth(meth cells.Method, s string) int {
	return cells.Width(meth, strings.TrimRight(ansi.Strip(s), " "))
}

func (m *Model) index(rec string) int {
	for i, s := range m.secs {
		if s.RecordID == rec {
			return i
		}
	}
	return -1
}

// target is the block an anchor falls in: its own, or the nearest one left
// in its section when the section has fewer blocks or no render now.
func (m *Model) target(a anchor) (ref, bool) {
	si := m.index(a.rec)
	if si < 0 {
		return ref{}, false
	}
	if _, ok := m.lay.starts[ref{si, -1}]; ok {
		return ref{si, -1}, true
	}
	b := max(a.block, 0)
	for b > 0 {
		if _, ok := m.lay.starts[ref{si, b}]; ok {
			break
		}
		b--
	}
	return ref{si, b}, true
}

// restore returns the row an anchor points at.
func (m *Model) restore(a anchor) int {
	r, ok := m.target(a)
	if !ok {
		return 0
	}
	start, end := m.lay.starts[r], m.lay.ends[r]
	if r.block != a.block {
		return start
	}
	rows := m.lay.rows
	f := start
	for f < end && rows[f].runes < a.runes {
		f++
	}
	switch {
	case f == end:
		return end - 1
	case rows[f].runes > a.runes:
		return f - 1 // the anchor rune is inside the previous row
	}
	for i := 0; i < a.extra && f+1 < end && rows[f+1].runes == a.runes; i++ {
		f++
	}
	return f
}

// saveAnchor records the top row as the anchor.
func (m *Model) saveAnchor() {
	if len(m.lay.rows) == 0 {
		return
	}
	r := m.lay.rows[m.y]
	f := m.y
	for f > m.lay.starts[r.ref] && m.lay.rows[f-1].runes == r.runes {
		f--
	}
	m.anc = anchor{rec: m.secs[r.ref.sec].RecordID, block: r.ref.block, runes: r.runes, extra: m.y - f}
}

func (m *Model) clampY() { m.y = max(0, min(m.y, len(m.lay.rows)-m.height)) }

// scroll moves the view by d rows; it is the user's choice, so the anchor
// follows.
func (m *Model) scroll(d int) {
	m.y += d
	m.clampY()
	m.saveAnchor()
	m.fixFocus()
}

func (m *Model) brefOf(r ref) bref { return bref{m.secs[r.sec].RecordID, r.block} }

func (m *Model) refOf(b bref) (ref, bool) {
	if b.rec == "" {
		return ref{}, false
	}
	r := ref{m.index(b.rec), b.block}
	_, ok := m.lay.starts[r]
	return r, ok
}

// scrollable reports whether r is an unwrapped block wider than the pane.
func (m *Model) scrollable(r ref) bool { return m.lay.widths[r] > m.width }

// visible reports whether a line of block r is on screen.
func (m *Model) visible(r ref) bool {
	for i := max(m.lay.starts[r], m.y); i < min(m.lay.ends[r], m.y+m.height); i++ {
		if m.lay.rows[i].wide {
			return true
		}
	}
	return false
}

// fixFocus keeps the focused block while it is scrollable and on screen,
// and otherwise focuses the first scrollable block on screen, if any.
func (m *Model) fixFocus() {
	if r, ok := m.refOf(m.foc); ok && m.scrollable(r) && m.visible(r) {
		return
	}
	m.foc = bref{}
	for i := m.y; i < min(len(m.lay.rows), m.y+m.height); i++ {
		if r := m.lay.rows[i]; r.wide && m.scrollable(r.ref) {
			m.foc = m.brefOf(r.ref)
			return
		}
	}
}

// hscroll scrolls the focused block by d cells.
func (m *Model) hscroll(d int) {
	r, ok := m.refOf(m.foc)
	if !ok {
		return
	}
	limit := max(0, m.lay.widths[r]-m.width)
	m.xoff[m.foc] = max(0, min(min(m.xoff[m.foc], limit)+d, limit))
}

// cycle moves the focus to the next (dir 1) or previous (dir -1) scrollable
// block, scrolling it into view.
func (m *Model) cycle(dir int) {
	var all []ref
	for i, r := range m.lay.rows {
		if r.wide && m.lay.starts[r.ref] == i && m.scrollable(r.ref) {
			all = append(all, r.ref)
		}
	}
	cur, ok := m.refOf(m.foc)
	next := -1
	for i, r := range all {
		switch {
		case ok && r == cur:
			next = i + dir
		case !ok && dir > 0 && m.lay.starts[r] >= m.y && next < 0:
			next = i
		case !ok && dir < 0 && m.lay.starts[r] < m.y:
			next = i
		}
	}
	if next < 0 || next >= len(all) {
		return
	}
	m.foc = m.brefOf(all[next])
	if !m.visible(all[next]) {
		m.y = m.lay.starts[all[next]]
		m.clampY()
		m.saveAnchor()
	}
}
