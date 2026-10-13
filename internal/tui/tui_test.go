package tui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/keys"
	"github.com/nerdwave-nick/lit/internal/tui/prefs"
)

func TestModelQuitsOnlyOnQuitKeys(t *testing.T) {
	m := NewModel("http://127.0.0.1:7411")
	for _, tc := range []struct {
		name string
		key  tea.KeyPressMsg
		quit bool
	}{
		{"q", tea.KeyPressMsg{Code: 'q', Text: "q"}, true},
		{"ctrl+c", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, true},
		{"x", tea.KeyPressMsg{Code: 'x', Text: "x"}, false},
		{"esc", tea.KeyPressMsg{Code: tea.KeyEscape}, false},
	} {
		_, cmd := m.Update(tc.key)
		quit := false
		if cmd != nil {
			_, quit = cmd().(tea.QuitMsg)
		}
		if quit != tc.quit {
			t.Errorf("%s: quit %v, want %v", tc.name, quit, tc.quit)
		}
	}
}

func TestViewUsesTheAlternateScreenAndShowsTheEndpoint(t *testing.T) {
	v := NewModel("http://127.0.0.1:9999").View()
	if !v.AltScreen {
		t.Error("view does not use the alternate screen")
	}
	if content := v.Content; !strings.Contains(content, "http://127.0.0.1:9999") || !strings.Contains(content, "q") {
		t.Errorf("content: %q", content)
	}
}

// The program leaves the alternate screen and shows the cursor again when the
// user quits: the terminal is restored.
func TestRunQuitsCleanlyAndRestoresTheTerminal(t *testing.T) {
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := Run(ctx, NewModel("http://127.0.0.1:7411"),
		tea.WithInput(strings.NewReader("q")), tea.WithOutput(&out), tea.WithWindowSize(80, 24), tea.WithEnvironment([]string{"TERM=xterm-256color"}))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	s := out.String()
	enter, leave := strings.Index(s, "\x1b[?1049h"), strings.LastIndex(s, "\x1b[?1049l")
	if enter < 0 || leave < enter {
		t.Fatalf("alternate screen not entered then left: %q", s)
	}
	if !strings.Contains(s[leave:], "\x1b[?25h") && !strings.Contains(s[:leave], "\x1b[?25h") {
		t.Fatalf("cursor not shown again: %q", s)
	}
}

// isolatePrefs keeps Main away from the user's preferences: it points HOME
// and XDG_CONFIG_HOME at an empty directory and clears LIT_TUI_*. It returns
// the default config path.
func isolatePrefs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	for _, v := range []string{prefs.EnvConfig, prefs.EnvTheme, prefs.EnvMouse} {
		t.Setenv(v, "")
	}
	return filepath.Join(dir, "config", "lit", "tui.json")
}

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	isolatePrefs(t)
	return runIsolated(t, args...)
}

// runIsolated runs Main with the current environment.
func runIsolated(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	var stderr bytes.Buffer
	code := Main(args, stdin, stdout, &stderr)
	b, err := os.ReadFile(stdout.Name())
	if err != nil {
		t.Fatal(err)
	}
	return code, string(b), stderr.String()
}

func TestMainOutsideATerminal(t *testing.T) {
	t.Setenv("LIT_ENDPOINT", "")
	for _, tc := range []struct {
		args           []string
		code           int
		stdout, stderr string
	}{
		{nil, 2, "", "lit-tui needs an interactive terminal: stdin and stdout must be terminals"},
		{[]string{"--endpoint", "http://127.0.0.1:1"}, 2, "", "lit-tui needs an interactive terminal"},
		{[]string{"--help"}, 0, "Usage:\n  lit-tui [flags]", ""},
		{[]string{"-h"}, 0, "--endpoint URL", ""},
		{[]string{"--version"}, 0, protocol.ReleaseVersion + "\n", ""},
		{[]string{"-v"}, 0, protocol.ReleaseVersion + "\n", ""},
		{[]string{"--bogus"}, 2, "", "unknown flag --bogus"},
		{[]string{"extra"}, 2, "", `unexpected argument "extra" for lit-tui`},
		{[]string{"--endpoint", "ftp://example.com"}, 2, "", "invalid endpoint"},
	} {
		code, stdout, stderr := run(t, tc.args...)
		if code != tc.code || !strings.Contains(stdout, tc.stdout) || !strings.Contains(stderr, tc.stderr) || tc.stdout == "" && stdout != "" || tc.stderr == "" && stderr != "" {
			t.Errorf("%v: code %d, stdout %q, stderr %q", tc.args, code, stdout, stderr)
		}
	}
}

