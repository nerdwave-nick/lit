package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withV2 is the shipped migrations plus a later one, to exercise upgrades.
func withV2() []migration {
	return append(append([]migration{}, migrations...), migration{2, func(ctx context.Context, tx *sql.Tx) error {
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
	if db, e = open(dir, withV2()); e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if db.SchemaVersion() != 2 || db.ServiceID() != id {
		t.Fatalf("migrated to version %d service %q, want 2 %q", db.SchemaVersion(), db.ServiceID(), id)
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
	if len(snaps) != 1 || !strings.Contains(filepath.Base(snaps[0]), "pre-migration-v1") {
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
	if version != 1 || snapID != id || projects != 1 {
		t.Fatalf("snapshot holds version %d service %q with %d projects", version, snapID, projects)
	}
}

func TestNewerSchemaIsRefused(t *testing.T) {
	dir := t.TempDir()
	db, e := open(dir, withV2())
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
	for _, want := range []string{"schema version 2", "newer", "version 1"} {
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
	if db, e = open(dir, withV2()); e != nil {
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
	broken := append(withV2(), migration{3, func(ctx context.Context, tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "CREATE TABLE later (x INTEGER)")
		return e
	}})
	if db, e = open(dir, broken); e == nil || !strings.Contains(e.Error(), "version 3") {
		if db != nil {
			db.Close()
		}
		t.Fatalf("broken migration: %v", e)
	}
	if db, e = Open(dir); e != nil {
		t.Fatalf("version 1 after a failed migration: %v", e)
	}
	db.Close()
}
