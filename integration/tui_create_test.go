package integration

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// These scenarios create issues with the real lit-tui, suspended into a
// real editor in a private tmux server, against a temporary lit-server: a
// top-level issue and a child of it are created (title inline, body in the
// editor, one previewed transaction each) and selected; a child of an issue
// an agent claims is refused before anything is written, naming the owner;
// and its draft survives quitting, shows in the Drafts view at the next
// launch and creates the child there once the claim is gone.

// titled lists test/edit's issues titled title, as the agent reads them.
func (x *editEnv) titled(title string) []protocol.Issue {
	x.t.Helper()
	page, err := litclient.List[protocol.Issue](context.Background(), x.agent, litclient.Query{ProjectID: x.project, Title: title, All: true})
	if err != nil {
		x.t.Fatal(err)
	}
	return page.Items
}

// inNewIssueEditor waits for the editor on a new issue's draft: its file
// name is shown and the TUI's footer is gone.
func inNewIssueEditor(p *pane, slug string) {
	p.e.t.Helper()
	p.await("the editor on the new issue's draft", func(s string) bool {
		return strings.Contains(s, slug+"--") && !strings.Contains(s, "Session: client")
	})
}

func TestTUICreateIssuesInTmux(t *testing.T) {
	for _, ed := range editors {
		t.Run("a top-level issue and a child with "+ed.name, func(t *testing.T) { createIssues(t, ed.command) })
	}
	t.Run("a child under a claimed parent is refused; its draft survives quitting", refusedChildSurvivesQuitting)
}

func createIssues(t *testing.T, editor string) {
	x := newEditEnv(t, editor, `{"schema_version": 1}`)
	p := x.launch("create")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Edited body") })
	p.keys("N")
	p.await("the title prompt", func(s string) bool {
		return strings.Contains(s, "New issue in “test/edit”") && strings.Contains(s, "Title: ▏")
	})
	p.text("Quarterly plan") // q is text here, not quit
	p.await("the typed title", func(s string) bool { return strings.Contains(s, "Title: Quarterly plan▏") })
	p.keys("Enter")
	inNewIssueEditor(p, "quarterly-plan")
	files := x.draftFiles()
	if len(files) != 1 {
		t.Fatalf("draft files %v", files)
	}
	if b, _ := os.ReadFile(files[0]); len(b) != 0 {
		t.Fatalf("a new issue's draft is not empty: %q", b)
	}
	p.replaceAndSave("The plan's body.")
	p.await("the preview", func(s string) bool {
		return strings.Contains(s, "Create the new issue “Quarterly plan”?") && strings.Contains(s, "+The plan's body.") &&
			strings.Contains(s, "issue “Quarterly plan” new (changed)") && strings.Contains(s, "New issues take no claim")
	})
	if len(x.titled("Quarterly plan")) != 0 {
		t.Fatal("created before the apply")
	}
	p.keys("Enter")
	p.await("created and selected", func(s string) bool {
		return strings.Contains(s, "Created “Quarterly plan”.") && strings.Contains(s, "test/edit › Quarterly plan")
	})
	top := x.titled("Quarterly plan")
	if len(top) != 1 || top[0].Body != "The plan's body.\n" || top[0].ParentID != nil {
		t.Fatalf("created %+v", top)
	}

	p.keys("C") // a child of the new issue, which is selected
	p.await("the child prompt", func(s string) bool { return strings.Contains(s, "New child of “Quarterly plan”") })
	p.text("First step")
	p.keys("Enter")
	inNewIssueEditor(p, "first-step")
	p.replaceAndSave("Do this first.")
	p.await("the preview lists the parent", func(s string) bool {
		return strings.Contains(s, "Create the new issue “First step”?") && strings.Contains(s, "issue “Quarterly plan” at revision 1 (read)") &&
			strings.Contains(s, "Creating it changes the children of “Quarterly plan”")
	})
	p.keys("Enter")
	p.await("the child created and selected", func(s string) bool {
		return strings.Contains(s, "Created “First step” under “Quarterly plan”.") && strings.Contains(s, "test/edit › Quarterly plan › First step")
	})
	child := x.titled("First step")
	if len(child) != 1 || child[0].ParentID == nil || *child[0].ParentID != top[0].ID || child[0].Body != "Do this first.\n" {
		t.Fatalf("child %+v", child)
	}
	if parent := x.titled("Quarterly plan")[0]; !slices.Contains(parent.ChildIDs, child[0].ID) {
		t.Fatalf("the parent does not list the child: %+v", parent)
	}
	if files := x.draftFiles(); len(files) != 0 {
		t.Fatalf("drafts kept: %v", files)
	}
	p.quit()
}

