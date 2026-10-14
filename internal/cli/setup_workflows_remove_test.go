package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type removalOutput struct {
	Removed []struct {
		Agent      string   `json:"agent"`
		Scope      string   `json:"scope"`
		Directory  string   `json:"directory"`
		Found      bool     `json:"found"`
		Skills     int      `json:"skills"`
		Hooks      bool     `json:"hooks"`
		StatusLine string   `json:"status_line"`
		Warnings   []string `json:"warnings"`
		Notes      []string `json:"notes"`
	} `json:"removed"`
}

func runRemoval(t *testing.T, args ...string) (removalOutput, int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(append([]string{"setup-workflows", "--remove"}, args...), &out, &errOut)
	var r removalOutput
	if code == 0 {
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatalf("receipt %q: %v", out.String(), err)
		}
	}
	return r, code, out.String(), errOut.String()
}

func filesBelow(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			files = append(files, rel)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestSetupWorkflowsRemoveUndoesEveryScope(t *testing.T) {
	cwd, home, claudeEnv, codexEnv := isolateSetup(t)
	work := filepath.Join(home, ".claude-work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "settings.json"), []byte("{\n  \"statusLine\": {\n    \"type\": \"command\",\n    \"command\": \"~/mine.sh\"\n  }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args  []string
		roots []string
		hooks bool
		scope string
	}{
		{[]string{"--scope", "local", "--agent", "both"}, []string{filepath.Join(cwd, ".codex"), filepath.Join(cwd, ".claude")}, false, "local"},
		{[]string{"--scope", "user", "--agent", "both"}, []string{filepath.Join(home, ".codex"), filepath.Join(home, ".claude")}, true, "user"},
		{[]string{"--agent", "claude", "--config-dir", work}, []string{work}, true, "config-dir"},
	} {
		t.Run(tc.scope, func(t *testing.T) {
			if _, code, out := runSetup(t, tc.args...); code != 0 {
				t.Fatalf("install: %d %s", code, out)
			}
			r, code, stdout, stderr := runRemoval(t, tc.args...)
			if code != 0 || len(r.Removed) != len(tc.roots) {
				t.Fatalf("remove: %d %s %s", code, stdout, stderr)
			}
			for i, e := range r.Removed {
				if e.Directory != tc.roots[i] || e.Scope != tc.scope || !e.Found || e.Skills != 16 || e.Hooks != tc.hooks {
					t.Errorf("receipt entry %+v", e)
				}
				// Only the user's own settings.json may remain.
				left := filesBelow(t, e.Directory)
				if tc.scope == "config-dir" {
					left = slices.DeleteFunc(left, func(f string) bool { return f == "settings.json" })
				}
				if len(left) != 0 {
					t.Errorf("%s still holds %v", e.Directory, left)
				}
			}
			// Nothing is left to remove the second time, which is not an error.
			r, code, stdout, stderr = runRemoval(t, tc.args...)
			if code != 0 || len(r.Removed) != len(tc.roots) || r.Removed[0].Found || !strings.Contains(stderr, "nothing to remove") {
				t.Errorf("second removal: %d %s %s", code, stdout, stderr)
			}
		})
	}
	// The wrapped status line is back exactly as the user wrote it.
	got, err := os.ReadFile(filepath.Join(work, "settings.json"))
	if err != nil || string(got) != "{\n  \"statusLine\": {\n    \"type\": \"command\",\n    \"command\": \"~/mine.sh\"\n  }\n}\n" {
		t.Errorf("settings.json after removal: %q (%v)", got, err)
	}
	for _, d := range []string{claudeEnv, codexEnv} {
		if ents, _ := os.ReadDir(d); len(ents) != 0 {
			t.Errorf("removal touched a vendor override directory %s", d)
		}
	}
}

func TestSetupWorkflowsRemoveReceiptShape(t *testing.T) {
	_, home, _, _ := isolateSetup(t)
	runSetup(t, "--scope", "user", "--agent", "claude")
	_, code, stdout, _ := runRemoval(t, "--scope", "user", "--agent", "claude")
	want := `{"removed":[{"agent":"claude","scope":"user","directory":"` + filepath.Join(home, ".claude") + `","found":true,"skills":16,"hooks":true,"status_line":"removed"}]}` + "\n"
	if code != 0 || stdout != want {
		t.Errorf("receipt %q, want %q", stdout, want)
	}
}

func TestSetupWorkflowsRemoveRefusesAModifiedSkill(t *testing.T) {
	_, home, _, _ := isolateSetup(t)
	runSetup(t, "--scope", "user", "--agent", "both")
	edited := filepath.Join(home, ".claude", "skills", "impl", "SKILL.md")
	if err := os.WriteFile(edited, []byte("my edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := append(filesBelow(t, filepath.Join(home, ".codex")), filesBelow(t, filepath.Join(home, ".claude"))...)
	_, code, stdout, stderr := runRemoval(t, "--scope", "user", "--agent", "both")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "impl") || !strings.Contains(stderr, "setup-workflows:") {
		t.Fatalf("%d %q %q", code, stdout, stderr)
	}
	after := append(filesBelow(t, filepath.Join(home, ".codex")), filesBelow(t, filepath.Join(home, ".claude"))...)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Errorf("a refused removal changed the installation:\n%v\n---\n%v", before, after)
	}
}

func TestSetupWorkflowsRemoveTakesTheSameTargetFlags(t *testing.T) {
	cwd, home, _, _ := isolateSetup(t)
	existing := filepath.Join(home, ".claude-work")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"nothing":              {nil, "--agent"},
		"agent only":           {[]string{"--agent", "codex"}, "--scope local|user or --config-dir"},
		"scope only":           {[]string{"--scope", "local"}, "--agent"},
		"custom scope is gone": {[]string{"--scope", "custom", "--agent", "codex"}, "custom"},
		"scope and config-dir": {[]string{"--scope", "local", "--agent", "claude", "--config-dir", existing}, "not both"},
		"config-dir with both": {[]string{"--agent", "both", "--config-dir", existing}, "exactly one agent"},
		"missing directory":    {[]string{"--agent", "claude", "--config-dir", filepath.Join(home, "typo")}, "does not exist"},
		"repeated remove":      {[]string{"--remove", "--scope", "local", "--agent", "codex"}, "duplicate flag --remove"},
		"positional":           {[]string{"--scope", "local", "--agent", "codex", "extra"}, "extra"},
	} {
		t.Run(name, func(t *testing.T) {
			_, code, stdout, stderr := runRemoval(t, tc.args...)
			if code != 2 || !strings.Contains(stdout+stderr, tc.want) {
				t.Fatalf("code %d, output %q %q; want exit 2 mentioning %q", code, stdout, stderr, tc.want)
			}
		})
	}
	for _, d := range []string{filepath.Join(cwd, ".codex"), filepath.Join(cwd, ".claude"), filepath.Join(home, "typo"), filepath.Join(home, ".claude"), filepath.Join(existing, "skills")} {
		if _, err := os.Stat(d); err == nil {
			t.Errorf("a refused run created %s", d)
		}
	}
}

func TestSetupWorkflowsRemoveCreatesNothingWhereNothingIsInstalled(t *testing.T) {
	cwd, home, _, _ := isolateSetup(t)
	r, code, stdout, stderr := runRemoval(t, "--scope", "local", "--agent", "both")
	if code != 0 || len(r.Removed) != 2 || r.Removed[0].Found || r.Removed[1].Found || !strings.Contains(stderr, "nothing to remove") {
		t.Fatalf("%d %s %s", code, stdout, stderr)
	}
	for _, d := range []string{filepath.Join(cwd, ".codex"), filepath.Join(cwd, ".claude"), filepath.Join(home, ".claude")} {
		if _, err := os.Stat(d); err == nil {
			t.Errorf("removal created %s", d)
		}
	}
}

func TestSetupWorkflowsHelpDocumentsRemoval(t *testing.T) {
	isolateSetup(t)
	var b bytes.Buffer
	if code := Run([]string{"setup-workflows", "--help"}, &b, &b); code != 0 {
		t.Fatal(code, &b)
	}
	for _, want := range []string{"--remove", "ownership record", "restor", "setup-workflows [--remove] --scope local|user --agent codex|claude|both"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("help lacks %q:\n%s", want, &b)
		}
	}
}
