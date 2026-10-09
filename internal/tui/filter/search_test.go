package filter

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// gate serializes a session like the lifecycle's Do, counts the calls and
// can refuse them.
type gate struct {
	mu     sync.Mutex
	s      *litclient.Session
	calls  int
	refuse bool
}

func (g *gate) Do(ctx context.Context, fn func(context.Context, *litclient.Session) error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if g.refuse {
		return protocol.E(503, "shutting_down", "refused")
	}
	return fn(ctx, g.s)
}

// The search finds issues by title, body and comment, ignoring case, only in
// the project; milestones and the project description are not issues.
func TestSearchFindsIssuesByTitleBodyAndComment(t *testing.T) {
	srv := tuitest.Service(t)
	_, s := tuitest.Instance(t, t.TempDir(), srv.URL)
	tuitest.Apply(t, s, litclient.CreateProject("test/search", "The RENEWAL spec"))
	tuitest.Apply(t, s, litclient.CreateProject("test/other", "spec"))
	byTitle := tuitest.CreateIssues(t, s, "test/search", "", "Renewal cadence")[0]
	tuitest.Apply(t, s, litclient.CreateIssue("test/search", "Body only", "the renewal of a lease", ""))
	commented := tuitest.CreateIssues(t, s, "test/search", "", "Commented", "Unrelated")[0]
	tuitest.Apply(t, s, litclient.CreateComment("test/search", commented, "see RENEWAL"))
	tuitest.Apply(t, s, litclient.CreateMilestone("test/search", "Renewal milestone", "body"))
	tuitest.CreateIssues(t, s, "test/other", "", "Renewal elsewhere")
	issues, err := litclient.Get[protocol.Issue](context.Background(), s, "test/search", "title:Body only")
	if err != nil {
		t.Fatal(err)
	}
	g := &gate{s: s}
	f := Search(context.Background(), g, issues[0].ProjectID, "reNEWal")
	if f.Err != nil || f.Query != "reNEWal" || f.Truncated {
		t.Fatalf("search: %+v", f)
	}
	want := map[string]bool{byTitle: true, issues[0].ID: true, commented: true}
	if len(f.Issues) != len(want) {
		t.Fatalf("found %v, want %v", f.Issues, want)
	}
	for id := range want {
		if !f.Issues[id] {
			t.Fatalf("found %v, want %v", f.Issues, want)
		}
	}
	if f.Results != 5 || g.calls != 1 {
		t.Fatalf("%d results in %d calls, want 5 (with the description and the milestone) in 1", f.Results, g.calls)
	}
	if f := Search(context.Background(), g, issues[0].ProjectID, "nowhere"); f.Err != nil || len(f.Issues) != 0 || f.Issues == nil {
		t.Fatalf("no match: %+v", f)
	}
}

// Results arrive in pages no larger than the service accepts, one call
// through the gate each; the search stops at its result limit and says so;
// refusals are returned.
func TestSearchPagesStopsAtItsLimitAndReportsErrors(t *testing.T) {
	limits := protocol.DefaultLimits()
	limits.ExpandedRecords = 4 // the service refuses pages of more than 4 results
	srv := tuitest.ServiceWithLimits(t, limits)
	_, s := tuitest.Instance(t, t.TempDir(), srv.URL)
	tuitest.Apply(t, s, litclient.CreateProject("test/many", "spec"))
	for i := 0; i < 9; i++ {
		tuitest.CreateIssues(t, s, "test/many", "", fmt.Sprintf("Match %d", i))
	}
	tuitest.CreateIssues(t, s, "test/many", "", "Other")
	ps, err := litclient.List[protocol.Project](context.Background(), s, litclient.Query{Title: "test/many"})
	if err != nil || len(ps.Items) != 1 {
		t.Fatal(ps, err)
	}
	project := ps.Items[0].ID
	g := &gate{s: s}
	f := Search(context.Background(), g, project, "match")
	if f.Err != nil || len(f.Issues) != 9 || f.Results != 9 || f.Truncated {
		t.Fatalf("paged search: %+v", f)
	}
	if g.calls != 3 {
		t.Fatalf("%d calls for 9 results in pages of 4, want 3", g.calls)
	}

	g.calls = 0
	f = search(context.Background(), g, project, "match", 5)
	if !f.Truncated || f.Results < 5 || f.Err != nil {
		t.Fatalf("limited search: %+v", f)
	}

	g.refuse = true
	if f := Search(context.Background(), g, project, "match"); litclient.Code(f.Err) != "shutting_down" {
		t.Fatalf("refused: %+v", f)
	}
}
