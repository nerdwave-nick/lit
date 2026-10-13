package comments

import (
	"context"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/claims"
	"github.com/nerdwave-nick/lit/internal/tui/drafts"
	"github.com/nerdwave-nick/lit/internal/tui/edit"
	"github.com/nerdwave-nick/lit/internal/tui/lifecycle"
	"github.com/nerdwave-nick/lit/internal/tui/tuitest"
)

// fixture is a connected lit-tui lifecycle with a claim holder and an
// editor that edits comments, and an agent client, over project
// test/comments with one issue that has one comment by the agent.
type fixture struct {
	t       *testing.T
	srv     *tuitest.Server
	l       *lifecycle.Lifecycle
	h       *claims.Holder
	e       *edit.Editor
	agent   *litclient.Session
	project string
	issue   string
	comment protocol.Comment
}

var ctx = context.Background()

func start(t *testing.T) *fixture {
	t.Helper()
	srv := tuitest.Service(t)
	_, agent := tuitest.InstanceAs(t, t.TempDir(), srv.URL, protocol.Actor{Name: "Claude", Kind: "agent"})
	tuitest.Apply(t, agent, litclient.CreateProject("test/comments", "spec"))
	ids := tuitest.CreateIssues(t, agent, "test/comments", "", "Discussed issue")
	tuitest.Apply(t, agent, litclient.CreateComment("test/comments", ids[0], "the agent's comment\n"))
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
	f := &fixture{t: t, srv: srv, l: l, h: h, e: e, agent: agent, issue: ids[0]}
	got, err := litclient.Get[protocol.Issue](ctx, agent, "test/comments", ids[0])
	if err != nil {
		t.Fatal(err)
	}
	f.project = got[0].ProjectID
	f.comment = f.comments()[0]
	return f
}

func (f *fixture) comments() []protocol.Comment {
	f.t.Helper()
	page, err := litclient.List[protocol.Comment](ctx, f.agent, litclient.Query{IssueID: f.issue, All: true})
	if err != nil {
		f.t.Fatal(err)
	}
	return page.Items
}

func (f *fixture) claim() *protocol.Claim {
	f.t.Helper()
	snap, err := f.agent.SnapshotClaims(ctx, litclient.ClaimSelection{Issues: []string{f.issue}})
	if err != nil || len(snap.Items) != 1 {
		f.t.Fatalf("claim snapshot: %+v %v", snap, err)
	}
	return snap.Items[0].Claim
}

func (f *fixture) ours() bool {
	c := f.claim()
	return c != nil && c.OwnerClientID == f.l.Status().Client
}

func (f *fixture) agentClaims() {
	f.t.Helper()
	if _, err := f.agent.AcquireClaims(ctx, litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{f.issue}}}); err != nil {
		f.t.Fatal(err)
	}
}

