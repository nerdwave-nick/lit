package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type hookReceipt struct {
	Installed []struct {
		Agent      string   `json:"agent"`
		Scope      string   `json:"scope"`
		Directory  string   `json:"directory"`
		Hooks      bool     `json:"hooks"`
		StatusLine string   `json:"status_line"`
		Warnings   []string `json:"warnings"`
		Notes      []string `json:"notes"`
	} `json:"installed"`
}

// runSetupSplit runs setup-workflows and returns the receipt printed on stdout
// and the human-readable report printed on stderr.
func runSetupSplit(t *testing.T, args ...string) (hookReceipt, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(append([]string{"setup-workflows"}, args...), &out, &errOut)
	var r hookReceipt
	if code == 0 {
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatalf("stdout is not the JSON receipt: %q: %v", out.String(), err)
		}
	}
	return r, errOut.String(), code
}

func TestSetupWorkflowsInstallsHooksOnlyIntoAgentConfigurations(t *testing.T) {
	cwd, home, _, _ := isolateSetup(t)
	work := filepath.Join(home, ".claude-work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		args  []string
		hooks bool
		dirs  map[string]string // directory -> agent
	}{
		{"local", []string{"--scope", "local", "--agent", "both"}, false, map[string]string{filepath.Join(cwd, ".codex"): "codex", filepath.Join(cwd, ".claude"): "claude"}},
		{"user", []string{"--scope", "user", "--agent", "both"}, true, map[string]string{filepath.Join(home, ".codex"): "codex", filepath.Join(home, ".claude"): "claude"}},
		{"config-dir", []string{"--agent", "claude", "--config-dir", work}, true, map[string]string{work: "claude"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, report, code := runSetupSplit(t, tc.args...)
			if code != 0 {
				t.Fatalf("%d %s", code, report)
			}
			for _, e := range r.Installed {
				if e.Hooks != tc.hooks {
					t.Errorf("%s: hooks=%v, want %v", e.Directory, e.Hooks, tc.hooks)
				}
				agent := tc.dirs[e.Directory]
				hookFile := map[string]string{"claude": "settings.json", "codex": "hooks.json"}[agent]
				present := func(p string) bool { _, err := os.Stat(filepath.Join(e.Directory, p)); return err == nil }
				if present(hookFile) != tc.hooks || present(filepath.Join("skills", "lit-hooks")) != (tc.hooks && agent == "claude") {
					t.Errorf("%s (%s): hook files present: %s=%v plugin=%v", e.Directory, agent, hookFile, present(hookFile), present(filepath.Join("skills", "lit-hooks")))
				}
				if agent == "claude" && (e.StatusLine == "set") != tc.hooks {
					t.Errorf("%s: status line %q", e.Directory, e.StatusLine)
				}
			}
		})
	}
}

func TestSetupWorkflowsHookCommandsCallTheInstalledLit(t *testing.T) {
	_, home, _, _ := isolateSetup(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r, report, code := runSetupSplit(t, "--scope", "user", "--agent", "both")
	if code != 0 {
		t.Fatalf("%d %s", code, report)
	}
	// The commands name this executable because lit on PATH is not it; the
	// receipt says so, since moving the executable breaks the hooks.
	for _, e := range r.Installed {
		if !strings.Contains(strings.Join(e.Notes, "\n"), "call "+exe) {
			t.Errorf("%s: no note about the executable the hooks call: %v", e.Agent, e.Notes)
		}
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct{ Command string }
		}
	}
	b, err := os.ReadFile(filepath.Join(home, ".codex", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(exe, " '\"$`\\") {
		t.Skip("the test binary path needs shell quoting")
	}
	// The test binary is not on PATH as lit, so the commands name it by path.
	got := doc.Hooks["Stop"][0].Hooks[0].Command
	if want := exe + " hook --host codex stop"; got != want {
		t.Errorf("command %q, want %q", got, want)
	}
}

func TestSetupWorkflowsReportsNotesAndWarningsOnStderr(t *testing.T) {
	_, home, _, _ := isolateSetup(t)
	codex := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codex, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codex, "config.toml"), []byte("[features]\nhooks = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, report, code := runSetupSplit(t, "--scope", "user", "--agent", "both")
	if code != 0 {
		t.Fatalf("%d %s", code, report)
	}
	for _, want := range []string{"/hooks", "approve", "hook --host codex session-start", "hook --host codex stop", "hook --host codex subagent-start", "warning:", "config.toml"} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	var codexSeen bool
	for _, e := range r.Installed {
		if e.Agent == "codex" {
			codexSeen = true
			if len(e.Warnings) == 0 || len(e.Notes) == 0 {
				t.Errorf("the receipt does not carry the warnings and notes: %+v", e)
			}
		}
	}
	if !codexSeen {
		t.Fatal("no codex target")
	}
}

func TestSetupWorkflowsSaysWhereHooksFindTheEndpoint(t *testing.T) {
	isolateSetup(t) // sets LIT_ENDPOINT
	r, report, code := runSetupSplit(t, "--scope", "user", "--agent", "claude")
	if code != 0 {
		t.Fatalf("%d %s", code, report)
	}
	if !strings.Contains(report, "LIT_ENDPOINT") || !strings.Contains(report, "http://127.0.0.1:1") || !strings.Contains(strings.Join(r.Installed[0].Notes, "\n"), "LIT_ENDPOINT") {
		t.Errorf("report lacks the endpoint note:\n%s", report)
	}
	t.Setenv("LIT_ENDPOINT", "")
	_, report, code = runSetupSplit(t, "--scope", "user", "--agent", "claude")
	if code != 0 || strings.Contains(report, "LIT_ENDPOINT") {
		t.Errorf("%d: an unset LIT_ENDPOINT needs no note:\n%s", code, report)
	}
	// Local installs have no hooks and need no note.
	t.Setenv("LIT_ENDPOINT", "http://127.0.0.1:1")
	if _, report, code = runSetupSplit(t, "--scope", "local", "--agent", "claude"); code != 0 || strings.Contains(report, "LIT_ENDPOINT") {
		t.Errorf("%d: a local install needs no endpoint note:\n%s", code, report)
	}
}

func TestResolveLitBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "bin", "lit")
	other := filepath.Join(dir, "other", "lit")
	link := filepath.Join(dir, "path", "lit")
	for _, p := range []string{exe, other} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, link); err != nil {
		t.Skip(err)
	}
	found := func(p string) func(string) (string, error) {
		return func(string) (string, error) {
			if p == "" {
				return "", os.ErrNotExist
			}
			return p, nil
		}
	}
	for name, tc := range map[string]struct {
		lookup func(string) (string, error)
		want   string
	}{
		"lit on PATH is this binary": {found(exe), "lit"},
		"a symlink to this binary":   {found(link), "lit"},
		"another lit comes first":    {found(other), exe},
		"lit is not on PATH":         {found(""), exe},
		"lit on PATH is gone":        {found(filepath.Join(dir, "gone", "lit")), exe},
	} {
		if got := resolveLitBinary(exe, tc.lookup); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}
