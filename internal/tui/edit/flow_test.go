package edit

import (
	"context"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/claims"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/lifecycle"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// fixture is a connected lit-tui lifecycle with a claim holder and an
// editor, and an agent client, over project test/edit with two issues.
type fixture struct {
	t              *testing.T
	srv            *tuitest.Server
	l              *lifecycle.Lifecycle
	h              *claims.Holder
	e              *Editor
	agent          *litclient.Session
	project        string
	issue, another string
}

func start(t *testing.T, cfg claims.Config) *fixture {
	t.Helper()
	srv := tuitest.Service(t)
	_, agent := tuitest.InstanceAs(t, t.TempDir(), srv.URL, protocol.Actor{Name: "Claude", Kind: "agent"})
	tuitest.Apply(t, agent, litclient.CreateProject("test/edit", "spec"))
	ids := tuitest.CreateIssues(t, agent, "test/edit", "", "Edited issue", "Another issue")
	got, err := litclient.Get[protocol.Issue](context.Background(), agent, "test/edit", ids[0])
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	l, err := lifecycle.New(lifecycle.Config{StateDir: state, Endpoint: srv.URL, HTTP: &http.Client{}, Actor: protocol.Actor{Name: "person", Kind: "human"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		l.Shutdown("test")
		<-l.Done()
	})
	h := claims.Start(l, cfg)
	return &fixture{t: t, srv: srv, l: l, h: h, e: New(l, h, drafts.New(state)), agent: agent, project: got[0].ProjectID, issue: ids[0], another: ids[1]}
}

var ctx = context.Background()

func (f *fixture) read(id string) protocol.Issue {
	f.t.Helper()
	got, err := litclient.Get[protocol.Issue](ctx, f.agent, "test/edit", id)
	if err != nil || len(got) != 1 {
		f.t.Fatalf("read %s: %v %v", id, got, err)
	}
	return got[0]
}

// claim is issue id's live claim, read by the agent, or nil.
func (f *fixture) claim(id string) *protocol.Claim {
	f.t.Helper()
	snap, err := f.agent.SnapshotClaims(ctx, litclient.ClaimSelection{Issues: []string{id}})
	if err != nil || len(snap.Items) != 1 {
		f.t.Fatalf("claim snapshot: %+v %v", snap, err)
	}
	return snap.Items[0].Claim
}

func (f *fixture) ours(id string) bool {
	c := f.claim(id)
	return c != nil && c.OwnerClientID == f.l.Status().Client
}

func (f *fixture) agentUpdates(id, body string) {
	f.t.Helper()
	tuitest.Apply(f.t, f.agent, litclient.UpdateIssue("test/edit", id, 0, litclient.IssueChange{Body: &body}))
}

func (f *fixture) start(id string) drafts.Draft {
	f.t.Helper()
	s := f.e.Start(ctx, f.project, id, "title")
	if s.Err != nil {
		f.t.Fatalf("start: %v", s.Err)
	}
	return s.Draft
}

func write(t *testing.T, d drafts.Draft, content string) {
	t.Helper()
	if err := os.WriteFile(d.Path(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Starting an edit claims the issue first, then records a draft holding
// exactly its body at the revision read after the claim; while it is held,
// an agent's claim attempt is refused (story 5).
func TestStartClaimsFirstAndRecordsTheBody(t *testing.T) {
	f := start(t, claims.Config{})
	before := f.read(f.issue)
	s := f.e.Start(ctx, f.project, f.issue, "Edited issue")
	if s.Err != nil || s.Kind != drafts.ClaimEdit || s.Draft.Claim != drafts.ClaimEdit {
		t.Fatalf("start: %+v", s)
	}
	if !f.ours(f.issue) {
		t.Fatal("the issue is not claimed by this session")
	}
	if h, ok := f.h.Snapshot().Hold(f.issue); !ok || h.State != claims.Holding {
		t.Fatalf("the holder does not renew the edit claim: %+v", f.h.Snapshot())
	}
	got, err := f.e.Store().Read(s.Draft)
	if err != nil || got != before.Body || s.Draft.Base != before.Body || s.Draft.Revision != before.Revision || s.Draft.Title != "Edited issue" {
		t.Fatalf("draft %q %+v %v (issue %+v)", got, s.Draft, err, before)
	}
	if !strings.HasSuffix(s.Draft.File, "--"+f.issue[:8]+".md") || s.Draft.ServiceID == "" || s.Draft.ProjectID != f.project {
		t.Fatalf("draft identity %+v", s.Draft)
	}
	if _, err := f.agent.AcquireClaims(ctx, litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{f.issue}}}); litclient.Code(err) != "claim_conflict" {
		t.Fatalf("the agent's claim attempt was not refused: %v", err)
	}
}

// Another client's claim refuses the edit before anything is written, and
// names its owner. An existing draft is refused without a claim request and
// never overwritten.
func TestStartRefusesAForeignClaimAndAnExistingDraft(t *testing.T) {
	f := start(t, claims.Config{})
	if _, err := f.agent.AcquireClaims(ctx, litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{f.another}}}); err != nil {
		t.Fatal(err)
	}
	s := f.e.Start(ctx, f.project, f.another, "Another issue")
	if litclient.Code(s.Err) != "claim_conflict" || s.Owner == nil || s.Owner.OwnerActor == nil || s.Owner.OwnerActor.Name != "Claude" {
		t.Fatalf("foreign claim: %+v", s)
	}
	if _, ok, _ := f.e.Existing(f.project, f.another); ok {
		t.Fatal("a refused edit left a draft")
	}
	d := f.start(f.issue)
	write(t, d, "my unsaved work")
	ops := f.srv.Count("POST /v1/operations")
	again := f.e.Start(ctx, f.project, f.issue, "Edited issue")
	if litclient.Code(again.Err) != "draft_exists" || f.srv.Count("POST /v1/operations") != ops {
		t.Fatalf("second start: %+v (operations %d -> %d)", again, ops, f.srv.Count("POST /v1/operations"))
	}
	if got, _ := f.e.Store().Read(d); got != "my unsaved work" {
		t.Fatalf("draft overwritten: %q", got)
	}
}

// Apply commits exactly the previewed update at the recorded revision (one
// preview, one commit), then releases the edit claim and removes the draft.
func TestApplyCommitsTheExactPreviewAndReleasesTheEditClaim(t *testing.T) {
	f := start(t, claims.Config{})
	d := f.start(f.issue)
	write(t, d, "body of Edited issue\nwith a new line\n")
	p := f.e.Preview(ctx, d)
	if !p.Ready() || p.Lost() || p.Current == nil || p.Current.Revision != d.Revision {
		t.Fatalf("preview: %+v", p)
	}
	if !slices.Equal(p.Diff(), []string{"@@ -1,1 +1,2 @@", "-body of Edited issue", drafts.NoNewline, "+body of Edited issue", "+with a new line"}) {
		t.Fatalf("diff %q", p.Diff())
	}
	if len(p.Affected) != 2 || p.Affected[0].ID != f.issue || p.Affected[0].ExpectedRevision == nil || *p.Affected[0].ExpectedRevision != d.Revision ||
		p.Affected[1].Type != "projects" || p.Affected[1].ID != f.project {
		t.Fatalf("affected: the issue at its recorded revision, then the project as a guard: %+v", p.Affected)
	}
	previews, commits := f.srv.Count("POST /v1/transaction-previews"), f.srv.Count("POST /v1/transactions")
	a := f.e.Apply(ctx, p)
	if a.Err != nil || !a.Released || len(a.Result.Items) != 1 || a.Result.Items[0].Revision != d.Revision+1 {
		t.Fatalf("apply: %+v", a)
	}
	if f.srv.Count("POST /v1/transaction-previews") != previews || f.srv.Count("POST /v1/transactions") != commits+1 {
		t.Fatal("apply did not commit exactly the previewed transaction once")
	}
	if got := f.read(f.issue); got.Body != "body of Edited issue\nwith a new line\n" || got.Revision != d.Revision+1 {
		t.Fatalf("issue after apply %+v", got)
	}
	if f.claim(f.issue) != nil {
		t.Fatal("the edit claim was not released")
	}
	if _, ok := f.h.Snapshot().Hold(f.issue); ok {
		t.Fatal("the holder still holds the edit claim")
	}
	if _, ok, _ := f.e.Existing(f.project, f.issue); ok {
		t.Fatal("the draft was not removed")
	}
	if again := f.e.Apply(ctx, p); again.Err == nil {
		t.Fatal("a preview was applied twice")
	}
}

// A claim the user already held explicitly is kept after apply and after
// discarding; an unchanged draft has nothing to apply.
func TestAnExplicitClaimIsKept(t *testing.T) {
	f := start(t, claims.Config{})
	if r := f.h.Claim(ctx, f.issue, "Edited issue"); r.Err != nil {
		t.Fatal(r.Err)
	}
	s := f.e.Start(ctx, f.project, f.issue, "Edited issue")
	if s.Err != nil || s.Kind != drafts.ClaimExplicit || !s.Claim.Already {
		t.Fatalf("start: %+v", s)
	}
	if p := f.e.Preview(ctx, s.Draft); !p.Unchanged || p.Ready() {
		t.Fatalf("unchanged preview: %+v", p)
	}
	write(t, s.Draft, "explicit edit")
	if a := f.e.Apply(ctx, f.e.Preview(ctx, s.Draft)); a.Err != nil || a.Released {
		t.Fatalf("apply: %+v", a)
	}
	if !f.ours(f.issue) {
		t.Fatal("the explicit claim was released")
	}
	d := f.start(f.issue)
	if r := f.e.Discard(ctx, d); r.Err != nil || r.Released || !f.ours(f.issue) {
		t.Fatalf("discard: %+v", r)
	}
	if _, ok, _ := f.e.Existing(f.project, f.issue); ok {
		t.Fatal("discard kept the draft")
	}
}

// Discarding an edit (cancel) removes the draft and releases its edit claim.
func TestDiscardReleasesTheEditClaim(t *testing.T) {
	f := start(t, claims.Config{})
	d := f.start(f.issue)
	if r := f.e.Discard(ctx, d); r.Err != nil || !r.Released {
		t.Fatalf("discard: %+v", r)
	}
	if f.claim(f.issue) != nil {
		t.Fatal("the edit claim was kept")
	}
	if _, err := os.Stat(d.Path()); !os.IsNotExist(err) {
		t.Fatalf("draft file kept: %v", err)
	}
}

// After the edit claim is lost, the preview says so; a revision change
// refuses the apply without sending it and keeps the draft; reload records
// the current revision and keeps the text, and the optimistic apply then
// commits fenced by that revision.
func TestRevisionConflictKeepsTheDraftAndReloadRebases(t *testing.T) {
	f := start(t, claims.Config{})
	d := f.start(f.issue)
	write(t, d, "my edit\n")
	if r := f.h.Release(ctx, f.issue); r.Err != nil {
		t.Fatal(r.Err)
	}
	f.agentUpdates(f.issue, "the agent's edit\n")
	p := f.e.Preview(ctx, d)
	if !p.Conflict || !p.Lost() || p.Ready() || p.Current == nil || p.Current.Revision != d.Revision+1 {
		t.Fatalf("preview: %+v", p)
	}
	commits := f.srv.Count("POST /v1/transactions")
	if a := f.e.Apply(ctx, p); a.Err == nil || f.srv.Count("POST /v1/transactions") != commits {
		t.Fatalf("a conflicting preview was applied: %+v", a)
	}
	if got, err := f.e.Store().Read(d); err != nil || got != "my edit\n" {
		t.Fatalf("draft after the refusal %q %v", got, err)
	}
	c := f.e.DiffCurrent(ctx, d)
	if c.Err != nil || !slices.Equal(c.Diff, []string{"@@ -1,1 +1,1 @@", "-the agent's edit", "+my edit"}) {
		t.Fatalf("diff against the current body: %+v", c)
	}
	d, err := f.e.Reload(ctx, d)
	if err != nil || d.Revision != p.Current.Revision || d.Base != "the agent's edit\n" {
		t.Fatalf("reload: %+v %v", d, err)
	}
	p = f.e.Preview(ctx, d)
	if !p.Ready() || !p.Lost() {
		t.Fatalf("preview after reload: %+v", p)
	}
	if a := f.e.Apply(ctx, p); a.Err != nil || a.Released {
		t.Fatalf("optimistic apply: %+v", a)
	}
	if got := f.read(f.issue); got.Body != "my edit\n" {
		t.Fatalf("body %q", got.Body)
	}
}

// Another client's claim on the issue refuses the apply before anything is
// written and names the owner, whether it is seen in the preview or only
// arrives between preview and commit. The draft is kept either way.
func TestForeignClaimRefusesTheApplyAndNamesTheOwner(t *testing.T) {
	f := start(t, claims.Config{})
	d := f.start(f.issue)
	write(t, d, "my edit\n")
	f.h.Release(ctx, f.issue)
	p := f.e.Preview(ctx, d)
	if !p.Ready() {
		t.Fatalf("preview: %+v", p)
	}
	if _, err := f.agent.AcquireClaims(ctx, litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{f.issue}}}); err != nil {
		t.Fatal(err)
	}
	a := f.e.Apply(ctx, p)
	if litclient.Code(a.Err) != "claim_conflict" || a.Owner == nil || a.Owner.OwnerActor == nil || a.Owner.OwnerActor.Name != "Claude" {
		t.Fatalf("apply against a claim taken after the preview: %+v", a)
	}
	p = f.e.Preview(ctx, d)
	if p.Foreign == nil || p.Foreign.OwnerActor == nil || p.Foreign.OwnerActor.Name != "Claude" || p.Ready() {
		t.Fatalf("preview with a foreign claim: %+v", p)
	}
	commits := f.srv.Count("POST /v1/transactions")
	if a := f.e.Apply(ctx, p); a.Err == nil || f.srv.Count("POST /v1/transactions") != commits {
		t.Fatalf("applied despite the foreign claim: %+v", a)
	}
	if got := f.read(f.issue); got.Revision != d.Revision {
		t.Fatalf("the issue changed: %+v", got)
	}
	if _, ok, _ := f.e.Existing(f.project, f.issue); !ok {
		t.Fatal("the draft was removed")
	}
	if r := f.e.Resume(ctx, d); litclient.Code(r.Err) != "claim_conflict" || r.Owner == nil {
		t.Fatalf("resume against the foreign claim: %+v", r)
	}
}

