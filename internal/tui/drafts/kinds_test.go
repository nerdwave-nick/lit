package drafts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

const service = "a29ca783-fb66-42a1-befe-9149289cc659"

// Every kind declares whether its record exists yet, whether an issue owns
// it, and so whether an edit claims that issue. A kind no row declares keeps
// the original rule: an owning issue is required.
func TestKindsDeclareTheirOwnerTheirRecordAndTheirClaim(t *testing.T) {
	for _, c := range []struct {
		kind                 Kind
		fresh, owned, claims bool
	}{
		{IssueBody, false, true, true},
		{CommentBody, false, true, true},
		{CommentNew, true, true, false},
		{IssueNew, true, false, false},
		{MilestoneDescription, false, false, false},
		{ProjectSpec, false, false, false},
		{"custom_kind", false, true, true},
	} {
		if c.kind.New() != c.fresh || c.kind.Owned() != c.owned || c.kind.Claims() != c.claims {
			t.Errorf("%s: new %v owned %v claims %v, want %v %v %v", c.kind, c.kind.New(), c.kind.Owned(), c.kind.Claims(), c.fresh, c.owned, c.claims)
		}
	}
	for _, k := range []Kind{CommentNew, CommentBody, IssueNew, MilestoneDescription, ProjectSpec} {
		if string(k) != map[Kind]string{CommentNew: "comment_new", CommentBody: "comment_body", IssueNew: "issue_new",
			MilestoneDescription: "milestone_description", ProjectSpec: "project_spec"}[k] {
			t.Errorf("kind constant %q", k)
		}
	}
}

// A record that does not exist yet (a new comment) gets a generated draft
// ID as its RecordID: the draft is stored, found by that ID and by its
// owning issue, and named after the kind, the title and the ID.
func TestADraftOfANewRecordUsesAGeneratedID(t *testing.T) {
	s := New(t.TempDir())
	id, other := NewID(), NewID()
	if !protocol.ValidUUID(id) || id == other {
		t.Fatalf("draft IDs %q %q", id, other)
	}
	d, err := s.Create(Draft{Kind: CommentNew, ServiceID: service, ProjectID: project, RecordID: id, IssueID: issueA, Title: "Alpha"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if d.File != "comment-alpha--"+id[:8]+".md" || d.Key() != "comment_new-"+id {
		t.Fatalf("file %q key %q", d.File, d.Key())
	}
	if got, ok, err := s.Find(project, CommentNew, id); err != nil || !ok || got.IssueID != issueA || got.Revision != 0 {
		t.Fatalf("find by draft ID: %+v %v %v", got, ok, err)
	}
	if got, ok, err := s.FindOwned(project, CommentNew, issueA); err != nil || !ok || got.RecordID != id {
		t.Fatalf("find by owning issue: %+v %v %v", got, ok, err)
	}
	for _, miss := range [][2]string{{string(CommentNew), issueB}, {string(CommentBody), issueA}} {
		if _, ok, err := s.FindOwned(project, Kind(miss[0]), miss[1]); err != nil || ok {
			t.Errorf("FindOwned(%s, %s) found a draft: %v", miss[0], miss[1], err)
		}
	}
	if _, err := s.Create(Draft{Kind: CommentNew, ServiceID: service, ProjectID: project, RecordID: "not-a-uuid", IssueID: issueA}, ""); litclient.Code(err) != "invalid_arguments" {
		t.Errorf("a draft ID that is not a UUID: %v", err)
	}
}

// Kinds without an owning issue (project specifications, milestone
// descriptions, new issues) are stored with an empty IssueID; kinds with
// one still require it, and an unowned kind refuses one.
func TestADraftWithoutAnOwnerNeedsNoIssue(t *testing.T) {
	s := New(t.TempDir())
	spec, err := s.Create(Draft{Kind: ProjectSpec, ServiceID: service, ProjectID: project, RecordID: project, Title: "test/x", Revision: 4, Base: "old"}, "new spec")
	if err != nil {
		t.Fatal(err)
	}
	ms := protocol.UUID()
	if _, err := s.Create(Draft{Kind: MilestoneDescription, ServiceID: service, ProjectID: project, RecordID: ms, Title: "TUI 3", Revision: 1}, "goal"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(Draft{Kind: IssueNew, ServiceID: service, ProjectID: project, RecordID: NewID(), Title: "New"}, ""); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Draft{
		{Kind: CommentBody, ServiceID: service, ProjectID: project, RecordID: protocol.UUID()},
		{Kind: CommentNew, ServiceID: service, ProjectID: project, RecordID: NewID()},
		{Kind: IssueBody, ServiceID: service, ProjectID: project, RecordID: issueB},
		{Kind: ProjectSpec, ServiceID: service, ProjectID: project, RecordID: project, IssueID: issueA},
	} {
		if _, err := s.Create(bad, "x"); litclient.Code(err) != "invalid_arguments" {
			t.Errorf("%s with issue %q: %v", bad.Kind, bad.IssueID, err)
		}
	}
	got, err := s.List(project)
	if err != nil || len(got) != 3 || got[0].Kind != ProjectSpec || got[0].IssueID != "" {
		t.Fatalf("listed %+v %v", got, err)
	}
	if found, ok, err := s.Find(project, ProjectSpec, project); err != nil || !ok || found.Revision != 4 {
		t.Fatalf("find %+v %v %v", found, ok, err)
	}
	spec.Revision, spec.Base = 5, "newer"
	if _, err := s.Save(spec); err != nil {
		t.Fatal(err)
	}
	if err := s.Discard(spec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(spec.Dir(), ".lit", "project_spec-"+project+".json")); !os.IsNotExist(err) {
		t.Fatalf("sidecar kept: %v", err)
	}
	all, err := s.ListAll()
	if err != nil || len(all) != 2 || !strings.HasPrefix(all[0].File, "tui-3--") {
		t.Fatalf("all %+v %v", all, err)
	}
}
