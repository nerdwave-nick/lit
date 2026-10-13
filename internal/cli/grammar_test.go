package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/nwcli"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// parsed lists the commands whose arguments Parse handles; setup-skills and
// workflow-session have their own parsers.
func parsedCommands(t *testing.T, fn func(path []string, cmd *nwcli.Command)) {
	t.Helper()
	n := 0
	grammar.Walk(func(path []string, cmd *nwcli.Command) {
		if len(path) == 0 || len(cmd.Commands) > 0 || path[0] == "setup-skills" || path[0] == "workflow-session" || path[0] == "completion" {
			return
		}
		n++
		fn(path, command(path...))
	})
	if n < 30 {
		t.Fatalf("only %d commands walked", n)
	}
}

// valueFor returns an argument the parser accepts for the flag.
func valueFor(f nwcli.Flag) string {
	switch {
	case f.Value.Kind == nwcli.Choice:
		return f.Value.Choices[0]
	case f.Name == "timeout":
		return "5s"
	case f.Name == "limit" || f.Name == "context" || f.Name == "revision":
		return "3"
	}
	return "v-" + f.Name
}

func give(f nwcli.Flag) []string {
	if f.Switch {
		return []string{"--" + f.Name}
	}
	return []string{"--" + f.Name, valueFor(f)}
}

func base(path []string) []string {
	argv := append([]string{}, path...)
	if path[0] == "grep" || path[0] == "search" {
		argv = append(argv, "pattern")
	}
	return argv
}

func TestGrammarMatchesParsers(t *testing.T) {
	parsedCommands(t, func(path []string, cmd *nwcli.Command) {
		name := strings.Join(path, " ")
		parse := func(extra ...string) (Args, error) { return Parse(append(base(path), extra...)) }
		records := map[string]bool{"projects": true, "issues": true, "milestones": true, "comments": true}[path[0]]
		cmd.EachFlag(func(f nwcli.Flag, owner *nwcli.Flag) {
			a, err := parse(give(f)...)
			if err != nil {
				t.Errorf("%s: declared --%s rejected: %v", name, f.Name, err)
				return
			}
			_, err = parse(append(give(f), give(f)...)...)
			adjacent := f.Repeat == nwcli.Many || cmd.StartsItem(f.Name)
			if adjacent != (err == nil) {
				t.Errorf("%s: --%s twice: err=%v, grammar says repeat=%v", name, f.Name, err, adjacent)
			}
			if owner != nil {
				a, err := parse(append(append(append(give(*owner), give(f)...), give(*owner)...), give(f)...)...)
				if err != nil || len(a.Groups) != 2 || len(a.Groups[1][f.Name]) != 1 {
					t.Errorf("%s: field --%s once per --%s: %+v %v", name, f.Name, owner.Name, a.Groups, err)
				}
			}
			if f.Switch {
				if _, err := parse("--" + f.Name + "=x"); err == nil {
					t.Errorf("%s: switch --%s accepted a value", name, f.Name)
				}
			} else if _, err := parse("--" + f.Name); err == nil {
				t.Errorf("%s: --%s accepted no value", name, f.Name)
			}
			// Item flags, their fields and the filters of item-less record commands
			// form item groups; invocation-wide flags stay in Values.
			wantValues := !records || owner == nil && !cmd.StartsItem(f.Name) && (cmd.HasItems() || invocationFlags[f.Name])
			if _, inValues := a.Values[f.Name]; inValues != wantValues || !wantValues && len(a.Groups) != 1 {
				t.Errorf("%s: --%s stored in values=%v, want %v (groups %+v)", name, f.Name, inValues, wantValues, a.Groups)
			}
			if records && cmd.HasItems() && owner == nil && !cmd.StartsItem(f.Name) && !invocationFlags[f.Name] {
				t.Errorf("%s: --%s is neither invocation-wide nor part of an item", name, f.Name)
			}
			if f.Short != "" && !shortWorks(parse, f) {
				t.Errorf("%s: shorthand -%s for --%s rejected", name, f.Short, f.Name)
			}
		})
		for flag := range flagDescriptions {
			if _, declared := cmd.Flag(flag); declared {
				continue
			}
			if _, err := parse("--"+flag, "x"); err == nil {
				t.Errorf("%s: undeclared --%s accepted", name, flag)
			}
		}
	})
}

