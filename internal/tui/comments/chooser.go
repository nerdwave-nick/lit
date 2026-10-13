package comments

import (
	"strings"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/keys"
	"github.com/nerdwave-nick/lit/internal/tui/render/cells"
	"github.com/nerdwave-nick/lit/internal/tui/render/sanitize"
)

// Excerpt is how many cells of a comment's first line its chooser entry
// shows (Proposal).
const Excerpt = 40

// Label names a comment in a chooser: its author, creation time and the
// start of its first non-empty line, sanitized.
func Label(c protocol.Comment) string {
	label := "comment by " + sanitize.Title(c.Author) + " · " + sanitize.Title(c.CreatedAt)
	for _, l := range strings.Split(c.Body, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return label + " · " + cells.Clip(cells.Default, sanitize.Title(l), Excerpt, "…")
		}
	}
	return label
}

// Chooser picks one of an issue's comments to edit, oldest first. Menu
// keys move; Enter chooses; Escape closes.
type Chooser struct {
	// Issue and Title identify the commented issue.
	Issue, Title string
	Comments     []protocol.Comment
	cursor       int
}

// NewChooser chooses among cs, the comments of the issue id titled title.
func NewChooser(id, title string, cs []protocol.Comment) *Chooser {
	return &Chooser{Issue: id, Title: title, Comments: cs}
}

// Do performs a menu-context action: it returns the chosen comment, or
// closed when the chooser is closed without one.
func (c *Chooser) Do(a keys.Action) (chosen *protocol.Comment, closed bool) {
	last := len(c.Comments) - 1
	switch a {
	case keys.Up:
		c.cursor = max(c.cursor-1, 0)
	case keys.Down:
		c.cursor = min(c.cursor+1, last)
	case keys.Top, keys.PageUp:
		c.cursor = 0
	case keys.Bottom, keys.PageDown:
		c.cursor = last
	case keys.Cancel:
		return nil, true
	case keys.Open:
		if last >= 0 {
			chosen := c.Comments[c.cursor]
			return &chosen, true
		}
		return nil, true
	}
	return nil, false
}

// Lines draws the chooser in a w×h area: a heading, then one line per
// comment around the cursor, then the key hint. The cursor's line is
// reversed, or marked with ">" when plain.
func (c *Chooser) Lines(m cells.Method, w, h int, plain bool, km keys.Keymap) []string {
	out := []string{"Edit which comment of “" + sanitize.Title(c.Title) + "”?", ""}
	room := max(h-len(out)-2, 1)
	first := max(min(c.cursor-room/2, len(c.Comments)-room), 0)
	for i := first; i < len(c.Comments) && i < first+room; i++ {
		marker := "  "
		if i == c.cursor {
			marker = "> "
		}
		line := clip(m, marker+Label(c.Comments[i]), w)
		if i == c.cursor && !plain {
			line = "\x1b[7m" + line + "\x1b[m"
		}
		out = append(out, line)
	}
	for len(out) < h-1 {
		out = append(out, "")
	}
	out[0] = clip(m, out[0], w)
	return append(out[:max(h-1, 1)], clip(m, km.Hint(keys.Menu, keys.Open, keys.Cancel), w))
}

func clip(m cells.Method, s string, w int) string {
	if w <= 0 {
		return s
	}
	return cells.Clip(m, s, w, "…")
}
