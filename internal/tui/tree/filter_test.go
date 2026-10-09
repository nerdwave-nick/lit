package tree

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
)

func ids(want ...string) func(protocol.IssueSummary) bool {
	return func(i protocol.IssueSummary) bool { return slices.Contains(want, i.ID) }
}

// rowIDs lists the visible rows' issues, top to bottom.
func rowIDs(m *Model) []string {
	var out []string
	for _, r := range m.rows {
		out = append(out, r.id)
	}
	return out
}

// A filter keeps its matches and every ancestor of a match, expanded so the
// matches show; everything else is hidden. Ancestors kept only as context
// are marked differently from matches.
func TestFilterKeepsMatchesWithTheirAncestors(t *testing.T) {
	m := New()
	m.SetSize(40, 8)
	m.SetPlain(true)
	m.SetIssues(sample())
	m.SetFilter(ids("a1x", "b"))
	if got := strings.Join(rowIDs(m), " "); got != "a a1 a1x b" {
		t.Fatalf("rows %q, want a a1 a1x b:\n%s", got, strings.Join(plain(m), "\n"))
	}
	if !m.Filtered() || m.Matches() != 2 || !m.Match("a1x") || m.Match("a") {
		t.Fatalf("filtered %v, matches %d", m.Filtered(), m.Matches())
	}
	got := plain(m)
	match, context := got[indexOf(got, "Grandchild")], got[indexOf(got, "First root")]
	if !strings.Contains(match, "*") || strings.Contains(context, "*") {
		t.Fatalf("matches and context rows look alike:\n%s", strings.Join(got, "\n"))
	}
	m.SetPlain(false)
	styled := strings.Split(m.View(), "\n")
	if row := styled[indexOf(got, "Child one")]; !strings.Contains(row, faint) || !strings.Contains(styled[indexOf(got, "Grandchild")], "•") {
		t.Fatalf("context row is not dimmed or the match is unmarked:\n%s", strings.Join(styled, "\n"))
	}
	fits(t, m, 40, 8)
	// A matching parent does not bring its non-matching children.
	m.SetFilter(ids("a"))
	if got := strings.Join(rowIDs(m), " "); got != "a" {
		t.Fatalf("rows %q, want only a", got)
	}
	if strings.Contains(plain(m)[0], "▸") || strings.Contains(plain(m)[0], "▾") {
		t.Fatalf("a match without matching children shows an expander: %q", plain(m)[0])
	}
}

// A match at the bottom of a deep chain keeps every ancestor up to its root,
// and navigation moves along that path.
func TestFilterKeepsDeepAncestors(t *testing.T) {
	var items []protocol.IssueSummary
	parent := ""
	for d := 0; d < 80; d++ {
		id := fmt.Sprintf("n%02d", d)
		items = append(items, iss(id, parent, d, fmt.Sprintf("Level %d", d)))
		items = append(items, iss(id+"-side", parent, d, fmt.Sprintf("Side %d", d)))
		parent = id
	}
	m := New()
	m.SetSize(36, 12)
	m.SetIssues(items)
	m.SetFilter(ids("n79"))
	rows := rowIDs(m)
	if len(rows) != 80 || rows[0] != "n00" || rows[79] != "n79" {
		t.Fatalf("%d rows: %v", len(rows), rows)
	}
	if got := selected(t, m); got != "n00" {
		t.Fatalf("the shown selection moved: %s", got)
	}
	m.Do(keys.Bottom)
	if got := selected(t, m); got != "n79" {
		t.Fatalf("bottom: %s", got)
	}
	if path := m.Path("n79"); len(path) != 80 {
		t.Fatalf("path %d", len(path))
	}
	fits(t, m, 36, 12)
	m.Do(keys.Collapse)
	if got := selected(t, m); got != "n78" {
		t.Fatalf("left from a match goes to its parent: %s", got)
	}
	m.Do(keys.Collapse)
	if m.Rows() != 79 {
		t.Fatalf("collapsing the parent: %d rows", m.Rows())
	}
	m.Do(keys.Expand)
	m.Do(keys.Expand)
	if got := selected(t, m); got != "n79" {
		t.Fatalf("expanding again: %s", got)
	}
}

