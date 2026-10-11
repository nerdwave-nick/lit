package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// ReadOnlyDB reads a lit database without owning it, for offline commands
// such as the Markdown export. It never takes the owner lock, never creates,
// migrates or writes the database, and works whether or not lit-server runs:
// its connections are read-only (mode=ro, query_only), and each ReadTx is one
// WAL snapshot, isolated from the owner's later commits by SQLite itself.
type ReadOnlyDB struct {
	readers   *sql.DB
	clock     *Clock
	serviceID string
}

// TransactionStamp identifies one committed request by its hash and time.
type TransactionStamp struct {
	RequestHash string `json:"request_hash"`
	Timestamp   string `json:"timestamp"`
}

// OpenReadOnly opens the database in the data directory dir for reading.
// A directory without a database is refused without creating anything, as is
// a legacy file store (ErrLegacyStore) and a schema version other than the
// one this binary knows: an owner migrates, a reader does not.
func OpenReadOnly(dir string) (db *ReadOnlyDB, err error) {
	if err = refuseLegacyStore(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, DatabaseFile)
	if _, err = os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s holds no lit database (%s); start lit-server on it first", dir, DatabaseFile)
	} else if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()))
	q.Add("_pragma", "query_only(1)")
	readers, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}).String())
	if err != nil {
		return nil, err
	}
	readers.SetMaxOpenConns(DefaultReadConcurrency)
	readers.SetMaxIdleConns(DefaultReadConcurrency)
	readers.SetConnMaxLifetime(0)
	db = &ReadOnlyDB{readers: readers, clock: NewClock(time.Now)}
	defer func() {
		if err != nil {
			_ = readers.Close()
		}
	}()
	var version int
	if err = classify(readers.QueryRow("SELECT service_id, schema_version FROM service").Scan(&db.serviceID, &version)); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if latest := migrations[len(migrations)-1].version; version != latest {
		return nil, fmt.Errorf("database schema version %d differs from the version %d this binary reads; start the matching lit-server first", version, latest)
	}
	return db, nil
}

// ServiceID is the tracker's persistent identity.
func (db *ReadOnlyDB) ServiceID() string { return db.serviceID }

// Read samples the clock, then begins one read snapshot; see DB.Read.
func (db *ReadOnlyDB) Read(ctx context.Context) (*ReadTx, error) {
	now := db.clock.Now()
	tx, e := db.readers.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, classify(e)
	}
	r := &ReadTx{queries{tx}, now}
	// A deferred transaction takes its snapshot at the first read; take it now.
	var v int
	if e = r.queryRow(ctx, []any{&v}, "SELECT schema_version FROM service"); e != nil {
		_ = tx.Rollback()
		return nil, e
	}
	return r, nil
}

// Close closes the connections; open snapshots must be closed first.
func (db *ReadOnlyDB) Close() error { return db.readers.Close() }

// NewestTransaction names the newest committed request; found is false when
// none has committed.
func (q queries) NewestTransaction(ctx context.Context) (s TransactionStamp, found bool, err error) {
	e := q.queryRow(ctx, []any{&s.RequestHash, &s.Timestamp}, "SELECT request_hash, timestamp FROM transactions ORDER BY seq DESC LIMIT 1")
	if errors.Is(e, sql.ErrNoRows) {
		return TransactionStamp{}, false, nil
	}
	return s, e == nil, e
}
