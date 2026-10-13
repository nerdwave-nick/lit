package browse_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/browse"
	"github.com/nerdwave-nick/lit/internal/tui/tuitest"
)

// gate serializes a session like the lifecycle does, and can refuse the nth
// call and later ones.
type gate struct {
	mu     sync.Mutex
	s      *litclient.Session
	calls  int
	refuse int // refuse calls from this one on (1-based); 0 never
}

func (g *gate) Do(ctx context.Context, fn func(context.Context, *litclient.Session) error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if g.refuse > 0 && g.calls >= g.refuse {
		return protocol.E(503, "shutting_down", "refused")
	}
	return fn(ctx, g.s)
}

// project creates a project with n issues: a root, then children and
// grandchildren under it, in batches of batch.
func project(t *testing.T, s *litclient.Session, title string, n int) []string {
	return projectIn(t, s, title, n, 10)
}

func projectIn(t *testing.T, s *litclient.Session, title string, n, batch int) []string {
	t.Helper()
	tuitest.Apply(t, s, litclient.CreateProject(title, "spec"))
	var ids []string
	parent := ""
	for len(ids) < n {
		var titles []string
		for i := 0; i < batch && len(ids)+len(titles) < n; i++ {
			titles = append(titles, fmt.Sprintf("Issue %03d", len(ids)+len(titles)))
		}
		ids = append(ids, tuitest.CreateIssues(t, s, title, parent, titles...)...)
		parent = ids[len(ids)-1]
	}
	return ids
}

func ids(items []protocol.IssueSummary) map[string]bool {
	out := map[string]bool{}
	for _, it := range items {
		out[it.ID] = true
	}
	return out
}

// A project that fits is one consistent bodiless read, with claim owners
// named by actor.
func TestLoadIssuesReadsOneConsistentSnapshot(t *testing.T) {
	srv := tuitest.Service(t)
	_, s := tuitest.Instance(t, t.TempDir(), srv.URL)
	want := project(t, s, "test/fits", 25)
	project(t, s, "test/other", 3)
	if _, err := s.AcquireClaims(context.Background(), litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Issues: []string{want[3]}}}); err != nil {
		t.Fatal(err)
	}
	g := &gate{s: s}
	got := browse.LoadIssues(context.Background(), g, "test/fits")
	if got.Err != nil || !got.Consistent || len(got.Items) != 25 || g.calls != 1 {
		t.Fatalf("load: err %v consistent %v items %d calls %d", got.Err, got.Consistent, len(got.Items), g.calls)
	}
	have := ids(got.Items)
	for _, id := range want {
		if !have[id] {
			t.Fatalf("missing %s", id)
		}
	}
	for _, it := range got.Items {
		if it.ID == want[3] && (!it.Claimed || it.Claim == nil || it.Claim.OwnerActor == nil || it.Claim.OwnerActor.Name != "tester") {
			t.Fatalf("claim owner not named: %+v", it.Claim)
		}
	}
}

// When the one bounded read hits a limit, the load falls back to pages and
// says so: it is not consistent. Pages too large for the response limit are
// retried smaller.
func TestLoadIssuesFallsBackToPagesOnLimits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits func(*protocol.Limits)
	}{
		{"small pages", func(l *protocol.Limits) { l.SnapshotBytes, l.MaxPage, l.DefaultPage = 8000, 8, 8 }},
		{"response bytes", func(l *protocol.Limits) { l.SnapshotBytes = 8000 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limits := protocol.DefaultLimits()
			tc.limits(&limits)
			srv := tuitest.ServiceWithLimits(t, limits)
			_, s := tuitest.Instance(t, t.TempDir(), srv.URL)
			want := projectIn(t, s, "test/paged", 30, 2)
			g := &gate{s: s}
			got := browse.LoadIssues(context.Background(), g, "test/paged")
			if got.Err != nil || got.Consistent || len(got.Items) != 30 || len(ids(got.Items)) != 30 || g.calls < 3 {
				t.Fatalf("load: err %v consistent %v items %d calls %d", got.Err, got.Consistent, len(got.Items), g.calls)
			}
			for _, id := range want {
				if !ids(got.Items)[id] {
					t.Fatalf("missing %s", id)
				}
			}
		})
	}
}

// A page that fails keeps what was loaded, marked partial, with the error.
func TestLoadIssuesKeepsPagesLoadedBeforeAnError(t *testing.T) {
	limits := protocol.DefaultLimits()
	limits.SnapshotBytes, limits.MaxPage, limits.DefaultPage = 8000, 8, 8
	srv := tuitest.ServiceWithLimits(t, limits)
	_, s := tuitest.Instance(t, t.TempDir(), srv.URL)
	projectIn(t, s, "test/broken", 30, 2)
	g := &gate{s: s, refuse: 3} // the bounded read, page 1, then refused
	got := browse.LoadIssues(context.Background(), g, "test/broken")
	if litclient.Code(got.Err) != "shutting_down" || got.Consistent || len(got.Items) != 8 {
		t.Fatalf("load: err %v consistent %v items %d", got.Err, got.Consistent, len(got.Items))
	}
}

// Any other error from the bounded read is reported without paging.
func TestLoadIssuesReportsErrors(t *testing.T) {
	srv := tuitest.Service(t)
	_, s := tuitest.Instance(t, t.TempDir(), srv.URL)
	g := &gate{s: s}
	got := browse.LoadIssues(context.Background(), g, "test/missing")
	if litclient.Code(got.Err) != "not_found" || len(got.Items) != 0 || g.calls != 1 {
		t.Fatalf("load: err %v items %d calls %d", got.Err, len(got.Items), g.calls)
	}
}

// Projects come in pages sorted by title; a cursor continues the list.
func TestLoadProjectsPages(t *testing.T) {
	srv := tuitest.Service(t)
	_, s := tuitest.Instance(t, t.TempDir(), srv.URL)
	for _, title := range []string{"test/c", "test/a", "test/e", "test/b", "test/d"} {
		tuitest.Apply(t, s, litclient.CreateProject(title, "spec"))
	}
	g := &gate{s: s}
	var titles []string
	cursor := ""
	for pages := 0; ; pages++ {
		page, err := browse.LoadProjects(context.Background(), g, cursor, 2)
		if err != nil || pages > 5 {
			t.Fatalf("page %d: %v", pages, err)
		}
		for _, p := range page.Items {
			titles = append(titles, p.Title)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if fmt.Sprint(titles) != "[test/a test/b test/c test/d test/e]" {
		t.Fatalf("titles %v", titles)
	}
}
