package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type statusReceipt struct {
	Installed []struct {
		Agent      string   `json:"agent"`
		Directory  string   `json:"directory"`
		Hooks      bool     `json:"hooks"`
		Status     string   `json:"status"`
		Mod        string   `json:"mod"`
		StatusLine string   `json:"status_line"`
		Notes      []string `json:"notes"`
	} `json:"installed"`
}

func runStatusSetup(t *testing.T, args ...string) (statusReceipt, int, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(append([]string{"setup-workflows"}, args...), &out, &errOut)
	var r statusReceipt
	if code == 0 {
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatalf("receipt %q: %v", out.String(), err)
		}
	}
	return r, code, out.String() + errOut.String()
}

func claudeWork(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, ".claude-work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func hasSettingsStatusLine(t *testing.T, dir string) bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	_, ok := m["statusLine"]
	return ok
}

func TestSetupWorkflowsStatusModes(t *testing.T) {
	for _, tc := range []struct {
		args       []string
		status     string
		mod, line  bool
		statusLine string
	}{
		{nil, "both", true, true, "set"},
		{[]string{"--status", "both"}, "both", true, true, "set"},
		{[]string{"--status", "line"}, "line", false, true, "set"},
		{[]string{"--status", "mod"}, "mod", true, false, ""},
	} {
		t.Run(tc.status+"/"+strings.Join(tc.args, " "), func(t *testing.T) {
			_, home, _, _ := isolateSetup(t)
			dir := claudeWork(t, home)
			r, code, out := runStatusSetup(t, append([]string{"--agent", "claude", "--config-dir", dir}, tc.args...)...)
			if code != 0 || len(r.Installed) != 1 {
				t.Fatalf("%d %s", code, out)
			}
			got := r.Installed[0]
			wantMod := map[bool]string{true: "installed", false: ""}[tc.mod]
			if got.Status != tc.status || got.Mod != wantMod || got.StatusLine != tc.statusLine {
				t.Errorf("receipt status %q mod %q status_line %q", got.Status, got.Mod, got.StatusLine)
			}
			modDir := filepath.Join(dir, "skills", "lit-ui")
			if _, err := os.Stat(modDir); (err == nil) != tc.mod {
				t.Errorf("mod installed: %v, want %v", err == nil, tc.mod)
			}
			if hasSettingsStatusLine(t, dir) != tc.line {
				t.Errorf("status line present: %v, want %v", !tc.line, tc.line)
			}
			if !tc.mod {
				return
			}
			var config map[string]any
			b, err := os.ReadFile(filepath.Join(modDir, "config.json"))
			if err != nil || json.Unmarshal(b, &config) != nil {
				t.Fatalf("config.json %v %s", err, b)
			}
			lit, _ := config["lit"].(string)
			if config["schema_version"] != float64(1) || config["status"] != tc.status || !filepath.IsAbs(lit) {
				t.Errorf("config.json %v", config)
			}
		})
	}
}