func refusedChildSurvivesQuitting(t *testing.T) {
	x := newEditEnv(t, nvim, `{"schema_version": 1}`)
	x.agentClaims()
	before := x.read()
	p := x.launch("refused-child")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Edited body") && strings.Contains(s, "Claude") })
	p.keys("C")
	p.await("the claim warning", func(s string) bool { return strings.Contains(s, "“Edited body” is claimed by Claude (agent)") })
	p.text("Blocked step")
	p.keys("Enter")
	inNewIssueEditor(p, "blocked-step")
	p.replaceAndSave("Waits for the claim.")
	p.await("refused, naming the owner", func(s string) bool {
		return strings.Contains(s, "Cannot apply the new issue “Blocked step”") && strings.Contains(s, "“Edited body” is claimed by Claude (agent) until") &&
			strings.Contains(s, "Nothing was written; the draft is kept")
	})
	if len(x.titled("Blocked step")) != 0 {
		t.Fatal("the refused child was created")
	}
	if after := x.read(); after.Revision != before.Revision || len(after.ChildIDs) != 0 {
		t.Fatalf("the parent changed: %+v", after)
	}
	if got := x.draftText(); got != "Waits for the claim.\n" {
		t.Fatalf("draft %q", got)
	}
	p.keys("Escape") // keep the draft; wait before the next key
	p.await("kept", func(s string) bool { return strings.Contains(s, "Kept the new issue “Blocked step”") })
	p.quitKeepingDrafts("“Blocked step”")
	if got := x.draftText(); got != "Waits for the claim.\n" {
		t.Fatalf("quitting changed the draft: %q", got)
	}

	if _, err := x.agent.ReleaseClaims(context.Background(), litclient.ClaimSelection{Issues: []string{x.issue}}); err != nil {
		t.Fatal(err)
	}
	n := x.launch("next")
	n.await("the banner", func(s string) bool { return strings.Contains(s, "1 unapplied draft · D opens the Drafts view") })
	n.keys("D")
	n.await("the Drafts view", func(s string) bool {
		return strings.Contains(s, "Unapplied drafts · 1") && strings.Contains(s, "new issue · “Blocked step” · project “test/edit”")
	})
	n.keys("Enter")
	n.await("offered", func(s string) bool {
		return strings.Contains(s, "The new issue “Blocked step” is not created yet") && strings.Contains(s, "It creates a child of “Edited body”.")
	})
	n.keys("Down", "Down", "Enter") // Preview and apply
	n.await("the preview", func(s string) bool {
		return strings.Contains(s, "Create the new issue “Blocked step”?") && strings.Contains(s, "+Waits for the claim.")
	})
	n.keys("Enter")
	n.await("created", func(s string) bool { return strings.Contains(s, "Created “Blocked step” under “Edited body”.") })
	child := x.titled("Blocked step")
	if len(child) != 1 || child[0].ParentID == nil || *child[0].ParentID != x.issue || child[0].Body != "Waits for the claim.\n" {
		t.Fatalf("child %+v", child)
	}
	if files := x.draftFiles(); len(files) != 0 {
		t.Fatalf("drafts kept: %v", files)
	}
	n.quit()
}
