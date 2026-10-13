package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/storage"
)

// scanSearch is lit grep as it was before the search index: read every
// record of the scope, match each field with searchExcerpt, sort by project,
// type and ID, and cut pages of limit results. It returns the pages.
func scanSearch(t *testing.T, s *Server, q protocol.SearchRequest, c protocol.Client) [][]protocol.SearchResult {
	t.Helper()
	scope := q.Scope.ProjectID
	if q.Scope.AllProjects {
		scope = ""
	} else if scope == "" {
		scope = *c.ProjectID
	}
	records, e := inSnapshot(s, func(rs *Server) ([]storage.SearchRecord, error) {
		var out []storage.SearchRecord
		e := rs.q().SearchScan(rs.ctx(), scope, "", func(v storage.SearchRecord) error { out = append(out, v); return nil })
		return out, e
	})
	if e != nil {
		t.Fatal(e)
	}
	matches := []protocol.SearchResult{}
	for _, record := range records {
		result := protocol.SearchResult{Type: record.Type, ID: record.ID, ProjectID: record.ProjectID, IssueID: record.IssueID, IssueTitle: record.IssueTitle, Revision: record.Revision, Excerpts: []protocol.SearchExcerpt{}}
		for _, field := range record.Fields {
			if ex, ok := searchExcerpt(field.Name, field.Text, q.Query, q.CaseSensitive, q.ContextLines); ok {
				result.Excerpts = append(result.Excerpts, ex)
			}
		}
		if len(result.Excerpts) > 0 {
			matches = append(matches, result)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return searchKey(matches[i]) < searchKey(matches[j]) })
	limit := q.Limit
	if limit == 0 {
		limit = 100
	}
	pages := [][]protocol.SearchResult{}
	for len(matches) > limit {
		pages = append(pages, matches[:limit])
		matches = matches[limit:]
	}
	return append(pages, matches)
}

// indexedSearch pages through lit grep with its cursors.
func indexedSearch(t *testing.T, s *Server, q protocol.SearchRequest, c protocol.Client) [][]protocol.SearchResult {
	t.Helper()
	pages := [][]protocol.SearchResult{}
	for {
		page, e := searchPageOf(s, q, c)
		if e != nil {
			t.Fatalf("%+v: %v", q, e)
		}
		items := []protocol.SearchResult{}
		for _, v := range page.Items {
			items = append(items, v.(protocol.SearchResult))
		}
		pages = append(pages, items)
		if page.NextCursor == nil {
			return pages
		}
		// The cursor names the page's last result, as before.
		raw, e := base64.RawURLEncoding.DecodeString(*page.NextCursor)
		var cursor searchCursor
		if e != nil || json.Unmarshal(raw, &cursor) != nil || len(items) == 0 || cursor.After != searchKey(items[len(items)-1]) {
			t.Fatalf("%+v: cursor %q after page %v", q, *page.NextCursor, items)
		}
		q.Cursor = *page.NextCursor
	}
}

// sameSearch requires byte-identical pages from the indexed search and the
// scan of every record.
func sameSearch(t *testing.T, s *Server, q protocol.SearchRequest, c protocol.Client) int {
	t.Helper()
	got, want := indexedSearch(t, s, q, c), scanSearch(t, s, q, c)
	if g, w := mustJSON(got), mustJSON(want); string(g) != string(w) {
		t.Fatalf("query %q case-sensitive %v context %d limit %d scope %+v:\nindexed %s\nscan    %s", q.Query, q.CaseSensitive, q.ContextLines, q.Limit, q.Scope, g, w)
	}
	n := 0
	for _, p := range want {
		n += len(p)
	}
	return n
}

