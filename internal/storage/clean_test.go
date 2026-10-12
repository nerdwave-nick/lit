package storage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func marker(dir string) string { return filepath.Join(dir, CleanMarker) }

func markerExists(t *testing.T, dir string) bool {
	t.Helper()
	_, e := os.Stat(marker(dir))
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
	return e == nil
}

// reopenPrior opens dir and returns how its previous owner left it; the database
// is closed (without a marker) at the end of the test.
func reopenPrior(t *testing.T, dir string) (*DB, PriorShutdown) {
	t.Helper()
	db, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	return db, db.PriorShutdown()
}

func closeClean(t *testing.T, db *DB) {
	t.Helper()
	if e := db.CloseClean(); e != nil {
		t.Fatal(e)
	}
}

func TestFreshDatabaseHasNoPriorShutdown(t *testing.T) {
	if _, prior := reopenPrior(t, t.TempDir()); prior != ShutdownNone {
		t.Fatal(prior)
	}
}

// CloseClean leaves the marker; the next open finds a clean shutdown and
// removes the marker before anything is served, so a crash of that owner is
// unclean. A plain Close leaves none.
func TestCloseCleanMarksTheNextOpenClean(t *testing.T) {
	dir := t.TempDir()
	db, _ := reopenPrior(t, dir)
	mustApply(t, db, at(1), newProject(id(1), "feat/one", "", at(1)))
	closeClean(t, db)
	if !markerExists(t, dir) {
		t.Fatal("no marker after CloseClean")
	}
	db, prior := reopenPrior(t, dir)
	if prior != ShutdownClean {
		t.Fatal(prior)
	}
	if markerExists(t, dir) {
		t.Fatal("the marker survived the open")
	}
	db.Close()
	if _, prior = reopenPrior(t, dir); prior != ShutdownUnclean {
		t.Fatal("after a plain Close:", prior)
	}
}

// lit-server verify opens with OpenExisting, which consumes the marker too.
func TestOpenExistingConsumesTheMarker(t *testing.T) {
	dir := t.TempDir()
	db, _ := reopenPrior(t, dir)
	closeClean(t, db)
	x, e := OpenExisting(dir)
	if e != nil {
		t.Fatal(e)
	}
	if x.PriorShutdown() != ShutdownClean || markerExists(t, dir) {
		t.Fatal(x.PriorShutdown(), markerExists(t, dir))
	}
	x.Close()
	if _, prior := reopenPrior(t, dir); prior != ShutdownUnclean {
		t.Fatal(prior)
	}
}

// The marker names the database files as the clean owner left them, so a
// database changed or replaced afterwards, a restored snapshot above all, is
// not clean, and neither is a damaged or deleted marker.
func TestMarkerIsStaleOnceTheDatabaseChanges(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, dir, snapshot string){
		"written behind lit-server": func(t *testing.T, dir, _ string) {
			o, e := sql.Open("sqlite", "file:"+filepath.Join(dir, DatabaseFile))
			if e != nil {
				t.Fatal(e)
			}
			defer o.Close()
			if _, e = o.Exec("UPDATE projects SET title = 'feat/changed'"); e != nil {
				t.Fatal(e)
			}
		},
		"snapshot restored by rename": func(t *testing.T, dir, snapshot string) {
			if e := os.Rename(snapshot, filepath.Join(dir, DatabaseFile)); e != nil {
				t.Fatal(e)
			}
		},
		"snapshot copied over in place": func(t *testing.T, dir, snapshot string) {
			b, e := os.ReadFile(snapshot)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(dir, DatabaseFile), b, 0o600); e != nil {
				t.Fatal(e)
			}
		},
		"marker deleted": func(t *testing.T, dir, _ string) {
			if e := os.Remove(marker(dir)); e != nil {
				t.Fatal(e)
			}
		},
		"marker damaged": func(t *testing.T, dir, _ string) {
			if e := os.WriteFile(marker(dir), []byte("{"), 0o600); e != nil {
				t.Fatal(e)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			db, _ := reopenPrior(t, dir)
			mustApply(t, db, at(1), newProject(id(1), "feat/one", "", at(1)))
			snapshot, e := db.Backup(context.Background(), filepath.Join(dir, BackupDir))
			if e != nil {
				t.Fatal(e)
			}
			closeClean(t, db)
			change(t, dir, snapshot)
			if _, prior := reopenPrior(t, dir); prior != ShutdownUnclean {
				t.Fatal(prior)
			}
			if markerExists(t, dir) {
				t.Fatal("a stale marker survived the open")
			}
		})
	}
}

// After a fatal database error the database is not left clean: CloseClean
// still closes it and releases ownership, but writes no marker.
func TestCloseCleanAfterAFatalErrorLeavesNoMarker(t *testing.T) {
	dir := t.TempDir()
	db, _ := reopenPrior(t, dir)
	mustApply(t, db, at(1), newProject(id(1), "feat/one", "", at(1)))
	corruptSchema(t, db)
	r, e := db.Read(context.Background())
	if e == nil {
		_, e = r.Project(context.Background(), id(1))
		r.Close()
	}
	if !errors.Is(e, ErrFatal) {
		t.Fatal(e)
	}
	if e = db.CloseClean(); e == nil {
		t.Fatal("CloseClean recorded a clean shutdown after a fatal error")
	}
	if markerExists(t, dir) {
		t.Fatal("marker written after a fatal error")
	}
	// Ownership is released: opening again meets the damage, not an owner.
	if again, e := Open(dir); e == nil {
		again.Close()
	} else if strings.Contains(e.Error(), "already owned") {
		t.Fatal(e)
	}
}

// An offline reader, such as lit-server export on the stopped tracker,
// changes no database file, so the clean shutdown stands.
func TestReadOnlyReaderKeepsTheMarker(t *testing.T) {
	dir := t.TempDir()
	db, _ := reopenPrior(t, dir)
	mustApply(t, db, at(1), newProject(id(1), "feat/one", "", at(1)))
	closeClean(t, db)
	ro, e := OpenReadOnly(dir)
	if e != nil {
		t.Fatal(e)
	}
	r, e := ro.Read(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Project(context.Background(), id(1)); e != nil {
		t.Fatal(e)
	}
	r.Close()
	if e = ro.Close(); e != nil {
		t.Fatal(e)
	}
	if _, prior := reopenPrior(t, dir); prior != ShutdownClean {
		t.Fatal(prior)
	}
}
