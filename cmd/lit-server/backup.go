package main

import (
	"context"
	"fmt"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/service"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// startJobs starts lit-server's periodic jobs for the service s started
// with the configuration c, and returns the function that stops them all.
// Degraded mode runs none: a backup of a damaged database would push good
// snapshots out of retention.
func startJobs(s started, c service.Config, logf func(format string, args ...any)) (stop func()) {
	if s.db == nil || s.fatal == nil {
		return func() {}
	}
	var stops []func()
	if job, ok := backupJob(s.db, c, time.Now(), logf); ok {
		stops = append(stops, job.start(logf))
	}
	return func() {
		for _, f := range stops {
			f()
		}
	}
}

// backupJob is the scheduled backup: a VACUUM INTO snapshot into
// c.BackupDir every c.BackupInterval, then retention of the newest
// c.BackupKeep scheduled snapshots. ok is false when the interval is zero.
func backupJob(db *storage.DB, c service.Config, now time.Time, logf func(format string, args ...any)) (job periodic, ok bool) {
	every := time.Duration(c.BackupInterval)
	if every <= 0 {
		logf("lit-server: scheduled backups are off (backup_interval 0)")
		return periodic{}, false
	}
	newest, found, e := storage.NewestBackup(c.BackupDir)
	if e != nil {
		logf("lit-server: cannot read the backup directory %s: %v", c.BackupDir, e)
	}
	first := firstBackupDelay(newest, found, every, now)
	logf("lit-server: scheduled backups every %s into %s, keeping %d; the next in %s", every, c.BackupDir, c.BackupKeep, first.Round(time.Second))
	return periodic{name: "scheduled backup", first: first, every: every, run: func(ctx context.Context) error {
		path, e := db.Backup(ctx, c.BackupDir)
		if e != nil {
			return e
		}
		removed, e := storage.PruneBackups(c.BackupDir, c.BackupKeep)
		logf("lit-server: backup written to %s; removed %d older scheduled backups", path, len(removed))
		if e != nil {
			return fmt.Errorf("removing older backups: %w", e)
		}
		return nil
	}}, true
}

// firstBackupDelay schedules the first backup one interval after the newest
// scheduled snapshot on disk, so restarts never postpone backups: at once
// when there is none or it is due, never later than one interval from now.
func firstBackupDelay(newest time.Time, found bool, every time.Duration, now time.Time) time.Duration {
	if !found {
		return 0
	}
	return min(max(newest.Add(every).Sub(now), 0), every)
}
