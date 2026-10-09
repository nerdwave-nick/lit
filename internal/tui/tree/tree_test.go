package tree

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
)

// iss builds a summary: id, parent ("" for none), a creation order and a title.
func iss(id, parent string, order int, title string) protocol.IssueSummary {
	s := protocol.IssueSummary{ID: id, Revision: 1, Title: title, State: "open", Labels: []string{},
		CreatedAt: fmt.Sprintf("2026-10-09T10:%02d:%02dZ", order/60, order%60), ChildIDs: []string{}}
	if parent != "" {
		s.ParentID = &parent
	}
	return s
}

// plain returns the view without styling, one string per line.
func plain(m *Model) []string { return strings.Split(ansi.Strip(m.View()), "\n") }

func selected(t *testing.T, m *Model) string {
	t.Helper()
	s, ok := m.Selected()
	if !ok {
		t.Fatal("nothing selected")
	}
	return s.ID
}

// fits asserts the view is exactly h lines of exactly w cells.
func fits(t *testing.T, m *Model, w, h int) {
	t.Helper()
	lines := strings.Split(m.View(), "\n")
	if len(lines) != h {
		t.Fatalf("%d lines, want %d:\n%s", len(lines), h, m.View())
	}
	for i, l := range lines {
		if got := cells.Width(cells.Default, l); got != w {
			t.Fatalf("line %d is %d cells, want %d: %q", i, got, w, ansi.Strip(l))
		}
	}
}

// Issues created together (one transaction, one timestamp) are ordered by
// title.
func TestSiblingsCreatedTogetherAreOrderedByTitle(t *testing.T) {
	m := New()
	m.SetSize(30, 4)
	m.SetIssues([]protocol.IssueSummary{iss("z1", "", 1, "Charlie"), iss("a9", "", 1, "alpha"), iss("m5", "", 1, "Bravo")})
	if got := strings.Join(plain(m), "|"); !strings.Contains(got, "alpha") || strings.Index(got, "alpha") > strings.Index(got, "Bravo") ||
		strings.Index(got, "Bravo") > strings.Index(got, "Charlie") {
		t.Fatalf("order: %s", got)
	}
}

func sample() []protocol.IssueSummary {
	return []protocol.IssueSummary{
		iss("b", "", 2, "Second root"),
		iss("a", "", 1, "First root"),
		iss("a2", "a", 4, "Child two"),
		iss("a1", "a", 3, "Child one"),
		iss("a1x", "a1", 5, "Grandchild"),
	}
}

// Roots and children are ordered by creation; only roots show at first, and
// expand/collapse follow the tree conventions: right expands, then moves to
// the first child; left collapses, then moves to the parent.
func TestNavigationExpandsCollapsesAndMovesAlongTheHierarchy(t *testing.T) {
	m := New()
	m.SetSize(40, 10)
	m.SetIssues(sample())
	if got := plain(m); !strings.Contains(got[0], "First root") || !strings.Contains(got[1], "Second root") || strings.Contains(got[2], "Child") {
		t.Fatalf("initial rows:\n%s", strings.Join(got, "\n"))
	}
	if selected(t, m) != "a" {
		t.Fatalf("first root not selected: %s", selected(t, m))
	}
	steps := []struct {
		action keys.Action
		want   string
	}{
		{keys.Expand, "a"},   // expands a
		{keys.Expand, "a1"},  // moves to the first child
		{keys.Down, "a1x"},   // not visible yet: a1 is collapsed, so down goes to a2
		{keys.Collapse, "a"}, // a2 is a leaf: moves to the parent
		{keys.Collapse, "a"}, // collapses a
		{keys.Down, "b"},     // children hidden again
		{keys.Collapse, "b"}, // a root leaf stays
		{keys.Top, "a"},      // first row
		{keys.Bottom, "b"},   // last row
		{keys.Up, "a"},       // previous row
		{keys.Up, "a"},       // stays at the top
	}
	for i, s := range steps {
		if s.want == "a1x" {
			s.want = "a2"
		}
		m.Do(s.action)
		if got := selected(t, m); got != s.want {
			t.Fatalf("step %d (%s): selected %s, want %s\n%s", i, s.action, got, s.want, strings.Join(plain(m), "\n"))
		}
	}
	if ch := m.Do(keys.Open); !ch.Open {
		t.Error("open does not report Open")
	}
}

