package tui

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/lifecycle"
	"github.com/nerdwave-nick/lit/internal/tui/prefs"
	"github.com/nerdwave-nick/lit/internal/tui/tuitest"
)

// evidence is every retained request file in state, to prove viewing leaves
// them byte-identical.
func evidence(t *testing.T, state string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	dir := filepath.Join(state, "pending")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = b
	}
	return out
}

func unchanged(t *testing.T, state string, before map[string][]byte) {
	t.Helper()
	after := evidence(t, state)
	if len(after) != len(before) {
		t.Fatalf("evidence files: %d before, %d after", len(before), len(after))
	}
	for name, b := range before {
		if !bytes.Equal(b, after[name]) {
			t.Fatalf("evidence %s changed or removed", name)
		}
	}
}

func mutationCount(srv *tuitest.Server) int {
	n := 0
	for _, k := range []string{"POST /v1/transactions", "POST /v1/operations", "POST /v1/disconnect", "POST /v1/connect"} {
		n += srv.Count(k)
	}
	return n
}

// viewing starts the root model at 120×60 for a lifecycle in state; with own
// set, the lifecycle's client first gets a retained claims.acquire whose
// outcome stays uncertain, through the gate. It returns that evidence path.
func viewing(t *testing.T, srv *tuitest.Server, state string, own bool) (*browsing, string) {
	t.Helper()
	l, err := lifecycle.New(lifecycle.Config{StateDir: state, Endpoint: srv.URL, HTTP: &http.Client{}, Actor: protocol.Actor{Name: "person", Kind: "human"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		l.Shutdown("test")
		<-l.Done()
	})
	path := ""
	if own {
		if err := l.Do(context.Background(), func(_ context.Context, s *litclient.Session) error {
			path = tuitest.UncertainAcquire(t, s)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	p := prefs.Default()
	p.Theme = prefs.ThemeDark
	tm := &timers{}
	m := NewModel(srv.URL).WithPrefs(p).WithLifecycle(l).withTimers(tm.after)
	b := &browsing{t: t, l: l, tm: tm, srv: srv}
	b.m = drive(t, m, m.Init(), tea.WindowSizeMsg{Width: 120, Height: 60})
	return b, path
}

func requestID(path string) string { return strings.TrimSuffix(filepath.Base(path), ".json") }

// shows reports whether the view contains s, which may be wrapped over lines.
func shows(view, s string) bool {
	flat := func(x string) string { return strings.Join(strings.Fields(x), "") }
	return strings.Contains(flat(view), flat(s))
}

// The action menu and the uncertain_requests key open the view of this
// session's retained requests, checked now and read-only; Escape returns to
// the tree. Nothing is sent that could change the tracker, and the evidence
// stays byte-identical.
func TestUncertainRequestsFromTheMenuAndKey(t *testing.T) {
	srv := tuitest.Service(t)
	state := t.TempDir()
	b, path := viewing(t, srv, state, true)
	before, sent := evidence(t, state), mutationCount(srv)
	if !strings.Contains(b.header(), "test/") {
		t.Fatalf("not browsing:\n%s", b.view())
	}
	b.press("m")
	if b.line("show uncertain requests") == "" || !strings.Contains(b.line("show uncertain requests"), "U") {
		t.Fatalf("menu lacks the command:\n%s", b.view())
	}
	b.press("down", "down", "down", "enter")
	view := b.view()
	client := b.l.Status().Client
	for _, want := range []string{"Uncertain requests · read-only", "This lit-tui session", client[:8], "checked now", requestID(path),
		"uncertain (acquisition_token_not_received)", "POST /v1/operations · claims.acquire", path, client, "docs/recovery.md"} {
		if !shows(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	b.press("esc")
	if strings.Contains(b.view(), "Uncertain requests") || !strings.Contains(b.header(), "test/") {
		t.Fatalf("escape did not return to the tree:\n%s", b.view())
	}
	b.press("U")
	if !strings.Contains(b.view(), requestID(path)) {
		t.Fatalf("U did not open the view:\n%s", b.view())
	}
	b.press("r")
	if !strings.Contains(b.view(), requestID(path)) {
		t.Fatalf("refresh lost the request:\n%s", b.view())
	}
	b.press("U")
	if strings.Contains(b.view(), "Uncertain requests") {
		t.Fatal("U did not close the view")
	}
	if mutationCount(srv) != sent {
		t.Fatal("viewing sent a mutation")
	}
	unchanged(t, state, before)
}

// The launch panel about an orphan with uncertain requests links to the view,
// which lists the orphan's requests as checked at launch, also after the
// panel is closed. The orphan is never disconnected.
func TestUncertainRequestsFromTheLaunchPanel(t *testing.T) {
	srv := tuitest.Service(t)
	state := t.TempDir()
	e, s := tuitest.Instance(t, state, srv.URL)
	path := tuitest.UncertainAcquire(t, s)
	tuitest.Crash(s)
	before := evidence(t, state)
	b, _ := viewing(t, srv, state, false)
	sent := mutationCount(srv)
	if !strings.Contains(b.view(), "U view the uncertain requests") {
		t.Fatalf("panel lacks the link:\n%s", b.view())
	}
	b.press("U")
	view := b.view()
	for _, want := range []string{"Orphaned lit-tui session " + e.ID[:8], "checked at launch", requestID(path), "claims.acquire", s.Mapping.ClientID, path} {
		if !shows(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	b.press("esc")
	if !strings.Contains(b.view(), "was not disconnected") {
		t.Fatalf("escape did not return to the panel:\n%s", b.view())
	}
	b.press("esc")
	if strings.Contains(b.view(), "was not disconnected") {
		t.Fatal("panel not closed")
	}
	b.press("U")
	if !strings.Contains(b.view(), requestID(path)) {
		t.Fatalf("the orphan's request is gone from the view:\n%s", b.view())
	}
	if tuitest.ClientStatus(t, srv.URL, s.Mapping.ClientID) != "connected" || mutationCount(srv) != sent {
		t.Fatal("viewing touched the orphan")
	}
	unchanged(t, state, before)
}

// The exit report that lists uncertain requests links to the view, which
// shows them as checked at exit; keys in the view do not leave, Escape
// returns to the report, and the next key leaves.
func TestUncertainRequestsFromTheExitReport(t *testing.T) {
	srv := tuitest.Service(t)
	state := t.TempDir()
	b, path := viewing(t, srv, state, true)
	before := evidence(t, state)
	b.press("q")
	select {
	case <-b.l.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	b.send(lifecycle.StatusMsg(b.l.Status()))
	if v := b.view(); !strings.Contains(v, "Press any key to exit") || !strings.Contains(v, "U first shows these requests read-only") {
		t.Fatalf("exit report:\n%s", v)
	}
	sent := mutationCount(srv)
	if _, cmd := b.m.Update(keyMsg("U")); quits(cmd) {
		t.Fatal("U left")
	}
	b.press("U")
	if v := b.view(); !strings.Contains(v, "checked at exit") || !strings.Contains(v, requestID(path)) || !strings.Contains(v, "claims.acquire") {
		t.Fatalf("view:\n%s", v)
	}
	if _, cmd := b.m.Update(keyMsg("x")); quits(cmd) {
		t.Fatal("a key in the view left")
	}
	b.press("esc")
	if !strings.Contains(b.view(), "Press any key to exit") {
		t.Fatalf("escape did not return to the report:\n%s", b.view())
	}
	if _, cmd := b.m.Update(keyMsg("x")); !quits(cmd) {
		t.Fatal("the next key does not leave")
	}
	if mutationCount(srv) != sent {
		t.Fatal("viewing sent a mutation")
	}
	unchanged(t, state, before)
}
