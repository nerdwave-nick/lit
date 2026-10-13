package service

import (
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"strings"
	"testing"
)

func TestSearchExcerpt(t *testing.T) {
	for _, tc := range []struct {
		body, query  string
		sensitive    bool
		context      int
		want         string
		start, match int
		truncated    bool
	}{
		{"before\n\nStraße.*\nafter", "STRASSE.*", false, 1, "\nStraße.*\nafter", 2, 3, false},
		{"a\nΣςσ\nb", "σσσ", false, 0, "Σςσ", 2, 2, false},
		{"one\ntwo\nthree", "two", false, 0, "two", 2, 2, false},
		{"a\n" + strings.Repeat("雪", 300) + "needle" + strings.Repeat("雪", 300) + "\nz", "needle", false, 1, strings.Repeat("雪", 197) + "needle" + strings.Repeat("雪", 197), 2, 2, true},
		{"first needle\nsecond needle", "needle", false, 0, "first needle", 1, 1, false},
		{strings.Repeat("雪", 500) + "ß", "SS", false, 0, strings.Repeat("雪", 399) + "ß", 1, 1, true},
		{"a\n\nneedle\n\nb", "needle", false, 99, "a\n\nneedle\n\nb", 1, 3, false},
	} {
		e, ok := searchExcerpt("body", tc.body, tc.query, tc.sensitive, tc.context)
		if !ok || e.Text != tc.want || e.StartLine != tc.start || e.MatchLine != tc.match || e.ExcerptTruncated != tc.truncated {
			t.Fatalf("%+v => %+v %v", tc, e, ok)
		}
	}
	if _, ok := searchExcerpt("body", "Straße", "STRASSE", true, 0); ok {
		t.Fatal("case override ignored")
	}
}

func TestSearchLivePagesAndLimits(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	p := protocol.UUID()
	executeProjectTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: p, Title: textPointer("feat/search")}))
	ids := []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000003"}
	mutate := func(op string, items ...protocol.ProjectInput) {
		t.Helper()
		prep, e := s.PrepareRecords(protocol.PrepareRequest{Operation: op, Project: p, Items: items}, c)
		if e != nil {
			t.Fatal(e)
		}
		executeProjectTest(t, s, c, prep)
	}
	mutate("issue.create", protocol.ProjectInput{ID: ids[0], Title: textPointer("First"), Content: textPointer("needle")}, protocol.ProjectInput{ID: ids[1], Title: textPointer("Third"), Content: textPointer("needle")})
	q := protocol.SearchRequest{Scope: protocol.SearchScope{ProjectID: p}, Query: "needle", Limit: 1}
	first, e := searchPageOf(s, q, c)
	if e != nil || first.NextCursor == nil {
		t.Fatal(first, e)
	}
	q.Cursor = *first.NextCursor
	mutate("issue.update", protocol.ProjectInput{Target: ids[1], Set: protocol.ProjectSet{Body: textPointer("removed")}})
	mutate("issue.create", protocol.ProjectInput{ID: "00000000-0000-4000-8000-000000000002", Title: textPointer("Second"), Content: textPointer("needle")})
	next, e := searchPageOf(s, q, c)
	if e != nil || len(next.Items) != 1 || next.Items[0].(protocol.SearchResult).IssueTitle != "Second" || next.NextCursor != nil {
		t.Fatal(next, e)
	}
	for _, change := range []func(*protocol.SearchRequest){func(q *protocol.SearchRequest) { q.Query = "other" }, func(q *protocol.SearchRequest) { q.CaseSensitive = true }, func(q *protocol.SearchRequest) { q.ContextLines = 1 }, func(q *protocol.SearchRequest) { q.Scope = protocol.SearchScope{AllProjects: true} }, func(q *protocol.SearchRequest) { q.Limit = 2 }} {
		bad := q
		change(&bad)
		if _, e := searchPageOf(s, bad, c); e == nil {
			t.Fatal("accepted mismatched cursor", bad)
		}
	}
	q.Cursor = ""
	q.Limit = 1001
	if _, e := searchPageOf(s, q, c); e == nil {
		t.Fatal("accepted oversized page")
	}
	q.Limit = 100
	q.ContextLines = -1
	if _, e := searchPageOf(s, q, c); e == nil {
		t.Fatal("accepted negative context")
	}
	q.ContextLines = 0
	q.Query = ""
	if _, e := searchPageOf(s, q, c); e == nil {
		t.Fatal("accepted empty query")
	}
}

func TestSearchHTTPBoundsAndShape(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	p := protocol.UUID()
	executeProjectTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: p, Title: textPointer("feat/search"), Content: textPointer(strings.Repeat("雪", 500) + " needle " + strings.Repeat("x", 500))}))
	c.ProjectID = &p
	if e := s.SaveClient(c); e != nil {
		t.Fatal(e)
	}
	code, v := directRequest(t, s, c, "POST", "/v1/search", map[string]any{"query": "needle"})
	if code != 200 || v["data"] != nil || v["next_cursor"] != nil || v["server_time"] == nil {
		t.Fatal(code, v)
	}
	ex := v["items"].([]any)[0].(map[string]any)["excerpts"].([]any)[0].(map[string]any)
	if len([]rune(ex["text"].(string))) != 400 || ex["excerpt_truncated"] != true || !strings.Contains(ex["text"].(string), "needle") {
		t.Fatal(v)
	}
	s.Config.Limits.SnapshotBytes = 100
	code, v = directRequest(t, s, c, "POST", "/v1/search", map[string]any{"query": "needle"})
	if code != 413 || v["items"] != nil {
		t.Fatal(code, v)
	}
	s.Config.Limits.SnapshotBytes = 1 << 20
	for _, body := range []any{map[string]any{"query": ""}, map[string]any{"query": "x", "context_lines": -1}, map[string]any{"query": "x", "scope": map[string]any{"project_id": p, "all_projects": true}}, map[string]any{"query": "x", "cursor": "broken"}} {
		code, v = directRequest(t, s, c, "POST", "/v1/search", body)
		if code < 400 {
			t.Fatal(code, v)
		}
	}
}
