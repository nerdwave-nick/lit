package storage

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

func scan(t *testing.T, db *DB, projectID string) []SearchRecord {
	return scanFor(t, db, projectID, "")
}

func scanFor(t *testing.T, db *DB, projectID, needle string) []SearchRecord {
	t.Helper()
	out := []SearchRecord{}
	read(t, db, func(ctx context.Context, r *ReadTx) {
		if e := r.SearchScan(ctx, projectID, needle, func(v SearchRecord) error { out = append(out, v); return nil }); e != nil {
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

// A needle narrows the scan to the records whose grep-folded title or body
// contains it: through the trigram tables from three folded characters on,
// by scanning the grep columns below that. GLOB's wildcards are literal, a
// match never spans two fields, and NUL is matched as U+FFFD, so U+FFFD finds
// NUL too (lit grep confirms every candidate on the original text).
func TestSearchScanNarrowsToTheRecordsContainingTheNeedle(t *testing.T) {
	w := newWorld(t)
	glob := newIssue(id(15), w.q.ID, "Wildcards", "a*b?c[d]e\\f and NUL\x00here", w.stamp())
	mustApply(t, w.db, glob.CreatedAt, glob)
	ids := func(records []SearchRecord) []string {
		out := []string{}
		for _, v := range records {
			out = append(out, v.ID)
		}
		return out
	}
	for _, c := range []struct {
		scope, needle string
		want          []string
	}{
		{"", "strasse", []string{w.a.ID, w.x.ID}},
		{w.p.ID, "strasse", []string{w.a.ID}},
		{w.q.ID, "ss", []string{w.x.ID}},
		{"", "e", []string{w.comment.ID, w.a.ID, w.p.ID, w.x.ID, glob.ID}},
		{"", "body of a\n", []string{w.a.ID}},
		{"", "ssebody", []string{}},
		{"", "ünïcode\r\nd", []string{w.p.ID}},
		{"", "goal", []string{w.milestone.ID}},
		{"", "m1", []string{w.milestone.ID}},
		{"", "a comment", []string{w.comment.ID}},
		{"", "a*b?c[d]e\\f", []string{glob.ID}},
		{"", "*", []string{glob.ID}},
		{"", "?c[", []string{glob.ID}},
		{"", "[d]", []string{glob.ID}},
		{"", "b*c", []string{}},
		{"", "a?b", []string{}},
		{"", "[de]", []string{}},
		{"", "nul\x00here", []string{glob.ID}},
		{"", "\x00", []string{glob.ID}},
		{"", "nul\uFFFDhere", []string{glob.ID}},
		{"", "absent", []string{}},
	} {
		if got := ids(scanFor(t, w.db, c.scope, c.needle)); !slices.Equal(got, c.want) {
			t.Errorf("scope %q needle %q: %v, want %v", c.scope, c.needle, got, c.want)
		}
	}
}

// Needles of three or more characters are looked up in the trigram index
// with an index GLOB; shorter ones scan grep_text.
func TestSearchScanUsesTheTrigramIndexFromThreeCharacters(t *testing.T) {
	db := openTemp(t)
	plan := func(projectID, needle string) string {
		query, args := searchQuery(projectID, needle)
		rows, e := outside(t, db).Query("EXPLAIN QUERY PLAN "+query, args...)
		if e != nil {
			t.Fatal(e)
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if e := rows.Scan(&id, &parent, &unused, &detail); e != nil {
				t.Fatal(e)
			}
			lines = append(lines, detail)
		}
		return strings.Join(lines, "\n")
	}
	for _, needle := range []string{"abc", "a*b", "日本語", "\x00\x00\x00"} {
		if got := plan(id(1), needle); strings.Count(got, "SCAN grep_index VIRTUAL TABLE INDEX 0:G") != 2 {
			t.Errorf("needle %q: no index GLOB on both columns in\n%s", needle, got)
		}
	}
	for _, needle := range []string{"ab", "ß", "\x00"} {
		if got := plan("", needle); strings.Contains(got, "grep_index") || !strings.Contains(got, "SCAN grep_text") {
			t.Errorf("needle %q does not scan grep_text:\n%s", needle, got)
		}
	}
}

// The scan honours cancellation between records: cancelled while handling
// the first record, it stops there with the context's error.
func TestSearchScanStopsWhenCancelled(t *testing.T) {
	w := newWorld(t)
	read(t, w.db, func(ctx context.Context, r *ReadTx) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		seen := 0
		e := r.SearchScan(ctx, "", "", func(SearchRecord) error { seen++; cancel(); return nil })
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
