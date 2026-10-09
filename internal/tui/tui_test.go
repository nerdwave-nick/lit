package tui

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
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

func run(t *testing.T, args ...string) (int, string, string) {
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
