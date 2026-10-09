package integration

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// This scenario manages milestones with the real lit-tui in a private tmux
// server against a temporary lit-server: it creates a milestone in the
// Milestones view, adds the selected issue as an explicit member and
// removes it again (its child never follows), edits the description in
// nvim through a TUI draft without a claim, and keeps the draft when the
// agent changed the description meanwhile (a revision conflict).

func (x *editEnv) milestone(title string) protocol.Milestone {
	x.t.Helper()
	got, err := litclient.Get[protocol.Milestone](context.Background(), x.agent, "test/edit", title)
	if err != nil || len(got) != 1 {
		x.t.Fatalf("milestone %q: %v %v", title, got, err)
	}
	return got[0]
}

func TestTUIMilestonesInTmux(t *testing.T) {
	x := newEditEnv(t, nvim, `{"schema_version": 1}`)
	child := tuitest.CreateIssues(t, x.agent, "test/edit", x.issue, "Child of the edited body")[0]
	p := x.launch("milestones")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Edited body") })

	p.keys("M")
	p.await("the empty view", func(s string) bool {
		return strings.Contains(s, "Milestones of “test/edit” · 0") && strings.Contains(s, "The project has no milestones yet.")
	})
	p.keys("Enter")
	p.await("the title field", func(s string) bool { return strings.Contains(s, "New milestone in “test/edit”") })
	p.text("Release 1")
	p.keys("Enter")
	p.await("the creation preview", func(s string) bool {
		return strings.Contains(s, "Create the milestone “Release 1” in “test/edit”") && strings.Contains(s, "Would write:")
	})
	p.keys("Enter")
	p.await("created", func(s string) bool {
		return strings.Contains(s, "Applied: Create the milestone “Release 1” in “test/edit”.") &&
			strings.Contains(s, "“Release 1” · no members: an empty scope, not complete")
	})
	m := x.milestone("Release 1")
	p.keys("Escape")
	p.await("the tree again", func(s string) bool {
		return !strings.Contains(s, "Milestones of") && strings.Contains(s, "Edited body")
	})

	p.keys("W")
	p.await("the membership picker", func(s string) bool { return strings.Contains(s, "[ ] “Release 1”") })
	p.keys("Enter")
	p.await("the membership preview", func(s string) bool {
		return strings.Contains(s, "Add “Edited body” to the milestone “Release 1”") && strings.Contains(s, "milestone “Release 1” · revision 1")
	})
	p.keys("Enter")
	p.await("added", func(s string) bool {
		return strings.Contains(s, "Applied: Add “Edited body” to the milestone “Release 1”.")
	})
	if got := x.milestone("Release 1"); !slices.Equal(got.IssueIDs, []string{x.issue}) || slices.Contains(got.IssueIDs, child) {
		t.Fatalf("members after adding: %v", got.IssueIDs)
	}
	if issue := x.read(); len(issue.BlockedBy)+len(issue.Blocks) != 0 {
		t.Fatalf("membership created a blocker: %+v", issue)
	}
	p.keys("M")
	p.await("the progress", func(s string) bool { return strings.Contains(s, "“Release 1” · 1 member: 0 closed, 1 open") })
	p.keys("Escape")
	p.await("the tree again", func(s string) bool { return !strings.Contains(s, "Milestones of") })
	p.keys("W")
	p.await("checked", func(s string) bool { return strings.Contains(s, "[x] “Release 1” (member: enter removes it)") })
	p.keys("Enter")
	p.await("the removal preview", func(s string) bool {
		return strings.Contains(s, "Remove “Edited body” from the milestone “Release 1”") && strings.Contains(s, "milestone “Release 1” · revision 2")
	})
	p.keys("Enter")
	p.await("removed", func(s string) bool {
		return strings.Contains(s, "Applied: Remove “Edited body” from the milestone “Release 1”.")
	})
	if got := x.milestone("Release 1"); len(got.IssueIDs) != 0 || got.Revision != 3 {
		t.Fatalf("after removing: %+v", got)
	}

	// The description, through nvim: no claim, applied at the revision.
	p.keys("M")
	p.await("the loaded view", func(s string) bool { return strings.Contains(s, "“Release 1” · no members") })
	p.keys("Down", "Enter")
	p.await("the actions", func(s string) bool { return strings.Contains(s, "Actions for the milestone “Release 1”:") })
	p.keys("Down", "Enter")
	p.await("nvim on the description's draft", func(s string) bool {
		return strings.Contains(s, "--"+m.ID[:8]+".md") && !strings.Contains(s, "Session: client")
	})
	if b, _ := os.ReadFile(x.draftFiles()[0]); len(b) != 0 {
		t.Fatalf("the draft does not hold exactly the (empty) description: %q", b)
	}
	p.replaceAndSave("Ship the first release.")
	p.await("the description preview", func(s string) bool {
		return strings.Contains(s, "Apply the draft of the description of the milestone “Release 1”?") &&
			strings.Contains(s, "+Ship the first release.") && strings.Contains(s, "milestone “Release 1” at revision 3 (changed)") &&
			strings.Contains(s, "Applying is optimistic")
	})
	p.keys("Enter")
	p.await("applied", func(s string) bool {
		return strings.Contains(s, "Applied the draft of the description of the milestone “Release 1”: revision 3 → 4.") &&
			strings.Contains(s, "Ship the first release.")
	})
	if got := x.milestone("Release 1"); got.Body != "Ship the first release.\n" || got.Revision != 4 {
		t.Fatalf("after the description: %+v", got)
	}
	if files := x.draftFiles(); len(files) != 0 {
		t.Fatalf("the applied draft was kept: %v", files)
	}

	// A revision conflict keeps the draft.
	p.keys("Enter")
	p.await("the actions", func(s string) bool { return strings.Contains(s, "Actions for the milestone “Release 1”:") })
	p.keys("Down", "Enter")
	p.inEditor("Ship the first release.")
	body := "The agent's description.\n"
	tuitest.Apply(t, x.agent, litclient.UpdateMilestone("test/edit", m.ID, 0, litclient.MilestoneChange{Body: &body}))
	p.replaceAndSave("My second take.")
	p.await("the revision conflict", func(s string) bool {
		return strings.Contains(s, "The description of the milestone “Release 1” changed since the draft was started: revision 4 → 5") &&
			strings.Contains(s, "the draft is kept") && strings.Contains(s, "Diff against the current description")
	})
	if got := x.milestone("Release 1"); got.Body != body {
		t.Fatalf("the conflict wrote: %q", got.Body)
	}
	if got := x.draftText(); got != "My second take.\n" {
		t.Fatalf("draft %q", got)
	}
	p.keys("Enter") // diff against the current description
	p.await("the diff", func(s string) bool {
		return strings.Contains(s, "-The agent's description.") && strings.Contains(s, "+My second take.")
	})
	p.keys("Escape")
	p.await("kept", func(s string) bool {
		return strings.Contains(s, "Kept the draft of the description of the milestone “Release 1”")
	})
	p.quitKeepingDrafts("“Release 1”")
	if got := x.draftText(); got != "My second take.\n" {
		t.Fatalf("quitting changed the draft: %q", got)
	}
}
