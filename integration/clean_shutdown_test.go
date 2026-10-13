package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

const (
	scanSkipped = "lit-server: clean shutdown: full integrity scan skipped"
	scanRun     = "lit-server: unclean shutdown: running full integrity scan"
)

func cleanMarker(t *testing.T, data string) bool {
	t.Helper()
	_, e := os.Stat(filepath.Join(data, storage.CleanMarker))
	if e != nil && !os.IsNotExist(e) {
		t.Fatal(e)
	}
	return e == nil
}

// A graceful stop records a clean shutdown, and the next start skips the
// full integrity scan; a start consumes the record before it serves, so after
// kill -9 the next start runs the scan. A database replaced after a clean
// stop, even by an identical copy, is scanned too.
func TestCleanShutdownSkipsTheIntegrityScan(t *testing.T) {
	root := t.TempDir()
	data, state := filepath.Join(root, "data"), filepath.Join(root, "state")
	s := start(t, data)
	if !strings.Contains(s.log.String(), "lit-server: new database: full integrity scan skipped") || cleanMarker(t, data) {
		t.Fatal(s.log.String())
	}
	run(t, root, state, 0, "connect", "--endpoint", s.endpoint, "--format", "json")
	run(t, root, state, 0, "projects", "create", "--project-title", "feat/clean")
	s.stop(t)
	if !cleanMarker(t, data) {
		t.Fatalf("no marker after a graceful stop: %s", s.log)
	}

	s = start(t, data)
	if !strings.Contains(s.log.String(), scanSkipped) || strings.Contains(s.log.String(), scanRun) {
		t.Fatal(s.log.String())
	}
	if cleanMarker(t, data) {
		t.Fatal("the marker survived into serving")
	}
	run(t, root, state, 0, "projects", "create", "--project-title", "feat/killed", "--endpoint", s.endpoint)
	if e := s.cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	if e := s.cmd.Wait(); e == nil {
		t.Fatal("expected killed process")
	}
	if cleanMarker(t, data) {
		t.Fatal("marker after kill -9")
	}

	s = start(t, data)
	if !strings.Contains(s.log.String(), scanRun) {
		t.Fatal(s.log.String())
	}
	if code, errCode, msg := errorCode(t, s, "GET", "/v1/meta"); code != 200 {
		t.Fatal(code, errCode, msg)
	}
	s.stop(t)

	// Restore an identical copy, as a restore without lit-server verify.
	db := filepath.Join(data, storage.DatabaseFile)
	b, e := os.ReadFile(db)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(db+".restored", b, 0o600); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(db+".restored", db); e != nil {
		t.Fatal(e)
	}
	s = start(t, data)
	if !strings.Contains(s.log.String(), scanRun) {
		t.Fatal(s.log.String())
	}
	s.stop(t)
}
