package quick_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/lifecycle"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/quick"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

const project = "test/quick"

// fixture is a temporary service with an agent client that seeds issues,
// and a connected lit-tui lifecycle whose gate the quick actions use.
type fixture struct {
	t     *testing.T
	srv   *tuitest.Server
	agent *litclient.Session
	l     *lifecycle.Lifecycle
	state string
	ids   map[string]string // title -> UUID
}

func newFixture(t *testing.T, titles ...string) *fixture {
	t.Helper()
	srv := tuitest.Service(t)
	_, agent := tuitest.InstanceAs(t, t.TempDir(), srv.URL, protocol.Actor{Name: "Claude", Kind: "agent"})
	tuitest.Apply(t, agent, litclient.CreateProject(project, "spec"))
	f := &fixture{t: t, srv: srv, agent: agent, state: t.TempDir(), ids: map[string]string{}}
	for n, id := range tuitest.CreateIssues(t, agent, project, "", titles...) {
		f.ids[titles[n]] = id
	}
	l, err := lifecycle.New(lifecycle.Config{StateDir: f.state, Endpoint: srv.URL, HTTP: &http.Client{}, Actor: protocol.Actor{Name: "person", Kind: "human"}})
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
	f.l = l
	return f
}

// summary reads the current summary of the issue titled title.
func (f *fixture) summary(title string) protocol.IssueSummary {
	f.t.Helper()
	page, err := f.agent.IssueSummaries(context.Background(), map[string]any{"project_id": f.project(), "all": true})
	if err != nil {
		f.t.Fatal(err)
	}
	for _, i := range page.Items {
		if i.Title == title {
			return i
		}
	}
	f.t.Fatalf("no issue %q", title)
	return protocol.IssueSummary{}
}

func (f *fixture) project() string {
	f.t.Helper()
	ps, err := litclient.Get[protocol.Project](context.Background(), f.agent, "", project)
	if err != nil || len(ps) != 1 {
		f.t.Fatalf("project: %v %v", ps, err)
	}
	return ps[0].ID
}

// lookup finds the seeded issues by UUID, as the loaded tree does; the
// summaries are read now.
func (f *fixture) lookup(id string) (protocol.IssueSummary, bool) {
	for _, v := range f.ids {
		if v == id {
			return f.summaryByID(id), true
		}
	}
	return protocol.IssueSummary{}, false
}

func (f *fixture) agentClaims(titles ...string) {
	f.t.Helper()
	var ids []string
	for _, title := range titles {
		ids = append(ids, f.ids[title])
	}
	if _, err := f.agent.AcquireClaims(context.Background(), litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: ids}}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) prepare(c quick.Change) quick.Preview {
	f.t.Helper()
	return quick.Prepare(context.Background(), f.l, c, f.lookup)
}

