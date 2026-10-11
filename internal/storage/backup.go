package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"
)

// backupStamp is the UTC time in snapshot names; names sort by it.
const backupStamp = "20060102T150405.000000Z"

// scheduledBackup matches exactly the names Backup writes. Retention removes
// only files with such a name, never anything else in the directory.
var scheduledBackup = regexp.MustCompile(`^lit-(\d{8}T\d{6}\.\d{6}Z)-scheduled\.db$`)

// Backup writes a consistent snapshot of the database with VACUUM INTO to
// dir/lit-<UTC stamp>-scheduled.db, creating dir when missing, and returns
// its path. The snapshot holds the committed state when it starts. It runs on
// a connection of its own and only reads the database, so it neither takes
// the write lock nor holds a read slot; writes and reads go on meanwhile. It
// is written under a temporary name and renamed when complete, so a failed or
// cancelled backup leaves nothing that retention could count. Its errors are
// never reported to OnFatal: a failed backup must not stop lit-server.
func (db *DB) Backup(ctx context.Context, dir string) (path string, err error) {
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := "lit-" + db.clock.Now().Format(backupStamp) + "-scheduled.db"
	path = filepath.Join(dir, name)
	if _, err = os.Lstat(path); err == nil {
		return "", fmt.Errorf("backup %s already exists", path)
	}
	partial := filepath.Join(dir, "."+name+".partial")
	defer func() {
		if err != nil {
			_ = os.Remove(partial)
		}
	}()
	c, err := sql.Open("sqlite", dsn(filepath.Join(db.dir, DatabaseFile), false))
	if err != nil {
		return "", err
	}
	defer func() { _ = c.Close() }()
	c.SetMaxOpenConns(1)
	if _, err = c.ExecContext(ctx, "VACUUM INTO ?", partial); err != nil {
		return "", fmt.Errorf("backup: %w", classify(err))
	}
	// A snapshot holds the whole tracker, like the database itself.
	if err = os.Chmod(partial, 0o600); err != nil {
		return "", err
	}
	if err = syncPath(partial); err != nil {
		return "", err
	}
	if err = os.Rename(partial, path); err != nil {
		return "", err
	}
	return path, syncPath(dir)
}

// syncPath flushes a file or directory to stable storage.
func syncPath(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	return errors.Join(f.Sync(), f.Close())
}

// scheduledBackups lists the names of the regular files in dir that Backup
// wrote, oldest first. A missing directory holds none.
func scheduledBackups(dir string) ([]string, error) {
	entries, e := os.ReadDir(dir)
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var out []string
	for _, v := range entries {
		if v.Type().IsRegular() && scheduledBackup.MatchString(v.Name()) {
			out = append(out, v.Name())
		}
	}
	slices.Sort(out)
	return out, nil
}

// PruneBackups keeps the newest keep snapshots that Backup wrote to dir and
// removes the older ones, returning their paths. Nothing else in dir is
// touched: pre-migration snapshots, other files and anything that is not a
// regular file with a scheduled snapshot's exact name stay.
func PruneBackups(dir string, keep int) ([]string, error) {
	if keep < 1 {
		return nil, fmt.Errorf("backup retention must keep at least one snapshot, not %d", keep)
	}
	list, e := scheduledBackups(dir)
	if e != nil || len(list) <= keep {
		return nil, e
	}
	var removed []string
	var errs []error
	for _, n := range list[:len(list)-keep] {
		p := filepath.Join(dir, n)
		if e = os.Remove(p); e != nil {
			errs = append(errs, e)
			continue
		}
		removed = append(removed, p)
	}
	return removed, errors.Join(errs...)
}

// NewestBackup returns the time of the newest snapshot Backup wrote to dir;
// found is false when there is none.
func NewestBackup(dir string) (at time.Time, found bool, err error) {
	list, err := scheduledBackups(dir)
	if err != nil || len(list) == 0 {
		return time.Time{}, false, err
	}
	at, err = time.Parse(backupStamp, scheduledBackup.FindStringSubmatch(list[len(list)-1])[1])
	return at, err == nil, err
}