func shortWorks(parse func(...string) (Args, error), f nwcli.Flag) bool {
	args := []string{"-" + f.Short}
	if !f.Switch {
		args = append(args, valueFor(f))
	}
	_, err := parse(args...)
	return err == nil
}

// Value kinds must describe what completion offers for each flag and operand.
func TestGrammarValueKindsMatchCompletion(t *testing.T) {
	offlineCompletion(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind := strings.TrimPrefix(r.URL.Path, "/v1/completions/")
		json.NewEncoder(w).Encode(map[string]any{"data": protocol.Completions{APIMajor: 1, ServiceID: protocol.UUID(), Items: []protocol.Completion{{ID: protocol.UUID(), Value: "record:" + kind}}}})
	}))
	defer srv.Close()
	t.Setenv("LIT_ENDPOINT", srv.URL)
	want := func(v nwcli.Value) (string, string) {
		switch v.Kind {
		case nwcli.Path:
			return "", ":0"
		case nwcli.Dir:
			return "", ":16"
		case nwcli.Choice:
			return strings.Join(v.Choices, ","), ""
		case nwcli.Dynamic:
			return map[string]string{"sessions": "", "endpoints": "http://127.0.0.1:7411", "durations": "15m,30m,45m,1h"}[v.Completer] + map[bool]string{true: "record:" + v.Completer}[strings.HasSuffix(v.Completer, "s") && v.Completer != "sessions" && v.Completer != "endpoints" && v.Completer != "durations"], ""
		}
		return "", ":4"
	}
	grammar.Walk(func(path []string, declared *nwcli.Command) {
		if len(path) == 0 || len(declared.Commands) > 0 || path[0] == "completion" {
			return
		}
		cmd := command(path...)
		prefix := append([]string{}, path...)
		if path[0] == "workflow-session" {
			prefix = append([]string{"workflow-session", "--host", "claude", "--runtime-id", "r"}, path[1:]...)
		}
		check := func(what string, args []string, v nwcli.Value) {
			values, directive := completion(t, append(append([]string{}, prefix...), args...)...)
			wantValues, wantDirective := want(v)
			if strings.Join(values, ",") != wantValues || wantDirective != "" && directive != wantDirective {
				t.Errorf("%s %s: got %v %s, want %q %s", strings.Join(path, " "), what, values, directive, wantValues, wantDirective)
			}
		}
		for _, f := range cmd.Flags {
			if !f.Switch {
				check("--"+f.Name, []string{"--" + f.Name, ""}, f.Value)
			}
		}
		if cmd.Operands.Value.Kind == nwcli.Dynamic {
			check("operands", []string{""}, cmd.Operands.Value)
		}
	})
}

func TestGrammarTicketExamples(t *testing.T) {
	a, err := Parse([]string{"issues", "create", "--issue", "A", "--content", "x", "--issue", "B", "--content", "y"})
	if err != nil || len(a.Groups) != 2 || a.Groups[0]["issue"][0] != "A" || a.Groups[1]["content"][0] != "y" {
		t.Fatalf("two issues with one content each: %+v %v", a.Groups, err)
	}
	if _, err := Parse([]string{"projects", "list", "--limit", "1", "--limit", "2"}); err == nil || err.Error() != "duplicate flag --limit" {
		t.Fatalf("repeated --limit: %v", err)
	}
	if _, err := Parse([]string{"issues", "create", "--issue", "A", "--content", "x", "--content", "y"}); err == nil || err.Error() != "duplicate flag --content" {
		t.Fatalf("one field twice in one item: %v", err)
	}
}