// pending lists the evidence files in the lit-tui state directory.
func (f *fixture) pending() []string {
	entries, _ := os.ReadDir(filepath.Join(f.state, "pending"))
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func affected(p quick.Preview, id string) (quick.Affected, bool) {
	for _, a := range p.Affected {
		if a.ID == id {
			return a, true
		}
	}
	return quick.Affected{}, false
}

// A link previews both of its ends: the other issue is listed with its title
// and fenced revision before anything is committed, and the commit applies
// exactly that preview.
func TestALinkListsTheOtherIssueItAffectsAndCommitsThePreview(t *testing.T) {
	f := newFixture(t, "Work", "Blocker")
	work, blocker := f.summary("Work"), f.summary("Blocker")
	p := f.prepare(quick.Link(project, work, quick.BlockedBy, blocker.ID, "Blocker"))
	if p.Refused() {
		t.Fatalf("preview refused: %v", p.Err)
	}
	self, ok := affected(p, work.ID)
	if !ok || !self.Self || !self.Changed || self.Revision != work.Revision || self.Title != "Work" {
		t.Fatalf("the issue itself: %+v %v", self, ok)
	}
	other, ok := affected(p, blocker.ID)
	if !ok || other.Self || !other.Changed || other.Revision != blocker.Revision || other.Title != "Blocker" || other.Claim != nil {
		t.Fatalf("the other issue: %+v %v", other, ok)
	}
	commits := f.srv.Count("POST /v1/transactions")
	out := quick.Commit(context.Background(), f.l, p)
	if out.Err != nil || out.Result.Outcome != "applied" {
		t.Fatalf("commit: %+v", out)
	}
	if got := f.srv.Count("POST /v1/transactions") - commits; got != 1 {
		t.Fatalf("%d commits sent, want 1", got)
	}
	if w, b := f.summary("Work"), f.summary("Blocker"); !slices.Contains(w.BlockedBy, blocker.ID) || !slices.Contains(b.Blocks, work.ID) {
		t.Fatalf("link not applied: %+v %+v", w, b)
	}
}

// User story 7: another client's claim on an affected issue refuses the
// action with claim_conflict before anything is written, naming the owner.
func TestAForeignClaimOnAnAffectedIssueIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	f := newFixture(t, "Work", "Blocker")
	f.agentClaims("Blocker")
	work, blocker := f.summary("Work"), f.summary("Blocker")
	p := f.prepare(quick.Link(project, work, quick.BlockedBy, blocker.ID, "Blocker"))
	if litclient.Code(p.Err) != "claim_conflict" || !p.Refused() {
		t.Fatalf("preview: %v", p.Err)
	}
	other, ok := affected(p, blocker.ID)
	if !ok || !other.Foreign() || other.Title != "Blocker" || other.Claim.OwnerActor == nil || other.Claim.OwnerActor.Name != "Claude" || other.Claim.OwnerActor.Kind != "agent" {
		t.Fatalf("the owner is not named: %+v", other)
	}
	if self, ok := affected(p, work.ID); !ok || !self.Self || self.Claim != nil {
		t.Fatalf("the issue itself: %+v", self)
	}
	commits := f.srv.Count("POST /v1/transactions")
	if out := quick.Commit(context.Background(), f.l, p); litclient.Code(out.Err) != "claim_conflict" {
		t.Fatalf("a refused preview was committed: %+v", out)
	}
	if f.srv.Count("POST /v1/transactions") != commits || len(f.pending()) != 0 {
		t.Fatalf("something was sent or journalled: %d commits, pending %v", f.srv.Count("POST /v1/transactions")-commits, f.pending())
	}
	if w, b := f.summary("Work"), f.summary("Blocker"); w.Revision != work.Revision || b.Revision != blocker.Revision {
		t.Fatal("an affected issue changed")
	}
}

// Claims this client holds are passed on: the commit carries their tokens
// and is applied.
func TestOwnClaimsArePassedOnAutomatically(t *testing.T) {
	f := newFixture(t, "Mine")
	mine := f.summary("Mine")
	var token string
	err := f.l.Do(context.Background(), func(ctx context.Context, s *litclient.Session) error {
		r, err := s.AcquireClaims(ctx, litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{mine.ID}}})
		if err == nil {
			token = r.Items[0].Claim.Token
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var sent protocol.DurableRequest
	f.srv.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/v1/transactions" {
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			_ = json.Unmarshal(body, &sent)
			mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		return false
	})
	p := f.prepare(quick.Title(project, mine, "Mine, renamed"))
	if self, _ := affected(p, mine.ID); p.Refused() || self.Claim == nil || !self.Mine {
		t.Fatalf("preview: %v %+v", p.Err, self)
	}
	if out := quick.Commit(context.Background(), f.l, p); out.Err != nil {
		t.Fatalf("commit: %v", out.Err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent.Claims) != 1 || sent.Claims[0].IssueID != mine.ID || sent.Claims[0].Token != token {
		t.Fatalf("claim tokens sent: %+v", sent.Claims)
	}
	if f.summary("Mine, renamed").ID != mine.ID {
		t.Fatal("not renamed")
	}
}

