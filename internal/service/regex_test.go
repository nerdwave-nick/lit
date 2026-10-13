package service

import (
	"errors"
	"fmt"
	"math/rand"
	"regexp"
	"regexp/syntax"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// regexWorld is one project whose issues, comment and milestone hold the
// regex acceptance cases; it returns the issue IDs by title.
func regexWorld(t *testing.T) (*Server, protocol.Client, string, map[string]string) {
	t.Helper()
	s, c := projectTestServer(t, t.TempDir())
	t.Cleanup(func() { s.DB.Close() })
	p := protocol.UUID()
	mustExecute(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: p, Title: textPointer("feat/regex"), Content: textPointer("Project notes\ncall 555-1234 for help")}))
	ids := map[string]string{}
	items := []protocol.ProjectInput{}
	for _, v := range [][2]string{
		{"Connection errors", "dial tcp: ECONNREFUSED\nread: econnreset by peer\nother line"},
		{"Anchors", "start of line\n  indented start\nend here"},
		{"Straße", "Die Straße\nstrasse lower"},
		{"GROẞE", "capital sharp s"},
		{"Lines", "alpha\nbeta"},
		{"Colors", "the colour red\nthe color blue"},
	} {
		id := protocol.UUID()
		ids[v[0]] = id
		items = append(items, protocol.ProjectInput{ID: id, Title: textPointer(v[0]), Content: textPointer(v[1])})
	}
	prep, e := s.PrepareRecords(protocol.PrepareRequest{Operation: "issue.create", Project: p, Items: items}, c)
	if e != nil {
		t.Fatal(e)
	}
	mustExecute(t, s, c, prep)
	prep, e = s.PrepareRecords(protocol.PrepareRequest{Operation: "comment.create", Project: p, Items: []protocol.ProjectInput{{ID: protocol.UUID(), Issue: ids["Anchors"], Content: textPointer("one\ntwo\nECONNRESET in a comment\nfour")}}}, c)
	if e != nil {
		t.Fatal(e)
	}
	mustExecute(t, s, c, prep)
	return s, c, p, ids
}

// regexHit is one result of a regex search: the record's type, its issue's
// title (or "" for projects), and each excerpt as field:match_line.
func regexHits(t *testing.T, s *Server, c protocol.Client, q protocol.SearchRequest) []string {
	t.Helper()
	page, e := searchPageOf(s, q, c)
	if e != nil {
		t.Fatalf("%q: %v", q.Query, e)
	}
	out := []string{}
	for _, item := range page.Items {
		v := item.(protocol.SearchResult)
		hit := v.Type + " " + v.IssueTitle
		for _, ex := range v.Excerpts {
			hit += fmt.Sprintf(" %s:%d", ex.Field, ex.MatchLine)
		}
		out = append(out, hit)
	}
	sort.Strings(out)
	return out
}

