package storage

import (
	"context"
	"database/sql"
	"errors"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const stamp = "2026-10-11T12:00:00.000000Z"

func addProject(ctx context.Context, q queries, title string) (string, error) {
	id := protocol.UUID()
	_, e := q.exec(ctx, "INSERT INTO projects (id, revision, created_at, updated_at, title, description, folded_description) VALUES (?, 1, ?, ?, ?, '', '')", id, stamp, stamp, title)
	return id, e
}

func countProjects(t *testing.T, ctx context.Context, q queries) int {
	t.Helper()
	var n int
	if e := q.queryRow(ctx, []any{&n}, "SELECT count(*) FROM projects"); e != nil {
		t.Fatal(e)
	}
	return n
}

// outside opens a separate connection to the same database file, as another
// process would, that never waits for locks.
func outside(t *testing.T, db *DB) *sql.DB {
	t.Helper()
	o, e := sql.Open("sqlite", "file:"+filepath.Join(db.dir, DatabaseFile)+"?_txlock=immediate&_pragma=busy_timeout(0)")
	if e != nil {
		t.Fatal(e)
	}
	o.SetMaxOpenConns(1)
	t.Cleanup(func() { o.Close() })
	return o
}

func commitProject(t *testing.T, ctx context.Context, db *DB, title string) {
	t.Helper()
	w, e := db.LockWrites(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Unlock()
	tx, e := w.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = addProject(ctx, tx.queries, title); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
}

func TestWriteTransactionsBeginImmediate(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	w, e := db.LockWrites(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Unlock()
	tx, e := w.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	// Nothing was read or written yet: only BEGIN IMMEDIATE holds the write lock now.
	o := outside(t, db)
	if other, e := o.BeginTx(ctx, nil); e == nil {
		other.Rollback()
		t.Fatal("another writer began while a write transaction was open")
	}
	if e = tx.Rollback(); e != nil {
		t.Fatal(e)
	}
	other, e := o.BeginTx(ctx, nil)
	if e != nil {
		t.Fatalf("after rollback: %v", e)
	}
	other.Rollback()
}

func TestBusyErrorsAreTyped(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	if _, e := db.writer.Exec("PRAGMA busy_timeout = 20"); e != nil {
		t.Fatal(e)
	}
	o := outside(t, db)
	held, e := o.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer held.Rollback()
	w, e := db.LockWrites(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Unlock()
	_, e = w.Begin(ctx)
	var be *BusyError
	if !errors.Is(e, ErrBusy) || !errors.As(e, &be) || be.Err == nil {
		t.Fatalf("contended begin returned %v", e)
	}
	if classify(errors.New("other")) == nil || errors.Is(classify(errors.New("other")), ErrBusy) {
		t.Fatal("unrelated errors are typed busy")
	}
}

func TestOnlyAHeldWriterBeginsWrites(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	w, e := db.LockWrites(ctx)
	if e != nil {
		t.Fatal(e)
	}
	waiting, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, e = db.LockWrites(waiting); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("second writer while held: %v", e)
	}
	tx, e := w.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = w.Begin(ctx); e == nil {
		t.Fatal("a second transaction began on one writer")
	}
	if _, e = addProject(ctx, tx.queries, "feat/left-open"); e != nil {
		t.Fatal(e)
	}
	w.Unlock()
	w.Unlock()
	if _, e = w.Begin(ctx); !errors.Is(e, errNotHeld) {
		t.Fatalf("begin after unlock: %v", e)
	}
	if e = tx.Commit(); e == nil {
		t.Fatal("a transaction committed after its writer unlocked")
	}
	r, e := db.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	if n := countProjects(t, ctx, r.queries); n != 0 {
		t.Fatalf("unlock kept %d uncommitted projects", n)
	}
	commitProject(t, ctx, db, "feat/next")
}

func TestReadSnapshotCoversTheWholeHandler(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	before, e := db.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer before.Close()
	commitProject(t, ctx, db, "feat/a")
	if n := countProjects(t, ctx, before.queries); n != 0 {
		t.Fatalf("snapshot saw %d projects committed after it began", n)
	}
	after, e := db.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer after.Close()
	if n := countProjects(t, ctx, after.queries); n != 1 {
		t.Fatalf("new snapshot sees %d projects", n)
	}
}

func TestReadsRunDuringAnOpenWrite(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	w, e := db.LockWrites(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Unlock()
	tx, e := w.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = addProject(ctx, tx.queries, "feat/pending"); e != nil {
		t.Fatal(e)
	}
	quick, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		r, e := db.Read(quick)
		if e == nil {
			var n int
			e = r.queryRow(quick, []any{&n}, "SELECT count(*) FROM projects")
			if e == nil && n != 0 {
				e = errors.New("read saw an uncommitted write")
			}
			r.Close()
		}
		done <- e
	}()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
}

func TestReadsOverTheCapWait(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	var open []*ReadTx
	for range DefaultReadConcurrency {
		r, e := db.Read(ctx)
		if e != nil {
			t.Fatal(e)
		}
		open = append(open, r)
	}
	waiting, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if r, e := db.Read(waiting); !errors.Is(e, context.DeadlineExceeded) {
		if r != nil {
			r.Close()
		}
		t.Fatalf("read over the cap: %v", e)
	}
	for _, r := range open {
		if e := r.Close(); e != nil {
			t.Fatal(e)
		}
	}
	r, e := db.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	r.Close()
}

// The read cap is configurable: with a cap of two, a third snapshot waits
// until one closes or its context ends.
func TestReadConcurrencyIsConfigurable(t *testing.T) {
	db, e := OpenWith(t.TempDir(), Options{ReadConcurrency: 2})
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	first, e := db.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	second, e := db.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	waiting, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if r, e := db.Read(waiting); !errors.Is(e, context.DeadlineExceeded) {
		if r != nil {
			r.Close()
		}
		t.Fatalf("read over a cap of two: %v", e)
	}
	first.Close()
	third, e := db.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	third.Close()
	second.Close()
	dir := filepath.Join(t.TempDir(), "refused")
	if _, e = OpenWith(dir, Options{ReadConcurrency: -1}); e == nil {
		t.Fatal("opened with a negative read cap")
	}
	if _, e = os.Stat(dir); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("refused open created the data directory", e)
	}
}

func TestRequestTimesFollowTheProcessClock(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	r, e := db.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	r.Close()
	w, e := db.LockWrites(ctx)
	if e != nil {
		t.Fatal(e)
	}
	tx, e := w.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if tx.Now() != w.Now() || w.Now().Before(r.Now()) {
		t.Fatalf("write time %v (tx %v) before read time %v", w.Now(), tx.Now(), r.Now())
	}
	tx.Rollback()
	w.Unlock()
	next, e := db.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	if next.Now().Before(w.Now()) {
		t.Fatalf("read time %v before earlier write time %v", next.Now(), w.Now())
	}
}
