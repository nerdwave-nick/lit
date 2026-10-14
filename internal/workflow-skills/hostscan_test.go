package workflowskills

import (
	"path/filepath"
	"strings"
	"testing"
)

// interimHome builds a home directory like the one hand-written lit hooks left
// behind: ~/.claude with a stage hook, a status line script that reads lit's
// sessions and an unrelated hook.
func interimHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	claude := filepath.Join(home, ".claude")
	put(t, filepath.Join(claude, "settings.json"), `{
  "hooks": {
    "PostToolUse": [
      {"matcher": "Write|Edit", "hooks": [{"type": "command", "command": "~/.claude/hooks/lint-on-edit.sh"}]},
      {"matcher": "Skill", "hooks": [{"type": "command", "command": "~/.claude/hooks/lit-stage-hook.sh"}]}
    ],
    "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "~/.claude/hooks/lit-stage-hook.sh"}]}]
  },
  "statusLine": {"type": "command", "command": "~/.claude/statusline.sh", "padding": 0, "refreshInterval": 2}
}`)
	put(t, filepath.Join(claude, "hooks", "lit-stage-hook.sh"), "#!/usr/bin/env bash\nexit 0\n")
	put(t, filepath.Join(claude, "hooks", "lint-on-edit.sh"), "#!/usr/bin/env bash\nexit 0\n")
	put(t, filepath.Join(claude, "statusline.sh"), "#!/usr/bin/env bash\nsessions=$HOME/.local/state/lit/local/vendor-sessions\nlit=(lit --session \"$s\" --format json)\n")
	return home
}

func installWithHome(t *testing.T, target Target, home, work string) Result {
	t.Helper()
	return install(t, target, Options{Home: home, WorkDir: work})
}

func TestWarnsAboutTheInterimHooksAndStatusLineOfAnotherConfiguration(t *testing.T) {
	home := interimHome(t)
	target := Target{Host: "claude", Dir: filepath.Join(home, ".claude-work"), Scope: ScopeConfigDir}
	put(t, filepath.Join(target.Dir, "settings.json"), `{"model": "m"}`)
	res := installWithHome(t, target, home, t.TempDir())
	interim := filepath.Join(home, ".claude", "settings.json")
	for _, want := range []string{"lit-stage-hook.sh", "statusline.sh"} {
		found := false
		for _, w := range res.Warnings {
			found = found || (strings.Contains(w, interim) && strings.Contains(w, want))
		}
		if !found {
			t.Errorf("no warning names %s in %s: %q", want, interim, res.Warnings)
		}
	}
	if contains(res.Warnings, "lint-on-edit.sh") {
		t.Errorf("an unrelated hook was reported: %q", res.Warnings)
	}
	// The other configuration is only read.
	if got := readText(t, interim); !strings.Contains(got, "lit-stage-hook.sh") {
		t.Error("the other configuration was edited")
	}
}

func TestWarnsAboutLitHooksInTheTargetsOwnSettingsAndWrapsNothingElse(t *testing.T) {
	home := interimHome(t)
	target := Target{Host: "claude", Dir: filepath.Join(home, ".claude"), Scope: ScopeUser}
	res := installWithHome(t, target, home, t.TempDir())
	own := filepath.Join(target.Dir, "settings.json")
	if !contains(res.Warnings, own) || !contains(res.Warnings, "lit-stage-hook.sh") {
		t.Errorf("warnings %q", res.Warnings)
	}
	// The foreign status line is wrapped, and it already shows lit's state.
	if res.StatusLine != "wrapped" || !contains(res.Warnings, "status line") || !contains(res.Warnings, "statusline.sh") {
		t.Errorf("status line %q, warnings %q", res.StatusLine, res.Warnings)
	}
	if !strings.Contains(readText(t, own), "lit-stage-hook.sh") {
		t.Error("setup removed the hand-written hook")
	}
}

func TestWarnsAboutLitHooksInTheProjectLayer(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	put(t, filepath.Join(work, ".claude", "settings.json"), `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "lit hook --host claude stop"}]}]}}`)
	put(t, filepath.Join(work, ".claude", "settings.local.json"), `{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "'/opt/my tools/lit' hook --host claude session-start"}]}]}}`)
	put(t, filepath.Join(work, ".codex", "hooks.json"), `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "lit hook --host codex stop"}]}]}}`)
	put(t, filepath.Join(work, ".codex", "config.toml"), "[[hooks.SessionStart.hooks]]\ntype = \"command\"\ncommand = \"lit hook --host codex session-start\"\n")
	claude := installWithHome(t, Target{Host: "claude", Dir: filepath.Join(home, ".claude"), Scope: ScopeUser}, home, work)
	codex := installWithHome(t, Target{Host: "codex", Dir: filepath.Join(home, ".codex"), Scope: ScopeUser}, home, work)
	for _, want := range []string{filepath.Join(work, ".claude", "settings.json"), filepath.Join(work, ".claude", "settings.local.json")} {
		if !contains(claude.Warnings, want) {
			t.Errorf("claude: no warning for %s in %q", want, claude.Warnings)
		}
	}
	for _, want := range []string{filepath.Join(work, ".codex", "hooks.json"), filepath.Join(work, ".codex", "config.toml")} {
		if !contains(codex.Warnings, want) {
			t.Errorf("codex: no warning for %s in %q", want, codex.Warnings)
		}
	}
	// A host reports the layers of its own kind.
	if contains(claude.Warnings, ".codex") || contains(codex.Warnings, filepath.Join(work, ".claude")) {
		t.Errorf("claude %q, codex %q", claude.Warnings, codex.Warnings)
	}
}