// A claim taken after the preview refuses the commit; the outcome names the
// owner it reads afterwards.
func TestAClaimTakenAfterThePreviewIsRefusedAndNamed(t *testing.T) {
	f := newFixture(t, "Work", "Blocker")
	work, blocker := f.summary("Work"), f.summary("Blocker")
	p := f.prepare(quick.Link(project, work, quick.BlockedBy, blocker.ID, "Blocker"))
	if p.Refused() {
		t.Fatal(p.Err)
	}
	f.agentClaims("Blocker")
	out := quick.Commit(context.Background(), f.l, p)
	if litclient.Code(out.Err) != "claim_conflict" || len(out.Owners) != 1 || out.Owners[0].ID != blocker.ID ||
		out.Owners[0].Title != "Blocker" || out.Owners[0].Claim.OwnerActor == nil || out.Owners[0].Claim.OwnerActor.Name != "Claude" {
		t.Fatalf("outcome: %+v", out)
	}
	if f.summary("Work").Revision != work.Revision {
		t.Fatal("written despite the conflict")
	}
}

// A change to an affected issue after the preview refuses the commit with
// revision_conflict; nothing is prepared again.
func TestAChangeAfterThePreviewIsRefusedWithRevisionConflict(t *testing.T) {
	f := newFixture(t, "Work", "Blocker")
	work, blocker := f.summary("Work"), f.summary("Blocker")
	p := f.prepare(quick.Link(project, work, quick.Related, blocker.ID, "Blocker"))
	if p.Refused() {
		t.Fatal(p.Err)
	}
	renamed := "Blocker, renamed"
	tuitest.Apply(t, f.agent, litclient.UpdateIssue(project, blocker.ID, 0, litclient.IssueChange{Title: &renamed}))
	previews := f.srv.Count("POST /v1/transaction-previews")
	if out := quick.Commit(context.Background(), f.l, p); litclient.Code(out.Err) != "revision_conflict" {
		t.Fatalf("outcome: %+v", out)
	}
	if f.srv.Count("POST /v1/transaction-previews") != previews {
		t.Fatal("prepared again")
	}
	if w := f.summary("Work"); len(w.Related) != 0 {
		t.Fatal("linked despite the conflict")
	}
}

// The preview is fenced at the revision the user saw: an issue changed since
// is refused at preview.
func TestAStaleSummaryIsRefusedAtPreview(t *testing.T) {
	f := newFixture(t, "Work")
	seen := f.summary("Work")
	body := "changed"
	tuitest.Apply(t, f.agent, litclient.UpdateIssue(project, seen.ID, 0, litclient.IssueChange{Body: &body}))
	if p := f.prepare(quick.State(project, seen, "closed")); litclient.Code(p.Err) != "revision_conflict" {
		t.Fatalf("preview: %v", p.Err)
	}
}

