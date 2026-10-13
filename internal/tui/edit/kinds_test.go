package edit

import (
	"context"
	"testing"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/claims"
	"github.com/nerdwave-nick/lit/internal/tui/drafts"
)

// specKind edits a project's specification, as a later ticket registers it.
var specKind = Kind{
	Read: func(ctx context.Context, s *litclient.Session, d drafts.Draft) (Record, error) {
		got, err := litclient.Get[protocol.Project](ctx, s, "", d.RecordID)
		if err != nil {
			return Record{}, err
		}
		return Record{Exists: true, Title: got[0].Title, Revision: got[0].Revision, Content: got[0].Description}, nil
	},
	Request: func(d drafts.Draft, content string) protocol.PrepareRequest {
		return litclient.UpdateProject(d.RecordID, d.Revision, litclient.ProjectChange{Description: &content})
	},
}

// A registered kind without an owning issue is edited through the same
// draft flow: no claim is taken, the preview is optimistic and fenced at the
// recorded revision, a revision change keeps the draft, and reload rebases.
// A kind nobody registered is refused.
func TestARegisteredKindWithoutAnOwnerIsOptimisticAndRevisionFenced(t *testing.T) {
	f := start(t, claims.Config{})
	if s := f.e.Open(ctx, Target{Kind: drafts.ProjectSpec, Project: f.project, Record: f.project}); litclient.Code(s.Err) != "invalid_arguments" {
		t.Fatalf("an unregistered kind: %+v", s)
	}
	f.e.Register(drafts.ProjectSpec, specKind)
	s := f.e.Open(ctx, Target{Kind: drafts.ProjectSpec, Project: f.project, Record: f.project, Title: "spec"})
	if s.Err != nil || s.Kind != drafts.ClaimNone || s.Draft.IssueID != "" || s.Draft.Title != "test/edit" || s.Draft.Base != "spec" {
		t.Fatalf("open: %+v", s)
	}
	if len(f.h.Snapshot().Holds) != 0 {
		t.Fatalf("a project draft took a claim: %+v", f.h.Snapshot())
	}
	write(t, s.Draft, "the new spec")
	p := f.e.Preview(ctx, s.Draft)
	if !p.Ready() || !p.Exempt || p.Lost() || p.Record.Revision != s.Draft.Revision {
		t.Fatalf("preview: %+v", p)
	}
	update := litclient.UpdateProject(f.project, 0, litclient.ProjectChange{Description: ptr("an agent's spec")})
	applyAs(t, f.agent, update)
	if a := f.e.Apply(ctx, p); a.Err == nil {
		t.Fatalf("applied over a newer revision: %+v", a)
	}
	if p := f.e.Preview(ctx, s.Draft); !p.Conflict || p.Ready() {
		t.Fatalf("conflict: %+v", p)
	}
	d, err := f.e.Reload(ctx, s.Draft)
	if err != nil || d.Base != "an agent's spec" {
		t.Fatalf("reload: %+v %v", d, err)
	}
	if a := f.e.Apply(ctx, f.e.Preview(ctx, d)); a.Err != nil || a.Released {
		t.Fatalf("apply: %+v", a)
	}
	got, err := litclient.Get[protocol.Project](ctx, f.agent, "", f.project)
	if err != nil || got[0].Description != "the new spec" {
		t.Fatalf("project %+v %v", got, err)
	}
	if _, ok, _ := f.e.ExistingFor(TargetOf(d)); ok {
		t.Fatal("the applied draft was kept")
	}
}

func ptr(s string) *string { return &s }

func applyAs(t *testing.T, s *litclient.Session, req protocol.PrepareRequest) {
	t.Helper()
	p, err := s.Prepare(ctx, req)
	if err == nil {
		_, err = s.Commit(ctx, p)
	}
	if err != nil {
		t.Fatal(err)
	}
}
