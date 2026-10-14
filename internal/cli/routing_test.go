package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRoutingErrorsAreOfflineArgumentErrors(t *testing.T) {
	t.Setenv("LIT_STATE_DIR", "/dev/null/no-state")
	t.Setenv("LIT_WORKFLOW_STATE_DIR", "/dev/null/no-state")
	t.Chdir(t.TempDir()) // a routing regression must not install skills into the source tree
	parent := t.TempDir()
	for _, tc := range []struct {
		argv []string
		msg  string
	}{
		{nil, "a subcommand is required for lit; run 'lit --help'"},
		{[]string{"projects"}, "a subcommand is required for lit projects; run 'lit projects --help'"},
		{[]string{"wat"}, `unknown command "wat" for lit; run 'lit --help'`},
		{[]string{"projects", "wat"}, `unknown command "wat" for lit projects; run 'lit projects --help'`},
		{[]string{"help", "wat"}, `unknown command "wat" for lit; run 'lit --help'`},
		{[]string{"issues", "list", "--bogus", "x"}, "unknown flag --bogus"},
		{[]string{"setup-workflows", "--session", "s", "--agent", "codex", "--config-dir", parent}, "--session does not apply to lit setup-workflows"},
		{[]string{"setup-workflows", "--scope", "local", "--scope", "user"}, "duplicate flag --scope"},
		{[]string{"setup-workflows", "extra"}, `unexpected argument "extra" for lit setup-workflows`},
		{[]string{"workflow-session", "--host", "claude", "--runtime-id", "r", "resume", "--endpoint", "http://x"}, "--endpoint does not apply to lit workflow-session resume"},
		{[]string{"workflow-session", "--host", "claude", "--runtime-id", "r", "fresh", "--timeout", "5s"}, "--timeout does not apply to lit workflow-session fresh"},
		{[]string{"version", "extra"}, "unexpected positional argument"},
		{[]string{"connect", "extra"}, "unexpected positional argument"},
	} {
		var out, errOut bytes.Buffer
		code := Run(tc.argv, &out, &errOut)
		var envelope struct {
			Error struct{ Code, Message string }
		}
		json.Unmarshal(bytes.TrimSpace(append(out.Bytes(), errOut.Bytes()...)), &envelope)
		if code != 2 || envelope.Error.Code != "invalid_arguments" || envelope.Error.Message != tc.msg {
			t.Errorf("%v: code %d, %s%s; want %q", tc.argv, code, &out, &errOut, tc.msg)
		}
	}
}

func TestHelpRoutesWhereverFlagsAppear(t *testing.T) {
	for _, argv := range [][]string{
		{"--session", "s", "issues", "--format", "json", "create", "--help"},
		{"workflow-session", "--host", "claude", "fresh", "-h"},
		{"setup-workflows", "help"},
	} {
		var out, errOut bytes.Buffer
		if code := Run(argv, &out, &errOut); code != 0 || errOut.Len() != 0 || !strings.Contains(out.String(), "Usage:\n  lit ") {
			t.Errorf("%v: %d %s %s", argv, code, &out, &errOut)
		}
	}
}
