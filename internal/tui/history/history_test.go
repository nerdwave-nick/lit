package history

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// gate serializes a session like the lifecycle does, counts its calls and
// can refuse calls from the nth on.
type gate struct {
	mu     sync.Mutex
	s      *litclient.Session
	calls  int
	refuse int // 1-based; 0 never
}

func (g *gate) Do(ctx context.Context, fn func(context.Context, *litclient.Session) error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if g.refuse > 0 && g.calls >= g.refuse {
		return protocol.E(503, "shutting_down", "lit-tui is shutting down; new requests are refused")
	}
	return fn(ctx, g.s)
}

func (g *gate) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

// edited creates an issue in a new project and retitles it n times, then
// comments on it and edits the comment: n+2 issue entries (creation, the
// edits, the comment's creation) and two comment entries.
func edited(t *testing.T, s *litclient.Session, project string, n int) (issue, comment string) {
	t.Helper()
	tuitest.Apply(t, s, litclient.CreateProject(project, "spec"))
	issue = tuitest.CreateIssues(t, s, project, "", "Edit 0")[0]
	for i := 1; i <= n; i++ {
		title := fmt.Sprintf("Edit %d", i)
		tuitest.Apply(t, s, litclient.UpdateIssue(project, issue, 0, litclient.IssueChange{Title: &title}))
	}
	c := litclient.CreateComment(project, issue, "first words")
	tuitest.Apply(t, s, c)
	tuitest.Apply(t, s, litclient.UpdateComment(c.Items[0].ID, 0, "better words"))
	return issue, c.Items[0].ID
}

// drain runs cmd and delivers its messages to m, and the commands those
// return, until none is left.
func drain(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, msg...)
		default:
			next, ok := m.Update(msg)
			if !ok {
				t.Fatalf("message %T not taken", msg)
			}
			queue = append(queue, next)
		}
	}
}

func screen(m *Model, w, h int) string {
	return m.View(cells.Default, w, h, keys.Default(), true)
}

// Pages load on demand: enough to fill the view, then the next one when
// scrolling reaches the end, following every cursor until the history is
// complete, oldest first and without duplicates. Nothing is written.
func TestModelLoadsPagesOnDemandFollowingCursors(t *testing.T) {
	limits := protocol.DefaultLimits()
	limits.MaxPage, limits.DefaultPage = 2, 2
	srv := tuitest.ServiceWithLimits(t, limits)
	_, s := tuitest.Instance(t, t.TempDir(), srv.URL)
	issue, _ := edited(t, s, "test/paged", 9) // 11 issue entries
	writes := srv.Count("POST /v1/transactions")
	g := &gate{s: s}

	m, cmd := New(g, Target{Type: "issues", ID: issue, Label: `issue "Edit 9"`})
	screen(m, 80, 8) // the view's size is known before the first page
	drain(t, m, cmd)
	first := g.count()
	if first < 2 || first >= 6 {
		t.Fatalf("%d pages read to fill an 8-line view", first)
	}
	if v := screen(m, 80, 8); !strings.Contains(v, "History · issue \"Edit 9\" · read-only") || !strings.Contains(v, "so far") {
		t.Fatalf("partial view:\n%s", v)
	}

	// Scrolling to the end loads the next pages, one at a time.
	for i := 0; i < 20; i++ {
		cmd, closed := m.Do(keys.Bottom)
		if closed {
			t.Fatal("bottom closed the view")
		}
		screen(m, 80, 8)
		drain(t, m, cmd)
	}
	if g.count() != 6 { // 11 entries in pages of 2
		t.Fatalf("%d pages read for 11 entries", g.count())
	}
	all := screen(m, 80, 0)
	if !strings.Contains(all, "11 entries") || strings.Contains(all, "so far") {
		t.Fatalf("complete view:\n%s", all)
	}
	last := -1
	for i := 1; i <= 11; i++ {
		at := strings.Index(all, fmt.Sprintf("\n#%d · ", i))
		if at <= last || strings.Count(all, fmt.Sprintf("\n#%d · ", i)) != 1 {
			t.Fatalf("entry %d out of order or repeated:\n%s", i, all)
		}
		last = at
	}
	for i := 1; i <= 9; i++ {
		if !strings.Contains(all, fmt.Sprintf(`title: "Edit %d" → "Edit %d"`, i-1, i)) {
			t.Fatalf("missing edit %d:\n%s", i, all)
		}
	}
	if !strings.Contains(all, "comment create") || !strings.Contains(all, "tester (human)") {
		t.Fatalf("comment creation or actor missing:\n%s", all)
	}
	// Further scrolling reads nothing more; neither did anything write.
	cmd, _ = m.Do(keys.Bottom)
	drain(t, m, cmd)
	if g.count() != 6 || srv.Count("POST /v1/transactions") != writes {
		t.Fatalf("calls %d, writes %d → %d", g.count(), writes, srv.Count("POST /v1/transactions"))
	}
}

