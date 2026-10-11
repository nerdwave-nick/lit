package create

import (
	"context"
	"net/http"
	"os"
	"slices"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/claims"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/edit"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/lifecycle"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// fixture is a connected lit-tui lifecycle with a claim holder and an
// editor that creates issues, and an agent client, over project test/create
// with one issue (the parent of new children).
type fixture struct {
	t       *testing.T
	srv     *tuitest.Server
	l       *lifecycle.Lifecycle
	h       *claims.Holder
	e       *edit.Editor
	agent   *litclient.Session
	project string
	parent  string
}

var ctx = context.Background()

func start(t *testing.T) *fixture {
	t.Helper()
	srv := tuitest.Service(t)
	_, agent := tuitest.InstanceAs(t, t.TempDir(), srv.URL, protocol.Actor{Name: "Claude", Kind: "agent"})
	tuitest.Apply(t, agent, litclient.CreateProject("test/create", "spec"))
	ids := tuitest.CreateIssues(t, agent, "test/create", "", "Parent issue")
	state := t.TempDir()
	l, err := lifecycle.New(lifecycle.Config{StateDir: state, Endpoint: srv.URL, HTTP: &http.Client{}, Actor: protocol.Actor{Name: "person", Kind: "human"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		l.Shutdown("test")
		<-l.Done()
	})
	h := claims.Start(l, claims.Config{})
	e := edit.New(l, h, drafts.New(state))
	Register(e)
	f := &fixture{t: t, srv: srv, l: l, h: h, e: e, agent: agent, parent: ids[0]}
	f.project = f.issue(ids[0]).ProjectID
	return f
}

func (f *fixture) issue(id string) protocol.Issue {
	f.t.Helper()
	got, err := litclient.Get[protocol.Issue](ctx, f.agent, "test/create", id)
	if err != nil || len(got) != 1 {
		f.t.Fatalf("read %s: %v %v", id, got, err)
	}
	return got[0]
}

// titled lists the project's issues with title.
func (f *fixture) titled(title string) []protocol.Issue {
	f.t.Helper()
	page, err := litclient.List[protocol.Issue](ctx, f.agent, litclient.Query{ProjectID: f.project, Title: title, All: true})
	if err != nil {
		f.t.Fatal(err)
	}
	return page.Items
}

func (f *fixture) agentClaims(id string) {
	f.t.Helper()
	if _, err := f.agent.AcquireClaims(ctx, litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{id}}}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) open(parent, title string) drafts.Draft {
	f.t.Helper()
	s := f.e.Open(ctx, Target(f.project, parent, title))
	if s.Err != nil {
		f.t.Fatalf("open: %v", s.Err)
	}
	return s.Draft
}

func write(t *testing.T, d drafts.Draft, content string) {
	t.Helper()
	if err := os.WriteFile(d.Path(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func refs(p edit.Preview) map[string]protocol.ObjectRef {
	out := map[string]protocol.ObjectRef{}
	for _, r := range p.Affected {
		out[r.Type+" "+r.ID] = r
	}
	return out
}

// A top-level issue is created by exactly the previewed transaction: the
// draft starts empty under a generated ID and takes no claim, the preview
// fences only the project and creates the issue under the draft's ID, and
// applying creates exactly that issue (title, body, no parent) and removes
// the draft.
func TestATopLevelIssueIsCreatedFromItsDraft(t *testing.T) {
	f := start(t)
	d := f.open("", "A new issue")
	if d.Kind != drafts.IssueNew || d.IssueID != "" || d.ParentID != "" || d.Title != "A new issue" || !protocol.ValidUUID(d.RecordID) || d.Claim != drafts.ClaimNone {
		t.Fatalf("draft %+v", d)
	}
	if got, _ := f.e.Store().Read(d); got != "" {
		t.Fatalf("a new issue's draft starts empty: %q", got)
	}
	write(t, d, "The body.\n")
	p := f.e.Preview(ctx, d)
	if !p.Ready() || !p.Exempt || p.Lost() {
		t.Fatalf("preview %+v", p)
	}
	affected := refs(p)
	if r, ok := affected["issues "+d.RecordID]; !ok || r.ExpectedRevision != nil || len(affected) != 2 {
		t.Fatalf("affected %+v", p.Affected)
	}
	if _, ok := affected["projects "+f.project]; !ok {
		t.Fatalf("the project is not fenced: %+v", p.Affected)
	}
	commits := f.srv.Count("POST /v1/transactions")
	a := f.e.Apply(ctx, p)
	if a.Err != nil || f.srv.Count("POST /v1/transactions") != commits+1 {
		t.Fatalf("apply: %+v", a)
	}
	got := f.issue(d.RecordID)
	if got.Title != "A new issue" || got.Body != "The body.\n" || got.ParentID != nil || got.State != "open" {
		t.Fatalf("created %+v", got)
	}
	if _, ok, _ := f.e.Store().Find(f.project, drafts.IssueNew, d.RecordID); ok {
		t.Fatal("the draft was kept after the issue was created")
	}
}

// A child's creation reads its parent: the preview lists the parent, fenced
// at its revision, and applying creates the child under it. The parent's
// revision stays (its children are computed). A blank body is applied: an
// issue may have none.
func TestAChildIsCreatedUnderItsParentWhichThePreviewLists(t *testing.T) {
	f := start(t)
	before := f.issue(f.parent)
	d := f.open(f.parent, "A child")
	if d.ParentID != f.parent || d.IssueID != "" {
		t.Fatalf("draft %+v", d)
	}
	p := f.e.Preview(ctx, d)
	if !p.Ready() || p.Unchanged {
		t.Fatalf("a blank new issue cannot be previewed: %+v", p)
	}
	r, ok := refs(p)["issues "+f.parent]
	if !ok || r.ExpectedRevision == nil || *r.ExpectedRevision != before.Revision {
		t.Fatalf("the parent is not listed at revision %d: %+v", before.Revision, p.Affected)
	}
	if a := f.e.Apply(ctx, p); a.Err != nil {
		t.Fatal(a.Err)
	}
	child := f.issue(d.RecordID)
	if child.ParentID == nil || *child.ParentID != f.parent || child.Body != "" {
		t.Fatalf("child %+v", child)
	}
	if after := f.issue(f.parent); !slices.Contains(after.ChildIDs, d.RecordID) || after.Revision != before.Revision {
		t.Fatalf("parent %+v", after)
	}
}

// Another client's claim on the parent refuses the creation before anything
// is written, naming the owner; the draft is kept. After the claim ends the
// same draft creates the child.
func TestAForeignClaimOnTheParentRefusesTheChild(t *testing.T) {
	f := start(t)
	f.agentClaims(f.parent)
	d := f.open(f.parent, "Refused child")
	write(t, d, "kept text\n")
	commits := f.srv.Count("POST /v1/transactions")
	p := f.e.Preview(ctx, d)
	if p.Ready() || p.Foreign == nil || p.Foreign.IssueID != f.parent || p.Foreign.OwnerActor == nil || p.Foreign.OwnerActor.Name != "Claude" {
		t.Fatalf("preview %+v", p)
	}
	a := f.e.Apply(ctx, p)
	if litclient.Code(a.Err) != "claim_conflict" || a.Owner == nil || a.Owner.IssueID != f.parent {
		t.Fatalf("apply %+v", a)
	}
	if f.srv.Count("POST /v1/transactions") != commits || len(f.titled("Refused child")) != 0 {
		t.Fatal("something was written")
	}
	if got, _ := f.e.Store().Read(d); got != "kept text\n" {
		t.Fatalf("draft %q", got)
	}
	if _, err := f.agent.ReleaseClaims(ctx, litclient.ClaimSelection{Issues: []string{f.parent}}); err != nil {
		t.Fatal(err)
	}
	if a := f.e.Apply(ctx, f.e.Preview(ctx, d)); a.Err != nil {
		t.Fatal(a.Err)
	}
	if got := f.issue(d.RecordID); got.Body != "kept text\n" {
		t.Fatalf("child %+v", got)
	}
}

// This session's own claim on the parent is passed on with the creation and
// stays held.
func TestOwnClaimOnTheParentIsPassedOn(t *testing.T) {
	f := start(t)
	if r := f.h.Claim(ctx, f.parent, "Parent issue"); r.Err != nil {
		t.Fatal(r.Err)
	}
	d := f.open(f.parent, "Child of a claimed parent")
	p := f.e.Preview(ctx, d)
	if !p.Ready() {
		t.Fatalf("preview %+v", p)
	}
	if a := f.e.Apply(ctx, p); a.Err != nil {
		t.Fatal(a.Err)
	}
	snap, err := f.agent.SnapshotClaims(ctx, litclient.ClaimSelection{Issues: []string{f.parent}})
	if err != nil || len(snap.Items) != 1 || snap.Items[0].Claim == nil || snap.Items[0].Claim.OwnerClientID != f.l.Status().Client {
		t.Fatalf("the claim on the parent: %+v %v", snap, err)
	}
}

// The draft's ID is the issue's ID, so a draft is created at most once: when
// an issue with that ID exists (an earlier apply whose answer was lost went
// through), the preview reports it created and nothing is applied again.
// An uncertain apply keeps the draft and is never resent.
func TestANewIssueIsCreatedAtMostOnce(t *testing.T) {
	f := start(t)
	d := f.open("", "Uncertain issue")
	p := f.e.Preview(ctx, d)
	f.srv.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/v1/transactions" || r.URL.Path == "/v1/transaction-status" {
			tuitest.HangUp(w)
			return true
		}
		return false
	})
	commits := f.srv.Count("POST /v1/transactions")
	a := f.e.Apply(ctx, p)
	f.srv.Intercept(nil)
	if litclient.Code(a.Err) != "outcome_uncertain" || a.Uncertain == nil || f.srv.Count("POST /v1/transactions") != commits+1 {
		t.Fatalf("apply: %+v", a)
	}
	if _, ok, _ := f.e.Store().Find(f.project, drafts.IssueNew, d.RecordID); !ok {
		t.Fatal("the draft was dropped")
	}
	// The issue was in fact created (by the lost request, here the agent).
	req := litclient.CreateIssue(f.project, "Uncertain issue", "", "")
	req.Items[0].ID = d.RecordID
	tuitest.Apply(t, f.agent, req)
	again := f.e.Preview(ctx, d)
	if !again.Created || again.Ready() || again.Prepared != nil {
		t.Fatalf("a later preview: %+v", again)
	}
	commits = f.srv.Count("POST /v1/transactions")
	if a := f.e.Apply(ctx, again); a.Err == nil || f.srv.Count("POST /v1/transactions") != commits {
		t.Fatalf("applied twice: %+v", a)
	}
	if n := len(f.titled("Uncertain issue")); n != 1 {
		t.Fatalf("%d issues", n)
	}
}

// Changing the title of a kept draft creates the issue under the new title.
func TestRetitleChangesTheIssueTitleOfTheDraft(t *testing.T) {
	f := start(t)
	d := f.open("", "First title")
	d, err := Retitle(f.e.Store(), d, "Second title")
	if err != nil || d.Title != "Second title" {
		t.Fatalf("retitle: %+v %v", d, err)
	}
	if found, _, _ := f.e.Store().Find(f.project, drafts.IssueNew, d.RecordID); found.Title != "Second title" {
		t.Fatalf("stored %+v", found)
	}
	if a := f.e.Apply(ctx, f.e.Preview(ctx, d)); a.Err != nil {
		t.Fatal(a.Err)
	}
	if got := f.issue(d.RecordID); got.Title != "Second title" {
		t.Fatalf("created %+v", got)
	}
}
