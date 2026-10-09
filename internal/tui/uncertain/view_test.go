package uncertain_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/uncertain"
)

const (
	reqA    = "6f1c2d3e-0000-4000-8000-00000000000a"
	reqB    = "6f1c2d3e-0000-4000-8000-00000000000b"
	reqC    = "6f1c2d3e-0000-4000-8000-00000000000c"
	service = "a29ca783-fb66-42a1-befe-9149289cc659"
	client  = "eccd071b-f391-41f2-a2ef-28f6ba25d4f3"
)

func groups() []uncertain.Group {
	committed := uncertain.Item{ID: reqB, Status: "committed", Proof: "immutable_history", Outcome: "applied", Evidence: "/state/pending/" + reqB + ".json",
		Method: "POST", Path: "/v1/transactions", Operation: "issue.update", ServiceID: service, ClientID: client, Endpoint: "http://127.0.0.1:9"}
	open := uncertain.Item{ID: reqA, Status: "uncertain", Proof: "acquisition_token_not_received", Evidence: "/state/pending/" + reqA + ".json",
		Method: "POST", Path: "/v1/operations", Operation: "claims.acquire", ServiceID: service, ClientID: client, Endpoint: "http://127.0.0.1:9", CreatedAt: "2026-10-09T08:00:00Z"}
	orphan := uncertain.Item{ID: reqC, Status: "uncertain", Proof: "registration_identity_not_received", Evidence: "/state/pending/" + reqC + ".json",
		Method: "POST", Path: "/v1/connect", Operation: "connect", ServiceID: service}
	return []uncertain.Group{
		{Title: "This session · client eccd071b", Checked: "checked now", Items: []uncertain.Item{committed, open}},
		{Title: "Orphaned session 1234abcd (pid 77)", Checked: "checked at launch", Items: []uncertain.Item{orphan}},
	}
}

// open returns a view whose loader returns load() and counts its runs, after
// its first load arrived.
func open(t *testing.T, load func() []uncertain.Group) (*uncertain.Model, *atomic.Int32) {
	t.Helper()
	var runs atomic.Int32
	m := uncertain.New(func(context.Context) []uncertain.Group { runs.Add(1); return load() })
	deliver(t, m, m.Load())
	return m, &runs
}

func deliver(t *testing.T, m *uncertain.Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command")
	}
	if !m.Update(cmd()) {
		t.Fatal("the view did not take its message")
	}
}

func screen(m *uncertain.Model, w, h int) []string {
	return strings.Split(m.View(cells.Default, w, h, keys.Default()), "\n")
}

// The view shows each request's ID, outcome, operation, evidence path and
// service and client identity, uncertain ones first, the groups' sessions
// and when they were checked, and the recovery guidance with the doc path.
func TestViewShowsEveryRequestAndTheGuidance(t *testing.T) {
	m, _ := open(t, groups)
	lines := screen(m, 120, 80)
	if len(lines) != 80 {
		t.Fatalf("%d lines", len(lines))
	}
	text := strings.Join(lines, "\n")
	for _, want := range []string{
		"Uncertain requests", "read-only",
		"This session · client eccd071b", "checked now", "Orphaned session 1234abcd (pid 77)", "checked at launch",
		reqA, "uncertain (acquisition_token_not_received)", "POST /v1/operations · claims.acquire", "/state/pending/" + reqA + ".json",
		"operation POST", "service   " + service, "client    " + client, "http://127.0.0.1:9", "2026-10-09T08:00:00Z",
		reqB, "committed: applied (immutable_history)", "POST /v1/transactions · issue.update",
		reqC, "registration_identity_not_received", "client    (no identity received)",
		"never replays", "docs/recovery.md", "lit transactions status", "escalate",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("view lacks %q:\n%s", want, text)
		}
	}
	if strings.Index(text, reqA) > strings.Index(text, reqB) {
		t.Error("the uncertain request is not listed before the resolved one")
	}
	if strings.Contains(text, "\x1b") {
		t.Error("the view writes escape sequences")
	}
	for i, l := range lines {
		if w := cells.Width(cells.Default, l); w > 120 {
			t.Errorf("line %d is %d cells wide", i, w)
		}
	}
}

// Narrow screens wrap long lines instead of cutting evidence paths; the view
// scrolls, refreshes, and closes on cancel or its own key.
func TestViewWrapsScrollsRefreshesAndCloses(t *testing.T) {
	m, runs := open(t, groups)
	lines := screen(m, 30, 12)
	if len(lines) != 12 {
		t.Fatalf("%d lines", len(lines))
	}
	for i, l := range lines {
		if w := cells.Width(cells.Default, l); w > 30 {
			t.Errorf("line %d is %d cells wide: %q", i, w, l)
		}
	}
	all := strings.Join(screen(m, 30, 400), "")
	if !strings.Contains(strings.ReplaceAll(all, " ", ""), "/state/pending/"+reqA+".json") {
		t.Errorf("the evidence path is cut:\n%s", all)
	}
	first := screen(m, 30, 12)[1]
	m.Do(keys.Down)
	if screen(m, 30, 12)[1] == first {
		t.Error("down did not scroll")
	}
	m.Do(keys.Bottom)
	if bottom := screen(m, 30, 12); !strings.Contains(strings.Join(bottom, "\n"), "000c.json") {
		t.Errorf("bottom:\n%s", strings.Join(bottom, "\n"))
	}
	m.Do(keys.Top)
	if screen(m, 30, 12)[1] != first {
		t.Error("top did not return")
	}

	cmd, closed := m.Do(keys.Refresh)
	if closed || cmd == nil {
		t.Fatal("refresh")
	}
	if text := strings.Join(screen(m, 120, 80), "\n"); !strings.Contains(text, "checking") {
		t.Errorf("no checking indicator while refreshing:\n%s", text)
	}
	deliver(t, m, cmd)
	if runs.Load() != 2 {
		t.Fatalf("%d loads", runs.Load())
	}
	other := uncertain.New(func(context.Context) []uncertain.Group { return nil })
	if m.Update(other.Load()()) {
		t.Error("took another view's result")
	}
	for _, a := range []keys.Action{keys.Cancel, keys.UncertainRequests} {
		if _, closed := m.Do(a); !closed {
			t.Errorf("%s did not close the view", a)
		}
	}
	if _, closed := m.Do(keys.Open); closed {
		t.Error("open closed the view")
	}
}

// Titles and fields are sanitized: a control byte in the evidence becomes a
// visible marker. An empty group says so.
func TestViewSanitizesAndShowsEmptyGroups(t *testing.T) {
	m, _ := open(t, func() []uncertain.Group {
		return []uncertain.Group{
			{Title: "This session", Checked: "checked now"},
			{Title: "Orphan \x1b[31m", Checked: "checked at launch", Items: []uncertain.Item{{ID: reqA, Status: "uncertain", Operation: "bad\x1b]0;x\x07", Problem: "cannot read\x1b"}}},
		}
	})
	text := strings.Join(screen(m, 120, 60), "\n")
	if strings.ContainsAny(text, "\x1b\x07") || !strings.Contains(text, "␛") || !strings.Contains(text, "No retained requests") || !strings.Contains(text, "cannot read") {
		t.Fatalf("view:\n%q", text)
	}
}
