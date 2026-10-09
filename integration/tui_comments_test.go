package integration

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// These scenarios add and edit comments with the real lit-tui, suspended
// into a real editor in a private tmux server, against a temporary
// lit-server: a new comment is posted while an agent holds the issue's
// claim (a claim-exempt append); editing an existing comment claims the
// issue, applies at the comment's revision and releases the claim; and a
// revision conflict keeps the comment's draft.

func (x *editEnv) comments() []protocol.Comment {
	x.t.Helper()
	page, err := litclient.List[protocol.Comment](context.Background(), x.agent, litclient.Query{IssueID: x.issue, All: true})
	if err != nil {
		x.t.Fatal(err)
	}
	return page.Items
}

func (x *editEnv) agentClaims() {
	x.t.Helper()
	if _, err := x.agent.AcquireClaims(context.Background(), litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{x.issue}}}); err != nil {
		x.t.Fatal(err)
	}
}

// inCommentEditor waits for the editor on a comment draft: its file name
// and text are shown and the TUI's footer is gone (the comment chooser also
// shows the text without the footer).
func inCommentEditor(p *pane, text string) {
	p.e.t.Helper()
	p.await("the editor on the comment's draft", func(s string) bool {
		return strings.Contains(s, "comment-edited-body--") && strings.Contains(s, text) && !strings.Contains(s, "Session: client")
	})
}

func TestTUICommentsInTmux(t *testing.T) {
	for _, ed := range editors {
		t.Run("a new comment while an agent holds the claim with "+ed.name, func(t *testing.T) { newCommentOverAClaim(t, ed.command) })
		t.Run("edit a comment: claim, apply, release with "+ed.name, func(t *testing.T) { editComment(t, ed.command) })
		t.Run("a conflict keeps the comment draft with "+ed.name, func(t *testing.T) { commentConflict(t, ed.command) })
	}
}

func newCommentOverAClaim(t *testing.T, editor string) {
	x := newEditEnv(t, editor, `{"schema_version": 1}`)
	x.agentClaims()
	p := x.launch("new-comment")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Edited body") && strings.Contains(s, "Claude") })
	p.keys("a")
	p.await("the editor on the new comment's draft", func(s string) bool {
		return strings.Contains(s, "comment-edited-body--") && !strings.Contains(s, "Session: client")
	})
	files := x.draftFiles()
	if len(files) != 1 {
		t.Fatalf("draft files %v", files)
	}
	if b, _ := os.ReadFile(files[0]); len(b) != 0 {
		t.Fatalf("a new comment's draft is not empty: %q", b)
	}
	p.replaceAndSave("A comment typed in the editor.")
	p.await("the preview", func(s string) bool {
		return strings.Contains(s, "Apply the new comment on “Edited body”?") && strings.Contains(s, "+A comment typed in the editor.") &&
			strings.Contains(s, "New comments take no claim")
	})
	p.keys("Enter")
	p.await("posted", func(s string) bool { return strings.Contains(s, "Posted the new comment on “Edited body”.") })
	got := x.comments()
	if len(got) != 1 || got[0].Body != "A comment typed in the editor.\n" {
		t.Fatalf("comments %+v", got)
	}
	if c := x.claim(); c == nil || c.OwnerClientID == p.m.ClientID || c.OwnerActor == nil || c.OwnerActor.Name != "Claude" {
		t.Fatalf("the agent's claim changed: %+v", c)
	}
	if files := x.draftFiles(); len(files) != 0 {
		t.Fatalf("the draft was kept: %v", files)
	}
	p.await("the detail shows it", func(s string) bool { return strings.Contains(s, "A comment typed in the editor.") })
	p.quit()
}

