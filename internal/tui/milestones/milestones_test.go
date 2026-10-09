package milestones

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

const project = "test/milestones"

var ctx = context.Background()

// fixture is a connected lit-tui lifecycle with an editor that edits
// milestone descriptions, and an agent client, over a project with one
// issue and milestone M1 (description "goal", the issue a member).
type fixture struct {
	t         *testing.T
	srv       *tuitest.Server
	l         *lifecycle.Lifecycle
	h         *claims.Holder
	e         *edit.Editor
	agent     *litclient.Session
	projectID string
	issue     string
}

func start(t *testing.T) *fixture {
	t.Helper()
	srv := tuitest.Service(t)
	_, agent := tuitest.InstanceAs(t, t.TempDir(), srv.URL, protocol.Actor{Name: "Claude", Kind: "agent"})
	tuitest.Apply(t, agent, litclient.CreateProject(project, "spec"))
	ids := tuitest.CreateIssues(t, agent, project, "", "Member issue")
	tuitest.Apply(t, agent, litclient.CreateMilestone(project, "M1", "goal\n", ids[0]))
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
	got, err := litclient.Get[protocol.Issue](ctx, agent, project, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, srv: srv, l: l, h: h, e: e, agent: agent, projectID: got[0].ProjectID, issue: ids[0]}
}

func (f *fixture) milestone(title string) protocol.Milestone {
	f.t.Helper()
	got, err := litclient.Get[protocol.Milestone](ctx, f.agent, project, title)
	if err != nil || len(got) != 1 {
		f.t.Fatalf("milestone %q: %v %v", title, got, err)
	}
	return got[0]
}

func write(t *testing.T, d drafts.Draft, content string) {
	t.Helper()
	if err := os.WriteFile(d.Path(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A milestone description is edited in a TUI draft holding exactly the
// description. Milestones have no claims: the draft takes none, the apply
// is optimistic and fenced at the recorded revision, a revision change
// keeps the draft (diff and reload), and the apply changes only the
// description, never the membership.
func TestADescriptionDraftIsOptimisticAndRevisionFenced(t *testing.T) {
	f := start(t)
	m := f.milestone("M1")
	s := f.e.Open(ctx, Description(f.projectID, m))
	if s.Err != nil || s.Kind != drafts.ClaimNone || s.Draft.Kind != drafts.MilestoneDescription || s.Draft.IssueID != "" ||
		s.Draft.RecordID != m.ID || s.Draft.Revision != m.Revision || s.Draft.Base != "goal\n" || s.Draft.Title != "M1" {
		t.Fatalf("open: %+v", s)
	}
	if body, err := os.ReadFile(s.Draft.Path()); err != nil || string(body) != "goal\n" {
		t.Fatalf("the draft file holds %q (%v), not exactly the description", body, err)
	}
	if len(f.h.Snapshot().Holds) != 0 {
		t.Fatalf("a milestone draft took a claim: %+v", f.h.Snapshot())
	}
	if again := f.e.Open(ctx, Description(f.projectID, m)); litclient.Code(again.Err) != "draft_exists" {
		t.Fatalf("a second draft: %+v", again)
	}

	write(t, s.Draft, "the new goal\n")
	p := f.e.Preview(ctx, s.Draft)
	if !p.Ready() || !p.Exempt || p.Record.Revision != m.Revision {
		t.Fatalf("preview: %+v", p)
	}
	tuitest.Apply(t, f.agent, litclient.UpdateMilestone(project, m.ID, 0, litclient.MilestoneChange{Body: ptr("the agent's goal\n")}))
	commits := f.srv.Count("POST /v1/transactions")
	if a := f.e.Apply(ctx, p); litclient.Code(a.Err) != "revision_conflict" {
		t.Fatalf("applied over a newer revision: %+v", a)
	}
	if f.milestone("M1").Body != "the agent's goal\n" {
		t.Fatal("the agent's description was overwritten")
	}
	if _, ok, _ := f.e.ExistingFor(edit.TargetOf(s.Draft)); !ok {
		t.Fatal("the conflict lost the draft")
	}
	c := f.e.Preview(ctx, s.Draft)
	if !c.Conflict || c.Ready() || c.Record.Revision != m.Revision+1 {
		t.Fatalf("conflict: %+v", c)
	}
	if cmp := f.e.DiffCurrent(ctx, s.Draft); cmp.Err != nil || !slices.Contains(cmp.Diff, "-the agent's goal") || !slices.Contains(cmp.Diff, "+the new goal") {
		t.Fatalf("diff against the current description: %+v", cmp)
	}
	d, err := f.e.Reload(ctx, s.Draft)
	if err != nil || d.Base != "the agent's goal\n" || d.Revision != m.Revision+1 {
		t.Fatalf("reload: %+v %v", d, err)
	}
	if a := f.e.Apply(ctx, f.e.Preview(ctx, d)); a.Err != nil || a.Released {
		t.Fatalf("apply: %+v", a)
	}
	got := f.milestone("M1")
	if got.Body != "the new goal\n" || got.Revision != m.Revision+2 || !slices.Equal(got.IssueIDs, []string{f.issue}) || got.Title != "M1" {
		t.Fatalf("milestone after apply: %+v", got)
	}
	if f.srv.Count("POST /v1/transactions")-commits < 1 {
		t.Fatal("nothing was committed")
	}
	if _, ok, _ := f.e.ExistingFor(edit.TargetOf(d)); ok {
		t.Fatal("the applied draft was kept")
	}
}

// Load reads every milestone of the project with its progress, following
// the pages.
func TestLoadReadsEveryPageWithProgress(t *testing.T) {
	f := start(t)
	tuitest.Apply(t, f.agent, litclient.CreateMilestone(project, "Empty", ""))
	tuitest.Apply(t, f.agent, litclient.CreateMilestone(project, "Third", ""))
	defer func(n int) { page = n }(page)
	page = 2
	got := Load(ctx, f.l, f.projectID)
	if got.Err != nil || len(got.Milestones) != 3 || got.Pages != 2 {
		t.Fatalf("load: %+v", got)
	}
	var titles []string
	for _, m := range got.Milestones {
		titles = append(titles, m.Title)
		if m.Title == "M1" && (m.Progress.Total != 1 || m.Progress.Open != 1) {
			t.Fatalf("M1 progress: %+v", m.Progress)
		}
	}
	if !slices.Equal(titles, []string{"M1", "Empty", "Third"}) {
		t.Fatalf("titles %q", titles)
	}
}

func ptr(s string) *string { return &s }