// Story 6 at the package seam: with no activity the edit claim pauses,
// lapses and is lost; the draft is kept and the preview warns that applying
// is optimistic. Resuming reacquires the claim as the edit's again.
func TestALapsedEditClaimIsLostAndResumeReacquires(t *testing.T) {
	f := start(t, claims.Config{Lease: 1500 * time.Millisecond, Idle: 200 * time.Millisecond})
	d := f.start(f.issue)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if h, ok := f.h.Snapshot().Hold(f.issue); ok && h.State == claims.Lost {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the edit claim did not lapse: %+v", f.h.Snapshot())
		}
		time.Sleep(20 * time.Millisecond)
	}
	write(t, d, "after the lapse\n")
	if p := f.e.Preview(ctx, d); !p.Lost() || !p.Ready() || p.Hold.State != claims.Lost {
		t.Fatalf("preview: %+v", p)
	}
	r := f.e.Resume(ctx, d)
	if r.Err != nil || r.Kind != drafts.ClaimEdit || !r.Claim.Reacquired || !f.ours(f.issue) {
		t.Fatalf("resume: %+v", r)
	}
	if got, _ := f.e.Store().Read(r.Draft); got != "after the lapse\n" || r.Draft.Revision != d.Revision {
		t.Fatalf("resume changed the draft: %q %+v", got, r.Draft)
	}
	if a := f.e.Apply(ctx, f.e.Preview(ctx, r.Draft)); a.Err != nil || !a.Released || f.claim(f.issue) != nil {
		t.Fatalf("apply: %+v", a)
	}
}

// A commit whose answer is lost is uncertain: the draft and the claim are
// kept, the retained request is named, and nothing is replayed.
func TestAnUncertainApplyKeepsTheDraft(t *testing.T) {
	f := start(t, claims.Config{})
	d := f.start(f.issue)
	write(t, d, "uncertain\n")
	p := f.e.Preview(ctx, d)
	f.srv.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/v1/transactions" || r.URL.Path == "/v1/transaction-status" {
			tuitest.HangUp(w)
			return true
		}
		return false
	})
	a := f.e.Apply(ctx, p)
	f.srv.Intercept(nil)
	if litclient.Code(a.Err) != "outcome_uncertain" || a.Uncertain == nil || a.Uncertain.ID == "" || a.Released {
		t.Fatalf("apply: %+v", a)
	}
	if _, err := os.Stat(a.Uncertain.Path); err != nil {
		t.Fatalf("evidence %v", err)
	}
	if _, ok, _ := f.e.Existing(f.project, f.issue); !ok || !f.ours(f.issue) {
		t.Fatal("the draft or the claim was dropped")
	}
}
