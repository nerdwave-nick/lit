package litclient_test

import (
	"context"
	"slices"
	"testing"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

func refIDs(refs []protocol.ObjectRef) []string {
	out := []string{}
	for _, r := range refs {
		out = append(out, r.ID)
	}
	return out
}

// An indirectly affected record that changes after the preview makes the
// commit fail. The client never prepares again behind the caller's back.
func TestCommitRefusesIndirectChangeAfterPreview(t *testing.T) {
	ctx := context.Background()
	t.Run("relationship mirror", func(t *testing.T) {
		h := newHarness(t)
		s := h.connect("mirror", "Human")
		project, issues := seed(t, s, "mirror", "A", "B")
		a, b := issues["A"], issues["B"]

		plan, err := s.Prepare(ctx, litclient.LinkIssues(project, a.ID, "blocks", b.ID))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(refIDs(plan.Intent().Targets), b.ID) {
			t.Fatalf("preview does not fence the mirror %s: %+v", b.ID, plan.Intent().Targets)
		}
		apply(t, s, litclient.UpdateIssue(project, b.ID, 0, litclient.IssueChange{Title: str("B renamed")}))

		previews, commits := h.count("POST /v1/transaction-previews"), h.count("POST /v1/transactions")
		if _, err = s.Commit(ctx, plan); litclient.Code(err) != "revision_conflict" {
			t.Fatalf("commit after mirror change: %v", err)
		}
		if h.count("POST /v1/transaction-previews") != previews || h.count("POST /v1/transactions") != commits+1 {
			t.Fatalf("commit re-prepared or resent: previews %d->%d commits %d->%d", previews, h.count("POST /v1/transaction-previews"), commits, h.count("POST /v1/transactions"))
		}
		if a, b = issue(t, s, project, a.ID), issue(t, s, project, b.ID); len(a.Blocks) != 0 || len(b.BlockedBy) != 0 || b.Title != "B renamed" {
			t.Fatalf("refused link changed records: %+v %+v", a, b)
		}
		if _, err = s.Commit(ctx, plan); litclient.Code(err) != "prepared_already_submitted" || h.count("POST /v1/transactions") != commits+1 {
			t.Fatalf("second commit of one Prepared: %v (commits %d)", err, h.count("POST /v1/transactions"))
		}
		if files := h.pending(); len(files) != 0 {
			t.Fatalf("definite refusal kept evidence: %v", files)
		}
	})
	t.Run("structural guard", func(t *testing.T) {
		h := newHarness(t)
		s := h.connect("guard", "Human")
		project, issues := seed(t, s, "guard", "Parent")
		parent := issues["Parent"]
		apply(t, s, litclient.CreateIssue(project, "Child", "child body", parent.ID))
		child := issue(t, s, project, "Child")

		plan, err := s.Prepare(ctx, litclient.UpdateIssue(project, child.ID, 0, litclient.IssueChange{Title: str("Child renamed")}))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(refIDs(plan.Intent().Guards), parent.ID) {
			t.Fatalf("preview does not guard the parent %s: %+v", parent.ID, plan.Intent().Guards)
		}
		apply(t, s, litclient.UpdateIssue(project, parent.ID, 0, litclient.IssueChange{Title: str("Parent renamed")}))
		previews := h.count("POST /v1/transaction-previews")
		if _, err = s.Commit(ctx, plan); litclient.Code(err) != "revision_conflict" {
			t.Fatalf("commit after guard change: %v", err)
		}
		if h.count("POST /v1/transaction-previews") != previews {
			t.Fatal("commit prepared again")
		}
		if got := issue(t, s, project, child.ID); got.Title != "Child" || got.Revision != child.Revision {
			t.Fatalf("refused update changed the child: %+v", got)
		}
	})
}

// The commit carries the claim tokens this client owned at preview, so a claim
// that ended and was reacquired after the preview refuses the commit.
func TestCommitUsesOwnedClaimTokensFromPreview(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	s := h.connect("tokens", "Human")
	other := h.connect("tokens-other", "Agent")
	project, issues := seed(t, s, "tokens", "A", "B")
	a, b := issues["A"], issues["B"]
	own := litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Project: project, Issues: []string{a.ID}}}
	if _, err := s.AcquireClaims(ctx, own); err != nil {
		t.Fatal(err)
	}
	plan, err := s.Prepare(ctx, litclient.UpdateIssue(project, a.ID, a.Revision, litclient.IssueChange{Body: str("edited")}))
	if err != nil {
		t.Fatal(err)
	}
	required := plan.RequiredClaims()
	if len(required) != 1 || required[0].IssueID != a.ID || required[0].Claim == nil || required[0].Claim.OwnerClientID != s.Client.ClientID {
		t.Fatalf("required claims: %+v", required)
	}
	if _, err = s.ReleaseClaims(ctx, own.ClaimSelection); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcquireClaims(ctx, own); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Commit(ctx, plan); litclient.Code(err) != "claim_conflict" {
		t.Fatalf("commit with a superseded claim token: %v", err)
	}
	if got := issue(t, s, project, a.ID); got.Body != a.Body {
		t.Fatalf("refused commit changed the body: %q", got.Body)
	}
	apply(t, s, litclient.UpdateIssue(project, a.ID, a.Revision, litclient.IssueChange{Body: str("edited")}))
	if got := issue(t, s, project, a.ID); got.Body != "edited" {
		t.Fatalf("body after commit with current token: %q", got.Body)
	}

	// A relationship touching an issue another client claims is refused before
	// anything is written.
	if _, err = other.AcquireClaims(ctx, litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Project: project, Issues: []string{b.ID}}}); err != nil {
		t.Fatal(err)
	}
	commits := h.count("POST /v1/transactions")
	if _, err = s.Prepare(ctx, litclient.LinkIssues(project, a.ID, "blocks", b.ID)); litclient.Code(err) != "claim_conflict" {
		t.Fatalf("link onto a foreign claim: %v", err)
	}
	if h.count("POST /v1/transactions") != commits {
		t.Fatal("refused preview was committed")
	}
}

