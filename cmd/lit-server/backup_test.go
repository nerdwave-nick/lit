package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/storage"
)

var scheduledName = regexp.MustCompile(`^lit-\d{8}T\d{6}\.\d{6}Z-scheduled\.db$`)

// scheduled lists the scheduled snapshots in dir, oldest first.
func scheduled(t *testing.T, dir string) []string {
	t.Helper()
	entries, e := os.ReadDir(dir)
	if e != nil && !os.IsNotExist(e) {
		t.Fatal(e)
	}
	var out []string
	for _, v := range entries {
		if scheduledName.MatchString(v.Name()) {
			out = append(out, v.Name())
		}
	}
	return out
}

// The first scheduled backup follows the newest one on disk, so restarts do
// not postpone backups; without one it runs at once.
func TestFirstBackupDelay(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	for name, x := range map[string]struct {
		newest time.Time
		found  bool
		want   time.Duration
	}{
		"none":            {time.Time{}, false, 0},
		"recent":          {now.Add(-3 * time.Hour), true, 21 * time.Hour},
		"due":             {now.Add(-day), true, 0},
		"overdue":         {now.Add(-5 * day), true, 0},
		"from the future": {now.Add(2 * day), true, day},
	} {
		if got := firstBackupDelay(x.newest, x.found, day, now); got != x.want {
			t.Errorf("%s: %v, want %v", name, got, x.want)
		}
	}
}

// While lit-server serves, scheduled backups land in the backup directory,
// retention keeps the newest backup_keep of them and leaves other files
// alone, and the newest snapshot restores, verifies and passes the startup
// checks.
func TestScheduledBackupsWhileServing(t *testing.T) {
	data := t.TempDir()
	backupDir := filepath.Join(t.TempDir(), "backup")
	t.Setenv("LIT_BACKUP_DIR", backupDir)
	t.Setenv("LIT_BACKUP_INTERVAL", "20ms")
	t.Setenv("LIT_BACKUP_KEEP", "2")
	if e := os.MkdirAll(backupDir, 0o700); e != nil {
		t.Fatal(e)
	}
	foreign := []string{"lit-20200101T000000.000000Z-pre-migration-v1.db", "notes.txt"}
	for _, n := range foreign {
		if e := os.WriteFile(filepath.Join(backupDir, n), []byte("keep me"), 0o600); e != nil {
			t.Fatal(e)
		}
	}
	c := config(t, data)
	s, _ := startTest(t, data)
	var log lines
	stop := startJobs(s, c, log.logf)
	defer stop()
	waitFor(t, "four scheduled backups", func() bool { return strings.Count(log.String(), "backup written") >= 4 })
	stop()
	got := scheduled(t, backupDir)
	if len(got) != 2 {
		t.Fatalf("kept %v, want the newest 2\n%s", got, log.String())
	}
	for _, n := range foreign {
		if b, e := os.ReadFile(filepath.Join(backupDir, n)); e != nil || string(b) != "keep me" {
			t.Fatalf("retention touched %s: %v", n, e)
		}
	}
	if strings.Contains(log.String(), "failed") {
		t.Fatal(log.String())
	}
	s.db.Close()

	restored := t.TempDir()
	b, e := os.ReadFile(filepath.Join(backupDir, got[1]))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(restored, storage.DatabaseFile), b, 0o600); e != nil {
		t.Fatal(e)
	}
	var out, errOut bytes.Buffer
	if code := runVerify([]string{"--data-dir", restored}, &out, &errOut); code != 0 {
		t.Fatalf("verify of the restored snapshot: %d %s %s", code, out.String(), errOut.String())
	}
	if r, log := startTest(t, restored); r.fatal == nil {
		t.Fatal("restored snapshot did not pass the startup checks:", log)
	}
}

// Scheduled backups are off with a zero interval, and never run in degraded
// mode, where they would replace good snapshots with damaged ones.
func TestNoScheduledBackups(t *testing.T) {
	t.Run("interval 0", func(t *testing.T) {
		data := t.TempDir()
		t.Setenv("LIT_BACKUP_INTERVAL", "0")
		c := config(t, data)
		s, _ := startTest(t, data)
		var log lines
		stop := startJobs(s, c, log.logf)
		time.Sleep(30 * time.Millisecond)
		stop()
		if got := scheduled(t, c.BackupDir); len(got) != 0 || !strings.Contains(log.String(), "scheduled backups are off") {
			t.Fatal(got, log.String())
		}
	})
	t.Run("degraded mode", func(t *testing.T) {
		data := fresh(t, func(o *sql.DB) {
			execSQL(t, o, "INSERT INTO project_repositories (project_id, repository_ref) VALUES ('00000000-0000-4000-8000-000000000009', 'x')")
		})
		t.Setenv("LIT_BACKUP_INTERVAL", "1ms")
		c := config(t, data)
		s, _ := startTest(t, data)
		if s.fatal != nil {
			t.Fatal("not degraded")
		}
		var log lines
		stop := startJobs(s, c, log.logf)
		time.Sleep(30 * time.Millisecond)
		stop()
		if got := scheduled(t, c.BackupDir); len(got) != 0 {
			t.Fatal(got, log.String())
		}
	})
}
