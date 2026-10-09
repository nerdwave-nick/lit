package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
)

// A menu longer than its pane scrolls to keep the selected command shown:
// the heading stays, and moving back up scrolls back.
func TestTheActionMenuScrollsToKeepTheSelectionShown(t *testing.T) {
	m := &actionMenu{}
	for n := range 10 {
		m.items = append(m.items, keys.Binding{Action: keys.Action(fmt.Sprint("a", n)), Description: fmt.Sprint("command ", n), Keys: []string{"x"}})
	}
	shown := func(h int) string { return strings.Join(m.lines(cells.Default, 40, h, true), "\n") }
	if s := shown(5); !strings.HasPrefix(s, "Actions") || !strings.Contains(s, "> command 0") || strings.Contains(s, "command 4") {
		t.Fatalf("top:\n%s", s)
	}
	m.cursor = 9
	if s := shown(5); !strings.HasPrefix(s, "Actions") || !strings.Contains(s, "> command 9") || strings.Contains(s, "command 5") {
		t.Fatalf("bottom:\n%s", s)
	}
	m.cursor = 2
	if s := shown(5); !strings.Contains(s, "> command 2") || len(strings.Split(s, "\n")) != 5 {
		t.Fatalf("back up:\n%s", s)
	}
}
