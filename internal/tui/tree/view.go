package tree

import (
	"strconv"
	"strings"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/render/cells"
	"github.com/nerdwave-nick/lit/internal/tui/render/sanitize"
)

const (
	indentStep = 2
	minTitle   = 8 // cells a title keeps before badges are dropped
	ellipsis   = "…"

	selectedFocused = "\x1b[7m" // reverse video
	selectedBlurred = "\x1b[4m" // underline while another pane has the focus
	faint           = "\x1b[2m"
	reset           = "\x1b[m"
)

// View draws exactly the pane's height in lines of exactly its width.
func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return strings.Repeat("\n", max(m.height-1, 0))
	}
	lines := make([]string, 0, m.height)
	switch {
	case len(m.rows) > 0:
	case m.match != nil:
		lines = append(lines, m.pad("No issues match the filters."))
	default:
		lines = append(lines, m.pad("No issues in this project."))
	}
	for r := m.offset; r < len(m.rows) && len(lines) < m.height; r++ {
		lines = append(lines, m.line(m.rows[r]))
	}
	for len(lines) < m.height {
		lines = append(lines, strings.Repeat(" ", m.width))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) pad(s string) string {
	s = cells.Clip(m.method, s, m.width, ellipsis)
	return s + strings.Repeat(" ", max(m.width-cells.Width(m.method, s), 0))
}

// line draws one row: the marker, the match column while filtered, the
// indentation (capped at half the pane; deeper rows show their depth
// instead), the expander, the sanitized title truncated to fit, and badges
// for the state and the claim owner. Ancestors shown only as context for a
// match are dimmed.
func (m *Model) line(r row) string {
	n := m.f.nodes[r.id]
	sel := r.id == m.selected
	marker := ""
	if m.plain {
		marker = "  "
		if sel {
			marker = "> "
		}
	}
	context := m.match != nil && !m.hits[r.id]
	switch {
	case m.match == nil:
	case context:
		marker += "  "
	case m.plain:
		marker += "* "
	default:
		marker += "• "
	}
	prefix := marker + m.indent(r.depth) + m.expander(n) + " "
	badges := m.badges(n.issue)
	title := sanitize.Title(n.issue.Title)
	fixed := cells.Width(m.method, prefix)
	room := m.width - fixed - cells.Width(m.method, badges)
	if room < min(minTitle, cells.Width(m.method, title)) {
		badges, room = "", m.width-fixed
	}
	title = cells.Clip(m.method, title, max(room, 0), ellipsis)
	text := cells.Clip(m.method, prefix+title, m.width, ellipsis)
	gap := max(m.width-cells.Width(m.method, text)-cells.Width(m.method, badges), 0)
	plainLine := text + strings.Repeat(" ", gap) + badges
	if m.plain {
		return plainLine
	}
	if (n.issue.State == "closed" || context) && !sel {
		plainLine = faint + plainLine + reset
	}
	switch {
	case sel && m.focused:
		return selectedFocused + plainLine + reset
	case sel:
		return selectedBlurred + plainLine + reset
	}
	return plainLine
}

func (m *Model) indent(depth int) string {
	limit := m.width / 2
	if depth*indentStep <= limit {
		return strings.Repeat(" ", depth*indentStep)
	}
	label := strconv.Itoa(depth) + "›"
	return strings.Repeat(" ", max(limit-len(label), 0)) + label
}

func (m *Model) expander(n *node) string {
	switch {
	case len(m.children(n.issue.ID)) == 0:
		return " "
	case m.expansion()[n.issue.ID] && m.plain:
		return "-"
	case m.expansion()[n.issue.ID]:
		return "▾"
	case m.plain:
		return "+"
	}
	return "▸"
}

// badges name the state when it is not open and the claim's owner by actor
// name, or by client ID when the service names no actor.
func (m *Model) badges(i protocol.IssueSummary) string {
	var parts []string
	if i.State != "open" {
		parts = append(parts, sanitize.Title(i.State))
	}
	if i.Claimed && i.Claim != nil {
		owner := ClaimOwner(i.Claim, false)
		if m.plain {
			parts = append(parts, "claimed:"+owner)
		} else {
			parts = append(parts, "⚑ "+owner)
		}
	}
	if mark := m.marks[i.ID]; mark != "" {
		parts = append(parts, sanitize.Title(mark))
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " · ")
}

// ClaimOwner names a claim's owner: the actor's name (with its kind when
// long), or the owner's client ID prefix when the service names no actor.
func ClaimOwner(c *protocol.Claim, long bool) string {
	if c.OwnerActor != nil && c.OwnerActor.Name != "" {
		name := sanitize.Title(c.OwnerActor.Name)
		if long && c.OwnerActor.Kind != "" {
			name += " (" + sanitize.Title(c.OwnerActor.Kind) + ")"
		}
		return name
	}
	id := c.OwnerClientID
	if len(id) > 8 {
		id = id[:8]
	}
	return "client " + sanitize.Title(id)
}