// A chain far deeper than the pane is wide still shows every level: the
// indentation is capped and replaced by the depth, every row fits, and the
// path names all ancestors.
func TestDeepTreesFitAndKeepTheirAncestors(t *testing.T) {
	var items []protocol.IssueSummary
	parent := ""
	for d := 0; d < 80; d++ {
		id := fmt.Sprintf("n%02d", d)
		items = append(items, iss(id, parent, d, fmt.Sprintf("Level %d issue with a fairly long title", d)))
		parent = id
	}
	m := New()
	m.SetSize(36, 12)
	m.SetIssues(items)
	for d := 0; d < 79; d++ {
		m.Do(keys.Expand)
		m.Do(keys.Expand)
	}
	if got := selected(t, m); got != "n79" {
		t.Fatalf("selected %s, want n79", got)
	}
	fits(t, m, 36, 12)
	view := strings.Join(plain(m), "\n")
	if !strings.Contains(view, "79") || !strings.Contains(view, "Level 79") {
		t.Fatalf("the deepest row lost its depth or title:\n%s", view)
	}
	path := m.Path("n79")
	if len(path) != 80 || path[0].ID != "n00" || path[79].ID != "n79" {
		t.Fatalf("path: %d entries", len(path))
	}
	for d := 79; d > 0; d-- {
		m.Do(keys.Collapse) // to the parent, which is expanded
		m.Do(keys.Collapse) // collapses it
	}
	if got := selected(t, m); got != "n00" || m.Rows() != 1 {
		t.Fatalf("after collapsing: %s, %d rows", got, m.Rows())
	}
}

// Titles are sanitized and truncated to the pane, keeping the claim owner's
// name and the state visible; every line is exactly the pane width, with CJK
// and emoji titles too.
func TestRowsTruncateTitlesAndKeepTheClaimOwner(t *testing.T) {
	claimed := iss("c", "", 1, "A very long title that cannot possibly fit into a narrow tree pane at all")
	claimed.Claimed = true
	claimed.Claim = &protocol.Claim{IssueID: "c", OwnerClientID: "0b3ad1f2-0000-4000-8000-000000000000", OwnerActor: &protocol.Actor{Name: "Claude", Kind: "agent"}}
	closed := iss("d", "", 2, "漢字のタイトル 👩‍💻 with\x1b[31mescape")
	closed.State = "closed"
	anon := iss("e", "", 3, "Claimed without an actor")
	anon.Claimed = true
	anon.Claim = &protocol.Claim{IssueID: "e", OwnerClientID: "7f00aa11-0000-4000-8000-000000000000"}
	for _, w := range []int{32, 40, 80} {
		m := New()
		m.SetSize(w, 5)
		m.SetIssues([]protocol.IssueSummary{claimed, closed, anon})
		fits(t, m, w, 5)
		got := plain(m)
		if !strings.Contains(got[0], "Claude") || !strings.Contains(got[0], "…") {
			t.Errorf("w%d: row lost the claim owner or the truncation marker: %q", w, got[0])
		}
		if !strings.Contains(got[1], "closed") || strings.Contains(m.View(), "\x1b[31m") || w == 80 && !strings.Contains(got[1], "␛") {
			t.Errorf("w%d: closed row %q", w, got[1])
		}
		if !strings.Contains(got[2], "7f00aa11") {
			t.Errorf("w%d: claim without actor does not name the client: %q", w, got[2])
		}
	}
}

