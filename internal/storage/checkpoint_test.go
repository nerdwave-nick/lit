package storage

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// walProgress reads the WAL index (lit.db-shm, https://sqlite.org/walformat.html):
// the frames in the WAL (mxFrame) and how many of them a checkpoint has
// copied into the database file (nBackfill). Both are native-endian.
func walProgress(t *testing.T, db *DB) (frames, backfilled uint32) {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(db.dir, DatabaseFile+"-shm"))
	if e != nil || len(b) < 100 {
		t.Fatalf("WAL index: %d bytes, %v", len(b), e)
	}
	return binary.NativeEndian.Uint32(b[16:]), binary.NativeEndian.Uint32(b[96:])
}

// writePages commits one transaction that appends about n pages to the WAL.
func writePages(t *testing.T, ctx context.Context, w *Writer, n int) {
	t.Helper()
	tx, e := w.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.exec(ctx, "CREATE TABLE IF NOT EXISTS scratch (b BLOB)"); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.exec(ctx, "INSERT INTO scratch VALUES (zeroblob(?))", n*4096); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
}

// A commit never checkpoints the WAL while it holds the write lock: once the
// WAL holds checkpointPages written pages, the checkpoint runs on its own
// connection after the write lock is released.
func TestCheckpointsRunOutsideTheWriteLock(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	w, e := db.LockWrites(ctx)
	if e != nil {
		t.Fatal(e)
	}
	writePages(t, ctx, w, checkpointPages+100)
	frames, backfilled := walProgress(t, db)
	if frames < checkpointPages || backfilled != 0 {
		t.Fatalf("after the commit, under the write lock: %d frames, %d checkpointed; want no checkpoint yet", frames, backfilled)
	}
	w.Unlock()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if frames, backfilled = walProgress(t, db); backfilled == frames {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no checkpoint after the write lock was released: %d frames, %d checkpointed", frames, backfilled)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Below checkpointPages written pages, no checkpoint runs.
func TestCheckpointsWaitForEnoughPages(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	w, e := db.LockWrites(ctx)
	if e != nil {
		t.Fatal(e)
	}
	writePages(t, ctx, w, checkpointPages/4)
	w.Unlock()
	time.Sleep(200 * time.Millisecond)
	if frames, backfilled := walProgress(t, db); frames == 0 || backfilled != 0 {
		t.Fatalf("%d frames, %d checkpointed; want no checkpoint", frames, backfilled)
	}
}
