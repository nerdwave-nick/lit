package detail

import (
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
)

// Markers. Without styling (the ASCII style: NO_COLOR, TERM=dumb) they are
// plain characters: > marks the focused block and $ a clipped edge, as in
// Emacs on a text terminal.
const (
	focusStyled = "\x1b[34m▌\x1b[39m"
	rightStyled = "\x1b[2m›\x1b[22m"
	leftStyled  = "\x1b[2m‹\x1b[22m"
	focusPlain  = ">"
	edgePlain   = "$"
	reset       = "\x1b[m"
)

func (m *Model) plain() bool { return m.style == render.ASCII }

func (m *Model) marker(styled string) string {
	if m.plain() {
		return edgePlain
	}
	return styled
}

// View draws exactly the pane: height lines of exactly width cells.
func (m *Model) View() string {
	if m.height <= 0 {
		return ""
	}
	lines := make([]string, m.height)
	for i := range lines {
		s := ""
		if r := m.y + i; r < len(m.lay.rows) {
			s = m.line(m.lay.rows[r])
		}
		if !m.plain() {
			s += reset
		}
		lines[i] = s + strings.Repeat(" ", max(0, m.width-cells.Width(m.method, s)))
	}
	return strings.Join(lines, "\n")
}

// line draws one row. Lines of a scrollable block show the cells at the
// block's offset between the fixed document margin (focus marker, left
// overflow marker) and the right overflow marker; every other line is
// clipped to the pane.
func (m *Model) line(r row) string {
	w, margin := m.width, m.style.Margin()
	if !r.wide || !m.scrollable(r.ref) || w < margin+2 {
		tail := ""
		if contentWidth(m.method, r.text) > w {
			tail = m.marker(rightStyled)
		}
		return cells.Clip(m.method, r.text, w, tail)
	}
	b := m.brefOf(r.ref)
	x := min(m.xoff[b], m.lay.widths[r.ref]-w)
	end := x + w // the line's cells [margin+x, end) fill the pane after the margin
	var content string
	if contentWidth(m.method, r.text) > end {
		content = cells.Cut(m.method, r.text, margin+x, end-1) + m.marker(rightStyled)
	} else {
		content = cells.Cut(m.method, r.text, margin+x, end)
	}
	gutter := " "
	if b == m.foc {
		gutter = focusStyled
		if m.plain() {
			gutter = focusPlain
		}
	}
	gutter += strings.Repeat(" ", margin-2)
	if x > 0 && contentWidth(m.method, r.text) > margin {
		gutter += m.marker(leftStyled)
	} else {
		gutter += " "
	}
	return gutter + content
}