// Without styling the selection is marked with ">".
func TestPlainSelectionMarker(t *testing.T) {
	m := New()
	m.SetSize(30, 3)
	m.SetPlain(true)
	m.SetIssues(sample())
	got := plain(m)
	if !strings.HasPrefix(got[0], ">") || strings.HasPrefix(got[1], ">") || strings.Contains(m.View(), "\x1b[") {
		t.Fatalf("plain view:\n%q", got)
	}
	m.SetPlain(false)
	if !strings.Contains(m.View(), "\x1b[7m") {
		t.Fatal("styled view does not highlight the selection")
	}
}

// The selection is kept by UUID across refreshes: it stays on the same
// screen row when issues are added above it, its ancestors are expanded when
// it moved under a collapsed parent, and a vanished selection moves to the
// row in its place and is reported.
func TestSelectionIsKeptByUUIDAcrossRefreshes(t *testing.T) {
	var items []protocol.IssueSummary
	for i := 0; i < 30; i++ {
		items = append(items, iss(fmt.Sprintf("i%02d", i), "", 10+i, fmt.Sprintf("Issue %d", i)))
	}
	m := New()
	m.SetSize(30, 8)
	m.SetIssues(items)
	for i := 0; i < 20; i++ {
		m.Do(keys.Down)
	}
	before := plain(m)
	row := indexOf(before, "Issue 20")
	if selected(t, m) != "i20" || row < 0 {
		t.Fatalf("setup: %s\n%s", selected(t, m), strings.Join(before, "\n"))
	}
	more := append([]protocol.IssueSummary{iss("new1", "", 1, "New A"), iss("new2", "", 2, "New B")}, items...)
	if ch := m.SetIssues(more); ch.Missing || ch.Selection {
		t.Fatalf("refresh reported %+v", ch)
	}
	if selected(t, m) != "i20" || indexOf(plain(m), "Issue 20") != row {
		t.Fatalf("selection moved: %s at row %d, want row %d", selected(t, m), indexOf(plain(m), "Issue 20"), row)
	}
	// i20 moves under i03, which is collapsed.
	moved := append([]protocol.IssueSummary{}, more...)
	for n := range moved {
		if moved[n].ID == "i20" {
			moved[n] = iss("i20", "i03", 30, "Issue 20")
		}
	}
	m.SetIssues(moved)
	if selected(t, m) != "i20" || indexOf(plain(m), "Issue 20") < 0 {
		t.Fatalf("moved selection is not shown: %s\n%s", selected(t, m), strings.Join(plain(m), "\n"))
	}
	// i20 disappears.
	var gone []protocol.IssueSummary
	for _, it := range moved {
		if it.ID != "i20" {
			gone = append(gone, it)
		}
	}
	ch := m.SetIssues(gone)
	if !ch.Missing || !ch.Selection || selected(t, m) == "i20" {
		t.Fatalf("vanished selection: %+v, selected %s", ch, selected(t, m))
	}
}

func indexOf(lines []string, s string) int {
	for i, l := range lines {
		if strings.Contains(l, s) {
			return i
		}
	}
	return -1
}

// State and Restore remember a project's selection, expansion and scroll
// for when the user switches back.
func TestStateRestoresSelectionAndExpansion(t *testing.T) {
	m := New()
	m.SetSize(30, 6)
	m.SetIssues(sample())
	m.Do(keys.Expand)
	m.Do(keys.Expand)
	m.Do(keys.Expand) // expands a1
	m.Do(keys.Expand) // a1x
	st := m.State()
	m.Restore(State{})
	m.SetIssues([]protocol.IssueSummary{iss("z", "", 1, "Other project")})
	if selected(t, m) != "z" {
		t.Fatal("a fresh state does not select the first issue")
	}
	m.Restore(st)
	m.SetIssues(sample())
	if selected(t, m) != "a1x" || indexOf(plain(m), "Grandchild") != st.Row || st.Row != 2 {
		t.Fatalf("restored: %s\n%s", selected(t, m), strings.Join(plain(m), "\n"))
	}
}

