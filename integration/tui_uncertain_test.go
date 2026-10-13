package integration

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/tui/tuitest"
)

// flat removes the whitespace of a screen, so a value wrapped over lines can
// be found.
func flat(s string) string { return strings.Join(strings.Fields(s), "") }

func sameEvidence(t *testing.T, path string, before []byte) {
	t.Helper()
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(before, after) {
		t.Fatalf("evidence %s changed or removed: %v", path, err)
	}
}

// The uncertain-requests view, driven in tmux against a temporary lit-server
// with fabricated uncertain evidence (a claims.acquire whose status stays
// acquisition_token_not_received). It is reached from the launch panel, the
// action menu, its key and the exit report, and never changes the evidence.
func TestTUIUncertainRequestsInTmux(t *testing.T) {
	t.Run("an orphan's request from the launch panel and the action menu", func(t *testing.T) {
		e := newTUIEnv(t)
		a := e.launch("orphan")
		path := tuitest.UncertainAcquire(t, a.session())
		before, _ := os.ReadFile(path)
		id := strings.TrimSuffix(filepath.Base(path), ".json")
		if err := syscall.Kill(a.info.PID, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		a.exit()
		b := e.launch("viewer")
		b.await("the panel's link", func(s string) bool {
			return strings.Contains(s, id) && strings.Contains(s, "not disconnected") && strings.Contains(s, "U view the uncertain requests")
		})
		b.keys("U")
		view := b.await("the view", func(s string) bool {
			return strings.Contains(s, "Uncertain requests · read-only") && strings.Contains(s, "claims.acquire")
		})
		for _, want := range []string{"Orphaned lit-tui session " + a.entry.ID[:8], "checked at launch", id, "uncertain (acquisition_token_not_received)",
			"POST /v1/operations · claims.acquire", a.m.ServiceID, a.m.ClientID, path, "never replays", "docs/recovery.md"} {
			if !strings.Contains(flat(view), flat(want)) {
				t.Errorf("view lacks %q:\n%s", want, view)
			}
		}
		sameEvidence(t, path, before)
		b.keys("Escape")
		b.await("the panel again", func(s string) bool {
			return strings.Contains(s, "not disconnected") && !strings.Contains(s, "Uncertain requests · read-only")
		})
		b.keys("Escape")
		b.await("the panel to close", func(s string) bool { return !strings.Contains(s, "not disconnected") })
		b.keys("m")
		b.await("the action menu", func(s string) bool { return strings.Contains(s, "show uncertain requests") })
		for range 3 {
			b.keys("Down")
		}
		b.keys("Enter")
		b.await("the view from the menu", func(s string) bool {
			return strings.Contains(s, "Uncertain requests · read-only") && strings.Contains(s, id)
		})
		b.keys("Escape")
		b.await("the view to close", func(s string) bool { return !strings.Contains(s, "Uncertain requests · read-only") })
		b.keys("q")
		if code := b.exit(); code != 0 {
			t.Fatalf("exit %d:\n%s", code, b.screen())
		}
		b.cleanlyGone()
		if a.client() != "connected" || !a.published() {
			t.Fatalf("orphan touched: client %s entry %v", a.client(), a.published())
		}
		sameEvidence(t, path, before)
	})
	t.Run("this session's request from its key and from the exit report", func(t *testing.T) {
		e := newTUIEnv(t)
		a := e.launch("own")
		path := tuitest.UncertainAcquire(t, a.session())
		before, _ := os.ReadFile(path)
		id := strings.TrimSuffix(filepath.Base(path), ".json")
		a.keys("U")
		view := a.await("the view", func(s string) bool { return strings.Contains(s, "checked now") && strings.Contains(s, id) })
		for _, want := range []string{"This lit-tui session · client " + a.m.ClientID[:8], "uncertain (acquisition_token_not_received)", "claims.acquire", a.m.ClientID, path} {
			if !strings.Contains(flat(view), flat(want)) {
				t.Errorf("view lacks %q:\n%s", want, view)
			}
		}
		a.keys("Escape")
		a.await("the view to close", func(s string) bool { return !strings.Contains(s, "Uncertain requests · read-only") })
		a.keys("q")
		a.await("the exit report's link", func(s string) bool {
			return strings.Contains(s, id) && strings.Contains(s, "Press any key to exit") && strings.Contains(s, "U first shows these requests read-only")
		})
		a.keys("U")
		a.await("the view at exit", func(s string) bool { return strings.Contains(s, "checked at exit") && strings.Contains(s, id) })
		a.keys("x") // keys in the view do not leave
		time.Sleep(300 * time.Millisecond)
		if syscall.Kill(a.info.PID, 0) != nil || !strings.Contains(a.screen(), "checked at exit") {
			t.Fatalf("a key in the view left:\n%s", a.screen())
		}
		a.keys("Escape")
		a.await("the exit report again", func(s string) bool { return strings.Contains(s, "Press any key to exit") })
		a.keys("x")
		if code := a.exit(); code != 4 {
			t.Fatalf("exit %d", code)
		}
		if a.client() != "connected" || !a.published() {
			t.Fatalf("uncertain exit: client %s entry %v", a.client(), a.published())
		}
		sameEvidence(t, path, before)
	})
}
