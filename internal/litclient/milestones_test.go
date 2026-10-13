package litclient_test

import (
	"context"
	"slices"
	"testing"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// Milestones reads the project's milestones with the service's progress
// projection of their members: an empty milestone has no members (not
// "complete"), open members count blocked and claimed ones, and an open
// blocker outside the milestone is an external blocker. Pages follow the
// service's cursor.
func TestMilestonesReadsTheProgressOfTheirMembers(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	s := h.connect("ms", "person")
	project, issues := seed(t, s, "ms", "Open", "Closed", "Claimed", "Outside blocker")
	a, c, d, x := issues["Open"], issues["Closed"], issues["Claimed"], issues["Outside blocker"]
	apply(t, s, litclient.LinkIssues(project, a.ID, "blocked-by", x.ID))
	apply(t, s, litclient.CloseIssue(project, c.ID, 0))
	if _, err := s.AcquireClaims(ctx, litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{d.ID}}}); err != nil {
		t.Fatal(err)
	}
	apply(t, s, litclient.CreateMilestone(project, "M1", "goal", a.ID, c.ID, d.ID))
	apply(t, s, litclient.CreateMilestone(project, "Empty", ""))

	page, err := s.Milestones(ctx, litclient.Query{ProjectID: a.ProjectID, Limit: 1})
	if err != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatalf("first page: %+v %v", page, err)
	}
	next, err := s.Milestones(ctx, litclient.Query{ProjectID: a.ProjectID, Limit: 1, Cursor: *page.NextCursor})
	if err != nil || len(next.Items) != 1 || next.NextCursor != nil {
		t.Fatalf("second page: %+v %v", next, err)
	}
	byTitle := map[string]litclient.MilestoneView{}
	for _, m := range append(page.Items, next.Items...) {
		byTitle[m.Title] = m
	}
	m1, empty := byTitle["M1"], byTitle["Empty"]
	if m1.Body != "goal" || m1.Revision != 1 || !protocol.ValidUUID(m1.ID) || len(m1.IssueIDs) != 3 {
		t.Fatalf("M1 record: %+v", m1)
	}
	want := litclient.MilestoneProgress{Total: 3, Open: 2, Closed: 1, BlockedOpen: 1, ClaimedOpen: 1, ExternalBlockers: []string{x.ID}}
	if got := m1.Progress; got.Total != want.Total || got.Open != want.Open || got.Closed != want.Closed || got.BlockedOpen != want.BlockedOpen ||
		got.ClaimedOpen != want.ClaimedOpen || !slices.Equal(got.ExternalBlockers, want.ExternalBlockers) {
		t.Fatalf("M1 progress %+v, want %+v", got, want)
	}
	if got := empty.Progress; got.Total != 0 || got.Open != 0 || got.Closed != 0 || len(empty.IssueIDs) != 0 {
		t.Fatalf("empty milestone: %+v", empty)
	}
}
