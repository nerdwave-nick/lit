package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// CleanMarker is the clean-shutdown marker in the data directory. CloseClean
// writes it after the database closed without a failure, and every open by an
// owner (Open, OpenWith, OpenExisting) reads and removes it before the
// database is used, so it never outlives the owner that consumed it: an owner
// that crashes, is killed or exits on a database failure leaves none.
//
// The marker records the identity of the database files as the clean owner
// left them (device, inode, size, modification and status change times of
// lit.db, and of lit.db-wal when it holds anything). A marker whose files no
// longer match is stale: a restored snapshot, a repair or any write from
// outside changes them, so restoring a database never inherits a marker.
// Deleting the marker forces lit-server's full integrity scan at the next
// start.
const CleanMarker = "lit.db.clean"

// markerFormat is the version of the marker's content.
const markerFormat = 1

// PriorShutdown is how the database's previous owner left it, as found by
// opening it.
type PriorShutdown int

const (
	// ShutdownUnclean means there was no current marker: the previous owner
	// crashed, was killed, exited on a failure or did not finish closing, or
	// the database changed after it closed, or the marker was deleted.
	ShutdownUnclean PriorShutdown = iota
	// ShutdownClean means the previous owner closed the database with
	// CloseClean, and its files are unchanged since.
	ShutdownClean
	// ShutdownNone means there was no database: this open created it.
	ShutdownNone
)

func (p PriorShutdown) String() string {
	switch p {
	case ShutdownClean:
		return "clean shutdown"
	case ShutdownNone:
		return "new database"
	}
	return "unclean shutdown"
}

// PriorShutdown is how the database's previous owner left it.
func (db *DB) PriorShutdown() PriorShutdown { return db.prior }

// cleanMarker is the content of the marker file.
type cleanMarker struct {
	Format   int           `json:"format"`
	Database *fileIdentity `json:"database"`
	// WAL is nil when lit.db-wal is absent or empty: then lit.db alone holds
	// the database.
	WAL *fileIdentity `json:"wal,omitempty"`
}

// fileIdentity tells one file version from another: replacing or writing the
// file changes it. The status change time cannot be set back by tools such as
// touch.
type fileIdentity struct {
	Device     uint64 `json:"device"`
	Inode      uint64 `json:"inode"`
	Size       int64  `json:"size"`
	ModTime    int64  `json:"mtime_ns"`
	ChangeTime int64  `json:"ctime_ns"`
}

// databaseFiles identifies lit.db and lit.db-wal in dir; db is nil without a
// database, wal without a WAL that holds anything.
func databaseFiles(dir string) (db, wal *fileIdentity, err error) {
	if db, err = identify(filepath.Join(dir, DatabaseFile)); err != nil {
		return nil, nil, err
	}
	if wal, err = identify(filepath.Join(dir, DatabaseFile+"-wal")); wal != nil && wal.Size == 0 {
		wal = nil
	}
	return db, wal, err
}

// identify identifies the file at path, or returns nil when there is none.
func identify(path string) (*fileIdentity, error) {
	fi, e := os.Stat(path)
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	id := statIdentity(fi)
	id.Size, id.ModTime = fi.Size(), fi.ModTime().UnixNano()
	return &id, nil
}

// takeMarker compares the marker in dir, if any, with the database files and
// removes it durably, before the database is opened. It runs under the owner
// lock.
func takeMarker(dir string) (PriorShutdown, error) {
	path := filepath.Join(dir, CleanMarker)
	content, readErr := os.ReadFile(path)
	if readErr == nil || !errors.Is(readErr, os.ErrNotExist) {
		if e := os.Remove(path); e != nil && !errors.Is(e, os.ErrNotExist) {
			return 0, fmt.Errorf("cannot remove the clean-shutdown marker %s: %w", path, e)
		}
		if e := syncPath(dir); e != nil {
			return 0, fmt.Errorf("cannot record the removal of the clean-shutdown marker %s: %w", path, e)
		}
	}
	db, wal, e := databaseFiles(dir)
	if e != nil {
		return 0, e
	}
	if db == nil {
		return ShutdownNone, nil
	}
	var m cleanMarker
	if readErr != nil || json.Unmarshal(content, &m) != nil || m.Format != markerFormat || m.Database == nil {
		return ShutdownUnclean, nil
	}
	if *m.Database != *db || (m.WAL == nil) != (wal == nil) || (wal != nil && *m.WAL != *wal) {
		return ShutdownUnclean, nil
	}
	return ShutdownClean, nil
}

// writeMarker writes the marker for the closed database in dir atomically:
// a temporary file, fsynced, renamed into place, and the directory fsynced.
func writeMarker(dir string) (err error) {
	db, wal, err := databaseFiles(dir)
	if err != nil {
		return err
	}
	if db == nil {
		return fmt.Errorf("no database in %s", dir)
	}
	content, err := json.Marshal(cleanMarker{Format: markerFormat, Database: db, WAL: wal})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+CleanMarker+"-*.partial")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(f.Name())
		}
	}()
	_, err = f.Write(append(content, '\n'))
	if err = errors.Join(err, f.Sync(), f.Close()); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(dir, CleanMarker)); err != nil {
		return err
	}
	return syncPath(dir)
}

// errNotClean refuses to record a clean shutdown after a database failure.
var errNotClean = errors.New("the database reported a failure while open")

// CloseClean closes the database like Close and, when it closed without an
// error and no read or write met a fatal error (ErrFatal) while it was open,
// writes CleanMarker before releasing ownership, so the next open finds a
// clean shutdown. lit-server calls it only after its jobs stopped and its
// HTTP server drained; lit-server verify calls it after the database
// verified. An error means no marker was written: the next start runs the
// full integrity scan.
func (db *DB) CloseClean() error {
	if db.lockFile == nil {
		return errors.New("the database is already closed")
	}
	e := db.closeConnections()
	if e == nil && db.failed.Load() {
		e = errNotClean
	}
	if e == nil {
		e = writeMarker(db.dir)
	}
	if e != nil {
		e = fmt.Errorf("clean shutdown not recorded: %w", e)
	}
	return errors.Join(e, db.release())
}
