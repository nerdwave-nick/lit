package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/claims"
	"github.com/nerdwave-nick/lit/internal/tui/comments"
	"github.com/nerdwave-nick/lit/internal/tui/drafts"
	"github.com/nerdwave-nick/lit/internal/tui/tuitest"
)

// startCommenting is startEditing with comment editing registered and one
// comment by the agent on the root issue.
func startCommenting(t *testing.T) *editingUI {
	t.Helper()
	u := startEditing(t, claims.Config{})
	comments.Register(u.e)
	tuitest.Apply(t, u.agent, litclient.CreateComment("test/one", u.root, "the agent's comment\n"))
	return u
}

func (u *editingUI) comments(id string) []protocol.Comment {
	u.t.Helper()
	page, err := litclient.List[protocol.Comment](context.Background(), u.agent, litclient.Query{IssueID: id, All: true})
	if err != nil {
		u.t.Fatal(err)
	}
	return page.Items
}

func (u *editingUI) drafts(kind drafts.Kind) []drafts.Draft {
	u.t.Helper()
	all, err := u.store.List(u.project())
	if err != nil {
		u.t.Fatal(err)
	}
	var out []drafts.Draft
	for _, d := range all {
		if d.Kind == kind {
			out = append(out, d)
		}
	}
	return out
}

// a writes a new comment in the editor while the agent holds the issue's
// claim: no claim is taken, the preview names the new comment, and applying
// appends exactly the draft's text; the agent's claim is untouched.
func TestANewCommentIsPostedWhileTheAgentHoldsTheClaim(t *testing.T) {
	u := startCommenting(t)
	u.press("right", "down") // the child, claimed by the agent
	u.content = "My reply.\n"
	u.press("a")
	if len(u.ran) != 1 || !strings.Contains(u.ran[0][2], "comment-child-issue--") {
		t.Fatalf("editor commands %q", u.ran)
	}
	u.expect("the preview", u.line("Apply the new comment on “Child issue”?") != "" && u.line("+My reply.") != "" &&
		u.line("new comment new (changed)") != "" && u.line("New comments take no claim") != "")
	u.choose("Apply")
	u.expect("posted note", u.line("Posted the new comment on “Child issue”.") != "")
	u.expect("the detail shows it", u.line("My reply.") != "")
	got := u.comments(u.child)
	if len(got) != 1 || got[0].Body != "My reply.\n" || got[0].Author != "person" {
		t.Fatalf("comments %+v", got)
	}
	if c := u.claim(u.child); c == nil || c.OwnerClientID == u.l.Status().Client {
		t.Fatalf("the agent's claim changed: %+v", c)
	}
	if left := u.drafts(drafts.CommentNew); len(left) != 0 {
		t.Fatalf("drafts kept: %+v", left)
	}
}

// An unposted new comment is offered again by a instead of a second draft.
func TestAnUnpostedNewCommentIsOffered(t *testing.T) {
	u := startCommenting(t)
	u.send(tea.WindowSizeMsg{Width: 250, Height: 30}) // room for the path
	u.content = "not yet\n"
	u.press("a")
	u.press("esc")
	u.expect("kept note", u.line("Kept the new comment on “Root issue”") != "" && u.line("a on the issue resumes") != "")
	u.press("a")
	if len(u.ran) != 1 || len(u.drafts(drafts.CommentNew)) != 1 {
		t.Fatalf("a second draft was started: %q", u.ran)
	}
	u.expect("the offer", u.line("“Root issue” has an unposted new comment") != "" && u.line("Resume in the editor") != "")
	u.choose("Discard the draft")
	u.choose("Discard")
	u.expect("discarded", u.line("Discarded the new comment on “Root issue”.") != "" && len(u.drafts(drafts.CommentNew)) == 0)
}

