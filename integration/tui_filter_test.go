package integration

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// filterLine is the footer line describing the filters, or "".
func (p *pane) filterLine() string {
	for _, l := range strings.Split(p.screen(), "\n") {
		if strings.Contains(l, "Filter:") {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

// The search and the filters drive the real lit-tui in tmux against a
// temporary lit-server: a search (through lit grep) keeps the ancestors of
// a deep match visible, typed letters belong to the search input, no match
// shows the empty state, filters combine with the search, and one key clears
// everything while keeping the selection.
func TestTUIFilterInTmux(t *testing.T) {
	e := newTUIEnv(t)
	s := e.seeder(protocol.Actor{Name: "seed", Kind: "human"})
	tuitest.Apply(t, s, litclient.CreateProject("test/filter", "spec"))
	parent := ""
	for d := 0; d < 6; d++ {
		parent = tuitest.CreateIssues(t, s, "test/filter", parent, fmt.Sprintf("Level %d", d))[0]
	}
	body := "the NEEDLE is deep down here"
	tuitest.Apply(t, s, litclient.UpdateIssue("test/filter", parent, 0, litclient.IssueChange{Body: &body}))
	tuitest.CreateIssues(t, s, "test/filter", "", "Unrelated root")
	closed := tuitest.CreateIssues(t, s, "test/filter", "", "Closed needle")[0]
	tuitest.Apply(t, s, litclient.CloseIssue("test/filter", closed, 0))

	p := e.launch("filter")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Level 0") && strings.Contains(s, "Unrelated root") })

	p.keys("/")
	p.e.tmux("send-keys", "-t", p.name, "-l", "q needle")
	p.keys("Enter")
	p.await("the empty state", func(s string) bool {
		return strings.Contains(s, "No issues match the filters.") && strings.Contains(p.filterLine(), `search "q needle"`) &&
			strings.Contains(p.filterLine(), "0 of 8 issues match") && strings.Contains(p.filterLine(), "F clear filters")
	})

	p.keys("/", "C-u")
	p.e.tmux("send-keys", "-t", p.name, "-l", "needle")
	p.keys("Enter")
	p.await("the deep match with its ancestors", func(s string) bool {
		return strings.Contains(s, "Level 5") && strings.Contains(s, "Level 0") && strings.Contains(s, "Closed needle") &&
			!strings.Contains(s, "Unrelated root") && strings.Contains(p.filterLine(), "2 of 8 issues match")
	})

	p.keys("f")
	p.await("the filter panel", func(s string) bool { return strings.Contains(s, "State: any") })
	p.keys("Enter")
	p.await("open only", func(s string) bool { return strings.Contains(s, "State: open") })
	p.keys("Escape")
	p.await("open and searched", func(s string) bool {
		// The panel is closed and the tree is back (a quick next key would
		// otherwise join the Escape into one sequence).
		return !strings.Contains(s, "Filters (") && strings.Contains(s, "▾ Level 0") && strings.Contains(s, "  Level 5") &&
			!strings.Contains(s, "Closed needle") && strings.Contains(p.filterLine(), "Filter: open · search")
	})

	p.keys("End")
	p.await("the match selected", func(string) bool { return strings.HasSuffix(p.crumb(), "Level 4 › Level 5") })
	p.keys("F")
	p.await("the full tree with the same selection", func(s string) bool {
		return strings.Contains(s, "Unrelated root") && strings.Contains(s, "Closed needle") && p.filterLine() == "" && strings.HasSuffix(p.crumb(), "Level 4 › Level 5")
	})
	p.quit()
}