func TestCommitAcceptsOnlyItsOwnSessionsPrepared(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	s := h.connect("owner", "Human")
	other := h.connect("stranger", "Human")
	project, issues := seed(t, s, "owned", "A")
	plan, err := s.Prepare(ctx, litclient.UpdateIssue(project, issues["A"].ID, 0, litclient.IssueChange{Title: str("A2")}))
	if err != nil {
		t.Fatal(err)
	}
	commits := h.count("POST /v1/transactions")
	if _, err = other.Commit(ctx, plan); litclient.Code(err) != "wrong_client" {
		t.Fatalf("commit through another client: %v", err)
	}
	if _, err = s.Commit(ctx, nil); litclient.Code(err) != "invalid_arguments" {
		t.Fatalf("nil prepared: %v", err)
	}
	if _, err = s.Commit(ctx, &litclient.Prepared{}); litclient.Code(err) != "invalid_arguments" {
		t.Fatalf("zero prepared: %v", err)
	}
	if h.count("POST /v1/transactions") != commits {
		t.Fatal("refused prepared transactions were sent")
	}
	// The refused attempts did not consume it for its own session.
	if v, err := s.Commit(ctx, plan); err != nil || v.Outcome != "applied" || v.RequestHash != plan.RequestHash() {
		t.Fatalf("own commit: %+v %v", v, err)
	}
	// Copies handed out by accessors cannot change what is committed.
	intent := plan.Intent()
	intent.Operations = nil
	if len(plan.Intent().Operations) == 0 {
		t.Fatal("Intent exposed internal state")
	}
}