// A failed page keeps the entries read before it and names the error;
// refresh reads the history again from the start.
func TestModelKeepsEntriesBeforeAnErrorAndRefreshes(t *testing.T) {
	limits := protocol.DefaultLimits()
	limits.MaxPage, limits.DefaultPage = 2, 2
	srv := tuitest.ServiceWithLimits(t, limits)
	_, s := tuitest.Instance(t, t.TempDir(), srv.URL)
	issue, _ := edited(t, s, "test/broken", 4)
	g := &gate{s: s, refuse: 2}
	m, cmd := New(g, Target{Type: "issues", ID: issue, Label: "issue"})
	screen(m, 80, 30)
	drain(t, m, cmd)
	v := screen(m, 80, 0)
	if !strings.Contains(v, "2 entries so far · stopped: shutting_down: lit-tui is shutting down") || !strings.Contains(v, "#2 · ") || strings.Contains(v, "#3 · ") {
		t.Fatalf("after the error:\n%s", v)
	}
	g.mu.Lock()
	g.refuse = 0
	g.mu.Unlock()
	cmd, _ = m.Do(keys.Refresh)
	drain(t, m, cmd)
	if v := screen(m, 80, 0); !strings.Contains(v, "6 entries") || strings.Contains(v, "stopped") || strings.Count(v, "\n#1 · ") != 1 {
		t.Fatalf("after refresh:\n%s", v)
	}
}

// With a comment to choose from, the view opens on a chooser: Enter shows
// the chosen record's history, Escape goes back to the chooser and closes
// it, and the history key closes the view at once.
func TestModelChoosesTheIssueOrAComment(t *testing.T) {
	srv := tuitest.Service(t)
	_, s := tuitest.InstanceAs(t, t.TempDir(), srv.URL, protocol.Actor{Name: "Claude", Kind: "agent"})
	issue, comment := edited(t, s, "test/choose", 1)
	g := &gate{s: s}
	m, cmd := New(g, Target{Type: "issues", ID: issue, Label: `issue "Edit 1"`}, Target{Type: "comments", ID: comment, Label: "comment by Claude"})
	if cmd != nil || g.count() != 0 {
		t.Fatal("the chooser read something")
	}
	v := screen(m, 80, 10)
	if !strings.Contains(v, "History of") || !strings.Contains(v, `> issue "Edit 1"`) || !strings.Contains(v, "  comment by Claude") {
		t.Fatalf("chooser:\n%s", v)
	}
	m.Do(keys.Down)
	cmd, closed := m.Do(keys.Open)
	if closed {
		t.Fatal("choosing closed the view")
	}
	drain(t, m, cmd)
	v = screen(m, 80, 0)
	if !strings.Contains(v, "History · comment by Claude") || !strings.Contains(v, "2 entries") ||
		!strings.Contains(v, "- first words") || !strings.Contains(v, "+ better words") || !strings.Contains(v, "Claude (agent)") {
		t.Fatalf("comment history:\n%s", v)
	}
	if _, closed = m.Do(keys.Cancel); closed || !strings.Contains(screen(m, 80, 10), "> comment by Claude") {
		t.Fatalf("escape did not return to the chooser on the comment:\n%s", screen(m, 80, 10))
	}
	if _, closed = m.Do(keys.Cancel); !closed {
		t.Fatal("escape in the chooser did not close")
	}

	m, _ = New(g, Target{Type: "issues", ID: issue, Label: "issue"}, Target{Type: "comments", ID: comment, Label: "comment"})
	cmd, _ = m.Do(keys.Open)
	if _, closed = m.Do(keys.History); !closed {
		t.Fatal("the history key did not close the view")
	}
	// The closed view's read was cancelled; its late answer changes nothing.
	if msg := cmd(); msg != nil {
		if _, ok := m.Update(msg); !ok {
			t.Fatal("the late answer was not recognised")
		}
	}
	if strings.Contains(screen(m, 80, 0), "#1 · ") {
		t.Fatal("a late answer was shown after closing")
	}
}

// A single target opens its history at once; Escape closes the view.
func TestModelWithOneTargetShowsItsHistory(t *testing.T) {
	srv := tuitest.Service(t)
	_, s := tuitest.Instance(t, t.TempDir(), srv.URL)
	issue, _ := edited(t, s, "test/one", 2)
	g := &gate{s: s}
	m, cmd := New(g, Target{Type: "issues", ID: issue, Label: "issue"})
	if v := screen(m, 80, 10); !strings.Contains(v, "loading…") {
		t.Fatalf("before the first page:\n%s", v)
	}
	drain(t, m, cmd)
	if v := screen(m, 80, 0); !strings.Contains(v, "4 entries") || !strings.Contains(v, `title: "Edit 1" → "Edit 2"`) {
		t.Fatalf("history:\n%s", v)
	}
	if _, closed := m.Do(keys.Cancel); !closed {
		t.Fatal("escape did not close")
	}
}