// o chooses one of the issue's comments; editing it claims the issue first,
// applies the previewed update at the comment's revision and releases the
// edit claim.
func TestEditingACommentClaimsAppliesAndReleases(t *testing.T) {
	u := startCommenting(t)
	before := u.comments(u.root)[0]
	u.content = "the agent's comment, corrected\n"
	u.press("o")
	u.expect("the chooser", u.line("Edit which comment of “Root issue”?") != "" && u.line("> comment by Claude") != "")
	u.press("enter")
	u.expect("claimed while editing", u.ours(u.root))
	u.expect("the preview", u.line("Apply the draft of the comment on “Root issue”?") != "" &&
		u.line("-the agent's comment") != "" && u.line("+the agent's comment, corrected") != "" &&
		u.line("comment "+before.ID[:8]+" at revision 1 (changed)") != "" && u.line("The edit claim on “Root issue” is held") != "")
	u.choose("Apply")
	u.expect("applied note", u.line("Applied the draft of the comment on “Root issue”: revision 1 → 2. Released the edit claim.") != "")
	u.expect("the detail shows it", u.line("the agent's comment, corrected") != "")
	if got := u.comments(u.root)[0]; got.Body != "the agent's comment, corrected\n" || got.Revision != 2 {
		t.Fatalf("comment %+v", got)
	}
	u.expect("edit claim released", u.claim(u.root) == nil)
}

// A foreign claim refuses a comment edit and names the owner; an issue
// without comments has nothing to choose.
func TestEditingACommentIsRefusedByAForeignClaim(t *testing.T) {
	u := startCommenting(t)
	tuitest.Apply(t, u.agent, litclient.CreateComment("test/one", u.child, "on the child\n"))
	u.press("right", "down")
	u.press("o", "enter")
	u.expect("owner named", u.line("Cannot edit the comment on “Child issue”: claimed by Claude (agent) until") != "")
	if len(u.ran) != 0 || len(u.drafts(drafts.CommentBody)) != 0 {
		t.Fatalf("a refused edit started: %q", u.ran)
	}
	u.press("down") // the second root has no comments
	u.press("o")
	u.expect("nothing to choose", u.line("“Second root” has no comments to edit; a writes a new one.") != "")
}

// A revision change of the comment after the claim is gone keeps the draft:
// o on it opens as a conflict with reload and diff; a foreign claim then refuses the apply and
// names the owner, keeping the draft.
func TestACommentConflictKeepsTheDraftInTheUI(t *testing.T) {
	u := startCommenting(t)
	c := u.comments(u.root)[0]
	u.content = "mine\n"
	u.press("o", "enter")
	u.press("esc")
	u.press("u") // release the claim: applying is optimistic
	u.expect("released", u.claim(u.root) == nil)
	tuitest.Apply(t, u.agent, litclient.UpdateComment(c.ID, 0, "the agent's version\n"))
	u.press("o", "enter") // the comment changed: its draft opens as a conflict
	u.expect("conflict", u.line("The comment on “Root issue” changed since the draft was started: revision 1 → 2") != "" && u.line("the draft is kept") != "")
	u.expect("diff choice", u.line("Diff against the current comment") != "")
	u.choose("Diff against the current")
	u.expect("diff", u.line("-the agent's version") != "" && u.line("+mine") != "")
	u.choose("Reload")
	u.expect("optimistic preview", u.line("Warning: this session holds no claim on “Root issue”") != "")
	if _, err := u.agent.AcquireClaims(context.Background(), litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{u.root}}}); err != nil {
		t.Fatal(err)
	}
	u.choose("Apply")
	u.expect("owner named", u.line("claimed by Claude (agent) until") != "" && u.line("the draft is kept") != "")
	if got := u.comments(u.root)[0]; got.Body != "the agent's version\n" {
		t.Fatalf("the refused apply wrote: %q", got.Body)
	}
	if d := u.drafts(drafts.CommentBody); len(d) != 1 {
		t.Fatalf("the draft was dropped: %+v", d)
	} else if got, _ := u.store.Read(d[0]); got != "mine\n" {
		t.Fatalf("draft %q", got)
	}
}

