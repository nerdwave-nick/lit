package tui

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/lifecycle"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// With a lifecycle, quit and Ctrl-C start the orderly shutdown instead of
// quitting the program; the coordinator quits it once the client is
// disconnected.
func TestQuitAndCtrlCRouteIntoTheShutdownCoordinator(t *testing.T) {
	srv := tuitest.Service(t)
	for _, k := range []tea.KeyPressMsg{key('q', 0, "q"), key('c', tea.ModCtrl, "")} {
		l, err := lifecycle.New(lifecycle.Config{StateDir: t.TempDir(), Endpoint: srv.URL, HTTP: &http.Client{}, Actor: protocol.Actor{Name: "p", Kind: "human"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := l.Connect(context.Background()); err != nil {
			t.Fatal(err)
		}
		m := NewModel(srv.URL).WithLifecycle(l)
		if !strings.Contains(m.View().Content, "Session: client "+l.Status().Client[:8]) {
			t.Errorf("no session line:\n%s", m.View().Content)
		}
		if _, cmd := m.Update(k); quits(cmd) || !l.Started() {
			t.Fatalf("%v quit the program directly or did not start shutdown", k)
		}
		select {
		case <-l.Done():
		case <-time.After(10 * time.Second):
			t.Fatal("shutdown did not finish")
		}
		if r := l.Result(); r.Outcome != lifecycle.Clean {
			t.Fatalf("result %+v", r)
		}
	}
}