func write(t *testing.T, d drafts.Draft, content string) {
	t.Helper()
	if err := os.WriteFile(d.Path(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A new comment is written in a draft that takes no claim, and is applied
// as a claim-exempt append while another client holds the issue's claim:
// exactly the previewed creation, with the draft's ID as the comment's.
func TestANewCommentIsAppendedWhileAnotherClientHoldsTheClaim(t *testing.T) {
	f := start(t)
	f.agentClaims()
	s := f.e.Open(ctx, New(f.project, f.issue, "Discussed issue"))
	if s.Err != nil || s.Kind != drafts.ClaimNone || s.Draft.Kind != drafts.CommentNew || s.Draft.IssueID != f.issue || !protocol.ValidUUID(s.Draft.RecordID) {
		t.Fatalf("open: %+v", s)
	}
	if got, _ := f.e.Store().Read(s.Draft); got != "" || s.Draft.Revision != 0 {
		t.Fatalf("a new comment's draft starts empty: %q at %d", got, s.Draft.Revision)
	}
	if _, ok := f.h.Snapshot().Hold(f.issue); ok {
		t.Fatal("a new comment took a claim")
	}
	content := "My reply,\n\nkept exactly\twith tabs"
	write(t, s.Draft, content)
	p := f.e.Preview(ctx, s.Draft)
	if !p.Ready() || p.Lost() || !p.Exempt || p.Foreign != nil {
		t.Fatalf("preview: %+v", p)
	}
	if !slices.ContainsFunc(p.Affected, func(r protocol.ObjectRef) bool { return r.Type == "comments" && r.ID == s.Draft.RecordID }) {
		t.Fatalf("the new comment is not among the affected records: %+v", p.Affected)
	}
	commits := f.srv.Count("POST /v1/transactions")
	a := f.e.Apply(ctx, p)
	if a.Err != nil || a.Released || f.srv.Count("POST /v1/transactions") != commits+1 {
		t.Fatalf("apply: %+v", a)
	}
	got := f.comments()
	if len(got) != 2 || got[1].ID != s.Draft.RecordID || got[1].Body != content || got[1].Author != "person" {
		t.Fatalf("comments after apply: %+v", got)
	}
	if c := f.claim(); c == nil || c.OwnerClientID == f.l.Status().Client {
		t.Fatalf("the agent's claim changed: %+v", c)
	}
	if _, ok, _ := f.e.ExistingFor(New(f.project, f.issue, "")); ok {
		t.Fatal("the applied draft was kept")
	}
}

// An unapplied new comment is offered again instead of a second draft; a
// blank one has nothing to apply; and a draft whose comment already exists
// (an earlier apply went through) is not applied twice.
func TestANewCommentDraftIsOfferedAndCreatedAtMostOnce(t *testing.T) {
	f := start(t)
	s := f.e.Open(ctx, New(f.project, f.issue, "Discussed issue"))
	if s.Err != nil {
		t.Fatal(s.Err)
	}
	if again := f.e.Open(ctx, New(f.project, f.issue, "Discussed issue")); litclient.Code(again.Err) != "draft_exists" {
		t.Fatalf("second new comment: %+v", again)
	}
	if d, ok, err := f.e.ExistingFor(New(f.project, f.issue, "")); err != nil || !ok || d.RecordID != s.Draft.RecordID {
		t.Fatalf("existing: %+v %v %v", d, ok, err)
	}
	write(t, s.Draft, " \n\t\n")
	if p := f.e.Preview(ctx, s.Draft); !p.Unchanged || p.Ready() {
		t.Fatalf("blank preview: %+v", p)
	}
	write(t, s.Draft, "posted twice?")
	req := litclient.CreateComment("test/comments", f.issue, "posted twice?")
	req.Items[0].ID = s.Draft.RecordID // as if an earlier, uncertain apply had been committed
	tuitest.Apply(t, f.agent, req)
	p := f.e.Preview(ctx, s.Draft)
	if !p.Created || p.Ready() {
		t.Fatalf("preview of an applied draft: %+v", p)
	}
	if a := f.e.Apply(ctx, p); a.Err == nil || len(f.comments()) != 2 {
		t.Fatalf("applied twice: %+v", a)
	}
	if _, err := f.e.Store().Read(s.Draft); err != nil {
		t.Fatalf("the draft was not kept: %v", err)
	}
}

// Editing an existing comment claims the owning issue first, records the
// comment's body at its revision, and applies exactly the previewed update
// fenced at that revision; then the edit claim is released.
func TestEditingACommentClaimsTheIssueAndAppliesAtItsRevision(t *testing.T) {
	f := start(t)
	s := f.e.Open(ctx, Edit(f.project, f.comment, "Discussed issue"))
	if s.Err != nil || s.Kind != drafts.ClaimEdit || s.Draft.RecordID != f.comment.ID || s.Draft.IssueID != f.issue ||
		s.Draft.Revision != f.comment.Revision || s.Draft.Base != f.comment.Body || s.Draft.Title != "Discussed issue" {
		t.Fatalf("open: %+v", s)
	}
	if !strings.HasPrefix(s.Draft.File, "comment-discussed-issue--") {
		t.Fatalf("draft file %q", s.Draft.File)
	}
	if got, _ := f.e.Store().Read(s.Draft); got != f.comment.Body {
		t.Fatalf("draft %q", got)
	}
	if !f.ours() {
		t.Fatal("the issue is not claimed for the edit")
	}
	if _, err := f.agent.AcquireClaims(ctx, litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{f.issue}}}); litclient.Code(err) != "claim_conflict" {
		t.Fatalf("the agent's claim attempt was not refused: %v", err)
	}
	write(t, s.Draft, "the agent's comment, corrected\n")
	p := f.e.Preview(ctx, s.Draft)
	if !p.Ready() || p.Lost() || p.Exempt || p.Record.Revision != f.comment.Revision {
		t.Fatalf("preview: %+v", p)
	}
	if !slices.Equal(p.Diff(), []string{"@@ -1,1 +1,1 @@", "-the agent's comment", "+the agent's comment, corrected"}) {
		t.Fatalf("diff %q", p.Diff())
	}
	i := slices.IndexFunc(p.Affected, func(r protocol.ObjectRef) bool { return r.Type == "comments" && r.ID == f.comment.ID })
	if i < 0 || p.Affected[i].ExpectedRevision == nil || *p.Affected[i].ExpectedRevision != f.comment.Revision {
		t.Fatalf("the comment is not fenced at its revision: %+v", p.Affected)
	}
	a := f.e.Apply(ctx, p)
	if a.Err != nil || !a.Released {
		t.Fatalf("apply: %+v", a)
	}
	got := f.comments()[0]
	if got.Body != "the agent's comment, corrected\n" || got.Revision != f.comment.Revision+1 {
		t.Fatalf("comment after apply %+v", got)
	}
	if f.claim() != nil {
		t.Fatal("the edit claim was not released")
	}
}

// Another client's claim refuses a comment edit before a draft exists, and
// names its owner.
func TestEditingACommentRefusedByAForeignClaimNamesTheOwner(t *testing.T) {
	f := start(t)
	f.agentClaims()
	s := f.e.Open(ctx, Edit(f.project, f.comment, "Discussed issue"))
	if litclient.Code(s.Err) != "claim_conflict" || s.Owner == nil || s.Owner.OwnerActor == nil || s.Owner.OwnerActor.Name != "Claude" {
		t.Fatalf("open: %+v", s)
	}
	if _, ok, _ := f.e.ExistingFor(Edit(f.project, f.comment, "")); ok {
		t.Fatal("a refused edit left a draft")
	}
}

// A revision change of the comment, or another client's claim, keeps the
// draft: reload records the current revision and keeps the text, diff
// compares with the current comment, and the owner is named.
func TestACommentConflictKeepsTheDraft(t *testing.T) {
	f := start(t)
	s := f.e.Open(ctx, Edit(f.project, f.comment, "Discussed issue"))
	if s.Err != nil {
		t.Fatal(s.Err)
	}
	write(t, s.Draft, "my version\n")
	if r := f.h.Release(ctx, f.issue); r.Err != nil {
		t.Fatal(r.Err)
	}
	tuitest.Apply(t, f.agent, litclient.UpdateComment(f.comment.ID, 0, "the agent's version\n"))
	p := f.e.Preview(ctx, s.Draft)
	if !p.Conflict || p.Ready() || !p.Lost() || p.Record.Revision != f.comment.Revision+1 {
		t.Fatalf("conflict preview: %+v", p)
	}
	if c := f.e.DiffCurrent(ctx, s.Draft); c.Err != nil || !slices.Equal(c.Diff, []string{"@@ -1,1 +1,1 @@", "-the agent's version", "+my version"}) {
		t.Fatalf("diff against current: %+v", c)
	}
	d, err := f.e.Reload(ctx, s.Draft)
	if err != nil || d.Revision != f.comment.Revision+1 || d.Base != "the agent's version\n" {
		t.Fatalf("reload: %+v %v", d, err)
	}
	if got, _ := f.e.Store().Read(d); got != "my version\n" {
		t.Fatalf("reload changed the text: %q", got)
	}
	f.agentClaims()
	p = f.e.Preview(ctx, d)
	if p.Foreign == nil || p.Foreign.OwnerActor == nil || p.Foreign.OwnerActor.Name != "Claude" || p.Ready() {
		t.Fatalf("foreign claim preview: %+v", p)
	}
	if a := f.e.Apply(ctx, p); litclient.Code(a.Err) != "claim_conflict" || a.Owner == nil {
		t.Fatalf("apply over a foreign claim: %+v", a)
	}
	if got, err := f.e.Store().Read(d); err != nil || got != "my version\n" {
		t.Fatalf("the draft was not kept: %q %v", got, err)
	}
	if f.comments()[0].Body != "the agent's version\n" {
		t.Fatal("the comment was overwritten")
	}
}

// A claim the user took explicitly is kept after a comment edit; drafts of
// the body and of a comment on one issue share its edit claim, which is
// released only when the last of them ends.
func TestCommentEditsKeepExplicitClaimsAndShareEditClaims(t *testing.T) {
	f := start(t)
	if r := f.h.Claim(ctx, f.issue, "Discussed issue"); r.Err != nil {
		t.Fatal(r.Err)
	}
	s := f.e.Open(ctx, Edit(f.project, f.comment, "Discussed issue"))
	if s.Err != nil || s.Kind != drafts.ClaimExplicit {
		t.Fatalf("open: %+v", s)
	}
	write(t, s.Draft, "explicitly claimed edit")
	if a := f.e.Apply(ctx, f.e.Preview(ctx, s.Draft)); a.Err != nil || a.Released || !f.ours() {
		t.Fatalf("apply: %+v (claim ours %v)", a, f.ours())
	}
	if r := f.h.Release(ctx, f.issue); r.Err != nil {
		t.Fatal(r.Err)
	}
	body := f.e.Start(ctx, f.project, f.issue, "Discussed issue")
	comment := f.e.Open(ctx, Edit(f.project, f.comments()[0], "Discussed issue"))
	if body.Err != nil || comment.Err != nil || body.Kind != drafts.ClaimEdit || comment.Kind != drafts.ClaimEdit {
		t.Fatalf("open both: %+v %+v", body, comment)
	}
	if r := f.e.Discard(ctx, comment.Draft); r.Err != nil || r.Released || !f.ours() {
		t.Fatalf("discarding the comment draft released the body's claim: %+v", r)
	}
	if r := f.e.Discard(ctx, body.Draft); r.Err != nil || !r.Released || f.claim() != nil {
		t.Fatalf("discarding the last draft: %+v", r)
	}
}

// An apply of a new comment whose outcome is uncertain keeps the draft,
// names the retained request and replays nothing; a later preview reads
// whether the comment exists under the draft's ID before offering to apply.
func TestAnUncertainNewCommentKeepsTheDraft(t *testing.T) {
	f := start(t)
	s := f.e.Open(ctx, New(f.project, f.issue, "Discussed issue"))
	if s.Err != nil {
		t.Fatal(s.Err)
	}
	write(t, s.Draft, "uncertain reply\n")
	p := f.e.Preview(ctx, s.Draft)
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
	if litclient.Code(a.Err) != "outcome_uncertain" || a.Uncertain == nil || a.Uncertain.ID == "" {
		t.Fatalf("apply: %+v", a)
	}
	if f.srv.Count("POST /v1/transactions") != commits+1 {
		t.Fatal("the commit was retried")
	}
	if _, ok, _ := f.e.ExistingFor(New(f.project, f.issue, "")); !ok {
		t.Fatal("the draft was dropped")
	}
	if again := f.e.Preview(ctx, s.Draft); again.Created || !again.Ready() || len(f.comments()) != 1 {
		t.Fatalf("a later preview, the comment never created: %+v", again)
	}
}
