package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/nerdwave-nick/lit/internal/storage"
)

const (
	// DefaultBackupInterval separates scheduled database snapshots.
	DefaultBackupInterval = 24 * time.Hour
	// DefaultBackupKeep is how many scheduled snapshots are kept.
	DefaultBackupKeep = 14
)

// Duration is a time.Duration written in JSON as a Go duration string, such
// as "24h" or "90m".
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if e := json.Unmarshal(b, &s); e != nil {
		return fmt.Errorf("a duration must be a string such as \"24h\": %w", e)
	}
	v, e := time.ParseDuration(s)
	if e != nil {
		return e
	}
	*d = Duration(v)
	return nil
}

// backupEnvironment applies LIT_BACKUP_DIR, LIT_BACKUP_INTERVAL and
// LIT_BACKUP_KEEP over the values from the JSON file.
func (c *Config) backupEnvironment() error {
	if v := os.Getenv("LIT_BACKUP_DIR"); v != "" {
		c.BackupDir = v
	}
	if v := os.Getenv("LIT_BACKUP_INTERVAL"); v != "" {
		d, e := time.ParseDuration(v)
		if e != nil {
			return fmt.Errorf("invalid LIT_BACKUP_INTERVAL %q: a Go duration such as 24h, or 0 for no scheduled backups", v)
		}
		c.BackupInterval = Duration(d)
	}
	if v := os.Getenv("LIT_BACKUP_KEEP"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil {
			return fmt.Errorf("invalid LIT_BACKUP_KEEP %q", v)
		}
		c.BackupKeep = n
	}
	return nil
}

// validateBackups checks the backup settings once the data directory is
// final; an unset backup directory becomes <data dir>/backup.
func (c *Config) validateBackups() error {
	if c.BackupDir == "" {
		c.BackupDir = filepath.Join(c.DataDir, storage.BackupDir)
	}
	if c.BackupInterval < 0 {
		return fmt.Errorf("backup_interval must not be negative (0 turns scheduled backups off)")
	}
	if c.BackupKeep < 1 {
		return fmt.Errorf("backup_keep must be a positive integer")
	}
	return nil
}
