package comments

import (
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
)

func comment(id, author, body string) protocol.Comment {
	return protocol.Comment{SchemaVersion: 1, ID: id, Revision: 1, Author: author, Body: body, CreatedAt: "2026-10-09T12:00:00Z", IssueID: "i"}
}

// The chooser names each comment by author, time and first line, with
// terminal controls made visible; menu keys move and Enter chooses the
// comment under the cursor, Escape closes without one.
func TestTheChooserNamesCommentsAndChoosesOne(t *testing.T) {
	cs := []protocol.Comment{comment("a", "Claude", "\n\n  first line\nsecond"), comment("b", "evil\x1b[2J", "x\x07y"), comment("c", "person", "")}
	c := NewChooser("i", "Issue \x1b]0;t\x07", cs)
	lines := c.Lines(cells.Default, 80, 10, true, keys.Default())
	if len(lines) != 10 || !strings.HasPrefix(lines[0], "Edit which comment of “Issue ") ||
		lines[2] != "> comment by Claude · 2026-10-09T12:00:00Z · first line" || lines[4] != "  comment by person · 2026-10-09T12:00:00Z" {
		t.Fatalf("lines %q", lines)
	}
	for _, l := range lines {
		if strings.ContainsAny(l, "\x1b\x07") {
			t.Fatalf("a control reached the screen: %q", l)
		}
	}
	if !strings.Contains(lines[9], "enter") || !strings.Contains(lines[9], "esc") {
		t.Fatalf("hint %q", lines[9])
	}
	for _, a := range []keys.Action{keys.Down, keys.Down, keys.Down, keys.Up} {
		if got, closed := c.Do(a); got != nil || closed {
			t.Fatalf("%s chose %v %v", a, got, closed)
		}
	}
	if got, closed := c.Do(keys.Open); got == nil || got.ID != "b" || !closed {
		t.Fatalf("chose %+v %v", got, closed)
	}
	if got, closed := NewChooser("i", "t", cs).Do(keys.Cancel); got != nil || !closed {
		t.Fatalf("cancel %+v %v", got, closed)
	}
	if styled := NewChooser("i", "t", cs).Lines(cells.Default, 20, 6, false, keys.Default()); !strings.HasPrefix(styled[2], "\x1b[7m> comment by") || cells.Width(cells.Default, styled[3]) > 20 {
		t.Fatalf("styled %q", styled)
	}
}