// Every action previews and applies its change. A parent change writes only
// the issue itself: the old and the new parent keep their revisions (their
// children are computed) and are read without changing, fenced as guards.
func TestEveryActionPreviewsAndAppliesItsChange(t *testing.T) {
	f := newFixture(t, "Work", "Old parent", "New parent", "Other")
	old := f.summary("Old parent")
	tuitest.Apply(t, f.agent, litclient.UpdateIssue(project, f.ids["Work"], 0, litclient.IssueChange{Parent: &old.ID}))
	other := f.ids["Other"]
	steps := []struct {
		name   string
		change func(protocol.IssueSummary) quick.Change
		check  func(protocol.IssueSummary) bool
		others []string // other affected issues
	}{
		{"title", func(i protocol.IssueSummary) quick.Change { return quick.Title(project, i, "Work 2") }, func(i protocol.IssueSummary) bool { return i.Title == "Work 2" }, nil},
		{"labels", func(i protocol.IssueSummary) quick.Change { return quick.Labels(project, i, []string{"a", "b"}, nil) },
			func(i protocol.IssueSummary) bool { return slices.Equal(i.Labels, []string{"a", "b"}) }, nil},
		{"labels again", func(i protocol.IssueSummary) quick.Change {
			return quick.Labels(project, i, []string{"c"}, []string{"a"})
		},
			func(i protocol.IssueSummary) bool { return slices.Equal(i.Labels, []string{"b", "c"}) }, nil},
		{"assignee", func(i protocol.IssueSummary) quick.Change { return quick.Assignee(project, i, "Ada") },
			func(i protocol.IssueSummary) bool { return i.Assignee != nil && *i.Assignee == "Ada" }, nil},
		{"unassign", func(i protocol.IssueSummary) quick.Change { return quick.Assignee(project, i, "") }, func(i protocol.IssueSummary) bool { return i.Assignee == nil }, nil},
		{"close", func(i protocol.IssueSummary) quick.Change { return quick.State(project, i, "closed") }, func(i protocol.IssueSummary) bool { return i.State == "closed" }, nil},
		{"reopen", func(i protocol.IssueSummary) quick.Change { return quick.State(project, i, "open") }, func(i protocol.IssueSummary) bool { return i.State == "open" }, nil},
		{"parent", func(i protocol.IssueSummary) quick.Change {
			return quick.Parent(project, i, f.ids["New parent"], "New parent")
		},
			func(i protocol.IssueSummary) bool { return i.ParentID != nil && *i.ParentID == f.ids["New parent"] }, nil},
		{"no parent", func(i protocol.IssueSummary) quick.Change { return quick.Parent(project, i, "", "") }, func(i protocol.IssueSummary) bool { return i.ParentID == nil }, nil},
		{"related", func(i protocol.IssueSummary) quick.Change {
			return quick.Link(project, i, quick.Related, other, "Other")
		},
			func(i protocol.IssueSummary) bool { return slices.Equal(i.Related, []string{other}) }, []string{"Other"}},
		{"blocks", func(i protocol.IssueSummary) quick.Change {
			return quick.Link(project, i, quick.Blocks, other, "Other")
		},
			func(i protocol.IssueSummary) bool { return slices.Equal(i.Blocks, []string{other}) }, []string{"Other"}},
		{"unlink", func(i protocol.IssueSummary) quick.Change {
			return quick.Unlink(project, i, quick.Related, other, "Other")
		},
			func(i protocol.IssueSummary) bool { return len(i.Related) == 0 && len(i.Blocks) == 1 }, []string{"Other"}},
	}
	for _, step := range steps {
		cur := f.summaryByID(f.ids["Work"])
		p := f.prepare(step.change(cur))
		if p.Refused() {
			t.Fatalf("%s: preview: %v", step.name, p.Err)
		}
		var others []string
		for _, a := range p.Affected {
			if a.Type == "issues" && a.Changed && !a.Self {
				others = append(others, a.Title)
			}
		}
		slices.Sort(others)
		want := slices.Sorted(slices.Values(step.others))
		if !slices.Equal(others, want) {
			t.Fatalf("%s: other affected issues %v, want %v", step.name, others, want)
		}
		if step.name == "parent" || step.name == "no parent" {
			read := map[string]bool{}
			for _, a := range p.Affected {
				read[a.Title] = read[a.Title] || a.Type == "issues" && !a.Changed
			}
			for _, parent := range []string{"Old parent", "New parent"} {
				if step.name == "no parent" && parent == "Old parent" {
					continue
				}
				if !read[parent] {
					t.Fatalf("%s: %s is not read as a guard: %+v", step.name, parent, p.Affected)
				}
			}
		}
		if out := quick.Commit(context.Background(), f.l, p); out.Err != nil {
			t.Fatalf("%s: commit: %v", step.name, out.Err)
		}
		if got := f.summaryByID(cur.ID); !step.check(got) {
			t.Fatalf("%s: not applied: %+v", step.name, got)
		}
	}
}

func (f *fixture) summaryByID(id string) protocol.IssueSummary {
	f.t.Helper()
	page, err := f.agent.IssueSummaries(context.Background(), map[string]any{"project_id": f.project(), "all": true})
	if err != nil {
		f.t.Fatal(err)
	}
	for _, i := range page.Items {
		if i.ID == id {
			return i
		}
	}
	f.t.Fatalf("no issue %s", id)
	return protocol.IssueSummary{}
}
