package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/nwcli"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// completion splits __complete output into candidate values (descriptions
// dropped) and the directive line.
func completion(t *testing.T, args ...string) ([]string, string) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(runCompletionTest(t, args...), "\n"), "\n")
	values := []string{}
	for _, line := range lines[:len(lines)-1] {
		values = append(values, strings.SplitN(line, "\t", 2)[0])
	}
	return values, lines[len(lines)-1]
}

func offlineCompletion(t *testing.T) {
	t.Setenv("LIT_STATE_DIR", t.TempDir())
	t.Setenv("LIT_SESSION", "")
	t.Setenv("LIT_ENDPOINT", "http://127.0.0.1:1") // record completers fail fast and quietly
}

func TestCompletionNeverFallsBackToFilesExceptPathFlags(t *testing.T) {
	offlineCompletion(t)
	grammar.Walk(func(path []string, declared *nwcli.Command) {
		if len(declared.Commands) > 0 || path[0] == "completion" {
			return
		}
		c := command(path...)
		argv := append([]string{}, path...)
		if path[0] == "workflow-session" {
			argv = append([]string{"workflow-session", "--host", "claude", "--runtime-id", "r"}, path[1:]...)
		}
		if _, directive := completion(t, append(argv, "")...); directive == ":0" {
			t.Errorf("%v positional completion falls back to files", path)
		}
		c.EachFlag(func(f nwcli.Flag, _ *nwcli.Flag) {
			if f.Switch {
				return
			}
			_, directive := completion(t, append(argv, "--"+f.Name, "")...)
			want := map[string]string{"file": ":0", "content-file": ":0", "cli": ":0", "path": ":16"}[f.Name]
			if want == "" && (directive == ":0" || directive == ":16") || want != "" && directive != want {
				t.Errorf("%v --%s: directive %s, want %s", path, f.Name, directive, map[bool]string{true: "no file completion", false: want}[want == ""])
			}
		})
	})
}

func TestCompletionOffersFlagsWhenCommandTakesNoOperands(t *testing.T) {
	offlineCompletion(t)
	values, directive := completion(t, "projects", "list", "")
	for _, want := range []string{"--limit", "--sort", "--session", "--format"} {
		if !contains(values, want) {
			t.Errorf("projects list missing %s: %v", want, values)
		}
	}
	if contains(values, "--help") || directive != ":36" {
		t.Errorf("projects list: %v %s", values, directive)
	}
	if out := runCompletionTest(t, "projects", "list", ""); !strings.Contains(out, "--limit\tOptional. Page size `N` (default 100") {
		t.Errorf("flag descriptions differ from cobra's flag-name completion: %s", out)
	}
	values, _ = completion(t, "projects", "list", "--limit", "5", "--session", "s", "")
	if contains(values, "--limit") || contains(values, "--session") || !contains(values, "--sort") {
		t.Errorf("used single-value flags still offered: %v", values)
	}
	values, _ = completion(t, "issues", "create", "--issue", "x", "--label", "a", "")
	if !contains(values, "--label") || !contains(values, "--issue") {
		t.Errorf("repeatable flags hidden after use: %v", values)
	}
	values, _ = completion(t, "connect", "--actor-kind", "human", "")
	if contains(values, "--actor-kind") || !contains(values, "--actor-name") {
		t.Errorf("connect: %v", values)
	}
}

func TestCompletionOffersNothingForFreeFormOperands(t *testing.T) {
	offlineCompletion(t)
	for _, args := range [][]string{{"grep", ""}, {"workflow-session", "run", ""}, {"issues", "list", "--title", ""}, {"issues", "list", "--limit", ""}} {
		if values, directive := completion(t, args...); len(values) != 0 || directive != ":4" {
			t.Errorf("%v: %v %s", args, values, directive)
		}
	}
}

func TestCompletionOffersSavedSessionsAndEndpoints(t *testing.T) {
	offlineCompletion(t)
	dir, _ := litclient.StateDir()
	for name, endpoint := range map[string]string{"alpha": "http://127.0.0.1:7411", "throwaway-1f2e": "http://127.0.0.1:7500"} {
		if err := litclient.WriteJSON(litclient.MappingPath(dir, name), Mapping{SchemaVersion: 1, ClientID: protocol.UUID(), ServiceID: protocol.UUID(), Endpoint: endpoint}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(litclient.MappingPath(dir, "alpha")+".lock", nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions", "not base64!.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	values, directive := completion(t, "projects", "list", "--session", "")
	if strings.Join(values, ",") != "alpha,throwaway-1f2e" || directive != ":36" {
		t.Errorf("sessions: %v %s", values, directive)
	}
	if out := runCompletionTest(t, "connect", "--session", ""); !strings.Contains(out, "throwaway-1f2e\thttp://127.0.0.1:7500") {
		t.Errorf("session description missing endpoint: %s", out)
	}
	values, directive = completion(t, "projects", "list", "--endpoint", "")
	if strings.Join(values, ",") != "http://127.0.0.1:7411,http://127.0.0.1:7500" || directive != ":36" {
		t.Errorf("endpoints: %v %s", values, directive)
	}
	values, directive = completion(t, "claims", "acquire", "--for", "")
	if strings.Join(values, ",") != "15m,30m,45m,1h" || directive != ":36" {
		t.Errorf("lease durations: %v", values)
	}
}

func TestCompletionWithoutSavedSessionsOffersDefaultEndpoint(t *testing.T) {
	offlineCompletion(t)
	if values, directive := completion(t, "connect", "--session", ""); len(values) != 0 || directive != ":36" {
		t.Errorf("sessions without state: %v %s", values, directive)
	}
	if values, _ := completion(t, "connect", "--endpoint", ""); strings.Join(values, ",") != "http://127.0.0.1:7411" {
		t.Errorf("endpoints without state: %v", values)
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestCompletionFollowsItemsExactly(t *testing.T) {
	offlineCompletion(t)
	for _, tc := range []struct {
		args          []string
		offered, gone []string
	}{
		{[]string{"issues", "create", "--issue", "A", "--content", "x", ""}, []string{"--issue", "--label"}, []string{"--content", "--content-file"}[:1]},
		{[]string{"issues", "create", "--issue", "A", "--content", "x", "--issue", "B", ""}, []string{"--content"}, nil},
		{[]string{"issues", "create", "--issue", "A", "--label", "a", ""}, []string{"--label"}, nil},
		{[]string{"issues", "list", "--labels-any", "a", "--limit", "5", ""}, []string{"--labels-any"}, []string{"--limit"}},
		{[]string{"issues", "link", "--from", "A", "--to", "B", "--relation", "blocks", ""}, []string{"--from", "--to"}, []string{"--relation"}},
	} {
		values, directive := completion(t, tc.args...)
		for _, want := range tc.offered {
			if !contains(values, want) {
				t.Errorf("%v: %s not offered: %v", tc.args, want, values)
			}
		}
		for _, absent := range tc.gone {
			if contains(values, absent) {
				t.Errorf("%v: %s offered again: %v", tc.args, absent, values)
			}
		}
		if directive != ":36" {
			t.Errorf("%v: directive %s", tc.args, directive)
		}
	}
}