// lit grep -E: Go RE2, line by line (^ and $ per line; nothing crosses a
// newline), per field, case-insensitive with RE2's simple folding unless
// case-sensitive, with literal grep's result shape.
func TestRegexGrep(t *testing.T) {
	s, c, p, _ := regexWorld(t)
	for _, tc := range []struct {
		pattern   string
		sensitive bool
		want      []string
	}{
		// The user story, and alternation.
		{`econn(refused|reset)`, false, []string{"comments Anchors body:3", "issues Connection errors body:1"}},
		{`econn(refused|reset)`, true, []string{"issues Connection errors body:2"}},
		{`ECONNREFUSED|colour`, true, []string{"issues Colors body:1", "issues Connection errors body:1"}},
		{`dial\s+tcp|colour\s+red`, false, []string{"issues Colors body:1", "issues Connection errors body:1"}},
		// Anchors match per line.
		{`^start`, false, []string{"issues Anchors body:1"}},
		{`^indented`, false, []string{}},
		{`^\s+indented`, false, []string{"issues Anchors body:2"}},
		{`^end here$`, false, []string{"issues Anchors body:3"}},
		{`\Aend`, false, []string{"issues Anchors body:3"}},
		{`^alpha$`, false, []string{"issues Lines body:1"}},
		// Nothing crosses a newline, whatever the flags.
		{`alpha.beta`, false, []string{}},
		{`(?s)alpha.beta`, false, []string{}},
		{`alpha\sbeta`, false, []string{}},
		{`alpha[^x]beta`, false, []string{}},
		{`alpha\nbeta`, false, []string{}},
		// Case, and RE2's simple folding: ss is not ß, but ß is ẞ.
		{`STRASSE`, false, []string{"issues Straße body:2"}},
		{`strasse`, true, []string{"issues Straße body:2"}},
		{`STRASSE`, true, []string{}},
		{`straße`, false, []string{"issues Straße title:1 body:1"}},
		{`gro(ß|ss)e`, false, []string{"issues GROẞE title:1"}},
		{`groSSe`, false, []string{}},
		{`(?-i)gro`, false, []string{}},
		// Patterns without a literal of three or more characters scan.
		{`\d{3}-\d{4}`, false, []string{"projects  description:2"}},
		{`colou?r`, false, []string{"issues Colors title:1 body:1"}},
		{`^b`, false, []string{"issues Lines body:2"}},
		{`x*`, true, []string{"comments Anchors body:1", "issues Anchors title:1 body:1", "issues Colors title:1 body:1", "issues Connection errors title:1 body:1", "issues GROẞE title:1 body:1", "issues Lines title:1 body:1", "issues Straße title:1 body:1", "projects  description:1"}},
	} {
		got := regexHits(t, s, c, protocol.SearchRequest{Scope: protocol.SearchScope{ProjectID: p}, Query: tc.pattern, CaseSensitive: tc.sensitive, Regex: true})
		if !slices.Equal(got, tc.want) {
			t.Errorf("pattern %q case-sensitive %v: %q, want %q", tc.pattern, tc.sensitive, got, tc.want)
		}
	}
	// Literal grep keeps full folding: strasse finds the ß title too.
	if got := regexHits(t, s, c, protocol.SearchRequest{Scope: protocol.SearchScope{ProjectID: p}, Query: "strasse"}); !slices.Equal(got, []string{"issues Straße title:1 body:1"}) {
		t.Errorf("literal strasse: %q", got)
	}
}

// Excerpts are literal grep's: the first matching line per field, with
// context lines and line numbers.
func TestRegexGrepExcerpts(t *testing.T) {
	s, c, p, _ := regexWorld(t)
	page, e := searchPageOf(s, protocol.SearchRequest{Scope: protocol.SearchScope{ProjectID: p}, Query: `econn\w+`, Regex: true, ContextLines: 1, CaseSensitive: true}, c)
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	ex := page.Items[0].(protocol.SearchResult).Excerpts
	if len(ex) != 1 || ex[0] != (protocol.SearchExcerpt{Field: "body", Text: "dial tcp: ECONNREFUSED\nread: econnreset by peer\nother line", StartLine: 1, MatchLine: 2}) {
		t.Fatalf("%+v", ex)
	}
}

// Invalid patterns, including RE2's missing backreferences and lookarounds,
// are validation_failed; so is a pattern over the length limit.
func TestRegexGrepRefusesInvalidPatterns(t *testing.T) {
	s, c, p, _ := regexWorld(t)
	for _, pattern := range []string{`(abc`, `a{2,1}`, `\8`, `(?<=a)b`, `(?=a)`, `a**`, `[z-a]`, `x{1001}`, strings.Repeat("a", 1001)} {
		_, e := searchPageOf(s, protocol.SearchRequest{Scope: protocol.SearchScope{ProjectID: p}, Query: pattern, Regex: true}, c)
		var pe *protocol.Error
		if !errors.As(e, &pe) || pe.Status != 422 || pe.Code != "validation_failed" {
			t.Errorf("pattern %q: %v", pattern, e)
		}
	}
	// The same text is a valid literal query.
	if _, e := searchPageOf(s, protocol.SearchRequest{Scope: protocol.SearchScope{ProjectID: p}, Query: `(abc`}, c); e != nil {
		t.Fatal(e)
	}
	code, v := directRequest(t, s, c, "POST", "/v1/search", map[string]any{"query": "(abc", "regex": true, "scope": map[string]any{"project_id": p}})
	if code != 422 || !strings.Contains(fmt.Sprint(v), "RE2") {
		t.Fatal(code, v)
	}
}

// A cursor is bound to the mode: a literal cursor does not continue a regex
// search of the same text, and pages continue as for literal grep.
func TestRegexGrepPages(t *testing.T) {
	s, c, p, _ := regexWorld(t)
	q := protocol.SearchRequest{Scope: protocol.SearchScope{ProjectID: p}, Query: `e`, Regex: true, Limit: 2}
	seen := 0
	for {
		page, e := searchPageOf(s, q, c)
		if e != nil {
			t.Fatal(e)
		}
		seen += len(page.Items)
		if page.NextCursor == nil {
			break
		}
		literal := q
		literal.Regex, literal.Cursor = false, *page.NextCursor
		if _, e := searchPageOf(s, literal, c); e == nil {
			t.Fatal("a regex cursor continued a literal search")
		}
		q.Cursor = *page.NextCursor
	}
	if seen != 8 {
		t.Fatalf("%d results over the pages, want 8", seen)
	}
}

