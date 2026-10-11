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
	// readConns caps concurrent read snapshots; further reads wait for one.
	readConns = 8
)

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
}

// Open takes ownership of the data directory dir, creating it when missing,
// and opens its database. An empty directory gets a fresh database with a new
// service identity. An older schema is migrated after a pre-migration snapshot
// in dir/backup; a newer schema than this binary knows is refused. A second
// owner fails with an error containing "already owned".
func Open(dir string) (*DB, error) { return open(dir, migrations) }

func open(dir string, ms []migration) (db *DB, err error) {
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
	if err = migrate(context.Background(), db.writer, ms, filepath.Join(dir, BackupDir), db.clock.Now()); err != nil {
		return nil, err
	}
	if err = classify(db.writer.QueryRow("SELECT service_id, schema_version FROM service").Scan(&db.serviceID, &db.version)); err != nil {
		return nil, err
	}
	if db.readers, err = sql.Open("sqlite", dsn(path, true)); err != nil {
		return nil, err
	}
	db.readers.SetMaxOpenConns(readConns)
	db.readers.SetMaxIdleConns(readConns)
	db.readers.SetConnMaxLifetime(0)
	return db, nil
}

// dsn configures every connection: WAL, enforced foreign keys and the busy
// timeout. Writer connections begin transactions with BEGIN IMMEDIATE through
// the driver's _txlock option; reader connections are query-only, and their
// read-only transactions begin deferred, which in WAL mode takes no write lock.
func dsn(path string, readOnly bool) string {
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()))
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	if readOnly {
		q.Add("_pragma", "query_only(1)")
	} else {
		q.Set("_txlock", "immediate")
	}
	return (&url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}).String()
}

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
