package storage

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func scanAny(t *testing.T, db *DB, projectID string, needles []string) []string {
	t.Helper()
	out := []string{}
	read(t, db, func(ctx context.Context, r *ReadTx) {
		if e := r.SearchScanAny(ctx, projectID, needles, func(v SearchRecord) error { out = append(out, v.ID); return nil }); e != nil {
			t.Fatal(e)
		}
	})
	return out
}

// SearchScanAny reads the records whose grep-folded title or body contains
// any of its needles, in SearchScan's order: no needle, or an empty one,
// reads every record of the scope; needles of three or more characters go
// through the trigram index, shorter ones scan grep_text.
func TestSearchScanAnyReadsTheRecordsContainingAnyNeedle(t *testing.T) {
	w := newWorld(t)
	glob := newIssue(id(15), w.q.ID, "Wildcards", "a*b?c[d]e and NUL\x00here", w.stamp())
	mustApply(t, w.db, glob.CreatedAt, glob)
	every := []string{}
	for _, v := range scan(t, w.db, "") {
		every = append(every, v.ID)
	}
	for _, c := range []struct {
		scope   string
		needles []string
		want    []string
	}{
		{"", nil, every},
		{"", []string{"absent", ""}, every},
		{"", []string{"strasse", "goal"}, []string{w.a.ID, w.milestone.ID, w.x.ID}},
		{w.q.ID, []string{"strasse", "goal"}, []string{w.x.ID}},
		{"", []string{"comment", "absent"}, []string{w.comment.ID}},
		{"", []string{"absent", "nothing here"}, []string{}},
		{"", []string{"m1", "description"}, []string{w.milestone.ID, w.p.ID}},
		{"", []string{"a*b", "[d]"}, []string{glob.ID}},
		{"", []string{"b*c", "?b"}, []string{}},
		{"", []string{"nul\x00here", "absent"}, []string{glob.ID}},
		{"", []string{"strasse", "body of a"}, []string{w.a.ID, w.x.ID}},
	} {
		if got := scanAny(t, w.db, c.scope, c.needles); !slices.Equal(got, c.want) {
			t.Errorf("scope %q needles %q: %v, want %v", c.scope, c.needles, got, c.want)
		}
	}
}

// Every needle of three or more characters is a GLOB on both columns of the
// trigram index; a shorter one scans grep_text.
func TestSearchScanAnyUsesTheTrigramIndexPerNeedle(t *testing.T) {
	db := openTemp(t)
	plan := func(needles ...string) string {
		query, args := searchAnyQuery("", needles)
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
	if got := plan("abc", "def", "ghi"); strings.Count(got, "SCAN grep_index VIRTUAL TABLE INDEX 0:G") != 6 || strings.Contains(got, "SCAN grep_text") {
		t.Errorf("no index GLOB per needle and column in\n%s", got)
	}
	if got := plan("abc", "de"); strings.Count(got, "SCAN grep_index VIRTUAL TABLE INDEX 0:G") != 2 || !strings.Contains(got, "SCAN grep_text") {
		t.Errorf("a short needle does not scan grep_text:\n%s", got)
	}
}
