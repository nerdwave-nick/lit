package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"os"
	"path/filepath"
	"time"
)

// migration upgrades the schema from version-1 to version inside the
// migration transaction. Versions are consecutive from 1.
type migration struct {
	version int
	apply   func(ctx context.Context, tx *sql.Tx) error
}

//go:embed schema_v1.sql
var schemaV1 string

//go:embed schema_v2.sql
var schemaV2 string

//go:embed schema_v2_index.sql
var schemaV2Index string

var migrations = []migration{
	{1, func(ctx context.Context, tx *sql.Tx) error { _, e := tx.ExecContext(ctx, schemaV1); return e }},
	{2, migrateGrepIndex},
}

// migrate brings the database to the newest version in ms. A fresh database
// (no schema objects) gets every migration and a new service identity. An
// older one is first snapshotted into backupDir with VACUUM INTO. A newer one
// is refused before anything is written.
func migrate(ctx context.Context, db *sql.DB, ms []migration, backupDir string, now time.Time) error {
	latest := ms[len(ms)-1].version
	v, e := schemaVersion(ctx, db)
	if e != nil {
		return e
	}
	if v > latest {
		return newerSchema(v, latest)
	}
	if v == latest {
		return nil
	}
	if v > 0 {
		if e = os.MkdirAll(backupDir, 0o700); e != nil {
			return e
		}
		name := fmt.Sprintf("lit-%s-pre-migration-v%d.db", now.Format("20060102T150405.000000Z"), v)
		if _, e = db.ExecContext(ctx, "VACUUM INTO ?", filepath.Join(backupDir, name)); e != nil {
			return fmt.Errorf("pre-migration snapshot: %w", classify(e))
		}
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return classify(e)
	}
	defer tx.Rollback()
	for _, m := range ms {
		if m.version <= v {
			continue
		}
		if e = m.apply(ctx, tx); e != nil {
			return fmt.Errorf("migrate schema to version %d: %w", m.version, classify(e))
		}
	}
	if v == 0 {
		_, e = tx.ExecContext(ctx, "INSERT INTO service (singleton, service_id, schema_version) VALUES (1, ?, ?)", protocol.UUID(), latest)
	} else {
		_, e = tx.ExecContext(ctx, "UPDATE service SET schema_version = ?", latest)
	}
	if e != nil {
		return classify(e)
	}
	return classify(tx.Commit())
}

// schemaVersion reads the stored version: 0 for a database without schema
// objects. Any other database without lit's service row is refused.
func schemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var objects, service int
	e := db.QueryRowContext(ctx, "SELECT count(*), count(*) FILTER (WHERE type = 'table' AND name = 'service') FROM sqlite_schema").Scan(&objects, &service)
	if e != nil {
		return 0, classify(e)
	}
	if objects == 0 {
		return 0, nil
	}
	var v int
	if service == 1 {
		e = db.QueryRowContext(ctx, "SELECT schema_version FROM service WHERE singleton = 1").Scan(&v)
	}
	if service != 1 || e == sql.ErrNoRows {
		return 0, fmt.Errorf("database holds no lit service identity; preserve it for host repair")
	}
	return v, classify(e)
}

func newerSchema(v, latest int) error {
	return fmt.Errorf("database schema version %d is newer than this lit-server supports (version %d); run a newer lit-server or restore a backup", v, latest)
}

// requireCurrent refuses a database without lit's schema, or with a schema
// version other than the newest in ms. It never writes.
func requireCurrent(ctx context.Context, db *sql.DB, ms []migration) error {
	latest := ms[len(ms)-1].version
	v, e := schemaVersion(ctx, db)
	switch {
	case e != nil:
		return e
	case v == 0:
		return fmt.Errorf("no lit database: the database file holds no schema")
	case v > latest:
		return newerSchema(v, latest)
	case v < latest:
		return fmt.Errorf("database schema version %d is older than this lit-server's (version %d); start lit-server on it once to migrate it", v, latest)
	}
	return nil
}
