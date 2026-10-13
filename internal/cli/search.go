package cli

import (
	"bytes"
	"fmt"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func parseSearchArgs(argv []string, a Args) (Args, error) {
	a.Verb = ""
	cmd := command("grep")
	seenCommand, literal := false, false
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if !seenCommand && arg == "grep" {
			seenCommand = true
			continue
		}
		if literal {
			a.Positionals = append(a.Positionals, arg)
			continue
		}
		if arg == "--" {
			literal = true
			continue
		}
		if arg == "--help" || arg == "-h" {
			a.Command = "help"
			return a, nil
		}
		if !strings.HasPrefix(arg, "-") {
			a.Positionals = append(a.Positionals, arg)
			continue
		}
		expanded := cmd.ExpandShort(arg)
		parts := strings.SplitN(strings.TrimPrefix(expanded, "--"), "=", 2)
		key := parts[0]
		f, ok := cmd.Flag(key)
		if !ok || !strings.HasPrefix(expanded, "--") {
			return a, fmt.Errorf("unknown grep option %s", arg)
		}
		takes := !f.Switch
		if a.Has(key) && !cmd.Repeatable(key) {
			return a, fmt.Errorf("duplicate grep option %s", arg)
		}
		value := "true"
		if takes {
			if len(parts) == 2 {
				value = parts[1]
			} else {
				i++
				if i >= len(argv) {
					return a, fmt.Errorf("missing option value")
				}
				value = argv[i]
			}
		} else if len(parts) == 2 {
			return a, fmt.Errorf("option takes no value")
		}
		a.Values[key] = append(a.Values[key], value)
	}
	if len(a.Positionals) != 1 || a.Positionals[0] == "" || !utf8.ValidString(a.Positionals[0]) {
		return a, fmt.Errorf("grep requires one nonempty UTF-8 literal pattern")
	}
	if a.Has("project") && (a.Has("all-projects") || a.One("project") == "") {
		return a, fmt.Errorf("invalid or conflicting project scope")
	}
	for _, key := range []string{"context", "limit"} {
		if a.Has(key) {
			n, e := strconv.Atoi(a.One(key))
			if e != nil || n < 0 || key == "limit" && (n < 1 || n > 1000) {
				return a, fmt.Errorf("invalid %s", key)
			}
		}
	}
	if a.Has("cursor") && a.One("cursor") == "" {
		return a, fmt.Errorf("empty cursor")
	}
	if a.Has("format") && !validOutputFormat(a.One("format")) {
		return a, fmt.Errorf("invalid format")
	}
	if a.Has("timeout") {
		d, e := time.ParseDuration(a.One("timeout"))
		if e != nil || d <= 0 {
			return a, fmt.Errorf("timeout must be positive")
		}
	}
	return a, nil
}
func (a *App) grep() (Result, error) {
	q := protocol.SearchRequest{Query: a.Args.Positionals[0], CaseSensitive: a.Args.Has("case-sensitive"), Regex: a.Args.Has("regex")}
	q.ContextLines, _ = strconv.Atoi(a.Args.One("context"))
	q.Limit, _ = strconv.Atoi(a.Args.One("limit"))
	q.Cursor = a.Args.One("cursor")
	q.Scope.AllProjects = a.Args.Has("all-projects")
	if !q.Scope.AllProjects {
		p, e := a.selectedProject()
		if e != nil {
			return Result{}, protocol.E(400, "invalid_arguments", e.Error())
		}
		q.Scope.ProjectID = p
	}
	var page struct {
		Items      []protocol.SearchResult `json:"items"`
		NextCursor *string                 `json:"next_cursor"`
	}
	e := a.Call("POST", "/v1/search", q, nil, &page, false)
	result := Result{Items: []any{}, NextCursor: page.NextCursor}
	for _, v := range page.Items {
		result.Items = append(result.Items, v)
	}
	return result, e
}
func (a *App) humanSearch(r Result) []byte {
	var b bytes.Buffer
	for _, item := range r.Items {
		v := item.(protocol.SearchResult)
		fmt.Fprintf(&b, "%s %s project=%s revision=%d", v.Type, v.ID, v.ProjectID, v.Revision)
		if v.IssueID != "" {
			fmt.Fprintf(&b, " issue=%s %s", v.IssueID, v.IssueTitle)
		}
		fmt.Fprintln(&b)
		for _, excerpt := range v.Excerpts {
			fmt.Fprintf(&b, "  %s", excerpt.Field)
			if excerpt.ExcerptTruncated {
				fmt.Fprint(&b, " [excerpt truncated]")
			}
			fmt.Fprintln(&b)
			for n, line := range strings.Split(excerpt.Text, "\n") {
				if a.Args.Has("n") {
					fmt.Fprintf(&b, "%d: ", excerpt.StartLine+n)
				}
				fmt.Fprintln(&b, line)
			}
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
