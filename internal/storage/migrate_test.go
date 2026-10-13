package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// current is the newest shipped schema version.
var current = len(migrations)

// withLater is the shipped migrations plus a later one (version current+1),
// to exercise upgrades.
func withLater() []migration {
	return append(append([]migration{}, migrations...), migration{current + 1, func(ctx context.Context, tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "CREATE TABLE later (x INTEGER) STRICT")
		return e
	}})
}

func backups(t *testing.T, dir string) []string {
	t.Helper()
	entries, e := os.ReadDir(filepath.Join(dir, BackupDir))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		t.Fatal(e)
	}
	var out []string
	for _, v := range entries {
		out = append(out, filepath.Join(dir, BackupDir, v.Name()))
	}
	return out
}

func TestOlderSchemaIsMigratedAfterASnapshot(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	db, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	id := db.ServiceID()
	commitProject(t, ctx, db, "feat/kept")
	db.Close()
	if db, e = open(dir, withLater(), false); e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if db.SchemaVersion() != current+1 || db.ServiceID() != id {
		t.Fatalf("migrated to version %d service %q, want %d %q", db.SchemaVersion(), db.ServiceID(), current+1, id)
	}
	r, e := db.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	var later int
	if e = r.queryRow(ctx, []any{&later}, "SELECT count(*) FROM later"); e != nil {
		t.Fatalf("migration not applied: %v", e)
	}
	if n := countProjects(t, ctx, r.queries); n != 1 {
		t.Fatalf("migration kept %d projects", n)
	}
	snaps := backups(t, dir)
	if len(snaps) != 1 || !strings.Contains(filepath.Base(snaps[0]), fmt.Sprintf("pre-migration-v%d", current)) {
		t.Fatalf("snapshots %v", snaps)
	}
	snap, e := sql.Open("sqlite", "file:"+snaps[0]+"?mode=ro")
	if e != nil {
		t.Fatal(e)
	}
	defer snap.Close()
	var version, projects int
	var snapID string
	if e = snap.QueryRow("SELECT service_id, schema_version, (SELECT count(*) FROM projects) FROM service").Scan(&snapID, &version, &projects); e != nil {
		t.Fatal(e)
	}
	if version != current || snapID != id || projects != 1 {
		t.Fatalf("snapshot holds version %d service %q with %d projects", version, snapID, projects)
	}
}

func TestNewerSchemaIsRefused(t *testing.T) {
	dir := t.TempDir()
	db, e := open(dir, withLater(), false)
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	path := filepath.Join(dir, DatabaseFile)
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	db, e = Open(dir)
	if e == nil {
		db.Close()
		t.Fatal("opened a newer schema")
	}
	for _, want := range []string{fmt.Sprintf("schema version %d", current+1), "newer", fmt.Sprintf("version %d", current)} {
		if !strings.Contains(e.Error(), want) {
			t.Fatalf("error %q lacks %q", e, want)
		}
	}
	after, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if string(after) != string(before) || len(backups(t, dir)) != 0 {
		t.Fatal("refusal changed the database or took a snapshot")
	}
	if db, e = open(dir, withLater(), false); e != nil {
		t.Fatalf("refusal left the database unusable: %v", e)
	}
	db.Close()
}

func TestFailedMigrationChangesNothing(t *testing.T) {
	dir := t.TempDir()
	db, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	broken := append(withLater(), migration{current + 2, func(ctx context.Context, tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "CREATE TABLE later (x INTEGER)")
		return e
	}})
	if db, e = open(dir, broken, false); e == nil || !strings.Contains(e.Error(), fmt.Sprintf("version %d", current+2)) {
		if db != nil {
			db.Close()
		}
		t.Fatalf("broken migration: %v", e)
	}
	if db, e = Open(dir); e != nil {
		t.Fatalf("version %d after a failed migration: %v", current, e)
	}
	db.Close()
}