// The selection is kept by UUID while it stays visible: through filtering,
// changing the filter and clearing it. A selection the filter hides moves to
// the first match. Clearing the filter shows the selection in the full tree
// and keeps the expansion the user had before filtering.
func TestFilterKeepsTheSelectionByUUID(t *testing.T) {
	m := New()
	m.SetSize(40, 8)
	m.SetIssues(sample())
	m.Do(keys.Down) // b
	if ch := m.SetFilter(ids("b", "a1x")); ch.Selection || selected(t, m) != "b" {
		t.Fatalf("visible selection moved: %+v %s", ch, selected(t, m))
	}
	if ch := m.SetFilter(ids("a2", "a1x")); !ch.Selection || selected(t, m) != "a1x" {
		t.Fatalf("hidden selection: %+v %s, want the first match a1x", ch, selected(t, m))
	}
	if ch := m.SetFilter(nil); ch.Selection || selected(t, m) != "a1x" {
		t.Fatalf("clearing moved the selection: %+v %s", ch, selected(t, m))
	}
	if m.Filtered() || m.Matches() != 5 {
		t.Fatalf("cleared: filtered %v, matches %d", m.Filtered(), m.Matches())
	}
	// The full tree shows a1x (its ancestors expanded) and b, but not the
	// collapsed rest the user never opened.
	if got := strings.Join(rowIDs(m), " "); got != "a a1 a1x a2 b" {
		t.Fatalf("rows after clearing: %q", got)
	}
	m.Do(keys.Top)
	m.Do(keys.Collapse) // collapse a
	m.SetFilter(ids("a1x"))
	if got := strings.Join(rowIDs(m), " "); got != "a a1 a1x" || selected(t, m) != "a" {
		t.Fatalf("filtered rows %q, selected %s", got, selected(t, m))
	}
	m.SetFilter(nil)
	if got := strings.Join(rowIDs(m), " "); got != "a b" || selected(t, m) != "a" {
		t.Fatalf("the filter's expansion leaked into the tree: %q %s", got, selected(t, m))
	}
}

// Refreshes apply the filter to the new issues: new matches appear, issues
// that stop matching are hidden, and a selection that stops matching moves
// and is reported as hidden, not missing.
func TestFilterAppliesToRefreshes(t *testing.T) {
	m := New()
	m.SetSize(40, 8)
	m.SetIssues(sample())
	m.SetFilter(func(i protocol.IssueSummary) bool { return strings.Contains(i.Title, "Child") })
	m.Do(keys.Down)
	m.Do(keys.Down) // a2
	if got := selected(t, m); got != "a2" {
		t.Fatalf("setup: %s", got)
	}
	items := sample()
	items = append(items, iss("b1", "b", 6, "Child of b"))
	if ch := m.SetIssues(items); ch.Selection || ch.Missing || ch.Hidden || m.Matches() != 3 {
		t.Fatalf("refresh: %+v, matches %d", ch, m.Matches())
	}
	if got := strings.Join(rowIDs(m), " "); got != "a a1 a2 b b1" {
		t.Fatalf("rows %q", got)
	}
	items[2].Title = "Renamed" // a2 no longer matches
	ch := m.SetIssues(items)
	if !ch.Hidden || ch.Missing || !ch.Selection || selected(t, m) == "a2" || slices.Contains(rowIDs(m), "a2") {
		t.Fatalf("selection stopped matching: %+v %s %v", ch, selected(t, m), rowIDs(m))
	}
	at := selected(t, m)
	var gone []protocol.IssueSummary
	for _, it := range items {
		if it.ID != at {
			gone = append(gone, it)
		}
	}
	if ch := m.SetIssues(gone); !ch.Missing || ch.Hidden {
		t.Fatalf("selection deleted: %+v", ch)
	}
}

// No match shows a clear empty state and selects nothing; clearing the filter
// brings the tree back with a selection.
func TestFilterWithoutMatches(t *testing.T) {
	m := New()
	m.SetSize(40, 4)
	m.SetIssues(sample())
	m.SetFilter(ids())
	if _, ok := m.Selected(); ok || m.Rows() != 0 || m.Matches() != 0 {
		t.Fatalf("selected with no matches: rows %d", m.Rows())
	}
	fits(t, m, 40, 4)
	if got := plain(m)[0]; !strings.Contains(got, "No issues match the filters") {
		t.Fatalf("empty state %q", got)
	}
	m.Do(keys.Down)
	m.Click(0)
	if ch := m.SetFilter(nil); !ch.Selection || selected(t, m) != "a" {
		t.Fatalf("after clearing: %+v", ch)
	}
}

// Switching projects (Restore) drops the filter.
func TestRestoreClearsTheFilter(t *testing.T) {
	m := New()
	m.SetSize(40, 6)
	m.SetIssues(sample())
	m.SetFilter(ids("b"))
	m.Restore(State{})
	m.SetIssues(sample())
	if m.Filtered() || m.Rows() != 2 {
		t.Fatalf("restored filtered %v with %d rows", m.Filtered(), m.Rows())
	}
}
