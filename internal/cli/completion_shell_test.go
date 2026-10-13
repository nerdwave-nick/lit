package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"testing"

	"github.com/nerdwave-nick/lit/internal/nwcli"
	"github.com/nerdwave-nick/lit/internal/nwcli/shelltest"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// TestShellCompletionThroughLit loads lit's own scripts into real shells and
// completes against lit (this test binary, see TestCompletionHelperProcess).
func TestShellCompletionThroughLit(t *testing.T) {
	const value = "feat/api:Quotes 'and' \"spaces\" $HOME; (echo nope)"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": protocol.Completions{APIMajor: 1, ServiceID: protocol.UUID(), Items: []protocol.Completion{{ID: protocol.UUID(), Value: value, Title: "Display title"}}}})
	}))
	defer srv.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	program := shelltest.Program{
		Name: "lit", Path: executable, Args: []string{"-test.run=^TestCompletionHelperProcess$", "--"},
		Env: []string{"LIT_COMPLETION_TEST_HELPER=1", "LIT_SESSION=", "LIT_STATE_DIR=" + t.TempDir(), "LIT_ENDPOINT=" + srv.URL},
		Dir: t.TempDir(),
	}
	for _, shell := range nwcli.Shells {
		t.Run(shell, func(t *testing.T) {
			var script, stderr bytes.Buffer
			if code := Run([]string{"completion", shell}, &script, &stderr); code != 0 {
				t.Fatal(code, &stderr)
			}
			if r := shelltest.Complete(t, shell, script.String(), program, "lit pro"); !slices.Equal(r.Values, []string{"projects"}) {
				t.Errorf("subcommands: %+v", r)
			}
			if r := shelltest.Complete(t, shell, script.String(), program, "lit projects list "); !slices.Contains(r.Values, "--limit") || slices.Contains(r.Values, "--help") {
				t.Errorf("bare-Tab flags: %+v", r)
			}
			r := shelltest.Complete(t, shell, script.String(), program, "lit issues get feat/api:")
			if shell == "bash" && len(r.Values) == 1 {
				// Readline replaces the text after ':'; check what bash passes once inserted.
				out, err := exec.Command("bash", "--norc", "-c", "printf %s feat/api:"+r.Values[0]).Output()
				if err != nil {
					t.Fatalf("bash rejects %q: %v", r.Values[0], err)
				}
				r.Values = []string{string(out)}
			}
			if !slices.Equal(r.Values, []string{value}) {
				t.Errorf("record value: %q", r.Values)
			}
		})
	}
}
