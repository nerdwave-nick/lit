package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/storage"
)

// identity is a record's ID, revision, update stamp and body.
func identity(r any) (id string, rev int64, updated, body string) {
	switch p := r.(type) {
	case protocol.Project:
		return p.ID, p.Revision, p.UpdatedAt, p.Description
	case protocol.Issue:
		return p.ID, p.Revision, p.UpdatedAt, p.Body
	case protocol.Comment:
		return p.ID, p.Revision, p.UpdatedAt, p.Body
	case protocol.Milestone:
		return p.ID, p.Revision, p.UpdatedAt, p.Body
	}
	panic(fmt.Sprintf("unknown record %T", r))
}

// manifestFile is the body SHA-256 manifest kept for audits.
type manifestFile struct {
	SchemaVersion int             `json:"schema_version"`
	ServiceID     string          `json:"service_id"`
	Source        string          `json:"source"`
	CreatedAt     string          `json:"created_at"`
	Records       []manifestEntry `json:"records"`
}

// install moves the checked database from dbDir into the target directory to
// and keeps the body manifest in to's default backup directory, where
// lit-server's backup retention leaves it (it removes only its own scheduled
// snapshots). A configured backup_dir is ignored: the import writes nothing
// outside to. The database must be
// closed: its write-ahead log is then checkpointed into the database file.
// The move never replaces a database that appeared in to meanwhile.
func install(dbDir, to, from, serviceID string, manifest []manifestEntry) (database, manifestPath string, err error) {
	src := filepath.Join(dbDir, storage.DatabaseFile)
	for _, suffix := range []string{"-wal", "-shm"} {
		info, e := os.Stat(src + suffix)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return "", "", e
		}
		if suffix == "-wal" && info.Size() > 0 {
			return "", "", fmt.Errorf("the new database's write-ahead log was not checkpointed (%d bytes)", info.Size())
		}
		if e = os.Remove(src + suffix); e != nil {
			return "", "", e
		}
	}
	now := time.Now().UTC()
	b, e := json.MarshalIndent(manifestFile{1, serviceID, from, now.Format(time.RFC3339Nano), manifest}, "", "  ")
	if e != nil {
		return "", "", e
	}
	backup := filepath.Join(to, storage.BackupDir)
	if e = os.Mkdir(backup, 0o700); e != nil {
		return "", "", e
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(backup)
		}
	}()
	manifestPath = filepath.Join(backup, "lit-migrate-"+now.Format("20060102T150405Z")+"-bodies.json")
	if e = os.WriteFile(manifestPath, append(b, '\n'), 0o600); e != nil {
		return "", "", e
	}
	database = filepath.Join(to, storage.DatabaseFile)
	// A hard link fails if the target exists, unlike a rename.
	if e = os.Link(src, database); e != nil {
		return "", "", fmt.Errorf("move the database into place: %w", e)
	}
	if e = os.Remove(src); e != nil {
		return database, manifestPath, e
	}
	return database, manifestPath, nil
}