func editComment(t *testing.T, editor string) {
	x := newEditEnv(t, editor, `{"schema_version": 1}`)
	tuitest.Apply(t, x.agent, litclient.CreateComment("test/edit", x.issue, "the agent's comment\n"))
	before := x.comments()[0]
	p := x.launch("edit-comment")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Edited body") })
	p.keys("o")
	p.await("the chooser", func(s string) bool {
		return strings.Contains(s, "Edit which comment of “Edited body”?") && strings.Contains(s, "comment by Claude")
	})
	p.keys("Enter")
	inCommentEditor(p, "the agent's comment")
	if c := x.claim(); c == nil || c.OwnerClientID != p.m.ClientID {
		t.Fatalf("not claimed by lit-tui before the editor opened: %+v", c)
	}
	if _, err := x.agent.AcquireClaims(context.Background(), litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{x.issue}}}); litclient.Code(err) != "claim_conflict" {
		t.Fatalf("the agent's claim attempt was not refused: %v", err)
	}
	p.replaceAndSave("The corrected comment.")
	p.await("the preview", func(s string) bool {
		return strings.Contains(s, "Apply the draft of the comment on “Edited body”?") && strings.Contains(s, "-the agent's comment") &&
			strings.Contains(s, "+The corrected comment.") && strings.Contains(s, "comment "+before.ID[:8]+" at revision 1 (changed)") &&
			strings.Contains(s, "The edit claim on “Edited body” is held")
	})
	p.keys("Enter")
	p.await("applied", func(s string) bool {
		return strings.Contains(s, "Applied the draft of the comment on “Edited body”: revision 1 → 2. Released the edit claim.")
	})
	if got := x.comments()[0]; got.Body != "The corrected comment.\n" || got.Revision != before.Revision+1 {
		t.Fatalf("comment after apply %+v", got)
	}
	if c := x.claim(); c != nil {
		t.Fatalf("the edit claim was not released: %+v", c)
	}
	if files := x.draftFiles(); len(files) != 0 {
		t.Fatalf("the draft was kept: %v", files)
	}
	p.quit()
}

func commentConflict(t *testing.T, editor string) {
	x := newEditEnv(t, editor, `{"schema_version": 1}`)
	tuitest.Apply(t, x.agent, litclient.CreateComment("test/edit", x.issue, "the agent's comment\n"))
	c := x.comments()[0]
	p := x.launch("comment-conflict")
	p.await("the tree", func(s string) bool { return strings.Contains(s, "Edited body") })
	p.keys("o")
	p.await("the chooser", func(s string) bool { return strings.Contains(s, "comment by Claude") })
	p.keys("Enter")
	inCommentEditor(p, "the agent's comment")
	p.replaceAndSave("My version of the comment.")
	p.await("the preview", func(s string) bool {
		return strings.Contains(s, "Apply the draft of the comment on “Edited body”?")
	})
	p.keys("Escape") // keep the draft; wait before the next key, or ESC u reads as alt+u
	p.await("kept", func(s string) bool { return strings.Contains(s, "Kept the draft of the comment on “Edited body”") })
	p.keys("u") // release the claim: the agent can now change the comment
	eventually(t, 15*time.Second, "the claim released", func() bool { return x.claim() == nil }, p.screen)
	tuitest.Apply(t, x.agent, litclient.UpdateComment(c.ID, 0, "the agent's newer comment\n"))
	p.keys("o")
	p.await("the chooser", func(s string) bool { return strings.Contains(s, "comment by Claude") })
	p.keys("Enter")
	p.await("the offer", func(s string) bool { return strings.Contains(s, "The comment on “Edited body” has a draft") })
	p.keys("Down", "Down", "Enter") // preview and apply
	p.await("the revision conflict", func(s string) bool {
		return strings.Contains(s, "The comment on “Edited body” changed since the draft was started: revision 1 → 2") &&
			strings.Contains(s, "the draft is kept") && strings.Contains(s, "Diff against the current comment")
	})
	if got := x.comments()[0]; got.Body != "the agent's newer comment\n" {
		t.Fatalf("the conflict wrote: %q", got.Body)
	}
	files := x.draftFiles()
	if len(files) != 1 {
		t.Fatalf("the draft was not kept: %v", files)
	}
	if b, _ := os.ReadFile(files[0]); string(b) != "My version of the comment.\n" {
		t.Fatalf("draft %q", b)
	}
	p.keys("Enter") // diff against the current comment
	p.await("the diff", func(s string) bool {
		return strings.Contains(s, "-the agent's newer comment") && strings.Contains(s, "+My version of the comment.")
	})
	p.keys("Escape")
	p.await("kept", func(s string) bool { return strings.Contains(s, "Kept the draft of the comment on “Edited body”") })
	p.quit()
}