// The menu offers the comment actions with their keys at the end.
func TestTheMenuOffersCommenting(t *testing.T) {
	c := startClaiming(t, claims.Config{})
	if strings.Contains(c.menu(), "add a comment") {
		t.Fatalf("comments offered without an editor:\n%s", c.menu())
	}
	u := startCommenting(t)
	menu := u.menu()
	for _, want := range []struct{ desc, key string }{{"add a comment, or resume its draft", "a"}, {"edit one of the issue's comments", "o"}} {
		found := false
		for _, l := range strings.Split(menu, "\n") {
			found = found || strings.Contains(l, want.desc) && strings.HasSuffix(strings.TrimSpace(l), " "+want.key)
		}
		if !found {
			t.Fatalf("menu lacks %q (%s):\n%s", want.desc, want.key, menu)
		}
	}
}

// Comment drafts left by an earlier launch are marked in the tree by kind,
// listed in the Drafts view and opened there: a new comment is offered, and
// a comment that changed since its draft opens as a conflict (its own
// revision, not the issue's). A comment draft resumed in this session makes
// quit ask, and Apply now previews it.
func TestCommentDraftsInTheDraftsViewAndTheQuitPrompt(t *testing.T) {
	x := newDrafted(t)
	tuitest.Apply(t, x.agent, litclient.CreateComment("test/one", x.root, "the agent's comment\n"))
	page, err := litclient.List[protocol.Comment](context.Background(), x.agent, litclient.Query{IssueID: x.root, All: true})
	if err != nil {
		t.Fatal(err)
	}
	c := page.Items[0]
	store := drafts.New(x.state)
	for _, d := range []struct {
		d       drafts.Draft
		content string
	}{
		{drafts.Draft{Kind: drafts.CommentNew, RecordID: drafts.NewID(), IssueID: x.child, Title: "Child issue", Claim: drafts.ClaimNone}, "an earlier reply\n"},
		{drafts.Draft{Kind: drafts.CommentBody, RecordID: c.ID, IssueID: x.root, Title: "Root issue", Revision: c.Revision, Base: c.Body, Claim: drafts.ClaimEdit}, "my edit\n"},
	} {
		d.d.ServiceID, d.d.ProjectID = x.agent.Meta.ServiceID, x.project
		if _, err := store.Create(d.d, d.content); err != nil {
			t.Fatal(err)
		}
	}
	tuitest.Apply(t, x.agent, litclient.UpdateComment(c.ID, 0, "the agent's newer comment\n"))
	u := x.launch()
	comments.Register(u.e)
	u.expect("marks by kind", strings.Contains(u.treeLine("Root issue"), "comment draft") && !strings.Contains(u.treeLine("Root issue"), "changed"))
	u.press("D")
	u.expect("listed", u.line("new comment · “Child issue”") != "" && u.line("comment · “Root issue”") != "")
	u.press("enter")
	u.expect("the new comment offered", u.line("“Child issue” has an unposted new comment") != "")
	u.press("esc")
	u.press("down", "enter")
	u.expect("the comment's conflict", u.line("The comment on “Root issue” changed since the draft was started: revision 1 → 2") != "" &&
		u.line("Diff against the current comment") != "")
	u.press("esc")
	u.press("esc") // close the view
	u.press("right", "down")
	u.press("a")
	u.expect("offered, not restarted", u.line("“Child issue” has an unposted new comment") != "" && len(u.ran) == 0)
	u.content = "an earlier reply, finished\n"
	u.choose("Resume in the editor")
	u.expect("the preview", u.line("Apply the new comment on “Child issue”?") != "")
	u.press("esc")
	u.press("q")
	u.expect("the prompt", u.line("1 open draft (“Child issue”)") != "")
	u.press("enter") // apply now
	u.expect("apply now previews it", u.line("Apply the new comment on “Child issue”?") != "" && u.line("+an earlier reply, finished") != "")
	u.choose("Apply")
	if got := u.comments(x.child); len(got) != 1 || got[0].Body != "an earlier reply, finished\n" {
		t.Fatalf("comments %+v", got)
	}
}
