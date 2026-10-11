package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

func scan(t *testing.T, db *DB, projectID string) []SearchRecord {
	t.Helper()
	out := []SearchRecord{}
	read(t, db, func(ctx context.Context, r *ReadTx) {
		if e := r.SearchScan(ctx, projectID, func(v SearchRecord) error { out = append(out, v); return nil }); e != nil {
			t.Fatal(e)
		}
	})
	return out
}

func TestSearchScanReadsEverySearchableField(t *testing.T) {
	w := newWorld(t)
	f := func(pairs ...string) []SearchField {
		out := []SearchField{}
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, SearchField{pairs[i], pairs[i+1]})
		}
		return out
	}
	a := SearchRecord{Issues, w.a.ID, 1, w.p.ID, w.a.ID, w.a.Title, f("title", w.a.Title, "body", w.a.Body)}
	p := []SearchRecord{
		{Comments, w.comment.ID, 1, w.p.ID, w.a.ID, w.a.Title, f("body", w.comment.Body)},
		a,
		{Issues, w.b.ID, 1, w.p.ID, w.b.ID, w.b.Title, f("title", w.b.Title, "body", w.b.Body)},
		{Issues, w.c.ID, 1, w.p.ID, w.c.ID, w.c.Title, f("title", w.c.Title, "body", w.c.Body)},
		{Milestones, w.milestone.ID, 1, w.p.ID, "", "", f("title", w.milestone.Title, "body", w.milestone.Body)},
		{Projects, w.p.ID, 1, w.p.ID, "", "", f("description", w.p.Description)},
	}
	q := []SearchRecord{
		{Issues, w.x.ID, 1, w.q.ID, w.x.ID, w.x.Title, f("title", w.x.Title, "body", w.x.Body)},
		{Projects, w.q.ID, 1, w.q.ID, "", "", f("description", w.q.Description)},
	}
	sameJSON(t, scan(t, w.db, w.p.ID), p)
	sameJSON(t, scan(t, w.db, ""), append(p, q...))
}

// The scan honours cancellation between records: cancelled while handling
// the first record, it stops there with the context's error.
func TestSearchScanStopsWhenCancelled(t *testing.T) {
	w := newWorld(t)
	read(t, w.db, func(ctx context.Context, r *ReadTx) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		seen := 0
		e := r.SearchScan(ctx, "", func(SearchRecord) error { seen++; cancel(); return nil })
		if !errors.Is(e, context.Canceled) || seen != 1 {
			t.Fatalf("scan after cancellation: %d records, %v", seen, e)
		}
	})
}

func TestCompletionCandidates(t *testing.T) {
	w := newWorld(t)
	candidates := func(typ, projectID string) []protocol.Completion {
		var out []protocol.Completion
		read(t, w.db, func(ctx context.Context, r *ReadTx) {
			var e error
			if out, e = r.CompletionCandidates(ctx, typ, projectID); e != nil {
				t.Fatal(e)
			}
		})
		return out
	}
	issue := func(p protocol.Issue, project protocol.Project) protocol.Completion {
		return protocol.Completion{ID: p.ID, Title: p.Title, ProjectID: p.ProjectID, ProjectTitle: project.Title, State: p.State}
	}
	sameJSON(t, candidates(Projects, ""), []protocol.Completion{{ID: w.p.ID, Title: w.p.Title}, {ID: w.q.ID, Title: w.q.Title}})
	sameJSON(t, candidates(Issues, ""), []protocol.Completion{issue(w.a, w.p), issue(w.b, w.p), issue(w.c, w.p), issue(w.x, w.q)})
	sameJSON(t, candidates(Issues, w.q.ID), []protocol.Completion{issue(w.x, w.q)})
	sameJSON(t, candidates(Milestones, w.q.ID), []protocol.Completion{})
	sameJSON(t, candidates(Milestones, ""), []protocol.Completion{{ID: w.milestone.ID, Title: w.milestone.Title, ProjectID: w.p.ID, ProjectTitle: w.p.Title}})
	sameJSON(t, candidates(Comments, w.p.ID), []protocol.Completion{{ID: w.comment.ID, ProjectID: w.p.ID, ProjectTitle: w.p.Title, IssueID: w.a.ID}})
	sameJSON(t, candidates(Comments, w.q.ID), []protocol.Completion{})
}
