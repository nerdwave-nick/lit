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
		path    string
		wants   []string
		service bool // contacts lit-server: offers --endpoint and --timeout
	}{
		{"context", []string{"checkout", "Git worktree root", "symlink", "nested repository", "moved", "LIT_WORKFLOW_STATE_DIR", "checkout-context", "client", "set", "show", "clear", "--validate", "offline"}, false},
		{"context set", []string{"Requirements:", "Valid forms:", "lit context set --project REF", "--project", "stable", "service ID", "LIT_ENDPOINT", "saved endpoint",
			"not_found", "ambiguous_reference", "service_unavailable", "preserved", "no session", "no client"}, true},
		{"context show", []string{"Requirements:", "Valid forms:", "lit context show", "--validate", "not validated", "offline", "No project remembered", "--format",
			"wrong_service", "not_found", "service_unavailable", "incompatible_service", "invalid_local_state", "renamed", "archived", "0c14ae79", "LIT_ENDPOINT", "saved endpoint", "no network request"}, true},
		{"context clear", []string{"Requirements:", "Valid forms:", "lit context clear", "idempotent", "damaged", "unsupported", "only this checkout", "claims", "--format"}, false},
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
			if strings.Contains(flags, "--session ") {
				t.Errorf("%s advertises --session, which does not apply:\n%s", tc.path, flags)
			}
			for _, flag := range []string{"--endpoint ", "--timeout "} {
				if got := strings.Contains(flags, flag); got != tc.service {
					t.Errorf("%s advertises %q: %v, want %v:\n%s", tc.path, flag, got, tc.service, flags)
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
	if values, _ := completion(t, "context", ""); !slices.Contains(values, "set") || !slices.Contains(values, "show") || !slices.Contains(values, "clear") {
		t.Errorf("subcommands: %v", values)
	}
	for sub, want := range map[string][]string{
		"set":   {"--format", "--project", "--endpoint", "--timeout"},
		"show":  {"--format", "--validate", "--endpoint", "--timeout"},
		"clear": {"--format"},
	} {
		values, _ := completion(t, "context", sub, "--")
		for _, flag := range want {
			if !slices.Contains(values, flag) {
				t.Errorf("%s flags lack %s: %v", sub, flag, values)
			}
		}
		for _, absent := range []string{"--session", "--endpoint", "--timeout", "--validate", "--project"} {
			if slices.Contains(values, absent) && !slices.Contains(want, absent) {
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
			if !slices.Contains(r.Values, "set") || !slices.Contains(r.Values, "show") || !slices.Contains(r.Values, "clear") {
				t.Errorf("subcommands: %+v", r)
			}
			if r := shelltest.Complete(t, shell, script.String(), program, "lit context show --v"); !slices.Equal(r.Values, []string{"--validate"}) {
				t.Errorf("show switch: %+v", r)
			}
			if r := shelltest.Complete(t, shell, script.String(), program, "lit context set --pro"); !slices.Equal(r.Values, []string{"--project"}) {
				t.Errorf("set flag: %+v", r)
			}
			if r := shelltest.Complete(t, shell, script.String(), program, "lit context clear --e"); len(r.Values) != 0 {
				t.Errorf("clear offers an endpoint: %+v", r)
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