func TestSetupWorkflowsStatusSwitchConverges(t *testing.T) {
	_, home, _, _ := isolateSetup(t)
	dir := claudeWork(t, home)
	runStatusSetup(t, "--agent", "claude", "--config-dir", dir, "--status", "line")
	r, code, out := runStatusSetup(t, "--agent", "claude", "--config-dir", dir, "--status", "mod")
	if code != 0 || r.Installed[0].Mod != "installed" || r.Installed[0].StatusLine != "removed" {
		t.Fatalf("line -> mod: %d %s", code, out)
	}
	r, code, out = runStatusSetup(t, "--agent", "claude", "--config-dir", dir, "--status", "line")
	if code != 0 || r.Installed[0].Mod != "removed" || r.Installed[0].StatusLine != "set" {
		t.Fatalf("mod -> line: %d %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "lit-ui")); err == nil {
		t.Error("mod -> line left the mod")
	}
}

func TestSetupWorkflowsStatusIsIgnoredForCodexAndLocalScope(t *testing.T) {
	cwd, home, _, _ := isolateSetup(t)
	r, code, out := runStatusSetup(t, "--scope", "user", "--agent", "both", "--status", "mod")
	if code != 0 || len(r.Installed) != 2 {
		t.Fatalf("%d %s", code, out)
	}
	for _, e := range r.Installed {
		noted := strings.Contains(strings.Join(e.Notes, "\n"), "--status")
		switch e.Agent {
		case "codex":
			if !noted || e.Status != "" || e.Mod != "" {
				t.Errorf("codex receipt %+v", e)
			}
			if _, err := os.Stat(filepath.Join(home, ".codex", "skills", "lit-ui")); err == nil {
				t.Error("codex got the mod")
			}
		case "claude":
			if noted || e.Status != "mod" || e.Mod != "installed" {
				t.Errorf("claude receipt %+v", e)
			}
		}
	}
	r, code, out = runStatusSetup(t, "--scope", "local", "--agent", "both", "--status", "both")
	if code != 0 || len(r.Installed) != 2 {
		t.Fatalf("%d %s", code, out)
	}
	for _, e := range r.Installed {
		if !strings.Contains(strings.Join(e.Notes, "\n"), "--status") || e.Mod != "" || e.Status != "" {
			t.Errorf("local receipt %+v", e)
		}
		if _, err := os.Stat(filepath.Join(cwd, "."+e.Agent, "skills", "lit-ui")); err == nil {
			t.Errorf("%s local got the mod", e.Agent)
		}
	}
	if _, err := os.Stat(filepath.Join(cwd, ".claude", "settings.json")); err == nil {
		t.Error("local scope got a status line")
	}
	// Without --status nothing is said about it.
	r, _, _ = runStatusSetup(t, "--scope", "user", "--agent", "codex")
	if strings.Contains(strings.Join(r.Installed[0].Notes, "\n"), "--status") {
		t.Errorf("codex note without --status: %v", r.Installed[0].Notes)
	}
}

func TestSetupWorkflowsStatusRefusals(t *testing.T) {
	_, home, _, _ := isolateSetup(t)
	dir := claudeWork(t, home)
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"unknown status":  {[]string{"--agent", "claude", "--config-dir", dir, "--status", "pane"}, "pane"},
		"status twice":    {[]string{"--agent", "claude", "--config-dir", dir, "--status", "mod", "--status", "line"}, "duplicate flag --status"},
		"status removing": {[]string{"--remove", "--agent", "claude", "--config-dir", dir, "--status", "mod"}, "--status"},
	} {
		t.Run(name, func(t *testing.T) {
			_, code, out := runStatusSetup(t, tc.args...)
			if code != 2 || !strings.Contains(out, tc.want) {
				t.Fatalf("code %d, output %q; want exit 2 mentioning %q", code, out, tc.want)
			}
		})
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("a refused run wrote into %s", dir)
	}
}

func TestSetupWorkflowsRemoveReportsTheMod(t *testing.T) {
	_, home, _, _ := isolateSetup(t)
	dir := claudeWork(t, home)
	runStatusSetup(t, "--agent", "claude", "--config-dir", dir, "--status", "mod")
	var out, errOut bytes.Buffer
	if code := Run([]string{"setup-workflows", "--remove", "--agent", "claude", "--config-dir", dir}, &out, &errOut); code != 0 {
		t.Fatalf("%d %s", code, &errOut)
	}
	if !strings.Contains(out.String(), `"mod":true`) {
		t.Errorf("removal receipt %s", &out)
	}
	if ents, _ := os.ReadDir(filepath.Join(dir, "skills")); len(ents) != 0 {
		t.Errorf("remove left %v", ents)
	}
}

func TestSetupWorkflowsHelpDocumentsStatus(t *testing.T) {
	isolateSetup(t)
	var b bytes.Buffer
	if code := Run([]string{"setup-workflows", "--help"}, &b, &b); code != 0 {
		t.Fatal(code, &b)
	}
	for _, want := range []string{"--status line|mod|both", "skills/lit-ui", "config.json", "default both", "ignored for Codex"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("help lacks %q:\n%s", want, &b)
		}
	}
}

func TestResolveLitPathIsAbsolute(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "bin", "lit")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "path", "lit")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, link); err != nil {
		t.Skip(err)
	}
	other := filepath.Join(dir, "other", "lit")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, nil, 0o755); err != nil {
		t.Fatal(err)
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
		// The PATH entry survives an upgrade that replaces the link's target.
		"lit on PATH links to this binary": {found(link), link},
		"lit on PATH is this binary":       {found(exe), exe},
		"another lit comes first":          {found(other), exe},
		"lit is not on PATH":               {found(""), exe},
	} {
		if got := resolveLitPath(exe, tc.lookup); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}