// Comment, project, milestone and issue mutations round-trip through
// Prepare and Commit.
func TestTypedMutations(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	s := h.connect("mutations", "Human")
	project, issues := seed(t, s, "mutations", "A", "B")
	a, b := issues["A"], issues["B"]

	apply(t, s, litclient.CreateComment(project, a.ID, "first"))
	a = issue(t, s, project, a.ID)
	if len(a.CommentIDs) != 1 {
		t.Fatalf("comment ids: %v", a.CommentIDs)
	}
	comment, err := litclient.Get[protocol.Comment](ctx, s, project, a.CommentIDs[0])
	if err != nil || len(comment) != 1 || comment[0].Body != "first" {
		t.Fatalf("comment: %+v %v", comment, err)
	}
	apply(t, s, litclient.UpdateComment(comment[0].ID, comment[0].Revision, "second"))
	if comment, err = litclient.Get[protocol.Comment](ctx, s, project, comment[0].ID); err != nil || comment[0].Body != "second" {
		t.Fatalf("updated comment: %+v %v", comment, err)
	}

	apply(t, s, litclient.UpdateIssue(project, a.ID, 0, litclient.IssueChange{Assignee: str("nick"), AddLabels: []string{"x"}, State: str("open")}))
	apply(t, s, litclient.LinkIssues(project, a.ID, "related", b.ID))
	if a = issue(t, s, project, a.ID); a.Assignee == nil || *a.Assignee != "nick" || !slices.Equal(a.Labels, []string{"x"}) || !slices.Equal(a.Related, []string{b.ID}) {
		t.Fatalf("issue after update and link: %+v", a)
	}
	apply(t, s, litclient.UnlinkIssues(project, a.ID, "related", b.ID))
	apply(t, s, litclient.UpdateIssue(project, a.ID, 0, litclient.IssueChange{Clear: []string{"assignee"}, RemoveLabels: []string{"x"}}))
	apply(t, s, litclient.CloseIssue(project, b.ID, 0))
	if a, b = issue(t, s, project, a.ID), issue(t, s, project, b.ID); a.Assignee != nil || len(a.Labels) != 0 || len(a.Related) != 0 || b.State != "closed" {
		t.Fatalf("after unlink, clear and close: %+v %+v", a, b)
	}
	apply(t, s, litclient.ReopenIssue(project, b.ID, 0))
	if b = issue(t, s, project, b.ID); b.State != "open" {
		t.Fatalf("reopen: %+v", b)
	}

	apply(t, s, litclient.CreateMilestone(project, "M1", "goal", a.ID))
	milestones, err := litclient.Get[protocol.Milestone](ctx, s, project, "M1")
	if err != nil || len(milestones) != 1 || !slices.Equal(milestones[0].IssueIDs, []string{a.ID}) {
		t.Fatalf("milestone: %+v %v", milestones, err)
	}
	m := milestones[0]
	apply(t, s, litclient.UpdateMilestone(project, m.ID, m.Revision, litclient.MilestoneChange{Title: str("M1b"), Body: str("new goal"), AddIssues: []string{b.ID}, RemoveIssues: []string{a.ID}}))
	if milestones, err = litclient.Get[protocol.Milestone](ctx, s, project, m.ID); err != nil || milestones[0].Title != "M1b" || milestones[0].Body != "new goal" || !slices.Equal(milestones[0].IssueIDs, []string{b.ID}) {
		t.Fatalf("updated milestone: %+v %v", milestones, err)
	}

	projects, err := litclient.Get[protocol.Project](ctx, s, "", project)
	if err != nil || len(projects) != 1 {
		t.Fatalf("project: %+v %v", projects, err)
	}
	p := projects[0]
	apply(t, s, litclient.UpdateProject(p.ID, p.Revision, litclient.ProjectChange{Description: str("new spec"), AddRepositories: []string{"https://example.org/r"}}))
	if projects, err = litclient.Get[protocol.Project](ctx, s, "", p.ID); err != nil || projects[0].Description != "new spec" || !slices.Equal(projects[0].RepositoryRefs, []string{"https://example.org/r"}) {
		t.Fatalf("updated project: %+v %v", projects, err)
	}
	// A stale recorded revision is fenced.
	if _, err = s.Prepare(ctx, litclient.UpdateProject(p.ID, p.Revision, litclient.ProjectChange{Title: str("test/renamed")})); litclient.Code(err) != "revision_conflict" {
		t.Fatalf("stale project revision: %v", err)
	}
	if files := h.pending(); len(files) != 0 {
		t.Fatalf("confirmed mutations kept evidence: %v", files)
	}
}
