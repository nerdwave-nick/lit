package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// highlighted lists the highlighted parts of a match's snippet.
func highlighted(m protocol.RankedMatch) []string {
	out := []string{}
	for _, h := range m.Highlights {
		out = append(out, m.Snippet[h.Start:h.End])
	}
	return out
}

// Snippets are plain text around the first match: at most snippetRunes
// characters of the field, whitespace runs as one space, "…" where the field
// continues, and the matched words as byte offsets into the snippet.
func TestRankedSnippet(t *testing.T) {
	long := strings.Repeat("hay ", 100) + "the needle is here" + strings.Repeat(" hay", 100)
	at := strings.Index(long, "needle")
	for _, c := range []struct {
		name, text string
		matches    [][2]int
		want       string
		words      []string
	}{
		{"whole title", "Connection timeout handling", [][2]int{{0, 10}, {11, 18}}, "Connection timeout handling", []string{"Connection", "timeout"}},
		{"whitespace", "  line one\n\n\ttimeout  here\r\n", [][2]int{{13, 20}}, "line one timeout here", []string{"timeout"}},
		{"phrase across a newline", "a connection\ntimeout", [][2]int{{2, 20}}, "a connection timeout", []string{"connection timeout"}},
		{"bytes", "Café résumé", [][2]int{{0, 5}, {6, 14}}, "Café résumé", []string{"Café", "résumé"}},
		{"no offsets", "Marker ﷐ text", [][2]int{}, "Marker ﷐ text", []string{}},
		{"long", long, [][2]int{{at, at + 6}}, "", []string{"needle"}},
		{"long without offsets", long, [][2]int{}, "", []string{}},
	} {
		got := rankedSnippet(storage.RankedField{Name: "body", Text: c.text, Matches: c.matches})
		if c.want != "" && got.Snippet != c.want {
			t.Errorf("%s: snippet %q, want %q", c.name, got.Snippet, c.want)
		}
		if words := highlighted(got); strings.Join(words, "|") != strings.Join(c.words, "|") {
			t.Errorf("%s: highlights %q in %q, want %q", c.name, words, got.Snippet, c.words)
		}
		if n := utf8.RuneCountInString(strings.Trim(got.Snippet, "…")); n > snippetRunes {
			t.Errorf("%s: %d characters", c.name, n)
		}
	}
	got := rankedSnippet(storage.RankedField{Text: long, Matches: [][2]int{{at, at + 6}}})
	if !strings.HasPrefix(got.Snippet, "…hay") || !strings.HasSuffix(got.Snippet, "hay…") || !strings.Contains(got.Snippet, "the needle is here") {
		t.Fatalf("long snippet %q", got.Snippet)
	}
	if got := rankedSnippet(storage.RankedField{Text: long}); !strings.HasPrefix(got.Snippet, "hay hay") || !strings.HasSuffix(got.Snippet, "hay…") {
		t.Fatalf("snippet without offsets %q", got.Snippet)
	}
}

// rankedWorld creates a project whose records rank for "connection timeout".
func rankedWorld(t *testing.T) (*Server, protocol.Client, string, []string) {
	t.Helper()
	s, c := projectTestServer(t, t.TempDir())
	t.Cleanup(func() { s.DB.Close() })
	p, other := protocol.UUID(), protocol.UUID()
	mustExecute(t, s, c, prepareProjectTest(t, s, c, "create",
		protocol.ProjectInput{ID: p, Title: textPointer("feat/rank"), Content: textPointer("Ranked search fixture")},
		protocol.ProjectInput{ID: other, Title: textPointer("feat/other"), Content: textPointer("Another connection timeout")}))
	issues := []protocol.ProjectInput{
		{ID: protocol.UUID(), Title: textPointer("Connection timeout handling"), Content: textPointer("Retries the request.")},
		{ID: protocol.UUID(), Title: textPointer("Retry policy"), Content: textPointer("A connection\ntimeout leads to a retry.")},
		{ID: protocol.UUID(), Title: textPointer("Unrelated work"), Content: textPointer("Nothing to see.")},
	}
	prep, e := s.PrepareRecords(protocol.PrepareRequest{Operation: "issue.create", Project: p, Items: issues}, c)
	if e != nil {
		t.Fatal(e)
	}
	mustExecute(t, s, c, prep)
	comment := protocol.UUID()
	prep, e = s.PrepareRecords(protocol.PrepareRequest{Operation: "comment.create", Project: p, Items: []protocol.ProjectInput{{ID: comment, Issue: issues[2].ID, Content: textPointer("Seen a connection timeout in CI.")}}}, c)
	if e != nil {
		t.Fatal(e)
	}
	mustExecute(t, s, c, prep)
	return s, c, p, []string{issues[0].ID, issues[1].ID, issues[2].ID, comment, other}
}

