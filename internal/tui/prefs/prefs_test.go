package prefs

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
)

// isolate points HOME and XDG_CONFIG_HOME at fresh directories and clears the
// lit-tui variables. It returns the default config path.
func isolate(t *testing.T) string {
	t.Helper()
	home, xdg := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	for _, v := range []string{EnvConfig, EnvTheme, EnvMouse} {
		t.Setenv(v, "")
	}
	return filepath.Join(xdg, "lit", "tui.json")
}

func write(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func str(s string) *string { return &s }

func mustLoad(t *testing.T, f Flags) Prefs {
	t.Helper()
	p, err := Load(f)
	if err != nil {
		t.Fatalf("Load(%+v): %v", f, err)
	}
	return p
}

func TestLoadWithoutConfigUsesTheDefaults(t *testing.T) {
	isolate(t)
	p := mustLoad(t, Flags{})
	if p.Theme != ThemeAuto || p.Mouse || p.File != "" {
		t.Errorf("prefs %+v", p)
	}
	if got := p.Keymap.Keys(keys.Global, keys.Quit); !slices.Equal(got, []string{"q"}) {
		t.Errorf("quit keys %v", got)
	}
	d := Default()
	if d.Theme != ThemeAuto || d.Mouse || !slices.Equal(d.Keymap.Keys(keys.Tree, keys.Up), []string{"up", "k"}) {
		t.Errorf("Default() %+v", d)
	}
}

func TestDefaultPathFollowsXDGConfigHome(t *testing.T) {
	want := isolate(t)
	if got, err := DefaultPath(); err != nil || got != want {
		t.Errorf("DefaultPath() = %q, %v; want %q", got, err, want)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative/dir")
	if got, err := DefaultPath(); err != nil || got != filepath.Join(os.Getenv("HOME"), ".config", "lit", "tui.json") {
		t.Errorf("relative XDG_CONFIG_HOME: %q, %v", got, err)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if got, err := DefaultPath(); err != nil || got != filepath.Join(os.Getenv("HOME"), ".config", "lit", "tui.json") {
		t.Errorf("unset XDG_CONFIG_HOME: %q, %v", got, err)
	}
}

func TestLoadReadsTheDefaultConfigFile(t *testing.T) {
	path := write(t, isolate(t), `{"schema_version": 1, "theme": "light", "mouse": true,
		"keymap": {"global": {"quit": ["x"]}, "tree": {"expand": ["space"]}}}`)
	p := mustLoad(t, Flags{})
	if p.Theme != ThemeLight || !p.Mouse || p.File != path {
		t.Errorf("prefs %+v", p)
	}
	if got := p.Keymap.Keys(keys.Global, keys.Quit); !slices.Equal(got, []string{"x"}) {
		t.Errorf("quit keys %v", got)
	}
	if got := p.Keymap.Keys(keys.Tree, keys.Expand); !slices.Equal(got, []string{"space"}) {
		t.Errorf("expand keys %v", got)
	}
}

// Flags, then environment, then the JSON config, then defaults.
func TestLoadPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		file      string
		env       [2]string // theme, mouse
		flags     Flags
		theme     Theme
		mouse     bool
		wantFile  bool
		noDefault bool
	}{
		{name: "defaults", noDefault: true, theme: ThemeAuto, mouse: false},
		{name: "file", file: `{"schema_version":1,"theme":"dark","mouse":true}`, theme: ThemeDark, mouse: true, wantFile: true},
		{name: "file without the fields keeps defaults", file: `{"schema_version":1}`, theme: ThemeAuto, mouse: false, wantFile: true},
		{name: "environment over file", file: `{"schema_version":1,"theme":"dark","mouse":true}`, env: [2]string{"light", "false"}, theme: ThemeLight, mouse: false, wantFile: true},
		{name: "environment over defaults", noDefault: true, env: [2]string{"dark", "true"}, theme: ThemeDark, mouse: true},
		{name: "flags over environment", file: `{"schema_version":1,"theme":"dark","mouse":false}`, env: [2]string{"light", "true"},
			flags: Flags{Theme: str("auto"), Mouse: str("false")}, theme: ThemeAuto, mouse: false, wantFile: true},
		{name: "flags over file", file: `{"schema_version":1,"theme":"dark"}`, flags: Flags{Theme: str("light"), Mouse: str("true")}, theme: ThemeLight, mouse: true, wantFile: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := isolate(t)
			if !tc.noDefault {
				write(t, path, tc.file)
			}
			t.Setenv(EnvTheme, tc.env[0])
			t.Setenv(EnvMouse, tc.env[1])
			p := mustLoad(t, tc.flags)
			if p.Theme != tc.theme || p.Mouse != tc.mouse || (p.File != "") != tc.wantFile {
				t.Errorf("prefs %+v, want theme %s mouse %v file %v", p, tc.theme, tc.mouse, tc.wantFile)
			}
		})
	}
}

