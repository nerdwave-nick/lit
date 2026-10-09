package tui

import (
	"context"
	"net/http"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// seedFilters is seed's project with labels, a closed issue and searchable
// text: the grandchild's body and a comment on the second root say
// "needle".
func seedFilters(t *testing.T, srv *tuitest.Server) (s *litclient.Session, root, child, grand, second string) {
	t.Helper()
	s, root, child, grand = seed(t, srv, "test/one")
	all, err := litclient.Get[protocol.Issue](context.Background(), s, "test/one", "title:Second root")
	if err != nil {
		t.Fatal(err)
	}
	second = all[0].ID
	body := "a NEEDLE in the body"
	tuitest.Apply(t, s, litclient.UpdateIssue("test/one", grand, 0, litclient.IssueChange{Body: &body, AddLabels: []string{"bug"}}))
	tuitest.Apply(t, s, litclient.UpdateIssue("test/one", root, 0, litclient.IssueChange{AddLabels: []string{"epic"}}))
	tuitest.Apply(t, s, litclient.CreateComment("test/one", second, "the needle is here too"))
	tuitest.Apply(t, s, litclient.CloseIssue("test/one", second, 0))
	return s, root, child, grand, second
}

// typeText types s into the focused view, one key per character.
func (b *browsing) typeText(s string) {
	b.t.Helper()
	for _, r := range s {
		b.m = drive(b.t, b.m, nil, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// filterLine is the footer line describing the filters, or "".
func (b *browsing) filterLine() string { return b.line("Filter:") }

// The search finds content through lit grep (titles, bodies and comments),
// keeps the ancestors of matches visible and the selection by UUID, and one
// key clears it. The search input owns typed letters, so q does not quit.
func TestSearchKeepsAncestorsAndClearsWithOneKey(t *testing.T) {
	srv := tuitest.Service(t)
	seedFilters(t, srv)
	b := startBrowsing(t, srv, 120, 30, false)
	searches := srv.Count("POST /v1/search")

	b.press("/")
	b.typeText("q")
	b.expect("q is typed, not quit", !b.l.Started() && strings.Contains(b.view(), "Search: q"))
	b.press("esc")
	b.expect("cancelled", !strings.Contains(b.view(), "Search: q") && b.filterLine() == "" && srv.Count("POST /v1/search") == searches)

	b.press("/")
	b.typeText("needle")
	b.press("enter")
	b.expect("one search request", srv.Count("POST /v1/search") == searches+1)
	b.expect("matches with their ancestors", strings.Contains(b.tree(), "Root issue") && strings.Contains(b.tree(), "Child issue") &&
		strings.Contains(b.tree(), "Grandchild issue") && strings.Contains(b.tree(), "Second root"))
	b.expect("ancestors marked as context, matches marked", !strings.Contains(b.treeLine("Child issue"), "•") && strings.Contains(b.treeLine("Grandchild issue"), "•") &&
		strings.Contains(b.treeLine("Second root"), "•"))
	b.expect("the selection stays on the visible root", strings.HasSuffix(strings.TrimSpace(b.crumb()), "Root issue"))
	line := b.filterLine()
	b.expect("the filter line names the search, the matches and the clear key", strings.Contains(line, `search "needle"`) &&
		strings.Contains(line, "2 of 4 issues match") && strings.Contains(line, "F clear filters"))

	b.press("F")
	b.expect("cleared: the full tree, the same selection, no filter line", b.filterLine() == "" &&
		strings.HasSuffix(strings.TrimSpace(b.crumb()), "Root issue") && strings.Contains(b.tree(), "Second root"))
}

// Filters by state, label, assignee and claim combine with each other and
// with the search; no match shows a clear empty state; the action menu
// clears them too.
func TestFiltersCombineAndShowAnEmptyState(t *testing.T) {
	srv := tuitest.Service(t)
	s, _, child, _, _ := seedFilters(t, srv)
	nick := "nick"
	tuitest.Apply(t, s, litclient.UpdateIssue("test/one", child, 0, litclient.IssueChange{Assignee: &nick}))
	b := startBrowsing(t, srv, 120, 30, false)

	b.press("f")
	b.expect("the filter panel", b.bodyLine("State: any") != "" && b.bodyLine("Label: [ ] bug") != "" && b.bodyLine("Assignee: [ ] nick") != "")
	b.choosePanelRow("State: any") // open
	b.expect("open only", strings.Contains(b.filterLine(), "Filter: open") && strings.Contains(b.filterLine(), "3 of 4 issues match"))
	b.press("esc")
	b.expect("the closed root is hidden", !strings.Contains(b.tree(), "Second root") && strings.Contains(b.tree(), "Root issue"))

	b.press("f")
	b.choosePanelRow("Claimed: any")
	b.press("esc")
	b.expect("open and claimed: the child with its ancestor", strings.Contains(b.tree(), "Child issue") && strings.Contains(b.tree(), "Root issue") &&
		!strings.Contains(b.tree(), "Grandchild") && strings.Contains(b.filterLine(), "1 of 4 issues match"))

	b.press("f")
	b.choosePanelRow("Assignee: [ ] nick")
	b.press("esc")
	b.expect("and assigned to nick", strings.Contains(b.tree(), "Child issue") && strings.Contains(b.filterLine(), "assignee nick"))

	b.press("/")
	b.typeText("needle")
	b.press("enter")
	b.expect("the search combines with the filters: nothing matches", strings.Contains(b.tree(), "No issues match the filters.") &&
		strings.Contains(b.filterLine(), "0 of 4 issues match") && strings.Contains(b.filterLine(), "F clear filters"))

	b.press("m")
	b.expect("the menu offers search, filter and clear", b.line("search issue content") != "" && b.line("filter the tree") != "" && b.line("clear the filters") != "")
	for !selectedRow(b.m, "clear the filters") {
		b.press("down")
	}
	b.press("enter")
	b.expect("cleared from the menu", b.filterLine() == "" && strings.Contains(b.tree(), "Second root"))

	b.press("f")
	b.choosePanelRow("Label: [ ] bug")
	b.choosePanelRow("Label: [ ] epic")
	b.press("esc")
	b.expect("labels: any of", strings.Contains(b.tree(), "Grandchild issue") && strings.Contains(b.filterLine(), "2 of 4 issues match"))
	b.press("f")
	b.choosePanelRow("Clear all filters")
	b.press("esc")
	b.expect("cleared from the panel", b.filterLine() == "")
}

// choosePanelRow moves the filter panel to the row starting with s and
// changes it.
func (b *browsing) choosePanelRow(s string) {
	b.t.Helper()
	b.send(tea.KeyPressMsg{Code: tea.KeyHome})
	for range 30 {
		if selectedRow(b.m, s) {
			b.press("enter")
			return
		}
		b.press("down")
	}
	b.t.Fatalf("no panel row %q:\n%s", s, b.view())
}

// Refreshes apply the filters to new content and run the search again; a
// selection that stops matching moves and says so. A failed search is shown
// and not applied. Switching projects drops the filters.
func TestFiltersFollowRefreshesAndFailuresAreShown(t *testing.T) {
	srv := tuitest.Service(t)
	s, _, child, _, _ := seedFilters(t, srv)
	tuitest.Apply(t, s, litclient.CreateProject("test/two", "spec"))
	b := startBrowsing(t, srv, 120, 30, false)
	b.press("enter") // the switcher lists both projects; test/one is first
	b.press("f")
	b.choosePanelRow("Claimed: any")
	b.press("esc")
	b.press("down")
	b.expect("the claimed child selected", strings.HasSuffix(strings.TrimSpace(b.crumb()), "Child issue"))

	if _, err := s.ReleaseClaims(context.Background(), litclient.ClaimSelection{Issues: []string{child}}); err != nil {
		t.Fatal(err)
	}
	b.press("r")
	b.expect("the released child is hidden and the note says why", !strings.Contains(b.tree(), "Child issue") &&
		strings.Contains(b.view(), "no longer matches the filters") && strings.Contains(b.tree(), "No issues match the filters."))
	b.press("F")

	b.press("/")
	b.typeText("needle")
	b.press("enter")
	searches := srv.Count("POST /v1/search")
	tuitest.CreateIssues(t, s, "test/one", "", "Another needle")
	b.press("r")
	b.expect("the refresh searched again and shows the new match", srv.Count("POST /v1/search") == searches+1 && strings.Contains(b.tree(), "Another needle"))

	srv.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/v1/search" {
			tuitest.HangUp(w)
			return true
		}
		return false
	})
	b.press("/")
	for range len("needle") {
		b.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	b.typeText("other")
	b.press("enter")
	b.expect("the failure is shown and the previous search kept", strings.Contains(b.filterLine(), `search "other" failed`) &&
		strings.Contains(b.filterLine(), `search "needle"`) && strings.Contains(b.tree(), "Another needle"))
	srv.Intercept(nil)

	b.press("p")
	b.press("down", "enter")
	b.expect("test/two has no filters", strings.Contains(b.header(), "test/two") && b.filterLine() == "")
}
