package storage

import (
	"strings"
	"testing"
)

// The search box syntax becomes an FTS5 query in which every term is a
// quoted string: words are ANDed, "phrases", -exclusions, OR between two
// terms and word* prefixes are operators, and everything else is plain text,
// so no input is FTS5 syntax.
func TestRankQueryParsesTheSearchBoxSyntax(t *testing.T) {
	for _, c := range []struct{ query, want string }{
		{"connection timeout", `"connection" AND "timeout"`},
		{"  Connection\t\ntimeout  ", `"Connection" AND "timeout"`},
		{`"connection timeout"`, `"connection timeout"`},
		{`"connection timeout" retry`, `"connection timeout" AND "retry"`},
		{"timeout -flaky", `("timeout") NOT ("flaky")`},
		{`timeout -flaky -"known issue"`, `("timeout") NOT ("flaky" OR "known issue")`},
		{"a OR b c", `("a" OR "b") AND "c"`},
		{"a OR b OR c", `("a" OR "b" OR "c")`},
		{"caf*", `"caf" *`},
		{"caf** OR tea", `("caf" * OR "tea")`},
		{"C++", `"C++"`},
		{"net/http", `"net/http"`},
		{"APP-123", `"APP-123"`},
		{"--force", `"--force"`},
		{"a*b", `"a*b"`},
		{`foo"bar`, `"foo""bar"`},
		{`say "hello`, `"say" AND "hello"`},
		{`say "hello world`, `"say" AND "hello world"`},
		{`"unbalanced" "`, `"unbalanced"`},
		{"title:foo", `"title:foo"`},
		{"NEAR(a b) AND NOT x", `"NEAR(a" AND "b)" AND "AND" AND "NOT" AND "x"`},
		{"OR", `"OR"`},
		{"a OR", `"a" AND "OR"`},
		{"OR a", `"OR" AND "a"`},
		{"a OR OR b", `"a" AND "OR" AND "OR" AND "b"`},
		{"a OR -b", `("a" AND "OR") NOT ("b")`},
		{"or a", `"or" AND "a"`},
		{"a OR ++ b", `("a" OR "b")`},
		{"- a", `"a"`},
		{"a\x00b", `"a" AND "b"`},
		{"Café", `"Café"`},
		{"café café", `"café" AND "cafe` + "́" + `"`},
	} {
		got, ok := rankQuery(c.query)
		if !ok || got != c.want {
			t.Errorf("rankQuery(%q) = %q %v, want %q", c.query, got, ok, c.want)
		}
	}
}

// A query without anything to search for, such as only exclusions or only
// punctuation, matches nothing instead of failing.
func TestRankQueryWithoutWordsMatchesNothing(t *testing.T) {
	for _, query := range []string{"", " ", `"`, `""`, "++", "-", "*", "-flaky", `-"a b" -c`, "\x00", "🚀", "́"} {
		if got, ok := rankQuery(query); ok || got != "" {
			t.Errorf("rankQuery(%q) = %q %v, want nothing", query, got, ok)
		}
	}
}

// Every query of at most 1,000 bytes is valid FTS5, however its operators
// nest: exclusions are one NOT, so no expression grows deep.
func TestRankQueryStaysShallow(t *testing.T) {
	got, ok := rankQuery("a " + strings.Repeat("-b ", 330))
	if !ok || strings.Count(got, "NOT") != 1 {
		t.Fatalf("%q %v", got, ok)
	}
}