func TestWarnsAboutLitHooksInAnotherCodexHome(t *testing.T) {
	home := t.TempDir()
	put(t, filepath.Join(home, ".codex", "hooks.json"), `{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "lit hook --host codex session-start"}]}], "Stop": []}}`)
	target := Target{Host: "codex", Dir: filepath.Join(home, "codex-work"), Scope: ScopeConfigDir}
	put(t, filepath.Join(target.Dir, "config.toml"), "")
	res := installWithHome(t, target, home, t.TempDir())
	if !contains(res.Warnings, filepath.Join(home, ".codex", "hooks.json")) || !contains(res.Warnings, "session-start") {
		t.Errorf("warnings %q", res.Warnings)
	}
}

func TestOwnEntriesAndEmptyLayersAreNotReported(t *testing.T) {
	home := t.TempDir()
	put(t, filepath.Join(home, ".codex", "hooks.json"), `{"hooks": {"PostToolUse": [], "PreCompact": [], "SessionStart": [], "Stop": []}}`)
	put(t, filepath.Join(home, ".claude", "settings.json"), `{"model": "m"}`)
	for _, host := range []string{"claude", "codex"} {
		target := Target{Host: host, Dir: filepath.Join(home, "."+host), Scope: ScopeUser}
		res := installWithHome(t, target, home, t.TempDir())
		if len(res.Warnings) != 0 {
			t.Errorf("%s: warnings %q", host, res.Warnings)
		}
		// A second run sees its own entries and stays quiet.
		if res = installWithHome(t, target, home, t.TempDir()); len(res.Warnings) != 0 {
			t.Errorf("%s: second run: warnings %q", host, res.Warnings)
		}
	}
}

func TestLocalScopeLooksForNoHooks(t *testing.T) {
	home := interimHome(t)
	res, err := InstallWith(Bundle(), localTargets(t.TempDir(), "claude"), Options{Home: home, WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(res[0].Warnings) != 0 {
		t.Errorf("warnings %q", res[0].Warnings)
	}
}

func TestUnreadableOrMalformedOtherLayersAreSkipped(t *testing.T) {
	home := t.TempDir()
	put(t, filepath.Join(home, ".claude", "settings.json"), `{"hooks": `)
	put(t, filepath.Join(home, ".claude-old", "settings.json"), `[1]`)
	put(t, filepath.Join(home, ".claude.json"), `{"not": "a directory of settings"}`)
	target := Target{Host: "claude", Dir: filepath.Join(home, ".claude-work"), Scope: ScopeConfigDir}
	put(t, filepath.Join(target.Dir, "settings.json"), `{}`)
	if res := installWithHome(t, target, home, t.TempDir()); len(res.Warnings) != 0 {
		t.Errorf("warnings %q", res.Warnings)
	}
}

func TestLitRelatedCommands(t *testing.T) {
	home := t.TempDir()
	put(t, filepath.Join(home, "bin", "line.sh"), "echo hi\nlit --session x claims list\n")
	put(t, filepath.Join(home, "bin", "plain.sh"), "echo hi\n")
	put(t, filepath.Join(home, "bin", "vendor.sh"), "cat $HOME/.local/state/lit/local/vendor-sessions/x\n")
	for cmd, want := range map[string]bool{
		"lit hook --host claude stop":                     true,
		"/usr/local/bin/lit hook --host codex stop":       true,
		"'/opt/my tools/lit' hook --host claude stop":     true,
		"lit.exe hook --host claude stop":                 true,
		"env LIT_ENDPOINT=http://x lit hook --host codex": true,
		"lit workflow-session --host claude resume":       true,
		"~/.claude/hooks/lit-stage-hook.sh":               true,
		"bash /home/me/hooks/lit_stage.sh":                true,
		"~/bin/line.sh":                                   true,
		"$HOME/bin/vendor.sh":                             true,
		"bash -c 'lit hook --host claude stop'":           true,
		"~/bin/plain.sh":                                  false,
		"~/bin/missing.sh":                                false,
		"~/.claude/hooks/lint-on-edit.sh":                 false,
		"echo literal":                                    false,
		"split hook --host x":                             false,
		"gofmt -l .":                                      false,
		"":                                                false,
	} {
		if got := litRelated(cmd, home); got != want {
			t.Errorf("litRelated(%q) = %v, want %v", cmd, got, want)
		}
	}
}
