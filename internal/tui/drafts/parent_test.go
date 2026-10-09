package drafts

import (
	"os"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// A new issue's draft records the parent it is created under (none for a
// top-level issue): the parent survives in the sidecar, a top-level draft
// writes no parent field, and only issue_new drafts may carry a parent.
func TestANewIssueDraftRecordsItsParent(t *testing.T) {
	s := New(t.TempDir())
	child, err := s.Create(Draft{Kind: IssueNew, ServiceID: service, ProjectID: project, RecordID: NewID(), ParentID: issueA, Title: "A child"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok, err := s.Find(project, IssueNew, child.RecordID); err != nil || !ok || got.ParentID != issueA || got.IssueID != "" {
		t.Fatalf("find the child draft: %+v %v %v", got, ok, err)
	}
	top, err := s.Create(Draft{Kind: IssueNew, ServiceID: service, ProjectID: project, RecordID: NewID(), Title: "Top level"}, "")
	if err != nil {
		t.Fatal(err)
	}
	side, err := os.ReadFile(s.sidecar(project, IssueNew, top.RecordID))
	if err != nil || strings.Contains(string(side), "parent_id") {
		t.Fatalf("a top-level draft's sidecar names a parent: %s %v", side, err)
	}
	for _, bad := range []Draft{
		{Kind: IssueNew, ServiceID: service, ProjectID: project, RecordID: NewID(), ParentID: "not-a-uuid"},
		{Kind: IssueBody, ServiceID: service, ProjectID: project, RecordID: issueB, IssueID: issueB, ParentID: issueA},
		{Kind: CommentNew, ServiceID: service, ProjectID: project, RecordID: NewID(), IssueID: issueB, ParentID: issueA},
	} {
		if _, err := s.Create(bad, "x"); litclient.Code(err) != "invalid_arguments" {
			t.Errorf("%s with parent %q: %v", bad.Kind, bad.ParentID, err)
		}
	}
	if !protocol.ValidUUID(child.RecordID) {
		t.Fatal(child.RecordID)
	}
}
