package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

// graphWorld adds to listWorld: c (closed) blocks a, and a is related to x.
func graphWorld(t *testing.T) *world {
	t.Helper()
	w, d := listWorld(t)
	s := w.stamp()
	a, c, x := next(w.a, s), next(w.c, s), next(w.x, s)
	a.BlockedBy, c.Blocks = []string{c.ID}, []string{a.ID}
	a.Related, x.Related = []string{x.ID}, []string{a.ID}
	mustApply(t, w.db, s, a, c, x)
	w.a, w.c, w.x = a, c, x
	_ = d
	return w
}

func TestNeighboursOfAnIssue(t *testing.T) {
	w := graphWorld(t)
	read(t, w.db, func(ctx context.Context, r *ReadTx) {
		got, e := r.Neighbours(ctx, w.a.ID)
		if e != nil {
			t.Fatal(e)
		}
		sameJSON(t, got, []Neighbour{
			{"blocked_by", w.c.ID, w.p.ID, "closed", w.c.Revision},
			{"blocks", w.b.ID, w.p.ID, "open", w.b.Revision},
			{"child", w.b.ID, w.p.ID, "open", w.b.Revision},
			{"related", w.x.ID, w.q.ID, "open", w.x.Revision},
		})
		got, e = r.Neighbours(ctx, w.b.ID)
		if e != nil {
			t.Fatal(e)
		}
		sameJSON(t, got, []Neighbour{
			{"blocked_by", w.a.ID, w.p.ID, "open", w.a.Revision},
			{"parent", w.a.ID, w.p.ID, "open", w.a.Revision},
		})
		if got, e = r.Neighbours(ctx, id(99)); e != nil || len(got) != 0 {
			t.Fatal(got, e)
		}
	})
}

func TestMilestoneProgressInputs(t *testing.T) {
	w := graphWorld(t)
	now := time.Date(2026, 10, 11, 13, 0, 0, 0, time.UTC)
	read(t, w.db, func(ctx context.Context, r *ReadTx) {
		got, e := r.MilestoneProgress(ctx, w.milestone.ID, now)
		if e != nil {
			t.Fatal(e)
		}
		// a's blocker c is closed; b's blocker a is open. a's claim is live,
		// b's expired.
		sameJSON(t, got, []MemberProgress{
			{ID: w.a.ID, State: "open", OpenBlockers: []string{}, Claimed: true},
			{ID: w.b.ID, State: "open", OpenBlockers: []string{w.a.ID}, Claimed: false},
		})
		if got, e = r.MilestoneProgress(ctx, w.milestone.ID, now.Add(2*time.Hour)); e != nil || got[0].Claimed {
			t.Fatal("claim outlived its expiry", got, e)
		}
	})
}

func TestStructuralSetFollowsParentsAndBlocks(t *testing.T) {
	w := graphWorld(t)
	p := RecordRef{Projects, w.p.ID, w.p.Revision}
	a, b, c := RecordRef{Issues, w.a.ID, w.a.Revision}, RecordRef{Issues, w.b.ID, w.b.Revision}, RecordRef{Issues, w.c.ID, w.c.Revision}
	read(t, w.db, func(ctx context.Context, r *ReadTx) {
		for _, x := range []struct {
			seeds []string
			want  []RecordRef
		}{
			{[]string{w.b.ID}, []RecordRef{a, b, p}},                                                          // b's parent a, and a blocks b
			{[]string{w.c.ID}, []RecordRef{a, b, c, p}},                                                       // c blocks a, a blocks b
			{[]string{w.a.ID}, []RecordRef{a, b, p}},                                                          // not c: blocked_by is not followed
			{[]string{w.x.ID}, []RecordRef{{Issues, w.x.ID, w.x.Revision}, {Projects, w.q.ID, w.q.Revision}}}, // related is not structural
			{[]string{id(99)}, []RecordRef{}},
			{nil, []RecordRef{}},
		} {
			got, e := r.StructuralSet(ctx, x.seeds)
			if e != nil {
				t.Fatal(e)
			}
			sameJSON(t, got, x.want)
		}
	})
}

func TestCycleQueries(t *testing.T) {
	w := graphWorld(t)
	read(t, w.db, func(ctx context.Context, r *ReadTx) {
		for _, ids := range [][]string{nil, {w.a.ID, w.b.ID, w.c.ID, w.x.ID}} {
			if cycle, e := r.ParentCycle(ctx, ids); e != nil || cycle {
				t.Fatal("parent cycle", ids, e)
			}
			if cycle, e := r.BlockingCycle(ctx, ids); e != nil || cycle {
				t.Fatal("blocking cycle", ids, e)
			}
		}
	})
	// Inside a write transaction, before the service decides, the queries see
	// the cycle its own statements made.
	e := write(t, w.db, func(ctx context.Context, tx *WriteTx) error {
		if e := stmts([]any{"INSERT INTO issue_links (kind, a, b) VALUES ('blocks', ?, ?)", w.b.ID, w.c.ID}, []any{"UPDATE issues SET parent_id = ? WHERE id = ?", w.b.ID, w.a.ID})(ctx, tx); e != nil {
			return e
		}
		for _, check := range []func(context.Context, []string) (bool, error){tx.ParentCycle, tx.BlockingCycle} {
			if cycle, e := check(ctx, []string{w.b.ID}); e != nil || !cycle {
				t.Fatal("missed a cycle", e)
			}
			if cycle, e := check(ctx, []string{w.x.ID}); e != nil || cycle {
				t.Fatal("cycle off the path", e)
			}
		}
		return errRolledBack
	})
	if e != errRolledBack {
		t.Fatal(e)
	}
}

var errRolledBack = errors.New("rolled back")
