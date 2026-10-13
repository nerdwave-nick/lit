package storage

import (
	"context"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// rankWorld holds two projects whose records rank differently for
// "connection timeout" and "timeout".
type rankWorld struct {
	db                          *DB
	p, other                    protocol.Project
	title, body, comment, group protocol.Issue
	closed, cafe, elsewhere     protocol.Issue
	inComment                   protocol.Comment
	groupComments               []protocol.Comment
	milestone                   protocol.Milestone
}

func newRankWorld(t *testing.T) rankWorld {
	t.Helper()
	w := rankWorld{db: openTemp(t)}
	s := at(1)
	w.p = newProject(id(1), "feat/rank", "Ranking fixture", s)
	w.other = newProject(id(2), "feat/other", "The timeout budget of the other project", s)
	w.title = newIssue(id(11), w.p.ID, "Connection timeout handling", "Retries the request.", s)
	w.body = newIssue(id(12), w.p.ID, "Retry policy", "A connection timeout leads to a retry.", s)
	w.comment = newIssue(id(13), w.p.ID, "Unrelated work", "Nothing to see.", s)
	w.group = newIssue(id(14), w.p.ID, "Grouped hits", "A timeout in the body.", s)
	w.closed = newIssue(id(15), w.p.ID, "Closed timeout", "", s)
	w.closed.State = "closed"
	w.cafe = newIssue(id(16), w.p.ID, "Café opening", "Naïve résumé of C++ and net/http work.", s)
	w.elsewhere = newIssue(id(17), w.other.ID, "Other timeout", "", s)
	w.inComment = newComment(id(21), w.comment.ID, "Seen a connection timeout in CI.", s)
	w.groupComments = []protocol.Comment{
		newComment(id(22), w.group.ID, "First timeout comment.", s),
		newComment(id(23), w.group.ID, "Second comment with a timeout, timeout, timeout.", s),
		newComment(id(24), w.group.ID, "No match here.", s),
		newComment(id(25), w.group.ID, "Third timeout comment.", s),
		newComment(id(26), w.group.ID, "Fourth timeout comment.", s),
	}
	w.milestone = newMilestone(id(31), w.p.ID, "Timeout milestone", "Goal", s)
	records := []any{w.p, w.other, w.title, w.body, w.comment, w.group, w.closed, w.cafe, w.elsewhere, w.inComment, w.milestone}
	for _, c := range w.groupComments {
		records = append(records, c)
	}
	mustApply(t, w.db, s, records...)
	return w
}

// search runs one ranked search page.
func (w rankWorld) search(t *testing.T, q RankedQuery) ([]RankedResult, bool) {
	t.Helper()
	var out []RankedResult
	var more bool
	read(t, w.db, func(ctx context.Context, r *ReadTx) {
		var e error
		if out, more, e = r.RankedSearch(ctx, q); e != nil {
			t.Fatalf("%+v: %v", q, e)
		}
	})
	return out, more
}

func keys(results []RankedResult) []string {
	out := []string{}
	for _, r := range results {
		out = append(out, r.Type+"/"+r.ID)
	}
	return out
}

// Ranking weights the title over the body over comments.
func TestRankedSearchRanksTitleOverBodyOverComments(t *testing.T) {
	w := newRankWorld(t)
	got, more := w.search(t, RankedQuery{Query: "connection timeout", Limit: 20})
	want := []string{owner(Issues, w.title.ID), owner(Issues, w.body.ID), owner(Issues, w.comment.ID)}
	if !slices.Equal(keys(got), want) || more {
		t.Fatalf("got %v %v, want %v", keys(got), more, want)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Score > got[i].Score {
			t.Fatalf("scores out of order: %v", got)
		}
	}
	if f := got[0].Fields; len(f) != 1 || f[0].Name != "title" || f[0].Text != w.title.Title || !slices.Equal(f[0].Matches, [][2]int{{0, 10}, {11, 18}}) {
		t.Fatalf("title result fields %+v", f)
	}
	if f := got[1].Fields; len(f) != 1 || f[0].Name != "body" || f[0].Text != w.body.Body || !slices.Equal(f[0].Matches, [][2]int{{2, 12}, {13, 20}}) {
		t.Fatalf("body result fields %+v", f)
	}
	if f := got[2].Fields; len(f) != 1 || f[0].Name != "comment" || f[0].CommentID != w.inComment.ID || got[2].CommentMatches != 1 {
		t.Fatalf("comment result %+v", got[2])
	}
	if r := got[0]; r.ProjectID != w.p.ID || r.Title != w.title.Title || r.State != "open" || r.Revision != 1 {
		t.Fatalf("result identity %+v", r)
	}
}

// One result per issue carries its matching title and body and its best
// matching comments, at most MaxRankedComments, with the number of matching
// comments; projects and milestones are results of their own.
func TestRankedSearchGroupsCommentsUnderTheirIssue(t *testing.T) {
	w := newRankWorld(t)
	got, _ := w.search(t, RankedQuery{Query: "timeout", Limit: 20})
	var group *RankedResult
	types := map[string]int{}
	for i, r := range got {
		types[r.Type]++
		if r.ID == w.group.ID {
			group = &got[i]
		}
	}
	if types[Issues] != 6 || types[Projects] != 1 || types[Milestones] != 1 || types[Comments] != 0 {
		t.Fatalf("result types %v: %v", types, keys(got))
	}
	if group == nil || group.CommentMatches != 4 || len(group.Fields) != 1+MaxRankedComments {
		t.Fatalf("group %+v", group)
	}
	if f := group.Fields; f[0].Name != "body" || f[1].CommentID != w.groupComments[1].ID || f[1].Name != "comment" {
		t.Fatalf("group fields %+v", f)
	}
	for _, r := range got {
		switch r.ID {
		case w.other.ID:
			if r.Type != Projects || r.Fields[0].Name != "description" || r.ProjectID != w.other.ID || r.Title != w.other.Title {
				t.Fatalf("project result %+v", r)
			}
		case w.milestone.ID:
			if r.Type != Milestones || r.Fields[0].Name != "title" || r.ProjectID != w.p.ID || r.State != "" {
				t.Fatalf("milestone result %+v", r)
			}
		}
	}
}

// Scope and state filter results; a state filter returns issues only.
func TestRankedSearchFiltersByProjectAndState(t *testing.T) {
	w := newRankWorld(t)
	for _, c := range []struct {
		q    RankedQuery
		want []string
	}{
		{RankedQuery{Query: "timeout", ProjectID: w.other.ID}, []string{owner(Issues, w.elsewhere.ID), owner(Projects, w.other.ID)}},
		{RankedQuery{Query: "timeout", State: "closed"}, []string{owner(Issues, w.closed.ID)}},
		{RankedQuery{Query: "timeout", ProjectID: w.other.ID, State: "closed"}, []string{}},
	} {
		c.q.Limit = 20
		got, _ := w.search(t, c.q)
		k := keys(got)
		slices.Sort(k)
		if !slices.Equal(k, c.want) {
			t.Errorf("%+v = %v, want %v", c.q, k, c.want)
		}
	}
	got, _ := w.search(t, RankedQuery{Query: "timeout", State: "open", Limit: 20})
	for _, r := range got {
		if r.Type != Issues || r.State != "open" {
			t.Fatalf("open filter returned %+v", r)
		}
	}
	if len(got) != 5 {
		t.Fatalf("open filter returned %v", keys(got))
	}
}

// Pages continue after the last result's key in rank order and together
// give the single page's results.
func TestRankedSearchPagesInRankOrder(t *testing.T) {
	w := newRankWorld(t)
	all, _ := w.search(t, RankedQuery{Query: "timeout", Limit: 100})
	var paged []RankedResult
	q := RankedQuery{Query: "timeout", Limit: 3}
	for {
		page, more := w.search(t, q)
		paged = append(paged, page...)
		if !more {
			break
		}
		if len(page) != 3 {
			t.Fatalf("short page %v with more", keys(page))
		}
		last := page[len(page)-1].RankedKey
		q.After = &last
	}
	if !slices.Equal(keys(paged), keys(all)) || len(all) != 8 {
		t.Fatalf("paged %v, single page %v", keys(paged), keys(all))
	}
}

// Diacritics and case are ignored; operators work; match offsets are byte
// offsets into the original text.
func TestRankedSearchQueries(t *testing.T) {
	w := newRankWorld(t)
	for _, c := range []struct {
		query string
		want  []string
	}{
		{"cafe", []string{owner(Issues, w.cafe.ID)}},
		{"CAFÉ", []string{owner(Issues, w.cafe.ID)}},
		{"naive resume", []string{owner(Issues, w.cafe.ID)}},
		{"C++", []string{owner(Issues, w.cafe.ID)}},
		{"net/http", []string{owner(Issues, w.cafe.ID)}},
		{`"timeout handling`, []string{owner(Issues, w.title.ID)}},
		{`"handling timeout"`, []string{}},
		{"timeout -comment -closed -other -milestone -body -retry -ci", []string{owner(Issues, w.title.ID)}},
		{"handling OR policy", []string{owner(Issues, w.body.ID), owner(Issues, w.title.ID)}},
		{"handl*", []string{owner(Issues, w.title.ID)}},
		{"-timeout", []string{}},
		{"++", []string{}},
	} {
		got, _ := w.search(t, RankedQuery{Query: c.query, Limit: 20})
		k := keys(got)
		slices.Sort(k)
		want := slices.Clone(c.want)
		slices.Sort(want)
		if !slices.Equal(k, want) {
			t.Errorf("%q = %v, want %v", c.query, k, want)
		}
	}
	got, _ := w.search(t, RankedQuery{Query: "cafe resume", Limit: 1})
	f := got[0].Fields
	if len(f) != 2 || f[0].Text[f[0].Matches[0][0]:f[0].Matches[0][1]] != "Café" || f[1].Text[f[1].Matches[0][0]:f[1].Matches[0][1]] != "résumé" {
		t.Fatalf("fields %+v", f)
	}
}

// Text containing the highlight markers is reported without match offsets
// instead of with wrong ones.
func TestRankedSearchTextWithMarkers(t *testing.T) {
	w := newRankWorld(t)
	odd := newIssue(id(18), w.p.ID, "Marker ﷐ zebra ﷑", "", at(2))
	mustApply(t, w.db, at(2), odd)
	got, _ := w.search(t, RankedQuery{Query: "zebra", Limit: 20})
	if len(got) != 1 || got[0].Fields[0].Text != odd.Title || len(got[0].Fields[0].Matches) != 0 {
		t.Fatalf("%+v", got)
	}
}

// No search box query is an FTS5 error, whatever its characters.
func TestRankedSearchNeverFailsOnSyntax(t *testing.T) {
	w := newRankWorld(t)
	alphabet := []string{"a", "timeout", "Café", " ", " ", `"`, "-", "*", "OR", "AND", "NOT", "NEAR", "(", ")", ":", "^", "+", "/", "\\", "'", "\x00", "{", "}", ".", "é", "́"}
	r := rand.New(rand.NewSource(1))
	for n := 0; n < 300; n++ {
		var b strings.Builder
		for k := r.Intn(12); k >= 0; k-- {
			b.WriteString(alphabet[r.Intn(len(alphabet))])
		}
		w.search(t, RankedQuery{Query: b.String(), Limit: 5})
	}
}
