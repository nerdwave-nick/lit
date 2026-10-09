package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/clientendpoint"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/nwcli"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"golang.org/x/term"
)

// command is lit-tui's command line. lit tui passes its flags through.
var command = &nwcli.Command{
	Name:    "lit-tui",
	Summary: "Browse lit in a terminal UI",
	Description: `Open the terminal UI for people using lit. It needs an interactive terminal:
stdin and stdout must be terminals. lit tui, or bare lit on a terminal, starts it.
Endpoint precedence: --endpoint > LIT_ENDPOINT > ` + clientendpoint.Default + `.
Quit with q or Ctrl-C.`,
	Example: "  lit-tui\n  lit-tui --endpoint http://127.0.0.1:7411",
	Flags: []nwcli.Flag{
		{Name: "endpoint", Usage: "Optional service `URL` (or LIT_ENDPOINT; default " + clientendpoint.Default + ")"},
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
	if !term.IsTerminal(int(stdin.Fd())) || !term.IsTerminal(int(stdout.Fd())) {
		fmt.Fprintln(stderr, "lit-tui needs an interactive terminal: stdin and stdout must be terminals")
		return 2
	}
	err = Run(context.Background(), NewModel(endpoint), tea.WithInput(stdin), tea.WithOutput(stdout))
	switch {
	case err == nil:
		return 0
	case errors.Is(err, tea.ErrInterrupted):
		return 130
	}
	fmt.Fprintln(stderr, "lit-tui:", err)
	return 1
}
