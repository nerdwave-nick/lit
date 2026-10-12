// Package storage owns lit-server's SQLite database: its schema, schema
// migrations and every query. The service keeps the business rules.
//
// One process owns a database, enforced by an exclusive lock file next to it.
// Writes are serialized: a Writer proves the caller holds the process write
// lock, and only a Writer begins a write transaction (BEGIN IMMEDIATE). Reads
// run in parallel, each in one WAL snapshot (ReadTx) covering a whole handler.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

const (
	// DatabaseFile and LockFile are the names inside the data directory.
	DatabaseFile = "lit.db"
	LockFile     = "lit.db.lock"
	// BackupDir is the default directory, inside the data directory, for
	// database snapshots such as the one taken before a schema migration.
	BackupDir = "backup"
	// busyTimeout bounds how long a statement waits for a SQLite lock before
	// failing with ErrBusy. The Go write lock normally makes waits rare.
	busyTimeout = 5 * time.Second
	// DefaultReadConcurrency is the default cap on concurrent read snapshots.
	DefaultReadConcurrency = 8
	// LegacyMarker is the identity file of the legacy file store. A data
	// directory holding it is refused; nothing else of that store is read.
	LegacyMarker = "service.json"
)

// ErrLegacyStore matches (with errors.Is) the refusal to open a data
// directory that still holds a legacy file store.
var ErrLegacyStore = errors.New("data directory holds a legacy lit file store")

// DB is an open, exclusively owned lit database.
type DB struct {
	dir       string
	lockFile  *os.File
	writer    *sql.DB // exactly one connection, used under the write lock
	readers   *sql.DB // query-only connections for read snapshots
	writeLock chan struct{}
	clock     *Clock
	serviceID string
	version   int
	fatal     atomic.Pointer[func(error)] // see OnFatal
}

// Open takes ownership of the data directory dir, creating it when missing,
// and opens its database. An empty directory gets a fresh database with a new
// service identity. An older schema is migrated after a pre-migration snapshot
// in dir/backup (Options.BackupDir with OpenWith); a newer schema than this
// binary knows is refused. A second owner fails with an error containing
// "already owned". A directory that still holds a legacy file store (its
// LegacyMarker) is refused with ErrLegacyStore before anything is written to
// it.
func Open(dir string) (*DB, error) { return OpenWith(dir, Options{}) }

// Options configure an opened database; the zero value uses the defaults.
type Options struct {
	// ReadConcurrency caps concurrent read snapshots; further reads wait for
	// one. Zero means DefaultReadConcurrency.
	ReadConcurrency int
	// BackupDir receives the snapshot taken before a schema migration.
	// Empty means BackupDir inside the data directory.
	BackupDir string
}

// OpenWith opens the data directory dir like Open, configured by o.
func OpenWith(dir string, o Options) (*DB, error) { return openWith(dir, migrations, o, false) }

// OpenExisting takes ownership of the data directory dir and opens its
// existing database for checks such as Verify. Unlike Open, it never creates a
// database and never migrates one: a missing database, or a schema version
// other than this binary's, is refused.
func OpenExisting(dir string) (*DB, error) { return openWith(dir, migrations, Options{}, true) }

func open(dir string, ms []migration, existing bool) (*DB, error) {
	return openWith(dir, ms, Options{}, existing)
}

func openWith(dir string, ms []migration, o Options, existing bool) (db *DB, err error) {
	reads := o.ReadConcurrency
	if reads == 0 {
		reads = DefaultReadConcurrency
	}
	if reads < 0 {
		return nil, fmt.Errorf("read concurrency must be positive, not %d", reads)
	}
	if err = refuseLegacyStore(dir); err != nil {
		return nil, err
	}
	if existing {
		if _, err = os.Stat(filepath.Join(dir, DatabaseFile)); err != nil {
			return nil, fmt.Errorf("no lit database in %s: %w", dir, err)
		}
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if dir, err = filepath.Abs(dir); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, LockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = lock(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("storage is already owned: %w", err)
	}
	db = &DB{dir: dir, lockFile: f, writeLock: make(chan struct{}, 1), clock: NewClock(time.Now)}
	owned := db
	defer func() {
		if err != nil {
			owned.Close()
		}
	}()
	path := filepath.Join(dir, DatabaseFile)
	if db.writer, err = sql.Open("sqlite", dsn(path, false)); err != nil {
		return nil, err
	}
	db.writer.SetMaxOpenConns(1)
	db.writer.SetMaxIdleConns(1)
	db.writer.SetConnMaxLifetime(0)
	if existing {
		err = requireCurrent(context.Background(), db.writer, ms)
	} else {
		backupDir := o.BackupDir
		if backupDir == "" {
			backupDir = filepath.Join(dir, BackupDir)
		}
		err = migrate(context.Background(), db.writer, ms, backupDir, db.clock.Now())
	}
	if err != nil {
		return nil, err
	}
	if err = db.report(db.writer.QueryRow("SELECT service_id, schema_version FROM service").Scan(&db.serviceID, &db.version)); err != nil {
		return nil, err
	}
	if db.readers, err = sql.Open("sqlite", dsn(path, true)); err != nil {
		return nil, err
	}
	db.readers.SetMaxOpenConns(reads)
	db.readers.SetMaxIdleConns(reads)
	db.readers.SetConnMaxLifetime(0)
	return db, nil
}

// refuseLegacyStore detects a legacy file store by its marker file only.
func refuseLegacyStore(dir string) error {
	_, e := os.Lstat(filepath.Join(dir, LegacyMarker))
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	return fmt.Errorf("%w in %s (it contains %s); lit-server no longer reads it. Convert it once with the setup import, lit-migrate, into a new, empty data directory, and start lit-server on that directory", ErrLegacyStore, dir, LegacyMarker)
}

// dsn configures every connection: WAL, enforced foreign keys and the busy
// timeout. Writer connections begin transactions with BEGIN IMMEDIATE through
// the driver's _txlock option and commit with synchronous=NORMAL: in WAL mode a
// commit is not fsynced, only checkpoints are, so power loss can lose the last
// commits but never corrupts the database (docs/storage-format.md). Reader
// connections are query-only, and their read-only transactions begin deferred,
// which in WAL mode takes no write lock.
func dsn(path string, readOnly bool) string {
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()))
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	if readOnly {
		q.Add("_pragma", "query_only(1)")
	} else {
		q.Add("_pragma", "synchronous(NORMAL)")
		q.Set("_txlock", "immediate")
	}
	return (&url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}).String()
}

// Dir is the data directory the database lives in.
func (db *DB) Dir() string { return db.dir }

// ServiceID is the tracker's persistent identity.
func (db *DB) ServiceID() string { return db.serviceID }

// SchemaVersion is the database's schema version after opening.
func (db *DB) SchemaVersion() int { return db.version }

// Close closes the database and releases ownership. Callers stop issuing
// requests first; open transactions must be finished.
func (db *DB) Close() error {
	var errs []error
	for _, p := range []*sql.DB{db.readers, db.writer} {
		if p != nil {
			errs = append(errs, p.Close())
		}
	}
	db.readers, db.writer = nil, nil
	if db.lockFile != nil {
		errs = append(errs, unlock(db.lockFile), db.lockFile.Close())
		db.lockFile = nil
	}
	return errors.Join(errs...)
}