func TestMainResolvesTheEndpointFromTheEnvironment(t *testing.T) {
	t.Setenv("LIT_ENDPOINT", "not a url")
	if code, _, stderr := run(t); code != 2 || !strings.Contains(stderr, "invalid endpoint") {
		t.Fatalf("invalid LIT_ENDPOINT: %d %q", code, stderr)
	}
	if code, _, stderr := run(t, "--endpoint", "http://127.0.0.1:1"); code != 2 || strings.Contains(stderr, "invalid endpoint") {
		t.Fatalf("--endpoint must override LIT_ENDPOINT: %d %q", code, stderr)
	}
}

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Preferences are validated before the terminal check, so a bad keymap,
// value or config file fails with a clear message and exit 2 anywhere.
func TestMainRejectsInvalidPreferencesAtStartup(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	for _, tc := range []struct {
		name   string
		config string // written to the default path when not empty
		env    [2]string
		args   []string
		stderr string
	}{
		{name: "quit unbound", config: `{"schema_version":1,"keymap":{"global":{"quit":[]}}}`,
			stderr: "keymap.global.quit: quit must keep at least one key"},
		{name: "ctrl+c rebound", config: `{"schema_version":1,"keymap":{"global":{"refresh":["ctrl+c"]}}}`,
			stderr: `"ctrl+c" is reserved for the orderly shutdown and cannot be bound`},
		{name: "unknown field", config: `{"schema_version":1,"colour":"red"}`, stderr: `unknown field "colour"`},
		{name: "missing --config", args: []string{"--config", missing}, stderr: "lit-tui: config: open " + missing},
		{name: "missing LIT_TUI_CONFIG", env: [2]string{prefs.EnvConfig, missing}, stderr: "lit-tui: config: open " + missing},
		{name: "bad --theme", args: []string{"--theme", "blue"}, stderr: `lit-tui: --theme: invalid theme "blue"`},
		{name: "bad LIT_TUI_MOUSE", env: [2]string{prefs.EnvMouse, "on"}, stderr: `lit-tui: LIT_TUI_MOUSE: invalid value "on"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := isolatePrefs(t)
			if tc.config != "" {
				writeConfig(t, path, tc.config)
			}
			if tc.env[0] != "" {
				t.Setenv(tc.env[0], tc.env[1])
			}
			code, stdout, stderr := runIsolated(t, tc.args...)
			if code != 2 || stdout != "" || !strings.Contains(stderr, tc.stderr) || strings.Contains(stderr, "interactive terminal") {
				t.Errorf("code %d, stdout %q, stderr %q", code, stdout, stderr)
			}
		})
	}
}

// Every line of a multi-line preferences error names lit-tui.
func TestMainReportsEveryPreferencesProblem(t *testing.T) {
	writeConfig(t, isolatePrefs(t), `{"schema_version":1,"keymap":{"global":{"help":[]},"menu":{"cancel":[]}}}`)
	code, _, stderr := runIsolated(t)
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if code != 2 || len(lines) != 2 {
		t.Fatalf("code %d, stderr %q", code, stderr)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "lit-tui: config ") {
			t.Errorf("line %q", l)
		}
	}
}

func TestMainAcceptsValidPreferences(t *testing.T) {
	writeConfig(t, isolatePrefs(t), `{"schema_version":1,"theme":"dark","mouse":true,"keymap":{"global":{"quit":["x"]}}}`)
	code, _, stderr := runIsolated(t, "--theme", "light", "--mouse", "false")
	if code != 2 || !strings.Contains(stderr, "lit-tui needs an interactive terminal") {
		t.Fatalf("valid preferences must reach the terminal check: %d %q", code, stderr)
	}
}

func TestHelpDocumentsThePreferenceFlags(t *testing.T) {
	_, stdout, _ := run(t, "--help")
	for _, want := range []string{"--config FILE", "LIT_TUI_CONFIG", "--theme THEME", "LIT_TUI_THEME", "auto, light or dark",
		"--mouse BOOL", "LIT_TUI_MOUSE", "lit/tui.json", "Ctrl-C"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help lacks %q:\n%s", want, stdout)
		}
	}
}

func key(code rune, mod tea.KeyMod, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: mod, Text: text}
}

func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func withKeymap(t *testing.T, o map[string]map[string][]string) Model {
	t.Helper()
	k, err := keys.New(o)
	if err != nil {
		t.Fatal(err)
	}
	p := prefs.Default()
	p.Keymap = k
	return NewModel("http://127.0.0.1:7411").WithPrefs(p)
}

// Quit follows the keymap; Ctrl-C always quits whatever the keymap says.
func TestModelQuitFollowsTheKeymapAndCtrlCIsReserved(t *testing.T) {
	m := withKeymap(t, map[string]map[string][]string{"global": {"quit": {"x"}}})
	if _, cmd := m.Update(key('q', 0, "q")); quits(cmd) {
		t.Error("q still quits after quit was rebound to x")
	}
	if _, cmd := m.Update(key('x', 0, "x")); !quits(cmd) {
		t.Error("x does not quit")
	}
	if _, cmd := m.Update(key('c', tea.ModCtrl, "")); !quits(cmd) {
		t.Error("ctrl+c does not quit")
	}
	if !strings.Contains(m.View().Content, "x quit") {
		t.Errorf("hint does not name the rebound key: %q", m.View().Content)
	}
}

func TestModelHelpShowsTheEffectiveBindings(t *testing.T) {
	m := withKeymap(t, map[string]map[string][]string{"global": {"help": {"y"}, "quit": {"x"}}})
	if strings.Contains(m.View().Content, "show or hide help") {
		t.Fatal("help shown before it was asked for")
	}
	next, _ := m.Update(key('y', 0, "y"))
	content := next.View().Content
	for _, want := range []string{"y", "show or hide help", "x", "quit", "ctrl+c"} {
		if !strings.Contains(content, want) {
			t.Errorf("help lacks %q:\n%s", want, content)
		}
	}
	closed, _ := next.Update(key(tea.KeyEscape, 0, ""))
	if strings.Contains(closed.View().Content, "show or hide help") {
		t.Error("cancel (esc) does not close help")
	}
	toggled, _ := next.Update(key('y', 0, "y"))
	if strings.Contains(toggled.View().Content, "show or hide help") {
		t.Error("help key does not hide help")
	}
	if _, cmd := next.Update(key('x', 0, "x")); !quits(cmd) {
		t.Error("quit does not work while help is shown")
	}
}

func TestModelMouseIsOffByDefaultAndOptIn(t *testing.T) {
	m := NewModel("http://127.0.0.1:7411")
	if m.View().MouseMode != tea.MouseModeNone || m.Prefs().Mouse || m.Prefs().Theme != prefs.ThemeAuto {
		t.Errorf("default: mouse mode %v, prefs %+v", m.View().MouseMode, m.Prefs())
	}
	p := prefs.Default()
	p.Mouse, p.Theme = true, prefs.ThemeLight
	m = m.WithPrefs(p)
	if m.View().MouseMode != tea.MouseModeCellMotion || m.Prefs().Theme != prefs.ThemeLight {
		t.Errorf("opted in: mouse mode %v, prefs %+v", m.View().MouseMode, m.Prefs())
	}
}