// grepWorld creates two projects whose records hold the search acceptance
// cases, with enough needles for several pages.
func grepWorld(t *testing.T) (*Server, protocol.Client, string, string) {
	t.Helper()
	s, c := projectTestServer(t, t.TempDir())
	t.Cleanup(func() { s.DB.Close() })
	a, b := protocol.UUID(), protocol.UUID()
	mustExecute(t, s, c, prepareProjectTest(t, s, c, "create",
		protocol.ProjectInput{ID: a, Title: textPointer("feat/grep-a"), Content: textPointer("Uses net/http and C++.\nSee foo.bar.baz() for 100% of APP-123.\nneedle in the description")},
		protocol.ProjectInput{ID: b, Title: textPointer("feat/grep-b"), Content: textPointer("Other project: Go, UI and a needle")}))
	issues := map[string][]protocol.ProjectInput{
		a: {
			{Title: textPointer("Go UI work"), Content: textPointer("The UI uses Go.\nline two\nline three a_b\nline four\nline five needle")},
			{Title: textPointer("Straße repair"), Content: textPointer("Die Straße ist lang.\nSTRASSE upper\nstrasse lower")},
			{Title: textPointer("Café opening"), Content: textPointer("Café and cafe\u0301 decomposed\nCAFÉ upper")},
			{Title: textPointer("Wildcards"), Content: textPointer("glob * star ? query [bracket]\na*b?c[d]e and a_b\\c")},
			{Title: textPointer("NUL bytes"), Content: textPointer("before\x00after NUL\nreplacement \uFFFD char")},
			{Title: textPointer("Kelvin \u212a sign"), Content: textPointer("İstanbul ΣΊΣΥΦΟΣ ﬃ ligature\nµ micro and ẞ capital")},
			{Title: textPointer("APP-123 tracking"), Content: textPointer("app-123 lower, App-123 mixed")},
		},
		b: {
			{Title: textPointer("B needle"), Content: textPointer("C++ in another project\nnet/http too")},
			{Title: textPointer("B Straße"), Content: textPointer("")},
		},
	}
	for n := 0; n < 7; n++ {
		issues[a] = append(issues[a], protocol.ProjectInput{Title: textPointer(fmt.Sprintf("Needle %d", n)), Content: textPointer(strings.Repeat("hay\n", n) + "a needle here\n" + strings.Repeat("hay\n", 3))})
	}
	ids := map[string][]string{}
	for _, p := range []string{a, b} {
		for i := range issues[p] {
			issues[p][i].ID = protocol.UUID()
			ids[p] = append(ids[p], issues[p][i].ID)
		}
		prep, e := s.PrepareRecords(protocol.PrepareRequest{Operation: "issue.create", Project: p, Items: issues[p]}, c)
		if e != nil {
			t.Fatal(e)
		}
		mustExecute(t, s, c, prep)
	}
	comment := func(issue string, bodies ...string) {
		t.Helper()
		items := []protocol.ProjectInput{}
		for _, body := range bodies {
			items = append(items, protocol.ProjectInput{ID: protocol.UUID(), Issue: issue, Content: textPointer(body)})
		}
		prep, e := s.PrepareRecords(protocol.PrepareRequest{Operation: "comment.create", Project: a, Items: items}, c)
		if e != nil {
			t.Fatal(e)
		}
		mustExecute(t, s, c, prep)
	}
	comment(ids[a][0], "needle in a comment", "C++ and Go in a comment")
	comment(ids[a][1], "strasse in a comment", "APP-123 in a comment\nwith context")
	prep, e := s.PrepareRecords(protocol.PrepareRequest{Operation: "milestone.create", Project: a, Items: []protocol.ProjectInput{{ID: protocol.UUID(), Title: textPointer("M1 net/http"), Content: textPointer("Goal: Straße, UI and a needle")}}}, c)
	if e != nil {
		t.Fatal(e)
	}
	mustExecute(t, s, c, prep)
	return s, c, a, b
}