// A parent missing from the loaded set (a partial load) or a parent cycle
// never hides issues: they show as roots.
func TestMissingParentsAndCyclesBecomeRoots(t *testing.T) {
	items := []protocol.IssueSummary{
		iss("x", "y", 1, "In a cycle X"),
		iss("y", "x", 2, "In a cycle Y"),
		iss("o", "unloaded", 3, "Orphan"),
		iss("s", "s", 4, "Own parent"),
	}
	m := New()
	m.SetSize(30, 6)
	m.SetIssues(items)
	seen := map[string]bool{}
	m.Do(keys.Top)
	for i := 0; i < 10; i++ {
		m.Do(keys.Expand)
		seen[selected(t, m)] = true
		m.Do(keys.Down)
	}
	for _, id := range []string{"x", "y", "o", "s"} {
		if !seen[id] {
			t.Errorf("%s is not reachable", id)
		}
	}
	if m.Len() != 4 {
		t.Errorf("Len %d", m.Len())
	}
}

// Page keys move by a page; the view follows the selection.
func TestPagingFollowsTheSelection(t *testing.T) {
	var items []protocol.IssueSummary
	for i := 0; i < 50; i++ {
		items = append(items, iss(fmt.Sprintf("i%02d", i), "", i, fmt.Sprintf("Issue %02d", i)))
	}
	m := New()
	m.SetSize(30, 10)
	m.SetIssues(items)
	m.Do(keys.PageDown)
	if selected(t, m) != "i09" {
		t.Fatalf("page down: %s", selected(t, m))
	}
	m.Do(keys.PageDown)
	if selected(t, m) != "i18" || indexOf(plain(m), "Issue 18") < 0 {
		t.Fatalf("page down twice: %s\n%s", selected(t, m), strings.Join(plain(m), "\n"))
	}
	m.Do(keys.PageUp)
	if selected(t, m) != "i09" || indexOf(plain(m), "Issue 09") < 0 {
		t.Fatalf("page up: %s", selected(t, m))
	}
	m.Do(keys.Bottom)
	if indexOf(plain(m), "Issue 49") != 9 {
		t.Fatalf("bottom row:\n%s", strings.Join(plain(m), "\n"))
	}
}

// Clicking a row selects it; clicking the selected row toggles it; the
// wheel moves the selection.
func TestClickAndWheel(t *testing.T) {
	m := New()
	m.SetSize(30, 6)
	m.SetIssues(sample())
	if ch := m.Click(1); !ch.Selection || selected(t, m) != "b" {
		t.Fatalf("click row 1: %+v %s", ch, selected(t, m))
	}
	m.Click(0)
	m.Click(0) // toggles a open
	if indexOf(plain(m), "Child one") < 0 {
		t.Fatalf("clicking the selected row does not expand it:\n%s", strings.Join(plain(m), "\n"))
	}
	if ch := m.Click(5); ch.Selection {
		t.Error("a click below the last row changed the selection")
	}
	m.Wheel(2)
	if selected(t, m) != "a2" {
		t.Fatalf("wheel down 2: %s", selected(t, m))
	}
	m.Wheel(-5)
	if selected(t, m) != "a" {
		t.Fatalf("wheel up: %s", selected(t, m))
	}
}

func TestEmptyTree(t *testing.T) {
	m := New()
	m.SetSize(20, 3)
	m.SetIssues(nil)
	if _, ok := m.Selected(); ok {
		t.Fatal("selection in an empty tree")
	}
	fits(t, m, 20, 3)
	if !strings.Contains(plain(m)[0], "No issues") {
		t.Errorf("empty view: %q", plain(m))
	}
	m.Do(keys.Down)
	m.Click(0)
	if ch := m.SetIssues(sample()); !ch.Selection || ch.Missing || selected(t, m) != "a" {
		t.Fatalf("issues after an empty load: %+v", ch)
	}
}
