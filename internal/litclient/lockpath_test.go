package litclient_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// LockPath holds a mapping outside the sessions namespace; Connect saves the
// mapping there, and a passed deadline tries the lock once without waiting.
func TestLockPathHoldsAMappingOutsideTheSessionsNamespace(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "elsewhere", "instance.json")
	first := &litclient.Session{StateDir: h.stateDir, HTTP: http.DefaultClient, Context: context.Background()}
	if err := first.LockPath(path, time.Now().Add(time.Second)); err != nil || first.Mapping != (litclient.Mapping{}) {
		t.Fatalf("lock unmapped path: %+v %v", first.Mapping, err)
	}
	if err := first.Open(litclient.OpenOptions{Endpoint: h.url, EndpointSet: true, AllowUnmapped: true}); err != nil {
		t.Fatal(err)
	}
	if err := first.Connect(context.Background(), litclient.Registration{Actor: protocol.Actor{Name: "tui", Kind: "human"}}); err != nil {
		t.Fatal(err)
	}
	saved, err := litclient.ReadMapping(path)
	if err != nil || saved != first.Mapping || saved.ClientID == "" {
		t.Fatalf("mapping at path: %+v %v", saved, err)
	}
	if sessions, _ := filepath.Glob(filepath.Join(h.stateDir, "sessions", "*")); len(sessions) != 0 {
		t.Fatalf("wrote into the sessions namespace: %v", sessions)
	}
	second := &litclient.Session{StateDir: h.stateDir}
	start := time.Now()
	if err := second.LockPath(path, start); litclient.Code(err) != "session_locked" || time.Since(start) > time.Second {
		t.Fatalf("held lock: %v after %s", err, time.Since(start))
	}
	first.Close()
	if err := second.LockPath(path, time.Now()); err != nil || second.Mapping != saved {
		t.Fatalf("after close: %+v %v", second.Mapping, err)
	}
	second.Close()
}