// rankedPage serves one POST /v1/ranked-search and decodes its page.
func rankedPage(t *testing.T, s *Server, c protocol.Client, body any) (int, []protocol.RankedSearchResult, *string, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/v1/ranked-search", strings.NewReader(string(b)))
	r.Header.Set("X-Lit-Client-ID", c.ClientID)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var page struct {
		Items      []protocol.RankedSearchResult `json:"items"`
		NextCursor *string                       `json:"next_cursor"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	return w.Code, page.Items, page.NextCursor, w.Body.Bytes()
}

func TestRankedSearchRoute(t *testing.T) {
	s, c, p, ids := rankedWorld(t)
	status, items, next, raw := rankedPage(t, s, c, map[string]any{"query": "connection timeout", "scope": map[string]any{"project_id": "feat/rank"}})
	if status != 200 || len(items) != 3 || next != nil {
		t.Fatalf("%d %s", status, raw)
	}
	for i, want := range ids[:3] {
		if items[i].ID != want || items[i].ProjectID != p || items[i].Type != "issues" {
			t.Fatalf("result %d: %+v", i, items[i])
		}
	}
	// The settled result shape.
	first, _ := json.Marshal(items[0])
	want := `{"type":"issues","id":"` + ids[0] + `","project_id":"` + p + `","title":"Connection timeout handling","state":"open","revision":1,` +
		`"matches":[{"field":"title","snippet":"Connection timeout handling","highlights":[{"start":0,"end":10},{"start":11,"end":18}]}],"comment_matches":0}`
	if string(first) != want {
		t.Fatalf("result shape\n got %s\nwant %s", first, want)
	}
	if m := items[1].Matches; len(m) != 1 || m[0].Snippet != "A connection timeout leads to a retry." || strings.Join(highlighted(m[0]), "|") != "connection|timeout" {
		t.Fatalf("body match %+v", m)
	}
	if m := items[2].Matches; len(m) != 1 || m[0].Field != "comment" || m[0].CommentID != ids[3] || items[2].CommentMatches != 1 {
		t.Fatalf("comment match %+v", items[2])
	}
	// The session's project is the default scope; all projects adds the
	// other project as a result of its own.
	selected := c
	selected.ProjectID = &p
	if e := s.SaveClient(selected); e != nil {
		t.Fatal(e)
	}
	if status, items, _, raw := rankedPage(t, s, selected, map[string]any{"query": "connection timeout"}); status != 200 || len(items) != 3 {
		t.Fatalf("selected project: %d %s", status, raw)
	}
	status, items, _, raw = rankedPage(t, s, c, map[string]any{"query": "connection timeout", "scope": map[string]any{"all_projects": true}})
	projects := 0
	for _, v := range items {
		if v.Type == "projects" && (v.ID != ids[4] || v.ProjectID != ids[4] || v.Title != "feat/other" || v.Matches[0].Field != "description" || v.State != "") {
			t.Fatalf("project result %+v", v)
		}
		if v.Type == "projects" {
			projects++
		}
	}
	if status != 200 || len(items) != 4 || projects != 1 {
		t.Fatalf("all projects: %d %s", status, raw)
	}
	if status, items, _, raw := rankedPage(t, s, c, map[string]any{"query": "connection timeout", "scope": map[string]any{"all_projects": true}, "state": "closed"}); status != 200 || len(items) != 0 {
		t.Fatalf("closed: %d %s", status, raw)
	}
}

// Cursors continue the same query in rank order; another query's cursor is
// refused.
func TestRankedSearchCursorPaging(t *testing.T) {
	s, c, _, _ := rankedWorld(t)
	q := map[string]any{"query": "connection timeout", "scope": map[string]any{"all_projects": true}, "limit": 1}
	_, single, _, _ := rankedPage(t, s, c, map[string]any{"query": "connection timeout", "scope": map[string]any{"all_projects": true}})
	want := []string{}
	for _, v := range single {
		want = append(want, v.ID)
	}
	var got []string
	for {
		status, items, next, raw := rankedPage(t, s, c, q)
		if status != 200 || len(items) != 1 {
			t.Fatalf("%d %s", status, raw)
		}
		got = append(got, items[0].ID)
		if next == nil {
			break
		}
		q["cursor"] = *next
	}
	if strings.Join(got, " ") != strings.Join(want, " ") || len(got) != 4 {
		t.Fatalf("pages %v, want %v", got, want)
	}
	q["query"] = "timeout"
	if status, code, msg := serveError(s, context.Background(), c.ClientID, "POST", "/v1/ranked-search", mustString(q)); status != 422 || code != "validation_failed" || !strings.Contains(msg, "cursor does not match") {
		t.Fatal(status, code, msg)
	}
	q["cursor"] = "not-a-cursor!"
	if status, code, _ := serveError(s, context.Background(), c.ClientID, "POST", "/v1/ranked-search", mustString(q)); status != 422 || code != "validation_failed" {
		t.Fatal(status, code)
	}
}

func mustString(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestRankedSearchValidation(t *testing.T) {
	s, c, _, _ := rankedWorld(t)
	all := map[string]any{"all_projects": true}
	for _, v := range []struct {
		body       string
		status     int
		code, text string
	}{
		{mustString(map[string]any{"query": "", "scope": all}), 422, "validation_failed", "query must be nonempty"},
		{mustString(map[string]any{"query": "x"}), 422, "validation_failed", "select a project"},
		{mustString(map[string]any{"query": "x", "scope": map[string]any{"project_id": "feat/missing"}}), 404, "", ""},
		{mustString(map[string]any{"query": "x", "scope": all, "limit": 101}), 422, "validation_failed", "limit must be between 1 and 100"},
		{mustString(map[string]any{"query": strings.Repeat("a", 1001), "scope": all}), 422, "validation_failed", "1000 bytes"},
		{`{"query":"x","scope":{"all_projects":true},"regex":true}`, 400, "invalid_json", "unknown ranked search field"},
		{`{"query":"x","scope":{"all_projects":true},"state":"done"}`, 400, "invalid_json", "state must be open or closed"},
	} {
		status, code, msg := serveError(s, context.Background(), c.ClientID, "POST", "/v1/ranked-search", v.body)
		if status != v.status || v.code != "" && (code != v.code || !strings.Contains(msg, v.text)) {
			t.Errorf("%s: %d %s %q", v.body, status, code, msg)
		}
	}
	for _, query := range []string{"C++", "net/http", `"unbalanced`, "-", "++", "a OR", "NEAR(x", "title:x", strings.Repeat("€", 333) + "a"} {
		if status, code, msg := serveError(s, context.Background(), c.ClientID, "POST", "/v1/ranked-search", mustString(map[string]any{"query": query, "scope": all})); status != 200 {
			t.Errorf("%q: %d %s %s", query, status, code, msg)
		}
	}
	if status, code, _ := serveError(s, context.Background(), c.ClientID, "GET", "/v1/ranked-search", ""); status != 404 || code != "not_found" {
		t.Fatal(status, code)
	}
}

// A ranked search runs under the search deadline like lit grep.
func TestSlowRankedSearchTimesOut(t *testing.T) {
	const timeout = 20 * time.Millisecond
	s, c := searchLimitFixture(t, 1, timeout)
	clock := newParkedClock(s, time.Now().UTC())
	finish := park(t, clock, func() [3]any {
		status, code, msg := serveError(s, context.Background(), c.ClientID, "POST", "/v1/ranked-search", searchBody("anything"))
		return [3]any{status, code, msg}
	})
	time.Sleep(10 * timeout)
	if got := finish(); got[0] != 504 || got[1] != "search_timeout" {
		t.Fatal("slow ranked search", got)
	}
}
