package storage

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
	"time"
)

// restore places the snapshot as the database of a new data directory, as
// docs/recovery.md describes, and returns that directory.
func restore(t *testing.T, snapshot string) string {
	t.Helper()
	b, e := os.ReadFile(snapshot)
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	if e = os.WriteFile(filepath.Join(dir, DatabaseFile), b, 0o600); e != nil {
		t.Fatal(e)
	}
	return dir
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, e := os.ReadDir(dir)
	if e != nil {
		t.Fatal(e)
	}
	var out []string
	for _, v := range entries {
		out = append(out, v.Name())
	}
	return out
}

// A scheduled snapshot restored into a data directory passes the startup
// checks and verify, and holds the same tracker.
func TestBackupSnapshotRestoresAndVerifies(t *testing.T) {
	w := verifyWorld(t)
	ctx := context.Background()
	backupDir := filepath.Join(t.TempDir(), "nested", "backup")
	path, e := w.db.Backup(ctx, backupDir)
	if e != nil {
		t.Fatal(e)
	}
	if filepath.Dir(path) != backupDir || !regexp.MustCompile(`^lit-\d{8}T\d{6}\.\d{6}Z-scheduled\.db$`).MatchString(filepath.Base(path)) {
		t.Fatalf("snapshot written to %s", path)
	}
	if got := names(t, backupDir); !slices.Equal(got, []string{filepath.Base(path)}) {
		t.Fatalf("backup directory holds %v, want only the snapshot", got)
	}
	want := verify(t, w.db)

	dir := restore(t, path)
	db, e := OpenExisting(dir)
	if e != nil {
		t.Fatal(e)
	}
	var log logLines
	r, e := db.Verify(ctx, log.logf)
	if e != nil || len(r.Problems) != 0 || r.Records != want.Records || r.Revisions != want.Revisions {
		t.Fatalf("verify of the restored snapshot: %+v %v, want %d records and %d revisions", r, e, want.Records, want.Revisions)
	}
	if db.ServiceID() != w.db.ServiceID() {
		t.Fatalf("restored service %s, want %s", db.ServiceID(), w.db.ServiceID())
	}
	db.Close()
	if db, e = Open(dir); e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = db.CheckIntegrity(ctx, log.logf); e != nil {
		t.Fatal(e)
	}
}

// A snapshot needs neither the write lock nor a quiet database: it is taken
// while a write transaction is open and holds only committed data.
func TestBackupRunsBesideAnOpenWrite(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	commitProject(t, ctx, db, "feat/committed")
	w, e := db.LockWrites(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Unlock()
	tx, e := w.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = addProject(ctx, tx.queries, "feat/uncommitted"); e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	var path string
	go func() {
		defer close(done)
		bctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		path, e = db.Backup(bctx, filepath.Join(t.TempDir(), "backup"))
	}()
	<-done
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	restored, e := OpenExisting(restore(t, path))
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	r, e := restored.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	if n := countProjects(t, ctx, r.queries); n != 1 {
		t.Fatalf("snapshot holds %d projects, want the committed one", n)
	}
}

// A backup that fails reports it and leaves nothing behind in the backup
// directory; the database keeps serving.
func TestFailedBackupLeavesNoFile(t *testing.T) {
	db := openTemp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := filepath.Join(t.TempDir(), "backup")
	if path, e := db.Backup(ctx, dir); e == nil {
		t.Fatalf("cancelled backup wrote %s", path)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("failed backup left %v", entries)
	}
	if os.Geteuid() != 0 {
		if e := os.Chmod(dir, 0o500); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { os.Chmod(dir, 0o700) })
		if path, e := db.Backup(context.Background(), dir); e == nil {
			t.Fatalf("backup into a read-only directory wrote %s", path)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Fatalf("failed backup left %v", entries)
		}
	}
	commitProject(t, context.Background(), db, "feat/still-writable")
}

