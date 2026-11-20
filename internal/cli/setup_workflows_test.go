package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateSetup points HOME and the working directory at fresh directories and
// sets the vendor configuration overrides to directories that must stay
// untouched: setup-workflows never reads them.
func isolateSetup(t *testing.T) (cwd, home, claudeEnv, codexEnv string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claudeEnv, codexEnv = t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeEnv)
	t.Setenv("CODEX_HOME", codexEnv)
	t.Setenv("LIT_SESSION", "does-not-exist")
	t.Setenv("LIT_ENDPOINT", "http://127.0.0.1:1")
	cwd = t.TempDir()
	t.Chdir(cwd)
	return cwd, home, claudeEnv, codexEnv
}

type receipt struct {
	Installed []struct {
		Agent     string `json:"agent"`
		Scope     string `json:"scope"`
		Directory string `json:"directory"`
		Adopted   bool   `json:"adopted"`
	} `json:"installed"`
}

func runSetup(t *testing.T, args ...string) (receipt, int, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(append([]string{"setup-workflows"}, args...), &out, &errOut)
	var r receipt
	if code == 0 {
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatalf("receipt %q: %v", out.String(), err)
		}
	}
	return r, code, out.String() + errOut.String()
}

func installed(t *testing.T, dir string) {
	t.Helper()
	for _, p := range []string{filepath.Join("skills", "lit", "SKILL.md"), ".lit-workflows.json"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".lit-skills.json")); err == nil {
		t.Fatalf("%s holds the removed setup-skills ownership file", dir)
	}
}

func TestSetupWorkflowsConfigDir(t *testing.T) {
	_, home, claudeEnv, codexEnv := isolateSetup(t)
	work := filepath.Join(home, ".claude-work")
	personal := filepath.Join(home, "codex personal")
	for _, d := range []string{work, personal} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ agent, dir string }{{"claude", work}, {"codex", personal}} {
		r, code, out := runSetup(t, "--agent", tc.agent, "--config-dir", tc.dir)
		if code != 0 {
			t.Fatalf("%d %s", code, out)
		}
		if len(r.Installed) != 1 || r.Installed[0].Agent != tc.agent || r.Installed[0].Scope != "config-dir" || r.Installed[0].Directory != tc.dir || r.Installed[0].Adopted {
			t.Fatalf("receipt %+v", r)
		}
		installed(t, tc.dir)
	}
	// Two configurations are two independent installs with their own record.
	if _, err := os.Stat(filepath.Join(work, "AGENTS.md")); err == nil {
		t.Error("the claude configuration holds a codex instruction file")
	}
	for _, d := range []string{claudeEnv, codexEnv} {
		if ents, _ := os.ReadDir(d); len(ents) != 0 {
			t.Errorf("setup wrote into a vendor override directory %s", d)
		}
	}
}

func TestSetupWorkflowsLocalAndUser(t *testing.T) {
	cwd, home, claudeEnv, codexEnv := isolateSetup(t)
	for scope, root := range map[string]string{"local": cwd, "user": home} {
		r, code, out := runSetup(t, "--scope", scope, "--agent", "both")
		if code != 0 {
			t.Fatalf("%s: %d %s", scope, code, out)
		}
		if len(r.Installed) != 2 || r.Installed[0].Agent != "codex" || r.Installed[1].Agent != "claude" {
			t.Fatalf("%s: receipt %+v", scope, r)
		}
		for _, e := range r.Installed {
			want := filepath.Join(root, "."+e.Agent)
			if e.Scope != scope || e.Directory != want {
				t.Errorf("%s: %+v, want directory %s", scope, e, want)
			}
			installed(t, want)
		}
	}
	for _, d := range []string{claudeEnv, codexEnv} {
		if ents, _ := os.ReadDir(d); len(ents) != 0 {
			t.Errorf("setup wrote into a vendor override directory %s", d)
		}
	}
}

