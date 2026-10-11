package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/export"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// writeProject commits one durable request that creates the project id
// titled title, as lit-server would.
func writeProject(t *testing.T, db *storage.DB, id, title string) string {
	t.Helper()
	ctx := context.Background()
	w, e := db.LockWrites(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Unlock()
	stamp := w.Now().UTC().Format(storage.TimeFormat)
	actor := protocol.DurableActor{ClientID: "60000000-0000-4000-8000-000000000001", Name: "codex", Kind: "agent"}
	intent := protocol.Intent{SchemaVersion: 1, Operation: "transaction", ServiceID: db.ServiceID(), Actor: actor,
		Operations: []protocol.Operation{{Type: "projects", ID: id, Kind: "create"}},
		Targets:    []protocol.ObjectRef{{Type: "projects", ID: id}}}
	_, hash, e := protocol.Canonical(intent)
	if e != nil {
		t.Fatal(e)
	}
	p := protocol.Project{SchemaVersion: 1, ID: id, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp, Title: title, RepositoryRefs: []string{}, IssueIDs: []string{}}
	tx, e := w.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	results := []protocol.ChangedObject{{Type: "projects", ID: id, Revision: 1}}
	if e = tx.WriteRequest(ctx, storage.Request{Hash: hash, Intent: intent, Timestamp: stamp, Results: results}, []any{p}); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	return hash
}

// readManifest reads dir/manifest.json and the directory's inode, which an
// export changes when it switches a new tree in.
func readManifest(t *testing.T, dir string) (export.Manifest, uint64) {
	t.Helper()
	var m export.Manifest
	b, e := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &m); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(dir)
	if e != nil {
		t.Fatal(e)
	}
	return m, info.Sys().(*syscall.Stat_t).Ino
}

func lastHash(m export.Manifest) string {
	if m.LastTransaction == nil {
		return ""
	}
	return m.LastTransaction.RequestHash
}

// An interval run exports when the tracker changed since the export in the
// directory, and leaves that export untouched when the newest committed
// transaction is the manifest's last_transaction.
func TestIntervalExportRunsOnlyWhenChanged(t *testing.T) {
	data, to := t.TempDir(), filepath.Join(t.TempDir(), "export")
	db, e := storage.Open(data)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	run := func(want bool) export.Manifest {
		t.Helper()
		_, exported, e := exportDatabase(ctx, data, to, true)
		if e != nil || exported != want {
			t.Fatalf("exported %v (%v), want %v", exported, e, want)
		}
		m, _ := readManifest(t, to)
		return m
	}

	if m := run(true); m.LastTransaction != nil {
		t.Fatalf("an empty tracker has no last transaction: %+v", m.LastTransaction)
	}
	run(false) // an empty tracker, already exported

	first := writeProject(t, db, "11111111-0000-4000-8000-000000000001", "feat/one")
	m := run(true)
	if lastHash(m) != first || m.Counts.Projects != 1 {
		t.Fatalf("after a change: %+v", m)
	}
	before, inode := readManifest(t, to)
	for range 2 {
		run(false)
		after, again := readManifest(t, to)
		if after.ExportedAt != before.ExportedAt || again != inode {
			t.Fatalf("an unchanged tracker was exported again: %+v, inode %d -> %d", after, inode, again)
		}
	}

	second := writeProject(t, db, "22222222-0000-4000-8000-000000000002", "feat/two")
	if m = run(true); lastHash(m) != second || m.Counts.Projects != 2 {
		t.Fatalf("after a second change: %+v", m)
	}
	if _, again := readManifest(t, to); again == inode {
		t.Fatal("the changed export was not switched in")
	}
}

// An export of another tracker in the directory is replaced even when its
// last_transaction matches, here both empty.
func TestIntervalExportReplacesAnotherTrackersExport(t *testing.T) {
	to := filepath.Join(t.TempDir(), "export")
	ctx := context.Background()
	var ids []string
	for range 2 {
		data := t.TempDir()
		db, e := storage.Open(data)
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, db.ServiceID())
		db.Close()
		if _, exported, e := exportDatabase(ctx, data, to, true); e != nil || !exported {
			t.Fatalf("exported %v: %v", exported, e)
		}
	}
	if m, _ := readManifest(t, to); m.ServiceID != ids[1] {
		t.Fatalf("service %s, want %s", m.ServiceID, ids[1])
	}
}

