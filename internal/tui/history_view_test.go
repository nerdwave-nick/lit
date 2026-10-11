package tui

import (
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// retitled is discussion with the Plan retitled to "Plan v2".
func retitled(t *testing.T, srv *tuitest.Server, project string) {
	t.Helper()
	s, id := discussion(t, srv, project)
	title := "Plan v2"
	tuitest.Apply(t, s, litclient.UpdateIssue(project, id, 0, litclient.IssueChange{Title: &title}))
}

func writes(srv *tuitest.Server) int {
	return srv.Count("POST /v1/transactions") + srv.Count("POST /v1/operations") + srv.Count("POST /v1/claims")
}

// From the tree, the history key shows the selected issue's history: who,
// when and which fields changed. Escape returns to the same selection, and
// nothing is written.
func TestHistoryKeyShowsTheSelectedIssuesHistory(t *testing.T) {
	srv := tuitest.Service(t)
	retitled(t, srv, "test/hist")
	b := startBrowsing(t, srv, 80, 30, false)
	b.expect("the tree", b.treeLine("Plan v2") != "")
	before, wrote := b.view(), writes(srv)

	b.press("H")
	v := b.view()
	// Comments do not change the issue: its history is the creation and the retitling.
	b.expect("the issue's history", strings.HasPrefix(v, `History · issue "Plan v2" · read-only · 2 entries`) &&
		strings.Contains(v, "#1 · created · rev 1") && strings.Contains(v, "Claude (agent)") && strings.Contains(v, "+ ## Plan") &&
		!strings.Contains(v, "comment create"))
	b.press("j", "G")
	b.expect("the retitling at the end", strings.Contains(b.view(), "#2 · rev 1 → 2") && strings.Contains(b.view(), `title: "Plan" → "Plan v2"`))
	b.press("k", "esc")
	b.expect("the same tree and selection", b.view() == before)
	if writes(srv) != wrote {
		t.Fatal("the history view wrote")
	}
}

// The action menu offers the history with its key.
func TestTheMenuOffersTheHistory(t *testing.T) {
	srv := tuitest.Service(t)
	retitled(t, srv, "test/menu")
	b := startBrowsing(t, srv, 80, 30, false)
	b.press("m")
	b.expect("the menu entry", strings.HasSuffix(strings.TrimSpace(b.line("show the issue's history")), "H"))
	b.press("G", "enter")
	b.expect("the history", strings.HasPrefix(b.view(), `History · issue "Plan v2"`))
	b.press("H")
	b.expect("the history key closes it", b.treeLine("Plan v2") != "" && !strings.Contains(b.view(), "History · "))
}

// From the detail, the history key offers the issue and each of its loaded
// comments; Escape steps back to the chooser, then to the detail.
func TestHistoryFromTheDetailChoosesTheIssueOrAComment(t *testing.T) {
	srv := tuitest.Service(t)
	s, id := discussion(t, srv, "test/detail")
	comments, err := litclient.List[protocol.Comment](t.Context(), s, litclient.Query{IssueID: id, All: true})
	if err != nil || len(comments.Items) != 2 {
		t.Fatalf("comments: %v %v", comments, err)
	}
	tuitest.Apply(t, s, litclient.UpdateComment(comments.Items[0].ID, 0, "First comment, edited."))
	b := startBrowsing(t, srv, 140, 40, false)
	b.press("enter")
	b.expect("the detail with comments", b.bodyLine("Comments (2)") != "" && b.bodyLine("First comment, edited.") != "")
	detail := b.view()

	b.press("H")
	b.expect("the chooser", b.line("History of") != "" && strings.Contains(b.m.View().Content, "\x1b[7m  issue \"Plan\"") && b.line("comment by Claude") != "" && b.line("comment by person") != "")
	b.press("j", "enter")
	v := b.view()
	b.expect("the comment's history", strings.HasPrefix(v, "History · comment by Claude") && strings.Contains(v, "2 entries") &&
		strings.Contains(v, "- First comment, with `code`.") && strings.Contains(v, "+ First comment, edited.") && strings.Contains(v, "Claude (agent)"))
	b.press("esc")
	b.expect("back to the chooser on the comment", strings.Contains(b.m.View().Content, "\x1b[7m  comment by Claude"))
	b.press("esc")
	b.expect("back to the detail", b.view() == detail)
}

// Quit closes the history view and starts the orderly shutdown; the
// uncertain-requests view opens over it and returns to it.
func TestQuitAndUncertainRequestsFromTheHistory(t *testing.T) {
	srv := tuitest.Service(t)
	retitled(t, srv, "test/quit")
	b := startBrowsing(t, srv, 80, 30, false)
	b.press("H")
	b.expect("the history", strings.HasPrefix(b.view(), `History · issue "Plan v2"`))
	b.press("U")
	b.expect("the uncertain requests over it", strings.Contains(b.view(), "Uncertain requests · read-only"))
	b.press("esc")
	b.expect("the history again", strings.HasPrefix(b.view(), `History · issue "Plan v2"`))
	b.press("q")
	if !b.l.Started() || strings.Contains(b.view(), "History · ") {
		t.Fatalf("quit: shutdown started %v; screen:\n%s", b.l.Started(), b.view())
	}
}
