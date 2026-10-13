package cli

import (
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

func TestSearchArguments(t *testing.T) {
	for _, c := range []struct {
		argv  []string
		query string
	}{
		{[]string{"search", "connection timeout", "--all-projects", "--state", "open", "--limit", "5"}, "connection timeout"},
		{[]string{"--format", "json", "search", "--", "-flaky timeout"}, "-flaky timeout"},
		{[]string{"search", "help"}, "help"},
		{[]string{"search", "--project", "feat/a", "C++", "--cursor", "c"}, "C++"},
	} {
		a, e := Parse(c.argv)
		if e != nil || a.Command != "search" || len(a.Positionals) != 1 || a.Positionals[0] != c.query {
			t.Fatalf("%v: %+v %v", c.argv, a, e)
		}
	}
	if a, e := Parse([]string{"search", "--help"}); e != nil || a.Command != "help" {
		t.Fatalf("help: %+v %v", a, e)
	}
	for _, argv := range [][]string{{"search"}, {"search", ""}, {"search", "a", "b"}, {"search", "-flaky"}, {"search", "a", "--state", "done"}, {"search", "a", "--limit", "0"},
		{"search", "a", "--limit", "101"}, {"search", "a", "--project", "feat/a", "--all-projects"}, {"search", "a", "--cursor", ""}, {"search", "a", "-C", "2"},
		{"search", "a", "--case-sensitive"}, {"search", "a", "--state", "open", "--state", "open"}} {
		if _, e := Parse(argv); e == nil {
			t.Errorf("accepted %v", argv)
		}
	}
}

func rankedResultFixture() Result {
	cursor := "next"
	return Result{ServerTime: "2026-10-12T00:00:00Z", NextCursor: &cursor, Items: []any{
		protocol.RankedSearchResult{Type: "issues", ID: "i1", ProjectID: "p1", Title: "Connection timeout handling", State: "open", Revision: 2, CommentMatches: 4, Matches: []protocol.RankedMatch{
			{Field: "title", Snippet: "Connection timeout handling", Highlights: []protocol.Highlight{{Start: 0, End: 10}, {Start: 11, End: 18}}},
			{Field: "comment", CommentID: "c1", Snippet: "…seen a timeout\x1b[2J in CI…", Highlights: []protocol.Highlight{{Start: 10, End: 17}}},
		}},
		protocol.RankedSearchResult{Type: "projects", ID: "p2", ProjectID: "p2", Title: "feat/other", Revision: 1, Matches: []protocol.RankedMatch{
			{Field: "description", Snippet: "The timeout budget", Highlights: []protocol.Highlight{{Start: 4, End: 11}}},
		}},
	}}
}

// Human output lists results in rank order with their matches; on a
// terminal the matched words are bold. Text from the tracker never carries
// terminal controls.
func TestRankedSearchHumanOutput(t *testing.T) {
	r := rankedResultFixture()
	plain := string(formatRankedSearch(r, false))
	want := "issues i1 project=p1 revision=2 state=open Connection timeout handling\n" +
		"  title: Connection timeout handling\n" +
		"  comment c1: …seen a timeout\\x1b[2J in CI…\n" +
		"  3 more matching comments\n" +
		"projects p2 project=p2 revision=1 feat/other\n" +
		"  description: The timeout budget\n" +
		"next_cursor: next\n" +
		"server_time: 2026-10-12T00:00:00Z\n"
	if plain != want {
		t.Fatalf("plain output\n%s\nwant\n%s", plain, want)
	}
	bold := string(formatRankedSearch(r, true))
	for _, part := range []string{"  title: \x1b[1mConnection\x1b[22m \x1b[1mtimeout\x1b[22m handling\n", "seen a \x1b[1mtimeout\x1b[22m\\x1b[2J in CI", "The \x1b[1mtimeout\x1b[22m budget"} {
		if !strings.Contains(bold, part) {
			t.Fatalf("terminal output lacks %q:\n%q", part, bold)
		}
	}
	if strings.Count(bold, "\x1b") != 8 {
		t.Fatalf("terminal output carries other escapes: %q", bold)
	}
	if empty := string(formatRankedSearch(Result{ServerTime: "t"}, false)); empty != "No matches.\nserver_time: t\n" {
		t.Fatalf("%q", empty)
	}
}