// While lit-server serves with LIT_EXPORT_DIR and LIT_EXPORT_INTERVAL, the
// export appears at once and follows later changes.
func TestIntervalExportsWhileServing(t *testing.T) {
	data, to := t.TempDir(), filepath.Join(t.TempDir(), "export")
	t.Setenv("LIT_BACKUP_INTERVAL", "0")
	t.Setenv("LIT_EXPORT_DIR", to)
	t.Setenv("LIT_EXPORT_INTERVAL", "20ms")
	c := config(t, data)
	s, _ := startTest(t, data)
	var log lines
	stop := startJobs(s, c, log.logf)
	defer stop()
	waitFor(t, "the first export", func() bool { _, e := os.Stat(filepath.Join(to, "manifest.json")); return e == nil })
	hash := writeProject(t, s.db, "11111111-0000-4000-8000-000000000001", "feat/one")
	waitFor(t, "an export of the change", func() bool {
		b, _ := os.ReadFile(filepath.Join(to, "manifest.json"))
		return strings.Contains(string(b), hash)
	})
	stop()
	if strings.Contains(log.String(), "failed") || !strings.Contains(log.String(), "interval exports every 20ms into "+to) {
		t.Fatal(log.String())
	}
	if code, _ := meta(t, s.handler); code != 200 {
		t.Fatal(code)
	}
}

// A failed interval export is logged and retried; lit-server goes on
// serving, and the failure never counts as a database failure.
func TestIntervalExportFailuresDoNotAffectServing(t *testing.T) {
	data, to := t.TempDir(), t.TempDir()
	if e := os.WriteFile(filepath.Join(to, "notes.txt"), []byte("not an export"), 0o600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("LIT_BACKUP_INTERVAL", "0")
	t.Setenv("LIT_EXPORT_DIR", to)
	t.Setenv("LIT_EXPORT_INTERVAL", "10ms")
	c := config(t, data)
	s, _ := startTest(t, data)
	var log lines
	stop := startJobs(s, c, log.logf)
	defer stop()
	waitFor(t, "two failed exports", func() bool { return strings.Count(log.String(), "interval export failed") >= 2 })
	if code, _ := meta(t, s.handler); code != 200 {
		t.Fatal(code, log.String())
	}
	writeProject(t, s.db, "11111111-0000-4000-8000-000000000001", "feat/one")
	stop()
	select {
	case e := <-s.fatal:
		t.Fatal("an export failure was reported as a database failure:", e)
	default:
	}
	if b, e := os.ReadFile(filepath.Join(to, "notes.txt")); e != nil || string(b) != "not an export" {
		t.Fatal("the refused directory changed:", e)
	}
}

// Interval exports are off by default, also when only a directory is set.
func TestNoIntervalExportByDefault(t *testing.T) {
	data, to := t.TempDir(), filepath.Join(t.TempDir(), "export")
	t.Setenv("LIT_BACKUP_INTERVAL", "0")
	t.Setenv("LIT_EXPORT_DIR", "")
	t.Setenv("LIT_EXPORT_INTERVAL", "")
	if c := config(t, data); c.ExportInterval != 0 {
		t.Fatalf("default interval %v", c.ExportInterval)
	} else if _, ok := exportJob(c, (&lines{}).logf); ok {
		t.Fatal("an export job by default")
	}
	t.Setenv("LIT_EXPORT_DIR", to)
	c := config(t, data)
	s, _ := startTest(t, data)
	var log lines
	stop := startJobs(s, c, log.logf)
	time.Sleep(50 * time.Millisecond)
	stop()
	if _, e := os.Stat(to); !os.IsNotExist(e) || strings.Contains(log.String(), "export") {
		t.Fatalf("exported without an interval: %v\n%s", e, log.String())
	}
}

// Degraded mode runs no interval export, which would show damaged records.
func TestNoIntervalExportInDegradedMode(t *testing.T) {
	data := fresh(t, func(o *sql.DB) {
		execSQL(t, o, "INSERT INTO project_repositories (project_id, repository_ref) VALUES ('00000000-0000-4000-8000-000000000009', 'x')")
	})
	to := filepath.Join(t.TempDir(), "export")
	t.Setenv("LIT_BACKUP_INTERVAL", "0")
	t.Setenv("LIT_EXPORT_DIR", to)
	t.Setenv("LIT_EXPORT_INTERVAL", "1ms")
	c := config(t, data)
	s, _ := startTest(t, data)
	if s.fatal != nil {
		t.Fatal("not degraded")
	}
	var log lines
	stop := startJobs(s, c, log.logf)
	time.Sleep(30 * time.Millisecond)
	stop()
	if _, e := os.Stat(to); !os.IsNotExist(e) {
		t.Fatalf("exported in degraded mode: %v\n%s", e, log.String())
	}
}
