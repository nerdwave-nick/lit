package cli

import (
	"bytes"
	"github.com/nerdwave-nick/lit/internal/nwcli"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestScopedCommandHelp(t *testing.T) {
	t.Setenv("LIT_STATE_DIR", "/dev/null/no-state")
	for _, args := range [][]string{{"projects", "list", "--help"}, {"projects", "list", "-h"}, {"help", "projects", "list"}, {"projects", "list", "help"}} {
		var out, err bytes.Buffer
		if code := Run(args, &out, &err); code != 0 {
			t.Fatalf("%v: %d %s", args, code, &err)
		}
		for _, want := range []string{"lit projects list", "--repository-ref", "--limit", "Examples:"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%v missing %s: %s", args, want, &out)
			}
		}
		if strings.Contains(out.String(), "--actor-kind") {
			t.Error("unrelated connect flags leaked")
		}
	}
}
func TestCompletionCommands(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		var out, err bytes.Buffer
		if code := Run([]string{"completion", shell}, &out, &err); code != 0 || out.Len() < 500 || !strings.Contains(out.String(), "__complete") || !strings.Contains(out.String(), "_lit") {
			t.Fatalf("%s: %d %s", shell, code, &err)
		}
	}
	var unsupported, errOut bytes.Buffer
	if code := Run([]string{"completion", "powershell"}, &unsupported, &errOut); code != 2 || !strings.Contains(unsupported.String()+errOut.String(), "invalid_arguments") {
		t.Fatalf("powershell: %d %s %s", code, &unsupported, &errOut)
	}
	var out, err bytes.Buffer
	if code := Run([]string{"__complete", "issues", "list", "--state", ""}, &out, &err); code != 0 || !strings.Contains(out.String(), "open") || !strings.Contains(out.String(), "closed") {
		t.Fatalf("%d %s %s", code, &out, &err)
	}
}
func TestCommandTreeErrorsAreOfflineJSON(t *testing.T) {
	t.Setenv("LIT_STATE_DIR", "/dev/null/no-state")
	for _, args := range [][]string{{"projects", "wat"}, {"projects"}, {"issues", "list", "--bogus"}, {"projects", "list", "--limit", "1", "--limit", "2"}} {
		var out, err bytes.Buffer
		args = append(args, "--format", "json")
		if code := Run(args, &out, &err); code != 2 || !strings.Contains(out.String()+err.String(), "invalid_arguments") {
			t.Fatalf("%v: %d %s %s", args, code, &out, &err)
		}
	}
}

func TestEveryCommandHasScopedOfflineHelp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("LIT_STATE_DIR", "/dev/null/blocked")
	t.Setenv("LIT_WORKFLOW_STATE_DIR", "/dev/null/blocked")
	grammar.Walk(func(path []string, cmd *nwcli.Command) {
		for _, form := range [][]string{append(append([]string{}, path...), "--help"), append(append([]string{}, path...), "-h"), append([]string{"help"}, path...), append(append([]string{}, path...), "help")} {
			if (cmd.Name == "grep" || cmd.Name == "search" || cmd.Name == "run") && len(form) > 0 && form[len(form)-1] == "help" || len(form) == 1 && form[0] == "help" && len(path) == 0 {
				continue
			}
			var out, err bytes.Buffer
			if code := Run(form, &out, &err); code != 0 || err.Len() != 0 || !strings.Contains(out.String(), "Usage:") {
				t.Fatalf("%v: %d %s %s", form, code, &out, &err)
			}
		}
	})
}

func TestHelpAliasDoesNotConsumeData(t *testing.T) {
	for _, tc := range []struct{ in, want []string }{
		{[]string{"--session", "s", "projects", "--format", "json", "list", "help"}, []string{"--session", "s", "projects", "--format", "json", "list", "--help"}},
		{[]string{"grep", "help"}, []string{"grep", "help"}},
		{[]string{"grep", "--", "--help"}, []string{"grep", "--", "--help"}},
		{[]string{"issues", "create", "--issue", "help"}, []string{"issues", "create", "--issue", "help"}},
		{[]string{"issues", "list", "-q", "x", "help"}, []string{"issues", "list", "-q", "x", "--help"}},
		{[]string{"issues", "list", "-q", "help"}, []string{"issues", "list", "-q", "help"}},
		{[]string{"issues", "list", "-qx", "help"}, []string{"issues", "list", "-qx", "--help"}},
		{[]string{"workflow-session", "run", "--", "projects", "help"}, []string{"workflow-session", "run", "--", "projects", "help"}},
	} {
		wantHelp := !reflect.DeepEqual(tc.in, tc.want) // the alias rewrote help to --help
		if got := grammar.Route(tc.in).Help; got != wantHelp {
			t.Fatalf("%v: help %v, want %v", tc.in, got, wantHelp)
		}
	}
}