// regexNeedles extracts folded literals one of which every match contains,
// or none when the pattern requires no literal of three or more characters.
func TestRegexNeedles(t *testing.T) {
	for _, tc := range []struct {
		pattern   string
		sensitive bool
		want      []string
	}{
		{`econn(refused|reset)`, false, []string{"econnrefused", "econnreset"}},
		{`ECONN(REFUSED|RESET)`, true, []string{"econnrefused", "econnreset"}},
		{`foo.*bar`, false, []string{"foo"}},
		{`x+longer.*bar`, false, []string{"longer"}},
		{`(?i)ERROR`, true, []string{"error"}},
		{`^Straße$`, true, []string{"strasse"}},
		{`colou?r`, false, []string{"color", "colour"}},
		{`[Ee]rror`, true, []string{"error"}},
		{`(abc|defg)+h`, false, []string{"abc", "defg"}},
		{`foo.*x|bar\s+y`, false, []string{"bar", "foo"}},
		{`foo.*x|b.*y`, false, nil},
		{`abc|xy`, false, nil},
		{`ab`, false, nil},
		{`a.c`, false, nil},
		{`[0-9]+`, false, nil},
		{`x*`, false, nil},
		{`(abc)*`, false, nil},
		{`(abc){0,2}`, false, nil},
		{`(abc){2}`, false, []string{"abc"}},
		{`\x00\x00\x00`, true, []string{"\x00\x00\x00"}},
	} {
		src := tc.pattern
		if !tc.sensitive {
			src = "(?i)" + src
		}
		tree, e := syntax.Parse(src, syntax.Perl)
		if e != nil {
			t.Fatal(e)
		}
		got := regexNeedles(tree)
		sort.Strings(got)
		if !slices.Equal(got, tc.want) {
			t.Errorf("pattern %q case-sensitive %v: %q, want %q", tc.pattern, tc.sensitive, got, tc.want)
		}
	}
}

