package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// The history view drives the real lit-tui in tmux against a temporary
// lit-server: an issue edited a few times (title, body, labels, state) and a
// comment edited by another client. From the tree the issue's history shows
// who changed what and when, with a readable body diff and visible markers
// for control characters; from the detail the comment's history is chosen.
// Escape returns to the same selection, and nothing is written.
func TestTUIHistoryInTmux(t *testing.T) {
	e := newTUIEnv(t)
	s := e.seeder(protocol.Actor{Name: "Claude", Kind: "agent"})
	tuitest.Apply(t, s, litclient.CreateProject("test/history", "spec"))
	req := litclient.CreateIssue("test/history", "Draft plan", "## Plan\n\nstep one\nstep two\nstep three", "")
	tuitest.Apply(t, s, req)
	id := req.Items[0].ID
	tuitest.CreateIssues(t, s, "test/history", "", "Other root")
	title, body := "Final plan", "## Plan\n\nstep one\nstep 2, revised\nstep three\nclear\x1b[2J"
	tuitest.Apply(t, s, litclient.UpdateIssue("test/history", id, 0, litclient.IssueChange{Title: &title}))
	tuitest.Apply(t, s, litclient.UpdateIssue("test/history", id, 0, litclient.IssueChange{Body: &body, AddLabels: []string{"ready"}}))
	tuitest.Apply(t, s, litclient.CloseIssue("test/history", id, 0))
	p := e.seeder(protocol.Actor{Name: "person", Kind: "human"})
	comment := litclient.CreateComment("test/history", id, "Looks fine.")
	tuitest.Apply(t, p, comment)
	tuitest.Apply(t, p, litclient.UpdateComment(comment.Items[0].ID, 0, "Looks fine, with one nit."))
	revision := func() int64 {
		got, err := litclient.Get[protocol.Issue](context.Background(), s, "test/history", id)
		if err != nil {
			t.Fatal(err)
		}
		return got[0].Revision
	}
	before := revision()

	v := e.launch("history")
	v.await("the tree and the detail beside it", func(s string) bool {
		return strings.Contains(s, "Other root") && strings.Contains(s, "Looks fine, with one nit.")
	})
	crumb := v.crumb()
	v.keys("H")
	screen := v.await("the issue's history", func(s string) bool {
		return strings.Contains(s, `History · issue "Final plan" · read-only · 5 entries`) && strings.Contains(s, "#1 · created · rev 1")
	})
	for _, want := range []string{"Claude (agent)", `title: "Draft plan" → "Final plan"`, `labels: [] → ["ready"]`, `state: "open" → "closed"`,
		"- step two", "+ step 2, revised", "+ clear␛[2J", "comment create " + comment.Items[0].ID[:8], "person (human)"} {
		if !strings.Contains(screen, want) {
			t.Errorf("history lacks %q:\n%s", want, screen)
		}
	}
	v.keys("Escape")
	v.await("the same tree and selection", func(s string) bool {
		return !strings.Contains(s, "History · ") && strings.Contains(s, "Other root") && v.crumb() == crumb && crumb == "test/history › Final plan"
	})

	v.keys("Enter")
	v.await("the detail with the comment", func(s string) bool { return strings.Contains(s, "Looks fine, with one nit.") })
	v.keys("H")
	v.await("the chooser", func(s string) bool {
		return strings.Contains(s, "History of · read-only") && strings.Contains(s, `issue "Final plan"`) && strings.Contains(s, "comment by person")
	})
	v.keys("Down", "Enter")
	v.await("the comment's history", func(s string) bool {
		return strings.Contains(s, "History · comment by person") && strings.Contains(s, "2 entries") && strings.Contains(s, "- Looks fine.") &&
			strings.Contains(s, "+ Looks fine, with one nit.") && strings.Contains(s, "person (human)")
	})
	v.keys("Escape")
	v.await("the chooser again", func(s string) bool { return strings.Contains(s, "History of · read-only") })
	v.keys("Escape")
	v.await("the detail again", func(s string) bool {
		return strings.Contains(s, "Looks fine, with one nit.") && !strings.Contains(s, "History") && strings.Contains(s, "esc back")
	})
	if got := revision(); got != before {
		t.Fatalf("the issue changed from revision %d to %d", before, got)
	}
	v.quit()
}