func TestExplicitConfigFileSelectionAndExistence(t *testing.T) {
	def := write(t, isolate(t), `{"schema_version":1,"theme":"dark"}`)
	dir := t.TempDir()
	envFile := write(t, filepath.Join(dir, "env.json"), `{"schema_version":1,"theme":"light"}`)
	flagFile := write(t, filepath.Join(dir, "flag.json"), `{"schema_version":1,"mouse":true}`)
	missing := filepath.Join(dir, "missing.json")

	t.Setenv(EnvConfig, envFile)
	if p := mustLoad(t, Flags{}); p.File != envFile || p.Theme != ThemeLight {
		t.Errorf("LIT_TUI_CONFIG: %+v", p)
	}
	if p := mustLoad(t, Flags{Config: &flagFile}); p.File != flagFile || p.Theme != ThemeAuto || !p.Mouse {
		t.Errorf("--config over LIT_TUI_CONFIG: %+v", p)
	}
	t.Setenv(EnvConfig, missing)
	if _, err := Load(Flags{}); err == nil || !strings.Contains(err.Error(), "config: open "+missing) {
		t.Errorf("missing LIT_TUI_CONFIG: %v", err)
	}
	if p := mustLoad(t, Flags{Config: &flagFile}); p.File != flagFile {
		t.Errorf("--config replaces a missing LIT_TUI_CONFIG: %+v", p)
	}
	t.Setenv(EnvConfig, "")
	if _, err := Load(Flags{Config: str("")}); err == nil || !strings.Contains(err.Error(), "config: ") {
		t.Errorf("empty --config must not fall back to the default file: %v", err)
	}
	if _, err := Load(Flags{Config: &missing});err == nil || !strings.Contains(err.Error(), "config: open "+missing) {
		t.Errorf("missing --config: %v", err)
	}
	if err := os.Remove(def); err != nil {
		t.Fatal(err)
	}
	if p := mustLoad(t, Flags{}); p.File != "" {
		t.Errorf("missing default file: %+v", p)
	}
	if err := os.Mkdir(def, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(Flags{}); err == nil || !strings.Contains(err.Error(), "config: ") {
		t.Errorf("unreadable default file: %v", err)
	}
}

func TestLoadRejectsInvalidConfigFiles(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
	}{
		{"unknown field", `{"schema_version":1,"colour":"red"}`, `unknown field "colour"`},
		{"missing schema_version", `{"theme":"dark"}`, "unsupported schema_version 0 (want 1)"},
		{"future schema_version", `{"schema_version":2}`, "unsupported schema_version 2 (want 1)"},
		{"duplicate key", `{"schema_version":1,"theme":"dark","theme":"light"}`, `duplicate JSON key "theme"`},
		{"trailing JSON", `{"schema_version":1}{}`, "trailing JSON"},
		{"wrong type", `{"schema_version":1,"mouse":"yes"}`, "cannot unmarshal"},
		{"invalid theme", `{"schema_version":1,"theme":"blue"}`, `theme: invalid theme "blue" (want auto, light or dark)`},
		{"empty theme", `{"schema_version":1,"theme":""}`, `theme: invalid theme ""`},
		{"invalid keymap", `{"schema_version":1,"keymap":{"global":{"quit":[]}}}`, "keymap.global.quit: quit must keep at least one key"},
		{"reserved ctrl+c", `{"schema_version":1,"keymap":{"tree":{"open":["ctrl+c"]}}}`, `"ctrl+c" is reserved for the orderly shutdown`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := write(t, isolate(t), tc.content)
			_, err := Load(Flags{})
			if err == nil || !strings.Contains(err.Error(), "config "+path+": ") || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want %q", err, tc.want)
			}
		})
	}
}

// Every keymap problem is reported, each line naming the file.
func TestLoadReportsEveryKeymapProblemWithTheFile(t *testing.T) {
	path := write(t, isolate(t), `{"schema_version":1,"keymap":{"global":{"help":[]},"tree":{"jump":["J"]}}}`)
	_, err := Load(Flags{})
	if err == nil {
		t.Fatal("no error")
	}
	lines := strings.Split(err.Error(), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines %q", lines)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "config "+path+": keymap") {
			t.Errorf("line %q does not name the file", l)
		}
	}
}

func TestLoadRejectsInvalidEnvironmentAndFlagValues(t *testing.T) {
	for _, tc := range []struct {
		name       string
		env, value string
		flags      Flags
		want       string
	}{
		{"theme env", EnvTheme, "blue", Flags{}, `LIT_TUI_THEME: invalid theme "blue" (want auto, light or dark)`},
		{"mouse env", EnvMouse, "yes", Flags{}, `LIT_TUI_MOUSE: invalid value "yes" (want true or false)`},
		{"theme flag", "", "", Flags{Theme: str("Dark")}, `--theme: invalid theme "Dark" (want auto, light or dark)`},
		{"mouse flag", "", "", Flags{Mouse: str("1")}, `--mouse: invalid value "1" (want true or false)`},
		{"empty theme flag", "", "", Flags{Theme: str("")}, `--theme: invalid theme ""`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			if tc.env != "" {
				t.Setenv(tc.env, tc.value)
			}
			if _, err := Load(tc.flags); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want %q", err, tc.want)
			}
		})
	}
}