// scanRegex is lit grep -E without its prefilter: every record of the scope
// matched line by line with a regexp compiled on its own, sorted and paged.
func scanRegex(t *testing.T, s *Server, q protocol.SearchRequest, c protocol.Client) [][]protocol.SearchResult {
	t.Helper()
	src := q.Query
	if !q.CaseSensitive {
		src = "(?i)" + src
	}
	re := regexp.MustCompile(src)
	scope := q.Scope.ProjectID
	if q.Scope.AllProjects {
		scope = ""
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
			if ex, ok := regexExcerpt(field.Name, field.Text, re, q.ContextLines); ok {
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

// sameRegex requires identical pages from lit grep -E and scanRegex.
func sameRegex(t *testing.T, s *Server, q protocol.SearchRequest, c protocol.Client) int {
	t.Helper()
	q.Regex = true
	got, want := indexedSearch(t, s, q, c), scanRegex(t, s, q, c)
	if g, w := mustJSON(got), mustJSON(want); string(g) != string(w) {
		t.Fatalf("pattern %q case-sensitive %v context %d limit %d scope %+v:\nindexed %s\nscan    %s", q.Query, q.CaseSensitive, q.ContextLines, q.Limit, q.Scope, g, w)
	}
	n := 0
	for _, p := range want {
		n += len(p)
	}
	return n
}

// The prefilter only drops records that cannot match: over the literal
// grep acceptance world plus case-folding hazards (Cherokee and the newest
// Latin letters fold differently in RE2 and in lit's full fold; the Kelvin
// sign; ß and ẞ), every pattern's pages equal a scan of every record.
func TestRegexGrepPrefilterMatchesTheScan(t *testing.T) {
	s, c, a, _ := grepWorld(t)
	prep, e := s.PrepareRecords(protocol.PrepareRequest{Operation: "issue.create", Project: a, Items: []protocol.ProjectInput{
		{ID: protocol.UUID(), Title: textPointer("Cherokee upper"), Content: textPointer("ᏣᎳᎩ")},
		{ID: protocol.UUID(), Title: textPointer("Cherokee lower"), Content: textPointer("ꮳꮃꭹ")},
		{ID: protocol.UUID(), Title: textPointer("Lambda small"), Content: textPointer("ƛƛƛ")},
		{ID: protocol.UUID(), Title: textPointer("Lambda capital"), Content: textPointer("\uA7DC\uA7DC\uA7DC")},
		{ID: protocol.UUID(), Title: textPointer("Kelvin signs"), Content: textPointer("\u212A\u212A\u212A and ſſſ")},
	}}, c)
	if e != nil {
		t.Fatal(e)
	}
	mustExecute(t, s, c, prep)
	patterns := []string{
		`net/http`, `C\+\+`, `\bGo\b`, `UI`, `stra(ss|ß)e`, `STRASSE`, `ß`, `ẞ`, `caf(e|é)`, `foo\.bar`, `a_b`, `100%`, `(?i)app-\d+`,
		`needle`, `^line (two|three)`, `needle$`, `^a needle`, `\[bracket\]`, `a\*b\?c`, `\x00`, `\x{FFFD}`, `ꮳꮃꭹ`, `ᏣᎳᎩ`, `ƛƛƛ`, "\uA7DC\uA7DC\uA7DC", `\x{A7DC}+`,
		`kkk`, `KKK`, `sss`, `ſſſ`, `σίσυφοσ`, `ΣΊΣΥΦΟΣ`, `(?i)ﬃ`, `^$`, `.`, `^.{3}$`, `[[:upper:]]{3}`, `\p{Greek}+`, `x*`, `absent|nothing`,
		`(needle|hay)+ here`, `colou?r`, `[Ee]\x{0301}`,
	}
	results := 0
	for _, pattern := range patterns {
		for _, sensitive := range []bool{false, true} {
			for _, limit := range []int{1, 3, 0} {
				for _, scope := range []protocol.SearchScope{{ProjectID: a}, {AllProjects: true}} {
					results += sameRegex(t, s, protocol.SearchRequest{Scope: scope, Query: pattern, CaseSensitive: sensitive, ContextLines: limit % 2, Limit: limit}, c)
				}
			}
		}
	}
	if results == 0 {
		t.Fatal("no pattern matched anything")
	}
	// Spot checks that the hazards mean what they say: each case of a
	// Cherokee or a lambda pattern finds the records in either case.
	for _, tc := range []struct {
		pattern string
		want    int
	}{{`ᏣᎳᎩ`, 2}, {`ꮳꮃꭹ`, 2}, {`ƛƛƛ`, 2}, {"\uA7DC\uA7DC\uA7DC", 2}, {`kkk`, 1}, {`sss`, 1}, {`absent|nothing`, 0}} {
		if got := sameRegex(t, s, protocol.SearchRequest{Scope: protocol.SearchScope{ProjectID: a}, Query: tc.pattern, Limit: 1000}, c); got != tc.want {
			t.Errorf("pattern %q matched %d records, want %d", tc.pattern, got, tc.want)
		}
	}
}

// Random patterns built from random slices of random hazard text, with
// alternation, optional parts and classes, match the scan exactly.
func TestRegexGrepPrefilterMatchesTheScanForRandomPatterns(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	p := protocol.UUID()
	mustExecute(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: p, Title: textPointer("feat/random-regex"), Content: textPointer("")}))
	alphabet := []rune("aAbBsSßẞeEéÉ\u0301kK\u212aσςΣﬃꮳᏣ\uA7DCƛ \n\x00")
	r := rand.New(rand.NewSource(11))
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
		items = append(items, protocol.ProjectInput{ID: protocol.UUID(), Title: textPointer(fmt.Sprintf("Random %d", n)), Content: textPointer(body)})
	}
	prep, e := s.PrepareRecords(protocol.PrepareRequest{Operation: "issue.create", Project: p, Items: items}, c)
	if e != nil {
		t.Fatal(e)
	}
	mustExecute(t, s, c, prep)
	piece := func() string {
		body := []rune(bodies[r.Intn(len(bodies))])
		if len(body) == 0 {
			return regexp.QuoteMeta(text(3))
		}
		i := r.Intn(len(body))
		return regexp.QuoteMeta(string(body[i:min(len(body), i+1+r.Intn(5))]))
	}
	for n := 0; n < 300; n++ {
		pattern := piece()
		switch n % 5 {
		case 1:
			pattern += "|" + piece()
		case 2:
			pattern = "(" + pattern + ")?" + piece()
		case 3:
			pattern += "[sS]" + piece()
		case 4:
			pattern = strings.ToUpper(pattern)
		}
		sameRegex(t, s, protocol.SearchRequest{Scope: protocol.SearchScope{ProjectID: p}, Query: pattern, CaseSensitive: n%3 == 0, Limit: 1 + r.Intn(4)}, c)
	}
}
