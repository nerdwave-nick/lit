package workflowskills

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// litUIMod is the lit-ui Claude Code mod's source, beside the skills it ships with.
const litUIMod = "mods/lit-ui"

// requireModTool finds a tool the mod checks need. A missing tool skips the
// check, unless LIT_REQUIRE_MOD_CHECKS=1 makes that a failure.
func requireModTool(t *testing.T, name, override string) string {
	t.Helper()
	if override != "" {
		return override
	}
	path, err := exec.LookPath(name)
	if err != nil && os.Getenv("LIT_REQUIRE_MOD_CHECKS") == "1" {
		t.Fatalf("%s is required for the mod checks but not on PATH", name)
	} else if err != nil {
		t.Skipf("%s is not on PATH; the lit-ui mod checks need it (LIT_REQUIRE_MOD_CHECKS=1 makes this a failure)", name)
	}
	return path
}

func runModTool(t *testing.T, dir string, argv ...string) string {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(argv, " "), err, out)
	}
	return string(out)
}

// TestLitUIModChecks runs the mod's own checks: its unit tests under
// `claude plugin test`, `claude plugin validate`, and tsc against the Claude
// Code API declarations (docs/development/testing.md).
func TestLitUIModChecks(t *testing.T) {
	mod, err := filepath.Abs(litUIMod)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("plugin test", func(t *testing.T) {
		claude := requireModTool(t, "claude", "")
		out := runModTool(t, mod, claude, "plugin", "test", mod)
		if !strings.Contains(out, " 0 fail") {
			t.Fatalf("claude plugin test did not report 0 failures:\n%s", out)
		}
	})
	t.Run("plugin validate", func(t *testing.T) {
		claude := requireModTool(t, "claude", "")
		out := runModTool(t, mod, claude, "plugin", "validate", mod)
		if !strings.Contains(out, "Validation passed") || strings.Contains(out, "warning") {
			t.Fatalf("claude plugin validate is not clean:\n%s", out)
		}
	})
	t.Run("tsc", func(t *testing.T) {
		tsc := requireModTool(t, "tsc", os.Getenv("LIT_TSC"))
		types := modTypeFiles(t, mod)
		config := map[string]any{
			"compilerOptions": map[string]any{
				"target": "es2023", "lib": []string{"es2023"}, "types": []string{},
				"module": "esnext", "moduleResolution": "bundler",
				"strict": true, "noUncheckedIndexedAccess": true, "noEmit": true, "skipLibCheck": true,
				"jsx": "react", "jsxFactory": "h", "jsxFragmentFactory": "Fragment",
			},
			"include": append(types, filepath.Join(mod, "hooks"), filepath.Join(mod, "types"), filepath.Join(mod, "tests")),
		}
		data, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "tsconfig.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		runModTool(t, mod, tsc, "-p", path)
	})
}

// modTypeFiles locates the Claude Code API declarations: LIT_CLAUDE_TYPES (the
// claude-code.d.ts a plugin-authoring skill load writes), else those the engine
// lays into the mod's .claude-plugin/types when a session loads it.
func modTypeFiles(t *testing.T, mod string) []string {
	t.Helper()
	if file := os.Getenv("LIT_CLAUDE_TYPES"); file != "" {
		return []string{file}
	}
	laid := filepath.Join(mod, ".claude-plugin", "types")
	if _, err := os.Stat(filepath.Join(laid, "claude-code", "index.d.ts")); err == nil {
		return []string{laid}
	}
	if os.Getenv("LIT_REQUIRE_MOD_CHECKS") == "1" {
		t.Fatal("no Claude Code API declarations: set LIT_CLAUDE_TYPES or load the mod once with claude --plugin-dir")
	}
	t.Skip("no Claude Code API declarations: set LIT_CLAUDE_TYPES or load the mod once with claude --plugin-dir")
	return nil
}
