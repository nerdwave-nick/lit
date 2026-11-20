package workflowskills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolate points the home directory and current directory at fresh temporary
// directories and returns them, with the vendor configuration overrides set to
// directories that must stay untouched.
func isolate(t *testing.T) (cwd, home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	cwd = t.TempDir()
	t.Chdir(cwd)
	return cwd, home
}

// relative spells target relative to base; a relative --config-dir resolves
// against the current directory.
func relative(t *testing.T, base, target string) string {
	t.Helper()
	rel, err := filepath.Rel(base, target)
	if err != nil {
		t.Fatal(err)
	}
	return rel
}

func TestResolveTargets(t *testing.T) {
	cwd, home := isolate(t)
	claudeWork := filepath.Join(home, ".claude-work")
	codexAlt := filepath.Join(home, "alt codex")
	for _, d := range []string{claudeWork, codexAlt} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		sel  Selection
		want []Target
	}{
		{"local both", Selection{Scope: "local", Agent: "both"}, []Target{
			{"codex", filepath.Join(cwd, ".codex"), ScopeLocal}, {"claude", filepath.Join(cwd, ".claude"), ScopeLocal}}},
		{"local claude", Selection{Scope: "local", Agent: "claude"}, []Target{{"claude", filepath.Join(cwd, ".claude"), ScopeLocal}}},
		{"user both", Selection{Scope: "user", Agent: "both"}, []Target{
			{"codex", filepath.Join(home, ".codex"), ScopeUser}, {"claude", filepath.Join(home, ".claude"), ScopeUser}}},
		{"user codex", Selection{Scope: "user", Agent: "codex"}, []Target{{"codex", filepath.Join(home, ".codex"), ScopeUser}}},
		{"claude configuration directory", Selection{Agent: "claude", ConfigDir: claudeWork}, []Target{{"claude", claudeWork, ScopeConfigDir}}},
		{"codex configuration directory with a space", Selection{Agent: "codex", ConfigDir: codexAlt}, []Target{{"codex", codexAlt, ScopeConfigDir}}},
		{"relative configuration directory", Selection{Agent: "claude", ConfigDir: relative(t, cwd, claudeWork)}, []Target{{"claude", claudeWork, ScopeConfigDir}}},
		{"unclean configuration directory", Selection{Agent: "claude", ConfigDir: claudeWork + "/./"}, []Target{{"claude", claudeWork, ScopeConfigDir}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveTargets(tc.sel)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("target %d: got %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// The user scope and an explicit directory naming the same configuration are
// the same install: same directory, so the same ownership record.
func TestUserScopeAndConfigDirAreTheSameTarget(t *testing.T) {
	_, home := isolate(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	user, err := ResolveTargets(Selection{Scope: "user", Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := ResolveTargets(Selection{Agent: "claude", ConfigDir: filepath.Join(home, ".claude")})
	if err != nil {
		t.Fatal(err)
	}
	if user[0].Dir != explicit[0].Dir || user[0].Host != explicit[0].Host {
		t.Fatalf("%+v != %+v", user[0], explicit[0])
	}
}

func TestResolveTargetsRefusals(t *testing.T) {
	_, home := isolate(t)
	existing := filepath.Join(home, ".claude-work")
	if err := os.MkdirAll(existing, 0755); err != nil {
		t.Fatal(err)
	}
	notDir := filepath.Join(home, "file")
	put(t, notDir, "x")
	// A directory that holds .claude is the parent of a configuration, which
	// the removed --scope custom --path used to take.
	parent := filepath.Join(home, "project")
	if err := os.MkdirAll(filepath.Join(parent, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		sel  Selection
		want string
	}{
		{"no agent", Selection{Scope: "local"}, "--agent"},
		{"unknown agent", Selection{Scope: "local", Agent: "gemini"}, `not "gemini"`},
		{"no target", Selection{Agent: "claude"}, "--scope local|user or --config-dir"},
		{"scope with configuration directory", Selection{Scope: "local", Agent: "claude", ConfigDir: existing}, "not both"},
		{"custom scope is gone", Selection{Scope: "custom", Agent: "claude"}, `--scope must be local or user, not "custom"`},
		{"both agents in one directory", Selection{Agent: "both", ConfigDir: existing}, "exactly one agent"},
		{"missing directory", Selection{Agent: "claude", ConfigDir: filepath.Join(home, ".claude-wrk")}, "does not exist"},
		{"file instead of directory", Selection{Agent: "claude", ConfigDir: notDir}, "not a directory"},
		{"parent of a configuration", Selection{Agent: "claude", ConfigDir: parent}, "parent of a .claude directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveTargets(tc.sel)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(home, ".claude-wrk")); !os.IsNotExist(err) {
		t.Fatal("a refused configuration directory must not be created")
	}
}
