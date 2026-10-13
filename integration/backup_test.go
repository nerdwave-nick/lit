package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/storage"
)

var scheduledBackup = regexp.MustCompile(`^lit-\d{8}T\d{6}\.\d{6}Z-scheduled\.db$`)

// scheduledBackups lists the scheduled snapshots in dir, oldest first.
func scheduledBackups(t *testing.T, dir string) []string {
	t.Helper()
	entries, e := os.ReadDir(dir)
	if e != nil && !os.IsNotExist(e) {
		t.Fatal(e)
	}
	var out []string
	for _, v := range entries {
		if scheduledBackup.MatchString(v.Name()) {
			out = append(out, v.Name())
		}
	}
	return out
}

// lit-server takes scheduled backups into LIT_BACKUP_DIR while it serves and
// keeps LIT_BACKUP_KEEP of them, leaving other files alone. A snapshot
// restored as docs/recovery.md describes (stop, replace the database,
// lit-server verify, start) passes verify, which leaves it clean, and serves
// the tracker as it was.
func TestScheduledBackupRestores(t *testing.T) {
	root := t.TempDir()
	data, state, backup := filepath.Join(root, "data"), filepath.Join(root, "state"), filepath.Join(root, "backup")
	if e := os.MkdirAll(backup, 0o700); e != nil {
		t.Fatal(e)
	}
	notes := filepath.Join(backup, "notes.txt")
	if e := os.WriteFile(notes, []byte("operator notes"), 0o600); e != nil {
		t.Fatal(e)
	}
	env := []string{"LIT_BACKUP_DIR=" + backup, "LIT_BACKUP_INTERVAL=100ms", "LIT_BACKUP_KEEP=2"}
	s := start(t, data, env...)
	run(t, root, state, 0, "connect", "--endpoint", s.endpoint, "--format", "json", "--actor-name", "Tester")
	run(t, root, state, 0, "projects", "create", "--project-title", "feat/backup")
	run(t, root, state, 0, "session", "set", "--project", "feat/backup")
	run(t, root, state, 0, "issues", "create", "--issue", "Kept", "--content", "one\ntwo\n")
	run(t, root, state, 0, "issues", "update", "title:Kept", "--content", "one\nthree\n")
	written := strings.Count(s.log.String(), "backup written")
	deadline := time.Now().Add(20 * time.Second)
	for strings.Count(s.log.String(), "backup written") < written+3 {
		if time.Now().After(deadline) {
			t.Fatalf("no scheduled backups: %s", s.log)
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.stop(t)
	if strings.Contains(s.log.String(), "failed") {
		t.Fatal(s.log.String())
	}
	kept := scheduledBackups(t, backup)
	if len(kept) != 2 {
		t.Fatalf("kept %v, want 2: %s", kept, s.log)
	}
	if b, e := os.ReadFile(notes); e != nil || string(b) != "operator notes" {
		t.Fatal("retention touched notes.txt", e)
	}

	// Restore the newest snapshot over the tracker, after damaging it.
	snapshot, e := os.ReadFile(filepath.Join(backup, kept[1]))
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range []string{storage.DatabaseFile, storage.DatabaseFile + "-wal", storage.DatabaseFile + "-shm"} {
		if e = os.Remove(filepath.Join(data, n)); e != nil && !os.IsNotExist(e) {
			t.Fatal(e)
		}
	}
	if e = os.WriteFile(filepath.Join(data, storage.DatabaseFile), snapshot, 0o600); e != nil {
		t.Fatal(e)
	}
	if code, out := verifyBinary(t, data); code != 0 || !strings.Contains(out, "verified") {
		t.Fatal(code, out)
	}
	s = start(t, data, "LIT_BACKUP_DIR="+backup, "LIT_BACKUP_INTERVAL=0")
	if code, errCode, msg := errorCode(t, s, "GET", "/v1/meta"); code != 200 {
		t.Fatal(code, errCode, msg, s.log)
	}
	// verify ran the full scan on the restored database and recorded it.
	if !strings.Contains(s.log.String(), scanSkipped) {
		t.Fatal(s.log.String())
	}
	got := item(run(t, root, state, 0, "issues", "get", "title:Kept", "--endpoint", s.endpoint, "--format", "json"))
	if got["body"] != "one\nthree\n" {
		t.Fatalf("restored issue %v", got)
	}
	s.stop(t)
}
