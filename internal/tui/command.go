package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/clientendpoint"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/nwcli"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/prefs"
	"golang.org/x/term"
)

// command is lit-tui's command line. lit tui passes only --endpoint through;
// the preferences reach lit-tui through LIT_TUI_* or the config file.
var command = &nwcli.Command{
	Name:    "lit-tui",
	Summary: "Browse lit in a terminal UI",
	Description: `Open the terminal UI for people using lit. It needs an interactive terminal:
stdin and stdout must be terminals. lit tui, or bare lit on a terminal, starts it.
Endpoint precedence: --endpoint > LIT_ENDPOINT > ` + clientendpoint.Default + `.

Preferences precedence: flags > environment > JSON config > defaults.
The config file defaults to ${XDG_CONFIG_HOME:-$HOME/.config}/lit/tui.json and
needs schema_version: 1; unknown fields are rejected. LIT_TUI_CONFIG or --config
explicitly selects a file that must exist. Its keymap section rebinds actions;
invalid preferences fail at startup with status 2. Changes apply on the next launch.
Quit with q (rebindable) or Ctrl-C, which is reserved and cannot be rebound.`,
	Example: "  lit-tui\n  lit-tui --endpoint http://127.0.0.1:7411\n  lit-tui --theme light --mouse true\n  lit-tui --config /path/to/tui.json",
	Flags: []nwcli.Flag{
		{Name: "endpoint", Usage: "Optional service `URL` (or LIT_ENDPOINT; default " + clientendpoint.Default + ")"},
		{Name: "config", Usage: "Optional JSON preferences `FILE` (or " + prefs.EnvConfig + ")", Value: nwcli.Value{Kind: nwcli.Path}},
		{Name: "theme", Usage: "Optional `THEME`: auto, light or dark (or " + prefs.EnvTheme + "; default auto, which detects the background)",
			Value: nwcli.Value{Kind: nwcli.Choice, Choices: []string{"auto", "light", "dark"}}},
		{Name: "mouse", Usage: "Optional `BOOL`: true enables click-to-select and wheel scrolling (or " + prefs.EnvMouse + "; default false)",
			Value: nwcli.Value{Kind: nwcli.Choice, Choices: []string{"true", "false"}}},
		{Name: "version", Short: "v", Switch: true, Usage: "version for lit-tui"},
	},
}

// Main runs lit-tui and returns its exit status. Help, version and argument
// errors work without a terminal; the UI itself refuses to start without one.
func Main(args []string, stdin, stdout *os.File, stderr io.Writer) int {
	p, err := command.Parse(args)
	switch {
	case err != nil:
		fmt.Fprintln(stderr, "lit-tui:", err)
		return 2
	case p.Help:
		command.WriteHelp(stdout)
		return 0
	case len(p.Flags["version"]) > 0:
		fmt.Fprintln(stdout, protocol.ReleaseVersion)
		return 0
	}
	flag := p.Flags["endpoint"]
	explicit := ""
	if len(flag) > 0 {
		explicit = flag[0]
	}
	endpoint, err := clientendpoint.Resolve(explicit, len(flag) > 0, os.Getenv("LIT_ENDPOINT"), "")
	if err != nil {
		fmt.Fprintln(stderr, "lit-tui: invalid endpoint:", err)
		return 2
	}
	pref, err := prefs.Load(prefs.Flags{Config: one(p.Flags["config"]), Theme: one(p.Flags["theme"]), Mouse: one(p.Flags["mouse"])})
	if err != nil {
		for _, line := range strings.Split(err.Error(), "\n") {
			fmt.Fprintln(stderr, "lit-tui:", line)
		}
		return 2
	}
	if !term.IsTerminal(int(stdin.Fd())) || !term.IsTerminal(int(stdout.Fd())) {
		fmt.Fprintln(stderr, "lit-tui needs an interactive terminal: stdin and stdout must be terminals")
		return 2
	}
	err = Run(context.Background(), NewModel(endpoint).WithPrefs(pref), tea.WithInput(stdin), tea.WithOutput(stdout))
	switch {
	case err == nil:
		return 0
	case errors.Is(err, tea.ErrInterrupted):
		return 130
	}
	fmt.Fprintln(stderr, "lit-tui:", err)
	return 1
}

// one returns a flag's single value, or nil when it was not given.
func one(values []string) *string {
	if len(values) == 0 {
		return nil
	}
	return &values[0]
}
