package storage

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// segments counts the b-tree segments of each search index.
func segments(t *testing.T, ctx context.Context, q queries) (grep, rank int) {
	t.Helper()
	if e := q.queryRow(ctx, []any{&grep}, "SELECT count(DISTINCT segid) FROM grep_index_idx"); e != nil {
		t.Fatal(e)
	}
	if e := q.queryRow(ctx, []any{&rank}, "SELECT count(DISTINCT segid) FROM rank_index_idx"); e != nil {
		t.Fatal(e)
	}
	return grep, rank
}

// indexText commits n transactions under w, each indexing one record's text
// in both search indexes, as a record write does.
func indexText(t *testing.T, ctx context.Context, w *Writer, n int) {
	t.Helper()
	rng := rand.New(rand.NewPCG(1, 2))
	for k := range n {
		tx, e := w.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		// Distinct words fill several index leaves per commit.
		var words []string
		for j := range 600 {
			words = append(words, fmt.Sprintf("w%x", rng.Uint64()>>uint(j%40)))
		}
		id, body := protocol.UUID(), fmt.Sprintf("record %d: %s", k, strings.Join(words, " "))
		if e = tx.writeGrep(ctx, Issues, id, "title", body); e == nil {
			e = tx.writeRank(ctx, Issues, id, "project", id, "title", body, "")
		}
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			t.Fatal(e)
		}
	}
}

// A commit only adds its own segment to each search index: merging segments
// is left to the background merger, which runs while no write holds the
// write lock, in steps of at most mergePages pages, and keeps the indexes
// consistent. The commits write more leaves than FTS5's automerge work unit
// (64) and fewer segments than its crisismerge level (16).
func TestSearchIndexesMergeOutsideCommits(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	w, e := db.LockWrites(ctx)
	if e != nil {
		t.Fatal(e)
	}
	r, e := db.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	grep0, rank0 := segments(t, ctx, r.queries)
	r.Close()
	const commits = 15
	indexText(t, ctx, w, commits)
	r, e = db.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	grep, rank := segments(t, ctx, r.queries)
	r.Close()
	if grep != grep0+commits || rank != rank0+commits {
		t.Fatalf("segments after %d commits: grep %d -> %d, rank %d -> %d; want one more per commit, no merge", commits, grep0, grep, rank0, rank)
	}
	w.Unlock()
	for deadline := time.Now().Add(10 * time.Second); grep > 4 || rank > 4; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("segments not merged after the write lock was released: grep %d, rank %d", grep, rank)
		}
		if r, e = db.Read(ctx); e != nil {
			t.Fatal(e)
		}
		grep, rank = segments(t, ctx, r.queries)
		r.Close()
	}
	w, e = db.LockWrites(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Unlock()
	tx, e := w.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	for _, table := range []string{"grep_index", "rank_index"} {
		if _, e = tx.exec(ctx, "INSERT INTO "+table+" ("+table+", rank) VALUES ('integrity-check', 1)"); e != nil {
			t.Fatalf("%s after the merges: %v", table, e)
		}
	}
}
