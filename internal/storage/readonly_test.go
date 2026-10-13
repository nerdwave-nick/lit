package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openReadOnly(t *testing.T, dir string) *ReadOnlyDB {
	t.Helper()
	ro, e := OpenReadOnly(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ro.Close() })
	return ro
}

func TestOpenReadOnlyReadsSnapshotsBesideTheRunningOwner(t *testing.T) {
	ctx := context.Background()
	owner := openTemp(t)
	commitProject(t, ctx, owner, "feat/one")
	ro := openReadOnly(t, owner.Dir())
	if ro.ServiceID() != owner.ServiceID() {
		t.Fatalf("service %q, want %q", ro.ServiceID(), owner.ServiceID())
	}
	snap, e := ro.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer snap.Close()
	// The owner keeps writing; the open snapshot keeps its point in time.
	commitProject(t, ctx, owner, "feat/two")
	if n := countProjects(t, ctx, snap.queries); n != 1 {
		t.Fatalf("snapshot sees %d projects, want 1", n)
	}
	later, e := ro.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer later.Close()
	if n := countProjects(t, ctx, later.queries); n != 2 {
		t.Fatalf("later snapshot sees %d projects, want 2", n)
	}
	if _, e = later.exec(ctx, "DELETE FROM projects"); e == nil {
		t.Fatal("a read-only snapshot wrote")
	}
	// Reading never takes the owner lock: ownership stays with the owner.
	if _, e = Open(owner.Dir()); e == nil || !strings.Contains(e.Error(), "already owned") {
		t.Fatalf("second owner: %v", e)
	}
}

func TestOpenReadOnlyWithoutARunningOwnerLeavesOwnershipFree(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	owner, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	commitProject(t, ctx, owner, "feat/one")
	if e = owner.Close(); e != nil {
		t.Fatal(e)
	}
	ro := openReadOnly(t, dir)
	snap, e := ro.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer snap.Close()
	if n := countProjects(t, ctx, snap.queries); n != 1 {
		t.Fatalf("snapshot sees %d projects, want 1", n)
	}
	// An owner can start while the reader is open.
	again, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer again.Close()
	commitProject(t, ctx, again, "feat/two")
	if n := countProjects(t, ctx, snap.queries); n != 1 {
		t.Fatalf("snapshot sees %d projects after the owner wrote, want 1", n)
	}
}

func TestOpenReadOnlyRefusesWhatItCannotRead(t *testing.T) {
	t.Run("missing directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "absent")
		if _, e := OpenReadOnly(dir); e == nil || !strings.Contains(e.Error(), "no lit database") {
			t.Fatalf("missing directory: %v", e)
		}
		if _, e := os.Stat(dir); !os.IsNotExist(e) {
			t.Fatalf("read-only open created the directory: %v", e)
		}
	})
	t.Run("empty directory", func(t *testing.T) {
		dir := t.TempDir()
		if _, e := OpenReadOnly(dir); e == nil || !strings.Contains(e.Error(), "no lit database") {
			t.Fatalf("empty directory: %v", e)
		}
		if names, _ := os.ReadDir(dir); len(names) != 0 {
			t.Fatalf("read-only open wrote %v", names)
		}
	})
	t.Run("legacy store", func(t *testing.T) {
		dir := t.TempDir()
		if e := os.WriteFile(filepath.Join(dir, LegacyMarker), []byte("{}"), 0o600); e != nil {
			t.Fatal(e)
		}
		if _, e := OpenReadOnly(dir); !errors.Is(e, ErrLegacyStore) {
			t.Fatalf("legacy store: %v", e)
		}
	})
	t.Run("other schema version", func(t *testing.T) {
		owner := openTemp(t)
		if e := write(t, owner, func(ctx context.Context, tx *WriteTx) error {
			_, e := tx.exec(ctx, "UPDATE service SET schema_version = schema_version + 1")
			return e
		}); e != nil {
			t.Fatal(e)
		}
		if _, e := OpenReadOnly(owner.Dir()); e == nil || !strings.Contains(e.Error(), fmt.Sprintf("schema version %d", current+1)) {
			t.Fatalf("newer schema: %v", e)
		}
	})
}

func TestNewestTransactionNamesTheLastCommittedRequest(t *testing.T) {
	db := openTemp(t)
	read(t, db, func(ctx context.Context, r *ReadTx) {
		if _, found, e := r.NewestTransaction(ctx); e != nil || found {
			t.Fatalf("empty database: %v %v", found, e)
		}
	})
	p := newProject(id(1), "feat/one", "", at(1))
	mustApply(t, db, at(1), p)
	p2 := next(p, at(2))
	p2.Description = "changed"
	second := request(t, at(2), p2)
	mustApply(t, db, at(2), p2)
	read(t, db, func(ctx context.Context, r *ReadTx) {
		got, found, e := r.NewestTransaction(ctx)
		if e != nil || !found {
			t.Fatalf("newest: %v %v", found, e)
		}
		if got.RequestHash != second.Hash || got.Timestamp != at(2) {
			t.Fatalf("newest %+v, want %s at %s", got, second.Hash, at(2))
		}
	})
}
