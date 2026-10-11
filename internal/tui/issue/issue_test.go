package issue_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/issue"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// gate serializes a session like the lifecycle does, counts its calls and
// can refuse the nth call and later ones.
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

// discussed creates project with one issue and n comments on it, each with a
// body of about size bytes, and returns the issue's UUID and project UUID.
func discussed(t *testing.T, s *litclient.Session, project string, n, size int) (id, projectID string) {
	t.Helper()
	tuitest.Apply(t, s, litclient.CreateProject(project, "spec"))
	id = tuitest.CreateIssues(t, s, project, "", "Discussed")[0]
	for c := 0; c < n; c++ {
		tuitest.Apply(t, s, litclient.CreateComment(project, id, fmt.Sprintf("comment %02d %s", c, strings.Repeat("x", size))))
	}
	ps, err := litclient.Get[protocol.Project](context.Background(), s, "", project)
	if err != nil {
		t.Fatal(err)
	}
	return id, ps[0].ID
}

func bodies(cs []protocol.Comment) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Body[:len("comment 00")])
	}
	return out
}

func wantComments(n int) []string {
	var out []string
	for c := 0; c < n; c++ {
		out = append(out, fmt.Sprintf("comment %02d", c))
	}
	return out
}

// A small discussion is the issue read plus one consistent comments read,
// oldest comment first.
func TestLoadReadsTheIssueAndItsComments(t *testing.T) {
	srv := tuitest.Service(t)
	_, s := tuitest.InstanceAs(t, t.TempDir(), srv.URL, protocol.Actor{Name: "Claude", Kind: "agent"})
	id, project := discussed(t, s, "test/one", 3, 10)
	g := &gate{s: s}
	got := issue.Load(context.Background(), g, project, id)
	if got.Err != nil || got.Issue == nil || got.Issue.ID != id || got.Issue.Body != "body of Discussed" {
		t.Fatalf("issue: %+v err %v", got.Issue, got.Err)
	}
	if !got.Consistent || g.calls != 2 || fmt.Sprint(bodies(got.Comments)) != fmt.Sprint(wantComments(3)) {
		t.Fatalf("comments: consistent %v calls %d %v", got.Consistent, g.calls, bodies(got.Comments))
	}
	if got.Comments[0].Author != "Claude" {
		t.Fatalf("author: %q", got.Comments[0].Author)
	}
}

// Comments over a read limit are read page by page, following the cursors;
// pages too large for the response limit are retried smaller. Paged
// comments are not consistent.
func TestLoadFollowsCommentPages(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits func(*protocol.Limits)
	}{
		{"small pages", func(l *protocol.Limits) { l.SnapshotBytes, l.MaxPage, l.DefaultPage = 8000, 4, 4 }},
		{"response bytes", func(l *protocol.Limits) { l.SnapshotBytes = 8000 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limits := protocol.DefaultLimits()
			tc.limits(&limits)
			srv := tuitest.ServiceWithLimits(t, limits)
			_, s := tuitest.Instance(t, t.TempDir(), srv.URL)
			id, project := discussed(t, s, "test/long", 11, 1000)
			g := &gate{s: s}
			got := issue.Load(context.Background(), g, project, id)
			if got.Err != nil || got.Issue == nil || got.Consistent {
				t.Fatalf("load: err %v consistent %v", got.Err, got.Consistent)
			}
			if fmt.Sprint(bodies(got.Comments)) != fmt.Sprint(wantComments(11)) {
				t.Fatalf("comments: %v", bodies(got.Comments))
			}
			if g.calls < 4 {
				t.Fatalf("only %d calls: the comments were not paged", g.calls)
			}
		})
	}
}

// A comments page that fails keeps the comments read before it, with the
// error.
func TestLoadKeepsCommentsReadBeforeAnError(t *testing.T) {
	limits := protocol.DefaultLimits()
	limits.SnapshotBytes, limits.MaxPage, limits.DefaultPage = 8000, 4, 4
	srv := tuitest.ServiceWithLimits(t, limits)
	_, s := tuitest.Instance(t, t.TempDir(), srv.URL)
	id, project := discussed(t, s, "test/broken", 10, 1000)
	g := &gate{s: s, refuse: 4} // issue, bounded comments read, page 1, then refused
	got := issue.Load(context.Background(), g, project, id)
	if litclient.Code(got.Err) != "shutting_down" || got.Issue == nil || got.Consistent || len(got.Comments) != 4 {
		t.Fatalf("load: err %v consistent %v comments %d", got.Err, got.Consistent, len(got.Comments))
	}
}

// An issue that cannot be read ends the load with its error.
func TestLoadReportsAnIssueThatCannotBeRead(t *testing.T) {
	srv := tuitest.Service(t)
	_, s := tuitest.Instance(t, t.TempDir(), srv.URL)
	_, project := discussed(t, s, "test/gone", 0, 0)
	got := issue.Load(context.Background(), &gate{s: s}, project, protocol.UUID())
	if litclient.Code(got.Err) != "not_found" || got.Issue != nil || len(got.Comments) != 0 {
		t.Fatalf("load: err %v issue %+v", got.Err, got.Issue)
	}
}