// The research doc's acceptance cases, and the cases that tell grep's fold
// from the index's (expanding folds, decomposed text, NUL, GLOB's wildcards),
// give byte-identical results and pages through the index and through a scan
// of every record.
func TestGrepThroughTheIndexMatchesTheScan(t *testing.T) {
	s, c, a, _ := grepWorld(t)
	queries := []string{
		"net/http", "C++", "c++", "Go", "UI", "ui", "strasse", "Straße", "STRASSE", "ß", "ss", "ẞ",
		"cafe", "café", "CAFÉ", "e\u0301", "e", "\u0301", "foo.bar", "a_b", "100%", "APP-123", "app-123",
		"needle", "a needle here\nhay", "line two\nline three", "*", "?", "[", "]", "a*b", "*b?c[", "[d]", "[bracket]",
		"\\", "_b\\c", "\x00", "e\x00a", "\uFFFD", "\uFFFD char", "\u212a", "k", "kelvin k", "i\u0307", "İstanbul",
		"σίσυφοσ", "ΣΊΣΥΦΟΣ", "ς", "ffi", "ﬃ", "μ", "µ", "absent", "no such text here",
	}
	results := 0
	for _, query := range queries {
		for _, sensitive := range []bool{false, true} {
			for _, context := range []int{0, 2} {
				for _, limit := range []int{1, 3, 0} {
					for _, scope := range []protocol.SearchScope{{ProjectID: a}, {AllProjects: true}} {
						results += sameSearch(t, s, protocol.SearchRequest{Scope: scope, Query: query, CaseSensitive: sensitive, ContextLines: context, Limit: limit}, c)
					}
				}
			}
		}
	}
	if results == 0 {
		t.Fatal("no query matched anything")
	}
	// Spot checks that the cases mean what they say.
	for _, c := range []struct {
		query     string
		sensitive bool
		want      bool
	}{{"strasse", false, true}, {"STRASSE", true, true}, {"cafe and", false, false}, {"e\u0301 dec", false, true}, {"e", true, true}, {"Go", false, true}, {"\x00", false, true}, {"absent", false, false}} {
		if got := sameSearch(t, s, protocol.SearchRequest{Scope: protocol.SearchScope{AllProjects: true}, Query: c.query, CaseSensitive: c.sensitive}, protocolClient(t, s)) > 0; got != c.want {
			t.Errorf("query %q case-sensitive %v matched %v, want %v", c.query, c.sensitive, got, c.want)
		}
	}
}

// protocolClient is a fresh client without a selected project.
func protocolClient(t *testing.T, s *Server) protocol.Client {
	t.Helper()
	c := protocol.Client{SchemaVersion: 1, ClientID: protocol.UUID(), StateRevision: 1, Status: "connected", Actor: protocol.Actor{Name: "Other", Kind: "agent"}}
	if e := s.SaveClient(c); e != nil {
		t.Fatal(e)
	}
	return c
}

// Random texts over an alphabet of folding hazards, queried with random
// slices of them (recased, decomposed or not), match the scan exactly.
func TestGrepThroughTheIndexMatchesTheScanForRandomText(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	p := protocol.UUID()
	mustExecute(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: p, Title: textPointer("feat/random"), Content: textPointer("")}))
	alphabet := []rune("aAbBsSßẞeEéÉ\u0301\u0308kK\u212aiIİıσςΣﬃ \n\r\x00\uFFFD*?[]\\%_.-")
	r := rand.New(rand.NewSource(7))
	text := func(n int) string {
		b := make([]rune, n)
		for i := range b {
			b[i] = alphabet[r.Intn(len(alphabet))]
		}
		return string(b)
	}
	bodies := []string{}
	items := []protocol.ProjectInput{}
	for n := 0; n < 40; n++ {
		body := text(r.Intn(60))
		bodies = append(bodies, body)
		items = append(items, protocol.ProjectInput{ID: protocol.UUID(), Title: textPointer(fmt.Sprintf("Random %d %s", n, strings.Map(func(r rune) rune {
			if r < ' ' || r == '\u0301' || r == '\u0308' {
				return 'x'
			}
			return r
		}, text(4)))), Content: textPointer(body)})
	}
	prep, e := s.PrepareRecords(protocol.PrepareRequest{Operation: "issue.create", Project: p, Items: items}, c)
	if e != nil {
		t.Fatal(e)
	}
	mustExecute(t, s, c, prep)
	for n := 0; n < 300; n++ {
		var query string
		switch body := []rune(bodies[r.Intn(len(bodies))]); {
		case n%4 == 0 || len(body) == 0:
			query = text(1 + r.Intn(4))
		default:
			i := r.Intn(len(body))
			query = string(body[i:min(len(body), i+1+r.Intn(5))])
			if n%3 == 0 {
				query = strings.ToUpper(query)
			}
		}
		sameSearch(t, s, protocol.SearchRequest{Scope: protocol.SearchScope{ProjectID: p}, Query: query, CaseSensitive: n%5 == 0, Limit: 1 + r.Intn(4)}, c)
	}
}