// Retention removes only the oldest scheduled snapshots beyond keep; every
// other file in the directory, including pre-migration snapshots and names
// that merely resemble a scheduled snapshot, is left alone.
func TestPruneBackupsRemovesOnlyOldScheduledSnapshots(t *testing.T) {
	dir := t.TempDir()
	scheduled := []string{
		"lit-20261001T000000.000000Z-scheduled.db",
		"lit-20261002T000000.000000Z-scheduled.db",
		"lit-20261003T000000.000000Z-scheduled.db",
		"lit-20261004T000000.000000Z-scheduled.db",
		"lit-20261005T000000.000000Z-scheduled.db",
	}
	others := []string{
		"lit-20260901T000000.000000Z-pre-migration-v1.db",
		"lit-20260901T000000.000000Z-scheduled.db.bak",
		"xlit-20260901T000000.000000Z-scheduled.db",
		"lit-20260901T000000Z-scheduled.db",
		".lit-20260901T000000.000000Z-scheduled.db.partial",
		"notes.txt",
	}
	for _, n := range append(append([]string{}, scheduled...), others...) {
		if e := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); e != nil {
			t.Fatal(e)
		}
	}
	// A directory and a symlink with a scheduled name are not snapshots it wrote.
	if e := os.Mkdir(filepath.Join(dir, "lit-20260902T000000.000000Z-scheduled.db"), 0o700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("notes.txt", filepath.Join(dir, "lit-20260903T000000.000000Z-scheduled.db")); e != nil {
		t.Fatal(e)
	}
	removed, e := PruneBackups(dir, 2)
	if e != nil {
		t.Fatal(e)
	}
	var want []string
	for _, n := range scheduled[:3] {
		want = append(want, filepath.Join(dir, n))
	}
	if !slices.Equal(removed, want) {
		t.Fatalf("removed %v, want %v", removed, want)
	}
	left := append(append(append([]string{}, scheduled[3:]...), others...), "lit-20260902T000000.000000Z-scheduled.db", "lit-20260903T000000.000000Z-scheduled.db")
	slices.Sort(left)
	if got := names(t, dir); !slices.Equal(got, left) {
		t.Fatalf("left %v, want %v", got, left)
	}
	if removed, e = PruneBackups(dir, 2); e != nil || len(removed) != 0 {
		t.Fatalf("second prune removed %v: %v", removed, e)
	}
	for _, keep := range []int{0, -1} {
		if _, e = PruneBackups(dir, keep); e == nil {
			t.Fatalf("keep %d accepted", keep)
		}
	}
	if removed, e = PruneBackups(filepath.Join(dir, "missing"), 2); e != nil || len(removed) != 0 {
		t.Fatalf("missing directory: %v %v", removed, e)
	}
}

// Real snapshots are pruned by age, keeping the newest.
func TestBackupsThenPruneKeepTheNewest(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "backup")
	var paths []string
	for range 4 {
		p, e := db.Backup(ctx, dir)
		if e != nil {
			t.Fatal(e)
		}
		paths = append(paths, p)
	}
	if _, e := PruneBackups(dir, 3); e != nil {
		t.Fatal(e)
	}
	var want []string
	for _, p := range paths[1:] {
		want = append(want, filepath.Base(p))
	}
	if got := names(t, dir); !slices.Equal(got, want) {
		t.Fatalf("kept %v, want %v", got, want)
	}
}

// NewestBackup reports the time of the newest scheduled snapshot only.
func TestNewestBackup(t *testing.T) {
	dir := t.TempDir()
	if _, ok, e := NewestBackup(filepath.Join(dir, "missing")); ok || e != nil {
		t.Fatal("missing directory", ok, e)
	}
	for _, n := range []string{"lit-20261001T120000.000000Z-scheduled.db", "lit-20261003T120000.250000Z-scheduled.db", "lit-20261009T000000.000000Z-pre-migration-v1.db", "notes.txt"} {
		if e := os.WriteFile(filepath.Join(dir, n), nil, 0o600); e != nil {
			t.Fatal(e)
		}
	}
	at, ok, e := NewestBackup(dir)
	want := time.Date(2026, 10, 3, 12, 0, 0, 250000000, time.UTC)
	if e != nil || !ok || !at.Equal(want) {
		t.Fatalf("newest %v %v %v, want %v", at, ok, e, want)
	}
}

// The pre-migration snapshot goes to the configured backup directory.
func TestMigrationSnapshotUsesTheConfiguredBackupDirectory(t *testing.T) {
	dir := t.TempDir()
	db, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	backupDir := filepath.Join(t.TempDir(), "elsewhere")
	if db, e = openWith(dir, withV2(), Options{BackupDir: backupDir}, false); e != nil {
		t.Fatal(e)
	}
	db.Close()
	if got := names(t, backupDir); len(got) != 1 || !regexp.MustCompile(`^lit-.*-pre-migration-v1\.db$`).MatchString(got[0]) {
		t.Fatalf("configured backup directory holds %v", got)
	}
	if b := backups(t, dir); len(b) != 0 {
		t.Fatalf("default backup directory holds %v", b)
	}
}
