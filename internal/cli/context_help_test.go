package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/nwcli"
	"github.com/nerdwave-nick/lit/internal/nwcli/shelltest"
)

func TestContextHelpExplainsScopeStorageAndOfflineUse(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LIT_STATE_DIR", t.TempDir())
	t.Setenv("LIT_SESSION", "")
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("help must not contact the service") }))
	defer server.Close()
	t.Setenv("LIT_ENDPOINT", server.URL)
	for _, tc := range []struct {
		path  string
		wants []string
	}{
		{"context", []string{"checkout", "Git worktree root", "symlink", "nested repository", "moved", "LIT_WORKFLOW_STATE_DIR", "checkout-context", "client", "show", "clear", "never contact lit-server"}},
		{"context show", []string{"Requirements:", "Valid forms:", "lit context show", "not validated", "offline", "No project remembered", "--format"}},
		{"context clear", []string{"Requirements:", "Valid forms:", "lit context clear", "idempotent", "damaged", "unsupported", "only this checkout", "claims", "--format"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			var baseline string
			for _, suffix := range []string{"--help", "-h", "help"} {
				var out, errOut bytes.Buffer
				args := append(strings.Fields(tc.path), suffix)
				if suffix == "help" && tc.path == "context" {
					args = []string{"help", "context"}
				} else if suffix == "help" {
					args = append([]string{"help"}, strings.Fields(tc.path)...)
				}
				if code := Run(args, &out, &errOut); code != 0 || errOut.Len() != 0 {
					t.Fatalf("%v: exit %d %s", args, code, &errOut)
				}
				for _, want := range tc.wants {
					if !strings.Contains(out.String(), want) {
						t.Errorf("%v lacks %q:\n%s", args, want, &out)
					}
				}
				if baseline != "" && baseline != out.String() {
					t.Errorf("help alias %s differs", suffix)
				}
				baseline = out.String()
			}
			_, flags, _ := strings.Cut(baseline, "\nUsage:\n")
			for _, flag := range []string{"--session ", "--endpoint ", "--timeout "} {
				if strings.Contains(flags, flag) {
					t.Errorf("%s advertises %s, which does not apply offline:\n%s", tc.path, flag, flags)
				}
			}
			if !strings.Contains(flags, "--format FORMAT") || strings.Contains(flags, "otherwise session preference") {
				t.Errorf("%s must describe --format without a session preference:\n%s", tc.path, flags)
			}
		})
	}
	var out, errOut bytes.Buffer
	if code := Run([]string{"--help"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "context") {
		t.Errorf("the command list lacks context: %d %s", code, &out)
	}
}

func TestContextCompletion(t *testing.T) {
	offlineCompletion(t)
	if values, _ := completion(t, "context", ""); !slices.Contains(values, "show") || !slices.Contains(values, "clear") {
		t.Errorf("subcommands: %v", values)
	}
	for _, sub := range []string{"show", "clear"} {
		values, _ := completion(t, "context", sub, "--")
		if !slices.Contains(values, "--format") {
			t.Errorf("%s flags: %v", sub, values)
		}
		for _, absent := range []string{"--session", "--endpoint", "--timeout"} {
			if slices.Contains(values, absent) {
				t.Errorf("%s offers %s: %v", sub, absent, values)
			}
		}
		if formats, _ := completion(t, "context", sub, "--format", ""); !slices.Equal(formats, []string{"cli", "markdown", "json"}) {
			t.Errorf("%s --format: %v", sub, formats)
		}
	}
}

func TestContextCompletionInRealShells(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	program := shelltest.Program{
		Name: "lit", Path: executable, Args: []string{"-test.run=^TestCompletionHelperProcess$", "--"},
		Env: []string{"LIT_COMPLETION_TEST_HELPER=1", "LIT_SESSION=", "LIT_STATE_DIR=" + t.TempDir(), "LIT_ENDPOINT=http://127.0.0.1:1"},
		Dir: t.TempDir(),
	}
	for _, shell := range nwcli.Shells {
		t.Run(shell, func(t *testing.T) {
			var script, stderr bytes.Buffer
			if code := Run([]string{"completion", shell}, &script, &stderr); code != 0 {
				t.Fatal(code, &stderr)
			}
			if r := shelltest.Complete(t, shell, script.String(), program, "lit cont"); !slices.Equal(r.Values, []string{"context"}) {
				t.Errorf("command: %+v", r)
			}
			r := shelltest.Complete(t, shell, script.String(), program, "lit context ")
			if !slices.Contains(r.Values, "show") || !slices.Contains(r.Values, "clear") {
				t.Errorf("subcommands: %+v", r)
			}
			if r := shelltest.Complete(t, shell, script.String(), program, "lit context show --f"); !slices.Equal(r.Values, []string{"--format"}) {
				t.Errorf("flag: %+v", r)
			}
			if r := shelltest.Complete(t, shell, script.String(), program, "lit context clear --format "); !slices.Equal(slices.Sorted(slices.Values(r.Values)), []string{"cli", "json", "markdown"}) {
				t.Errorf("flag value: %+v", r)
			}
		})
	}
}