func TestFrameworkPreservesOrderedDomainArguments(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("LIT_STATE_DIR", t.TempDir())
	t.Chdir(t.TempDir())
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(503) }))
	defer srv.Close()
	for _, args := range [][]string{
		{"projects", "create", "--project-title", "test/a", "--content", "A", "--project-title", "test/b", "--content", "B"},
		{"issues", "link", "--from", "A", "--to", "B", "--to", "C", "--relation", "blocks", "--from", "B", "--to", "C", "--relation", "related"},
		{"--project", "test/poc", "projects", "list"},
		{"grep", "--", "--help"},
		{"grep", "help"},
		{"session", "get", "--project"},
	} {
		argv := append([]string{"--endpoint", srv.URL, "--session", "test"}, args...)
		var out, err bytes.Buffer
		before := requests
		Run(argv, &out, &err)
		if requests != before+1 {
			t.Fatalf("did not reach transport: %v: %s %s", args, &out, &err)
		}
	}
}

func TestContextualWorkflowAndCommentHelp(t *testing.T) {
	for _, tc := range []struct {
		path         []string
		want, absent string
	}{
		{[]string{"workflow-session", "checkout"}, "Required checkout DIRECTORY", "Parent DIRECTORY of .codex"},
		{[]string{"workflow-session", "subagent"}, "--parent-runtime-id PARENT", ".codex"},
		{[]string{"comments", "get"}, "01234567-89ab-4cde-8fab-0123456789ab", "'Verify the PoC'"},
		{[]string{"comments", "update"}, "01234567-89ab-4cde-8fab-0123456789ab", "'Verify the PoC'"},
		{[]string{"comments", "history"}, "01234567-89ab-4cde-8fab-0123456789ab", "'Verify the PoC'"},
	} {
		var out, err bytes.Buffer
		if code := Run(append(tc.path, "--help"), &out, &err); code != 0 || !strings.Contains(out.String(), tc.want) || strings.Contains(out.String(), tc.absent) {
			t.Fatalf("%v: %d %s %s", tc.path, code, &out, &err)
		}
	}
}

func TestClaimsAllAndHiddenIrrelevantFlags(t *testing.T) {
	for _, action := range []string{"renew", "release"} {
		var out, err bytes.Buffer
		if code := Run([]string{"claims", action, "--help"}, &out, &err); code != 0 || !strings.Contains(out.String(), "all claims owned by this session") || strings.Contains(out.String(), "bounded snapshot") {
			t.Fatalf("%d %s %s", code, &out, &err)
		}
	}
	for _, path := range [][]string{{"setup-skills"}, {"workflow-session"}, {"workflow-session", "fresh"}, {"workflow-session", "run"}} {
		var out, err bytes.Buffer
		if code := Run(append(path, "--help"), &out, &err); code != 0 {
			t.Fatal(code, &err)
		}
		// Explanatory prose may state that a flag does not apply. Only the
		// actual flag listings must omit unsupported adapter flags.
		_, flagHelp, found := strings.Cut(out.String(), "\nFlags:\n")
		if !found {
			t.Fatalf("%v lacks a flag section", path)
		}
		for _, flag := range []string{"--session ", "--format ", "--timeout "} {
			if strings.Contains(flagHelp, flag) {
				t.Fatalf("%v advertises unsupported %s", path, flag)
			}
		}
	}
	var out, err bytes.Buffer
	Run([]string{"projects", "list", "--help"}, &out, &err)
	if !strings.Contains(out.String(), "--session ") {
		t.Fatal("hiding leaked to tracker commands")
	}
}
