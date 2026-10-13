package export_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/export"
	"github.com/nerdwave-nick/lit/internal/storage"
)

// siblings lists the names next to dir other than dir itself.
func siblings(t *testing.T, dir string) []string {
	t.Helper()
	entries, e := os.ReadDir(filepath.Dir(dir))
	if e != nil {
		t.Fatal(e)
	}
	var out []string
	for _, d := range entries {
		if d.Name() != filepath.Base(dir) {
			out = append(out, d.Name())
		}
	}
	return out
}

func TestExportReplacesAPreviousExport(t *testing.T) {
	db := fixture(t)
	dir := filepath.Join(t.TempDir(), "export")
	run(t, db, dir, exportTime)
	stale := filepath.Join(dir, "feat", "gone", "project.md")
	if e := os.MkdirAll(filepath.Dir(stale), 0o755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(stale, []byte("old"), 0o644); e != nil {
		t.Fatal(e)
	}
	run(t, db, dir, exportTime)
	if _, e := os.Stat(stale); !os.IsNotExist(e) {
		t.Fatalf("a file of the previous export survived: %v", e)
	}
	// Only the lock file stays next to the export; no staging is left.
	if got := siblings(t, dir); len(got) != 1 || got[0] != ".export.export-lock" {
		t.Fatalf("left next to the export: %v", got)
	}
	info, e := os.Stat(dir)
	if e != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("export directory %v: %v", info.Mode(), e)
	}
}

func TestExportIntoAnEmptyDirectory(t *testing.T) {
	db := fixture(t)
	dir := t.TempDir()
	run(t, db, dir, exportTime)
	if _, e := os.Stat(filepath.Join(dir, "manifest.json")); e != nil {
		t.Fatal(e)
	}
}

func TestExportRefusesADirectoryItDidNotWrite(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	for _, c := range []struct {
		name  string
		setup func(dir string) error
	}{
		{"other files", func(dir string) error {
			if e := os.MkdirAll(dir, 0o755); e != nil {
				return e
			}
			return os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o644)
		}},
		{"foreign manifest", func(dir string) error {
			if e := os.MkdirAll(dir, 0o755); e != nil {
				return e
			}
			return os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"name":"other"}`), 0o644)
		}},
		{"a file", func(dir string) error { return os.WriteFile(dir, []byte("x"), 0o644) }},
		{"a symlink", func(dir string) error { return os.Symlink(os.TempDir(), dir) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "target")
			if e := c.setup(dir); e != nil {
				t.Fatal(e)
			}
			before := tree(t, filepath.Dir(dir))
			snap, e := db.Read(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer snap.Close()
			_, e = export.Export(ctx, snap, dir, export.Options{ServiceID: fixtureService, Time: exportTime})
			if e == nil || !strings.Contains(e.Error(), "not a lit export") {
				t.Fatalf("export into %s: %v", c.name, e)
			}
			after := tree(t, filepath.Dir(dir))
			delete(after, ".target.export-lock")
			if strings.Join(names(before), ",") != strings.Join(names(after), ",") {
				t.Fatalf("refused export changed %v into %v", names(before), names(after))
			}
		})
	}
}

func TestExportFailureLeavesThePreviousExport(t *testing.T) {
	db := fixture(t)
	dir := filepath.Join(t.TempDir(), "export")
	run(t, db, dir, exportTime)
	before := tree(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	snap, e := db.Read(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer snap.Close()
	cancel()
	if _, e = export.Export(ctx, snap, dir, export.Options{ServiceID: fixtureService, Time: exportTime.Add(1)}); !errors.Is(e, context.Canceled) {
		t.Fatalf("cancelled export: %v", e)
	}
	after := tree(t, dir)
	if strings.Join(names(before), ",") != strings.Join(names(after), ",") || string(before["manifest.json"]) != string(after["manifest.json"]) {
		t.Fatal("a failed export changed the previous one")
	}
	if got := siblings(t, dir); len(got) != 1 {
		t.Fatalf("left next to the export: %v", got)
	}
}

// blocking reads through a snapshot, but its first List waits for release.
type blocking struct {
	*storage.ReadTx
	started, release chan struct{}
}

func (s *blocking) List(ctx context.Context, q storage.ListQuery) ([]any, error) {
	if s.started != nil {
		close(s.started)
		s.started = nil
		<-s.release
	}
	return s.ReadTx.List(ctx, q)
}

func TestConcurrentExportsIntoOneDirectoryAreRefused(t *testing.T) {
	db := fixture(t)
	dir := filepath.Join(t.TempDir(), "export")
	ctx := context.Background()
	first, e := db.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer first.Close()
	slow := &blocking{ReadTx: first, started: make(chan struct{}), release: make(chan struct{})}
	started := slow.started
	done := make(chan error, 1)
	go func() {
		_, e := export.Export(ctx, slow, dir, export.Options{ServiceID: fixtureService, Time: exportTime})
		done <- e
	}()
	select {
	case <-started:
	case e := <-done:
		t.Fatalf("the first export ended before reading: %v", e)
	}
	second, e := db.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	if _, e = export.Export(ctx, second, dir, export.Options{ServiceID: fixtureService, Time: exportTime}); !errors.Is(e, export.ErrBusy) {
		t.Fatalf("second export: %v", e)
	}
	close(slow.release)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	run(t, db, dir, exportTime)
}
