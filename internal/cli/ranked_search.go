package cli

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// lit search QUERY: ranked search (POST /v1/ranked-search). Like grep, its
// operand is free text, so it has its own parser: -- ends the options, and a
// query such as help or -flaky (after --) is searched for.

func parseRankedSearchArgs(argv []string, a Args) (Args, error) {
	a.Verb = ""
	cmd := command("search")
	seenCommand, literal := false, false
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		switch {
		case !seenCommand && arg == "search":
			seenCommand = true
			continue
		case literal || !strings.HasPrefix(arg, "-"):
			a.Positionals = append(a.Positionals, arg)
			continue
		case arg == "--":
			literal = true
			continue
		case arg == "--help" || arg == "-h":
			a.Command = "help"
			return a, nil
		}
		parts := strings.SplitN(strings.TrimPrefix(arg, "--"), "=", 2)
		key := parts[0]
		f, ok := cmd.Flag(key)
		if !ok || !strings.HasPrefix(arg, "--") {
			return a, fmt.Errorf("unknown search option %s; put a query that begins with a dash after --", arg)
		}
		if a.Has(key) {
			return a, fmt.Errorf("duplicate search option %s", arg)
		}
		value := "true"
		switch {
		case f.Switch && len(parts) == 2:
			return a, fmt.Errorf("option takes no value")
		case !f.Switch && len(parts) == 2:
			value = parts[1]
		case !f.Switch:
			if i++; i >= len(argv) {
				return a, fmt.Errorf("missing option value")
			}
			value = argv[i]
		}
		a.Values[key] = append(a.Values[key], value)
	}
	return a, validateRankedSearchArgs(a)
}

func validateRankedSearchArgs(a Args) error {
	if len(a.Positionals) != 1 || a.Positionals[0] == "" || !utf8.ValidString(a.Positionals[0]) {
		return fmt.Errorf("search requires one nonempty UTF-8 QUERY")
	}
	if a.Has("project") && (a.Has("all-projects") || a.One("project") == "") {
		return fmt.Errorf("invalid or conflicting project scope")
	}
	if a.Has("state") && a.One("state") != "open" && a.One("state") != "closed" {
		return fmt.Errorf("state must be open or closed")
	}
	if a.Has("limit") {
		if n, e := strconv.Atoi(a.One("limit")); e != nil || n < 1 || n > 100 {
			return fmt.Errorf("limit must be between 1 and 100")
		}
	}
	if a.Has("cursor") && a.One("cursor") == "" {
		return fmt.Errorf("empty cursor")
	}
	if a.Has("format") && !validOutputFormat(a.One("format")) {
		return fmt.Errorf("invalid format")
	}
	if a.Has("timeout") {
		if d, e := time.ParseDuration(a.One("timeout")); e != nil || d <= 0 {
			return fmt.Errorf("timeout must be positive")
		}
	}
	return nil
}

func (a *App) rankedSearch() (Result, error) {
	q := protocol.RankedSearchRequest{Query: a.Args.Positionals[0], State: a.Args.One("state"), Cursor: a.Args.One("cursor")}
	q.Limit, _ = strconv.Atoi(a.Args.One("limit"))
	q.Scope.AllProjects = a.Args.Has("all-projects")
	if !q.Scope.AllProjects {
		p, e := a.selectedProject()
		if e != nil {
			return Result{}, protocol.E(400, "invalid_arguments", e.Error())
		}
		q.Scope.ProjectID = p
	}
	page, e := a.RankedSearch(a.Context, q)
	result := Result{Items: []any{}, NextCursor: page.NextCursor}
	for _, v := range page.Items {
		result.Items = append(result.Items, v)
	}
	return result, e
}

// highlightMatches reports whether search output marks matches: on a
// terminal, unless NO_COLOR is set.
func (a *App) highlightMatches() bool { return isTerminal(a.Out) && os.Getenv("NO_COLOR") == "" }

// formatRankedSearch is the human output of a ranked search page: each
// result with its matches, matched words in bold when highlight is set.
func formatRankedSearch(r Result, highlight bool) []byte {
	var b bytes.Buffer
	for _, item := range r.Items {
		v := item.(protocol.RankedSearchResult)
		fmt.Fprintf(&b, "%s %s project=%s revision=%d", v.Type, v.ID, v.ProjectID, v.Revision)
		if v.State != "" {
			fmt.Fprintf(&b, " state=%s", v.State)
		}
		fmt.Fprintf(&b, " %s\n", humanText(v.Title))
		comments := 0
		for _, m := range v.Matches {
			name := m.Field
			if m.CommentID != "" {
				name += " " + m.CommentID
				comments++
			}
			fmt.Fprintf(&b, "  %s: %s\n", name, snippetText(m, highlight))
		}
		if more := v.CommentMatches - comments; more > 0 {
			fmt.Fprintf(&b, "  %d more matching comments\n", more)
		}
	}
	if len(r.Items) == 0 {
		fmt.Fprintln(&b, "No matches.")
	}
	if r.NextCursor != nil {
		fmt.Fprintf(&b, "next_cursor: %s\n", *r.NextCursor)
	}
	fmt.Fprintf(&b, "server_time: %s\n", r.ServerTime)
	return b.Bytes()
}

// snippetText is a snippet with controls shown visibly and, with highlight,
// its highlights in bold. Out-of-range highlights are ignored.
func snippetText(m protocol.RankedMatch, highlight bool) string {
	if !highlight {
		return humanText(m.Snippet)
	}
	var b strings.Builder
	at := 0
	for _, h := range m.Highlights {
		if h.Start < at || h.End <= h.Start || h.End > len(m.Snippet) {
			continue
		}
		b.WriteString(humanText(m.Snippet[at:h.Start]) + "\x1b[1m" + humanText(m.Snippet[h.Start:h.End]) + "\x1b[22m")
		at = h.End
	}
	b.WriteString(humanText(m.Snippet[at:]))
	return b.String()
}

const rankedSearchHelp = "Search the selected project, or explicitly use --all-projects, for the most relevant issues,\n" +
	"projects and milestones first. An issue's result includes its matching comments.\n" +
	"Words match whole words, ignoring case and diacritics (cafe finds Café); all words must match.\n" +
	"\"a phrase\" matches words in order, -word excludes, a OR b matches either, and word* matches prefixes.\n" +
	"Everything else is plain text. Titles count most, then bodies and descriptions, then comments.\n" +
	"Use -- before a query beginning with a dash. For literal substrings, use lit grep."