// legacySkillsInstall leaves what the removed setup-skills command wrote: one
// owned skill, its ownership file and an instruction file with a managed block.
func legacySkillsInstall(t *testing.T, root, host, instruction string) {
	t.Helper()
	body := []byte("an older lit skill\n")
	h := sha256.New()
	h.Write([]byte("SKILL.md\x00"))
	h.Write(body)
	h.Write([]byte{0})
	files := map[string]string{
		filepath.Join("skills", "lit", "SKILL.md"): string(body),
		".lit-skills.json":                         `{"schema_version":1,"host":"` + host + `","entries":{"lit":"` + hex.EncodeToString(h.Sum(nil)) + `"}}`,
		instruction:                                "mine\n<!-- BEGIN lit managed -->\nold\n<!-- END lit managed -->\n",
	}
	for name, content := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSetupWorkflowsAdoptsASetupSkillsInstall(t *testing.T) {
	cwd, home, _, _ := isolateSetup(t)
	legacySkillsInstall(t, filepath.Join(cwd, ".claude"), "claude", "CLAUDE.md")
	r, code, out := runSetup(t, "--scope", "local", "--agent", "both")
	if code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	if len(r.Installed) != 2 || r.Installed[0].Adopted || !r.Installed[1].Adopted {
		t.Fatalf("receipt %+v: only the claude install was a setup-skills install", r.Installed)
	}
	installed(t, filepath.Join(cwd, ".claude"))
	b, _ := os.ReadFile(filepath.Join(cwd, ".claude", "CLAUDE.md"))
	if !strings.HasPrefix(string(b), "mine\n") || strings.Contains(string(b), "\nold\n") {
		t.Errorf("instruction file %q", b)
	}
	// An explicit configuration directory adopts the same way.
	work := filepath.Join(home, ".claude-work")
	legacySkillsInstall(t, work, "claude", "CLAUDE.md")
	if r, code, out = runSetup(t, "--agent", "claude", "--config-dir", work); code != 0 || !r.Installed[0].Adopted {
		t.Fatalf("%d %s %+v", code, out, r)
	}
	installed(t, work)
}

func TestSetupSkillsWasRemovedWithoutAnAlias(t *testing.T) {
	isolateSetup(t)
	var out, errOut bytes.Buffer
	if code := Run([]string{"setup-skills", "--scope", "local", "--agent", "codex"}, &out, &errOut); code != 2 || !strings.Contains(out.String()+errOut.String(), "unknown command") {
		t.Fatalf("%d %s%s", code, &out, &errOut)
	}
	if _, err := os.Stat(".codex"); err == nil {
		t.Fatal("the removed command installed something")
	}
}

func TestSetupWorkflowsRefusals(t *testing.T) {
	cwd, home, _, _ := isolateSetup(t)
	existing := filepath.Join(home, ".claude-work")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(cwd, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "parent", ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"nothing":                    {nil, "--agent"},
		"agent only":                 {[]string{"--agent", "codex"}, "--scope local|user or --config-dir"},
		"scope only":                 {[]string{"--scope", "local"}, "--agent"},
		"unknown agent":              {[]string{"--scope", "local", "--agent", "bad"}, "bad"},
		"custom scope is gone":       {[]string{"--scope", "custom", "--agent", "codex"}, "custom"},
		"path is gone":               {[]string{"--scope", "user", "--agent", "codex", "--path", home}, "--path"},
		"scope and config-dir":       {[]string{"--scope", "local", "--agent", "claude", "--config-dir", existing}, "not both"},
		"config-dir with both":       {[]string{"--agent", "both", "--config-dir", existing}, "exactly one agent"},
		"config-dir without agent":   {[]string{"--config-dir", existing}, "--agent"},
		"missing directory":          {[]string{"--agent", "claude", "--config-dir", filepath.Join(home, "typo")}, "does not exist"},
		"a file":                     {[]string{"--agent", "claude", "--config-dir", file}, "not a directory"},
		"parent of a host directory": {[]string{"--agent", "claude", "--config-dir", filepath.Join(home, "parent")}, "parent of a .claude directory"},
		"repeated flag":              {[]string{"--scope", "local", "--scope", "user", "--agent", "codex"}, "duplicate flag --scope"},
		"positional":                 {[]string{"--scope", "local", "--agent", "codex", "extra"}, "extra"},
		"unknown flag":               {[]string{"--unknown"}, "unknown"},
	} {
		t.Run(name, func(t *testing.T) {
			_, code, out := runSetup(t, tc.args...)
			if code != 2 || !strings.Contains(out, tc.want) {
				t.Fatalf("code %d, output %q; want exit 2 mentioning %q", code, out, tc.want)
			}
		})
	}
	for _, d := range []string{filepath.Join(cwd, ".codex"), filepath.Join(cwd, ".claude"), filepath.Join(home, "typo"), filepath.Join(home, ".claude"), filepath.Join(existing, "skills")} {
		if _, err := os.Stat(d); err == nil {
			t.Errorf("a refused run created %s", d)
		}
	}
}

func TestSetupWorkflowsHelp(t *testing.T) {
	isolateSetup(t)
	var b bytes.Buffer
	if code := Run([]string{"setup-workflows", "--help"}, &b, &b); code != 0 {
		t.Fatal(code, &b)
	}
	for _, want := range []string{"--config-dir DIRECTORY", "--scope local|user", "CLAUDE_CONFIG_DIR and CODEX_HOME are not read", ".lit-skills.json", "adopt"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("help lacks %q:\n%s", want, &b)
		}
	}
	if strings.Contains(b.String(), "custom") || strings.Contains(b.String(), "--path") {
		t.Errorf("help still documents the removed custom scope:\n%s", &b)
	}
}

func TestGlobalHelpIncludesWorkflowSetup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	var b bytes.Buffer
	if code := Run([]string{"--help"}, &b, &b); code != 0 || !strings.Contains(b.String(), "setup-workflows") || strings.Contains(b.String(), "setup-skills") {
		t.Fatalf("%d %s", code, &b)
	}
}
