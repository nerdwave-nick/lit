package storage

import (
	"context"
	"errors"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openTemp(t testing.TB) *DB {
	t.Helper()
	db, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenEmptyDirectoryCreatesFreshDatabase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data dir #1?%20")
	db, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	id := db.ServiceID()
	if !protocol.ValidUUID(id) || db.SchemaVersion() != current {
		t.Fatalf("service %q version %d", id, db.SchemaVersion())
	}
	if _, e = os.Stat(filepath.Join(dir, DatabaseFile)); e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(dir, BackupDir)); !os.IsNotExist(e) {
		t.Fatalf("fresh database took a snapshot: %v", e)
	}
	again, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer again.Close()
	if again.ServiceID() != id {
		t.Fatalf("reopened service %q, want %q", again.ServiceID(), id)
	}
	other := openTemp(t)
	if other.ServiceID() == id {
		t.Fatal("two fresh databases share a service identity")
	}
}

func TestOpenRefusesASecondOwner(t *testing.T) {
	dir := t.TempDir()
	db, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if second, e := Open(dir); e == nil || !strings.Contains(e.Error(), "already owned") {
		if second != nil {
			second.Close()
		}
		t.Fatalf("second owner: %v", e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	db, e = Open(dir)
	if e != nil {
		t.Fatalf("after release: %v", e)
	}
	db.Close()
}

func TestOpenRefusesAForeignDatabase(t *testing.T) {
	dir := t.TempDir()
	db, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.writer.Exec("DROP TABLE history; DROP TABLE transactions; DROP TABLE service"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	if db, e = Open(dir); e == nil || !strings.Contains(e.Error(), "no lit service identity") {
		if db != nil {
			db.Close()
		}
		t.Fatalf("foreign database: %v", e)
	}
}

func TestEveryConnectionUsesWALForeignKeysAndBusyTimeout(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	check := func(t *testing.T, q queries) {
		var mode string
		var fk, timeout int
		if e := q.queryRow(ctx, []any{&mode}, "PRAGMA journal_mode"); e != nil || mode != "wal" {
			t.Fatalf("journal_mode %q: %v", mode, e)
		}
		if e := q.queryRow(ctx, []any{&fk}, "PRAGMA foreign_keys"); e != nil || fk != 1 {
			t.Fatalf("foreign_keys %d: %v", fk, e)
		}
		if e := q.queryRow(ctx, []any{&timeout}, "PRAGMA busy_timeout"); e != nil || timeout != int(busyTimeout.Milliseconds()) {
			t.Fatalf("busy_timeout %d: %v", timeout, e)
		}
		// Checkpoints flush the drive cache on macOS (F_FULLFSYNC); see
		// docs/storage-format.md. The flag is a no-op on Linux.
		var full int
		if e := q.queryRow(ctx, []any{&full}, "PRAGMA checkpoint_fullfsync"); e != nil || full != 1 {
			t.Fatalf("checkpoint_fullfsync %d: %v", full, e)
		}
	}
	t.Run("write", func(t *testing.T) {
		w, e := db.LockWrites(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer w.Unlock()
		tx, e := w.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		check(t, tx.queries)
		// The writer commits with synchronous=NORMAL (1): in WAL mode a commit
		// is not fsynced, only checkpoints are; see docs/storage-format.md.
		var sync int
		if e := tx.queryRow(ctx, []any{&sync}, "PRAGMA synchronous"); e != nil || sync != 1 {
			t.Fatalf("synchronous %d: %v", sync, e)
		}
		// The writer checkpoints the WAL itself, at SQLite's default of 1,000
		// pages: a background checkpointer made writes slower (ticket 2aaa11f1).
		var pages int
		if e := tx.queryRow(ctx, []any{&pages}, "PRAGMA wal_autocheckpoint"); e != nil || pages != 1000 {
			t.Fatalf("wal_autocheckpoint %d: %v", pages, e)
		}
	})
	t.Run("read", func(t *testing.T) {
		// Hold several snapshots at once so more than one reader connection is checked.
		var open []*ReadTx
		defer func() {
			for _, r := range open {
				r.Close()
			}
		}()
		for range DefaultReadConcurrency {
			r, e := db.Read(ctx)
			if e != nil {
				t.Fatal(e)
			}
			open = append(open, r)
			check(t, r.queries)
		}
	})
}

func TestReadSnapshotsAreQueryOnly(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	r, e := db.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	if _, e = r.exec(ctx, "UPDATE service SET schema_version = 2"); e == nil {
		t.Fatal("a read snapshot wrote")
	}
}

func TestOpenRefusesALegacyFileStore(t *testing.T) {
	dir := t.TempDir()
	// Only the marker counts: nothing of the legacy store is read.
	marker := []byte("not even JSON")
	if e := os.WriteFile(filepath.Join(dir, LegacyMarker), marker, 0o600); e != nil {
		t.Fatal(e)
	}
	db, e := Open(dir)
	if e == nil {
		db.Close()
		t.Fatal("opened a legacy file store")
	}
	if !errors.Is(e, ErrLegacyStore) || !strings.Contains(e.Error(), "lit-migrate") || !strings.Contains(e.Error(), "setup import") {
		t.Fatalf("legacy store refusal: %v", e)
	}
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 1 || entries[0].Name() != LegacyMarker {
		t.Fatalf("refusal changed the directory: %v %v", entries, e)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, LegacyMarker)); string(b) != string(marker) {
		t.Fatal("refusal changed the marker")
	}
}
