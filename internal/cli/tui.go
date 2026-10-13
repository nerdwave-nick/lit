package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/nerdwave-nick/lit/internal/nwcli"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"golang.org/x/term"
)

// The terminal UI is a separate executable, so that only it links the Charm
// stack. lit tui, and bare lit on a terminal, hand the terminal over to it.
const (
	tuiBinary  = "lit-tui"
	tuiInstall = "go install github.com/nerdwave-nick/lit/cmd/lit-tui@latest"
)

// tuiFlags are the lit flags passed through to lit-tui.
var tuiFlags = []string{"endpoint"}

// runTUI launches lit-tui with the parsed tui command's flags. On Unix it
// replaces this process and returns only when that fails.
func runTUI(argv []string, p *nwcli.Parsed, out, errOut io.Writer) int {
	fail := func(e *protocol.Error) int {
		format := offlineFormat(argv)
		if format == "" {
			format = "json"
		}
		return (&App{Out: out, Err: errOut, Format: format}).Error(e)
	}
	executable, err := os.Executable()
	if err != nil {
		executable = ""
	}
	path, err := findTUI(executable)
	if err != nil {
		return fail(protocol.E(500, "tui_not_found", err.Error()))
	}
	code, err := launchTUI(path, flagArgs(p, tuiFlags...), out, errOut)
	if err != nil {
		return fail(protocol.E(500, "tui_launch_failed", fmt.Sprintf("could not start %s: %s", path, err)))
	}
	return code
}

// bareTUI reports whether bare lit (no command) should launch lit-tui and
// returns its parse: only when stdin and stdout are terminals and the
// arguments are valid for lit tui. Scripts and agents keep the usage error.
func bareTUI(argv []string, out io.Writer) (*nwcli.Parsed, bool) {
	if !isTerminal(os.Stdin) || !isTerminal(out) {
		return nil, false
	}
	p, err := grammar.Parse(append([]string{"tui"}, argv...))
	return p, err == nil && !p.Help
}

func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// findTUI locates lit-tui: next to the lit executable first, then on PATH.
func findTUI(executable string) (string, error) {
	name := tuiBinary
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dir := ""
	if executable != "" {
		dir = filepath.Dir(executable)
		if sibling := filepath.Join(dir, name); isExecutable(sibling) {
			return sibling, nil
		}
	}
	if path, err := exec.LookPath(tuiBinary); err == nil {
		return path, nil
	} else if !errors.Is(err, exec.ErrNotFound) && !errors.Is(err, exec.ErrDot) {
		return "", fmt.Errorf("cannot look up %s on PATH: %w", tuiBinary, err)
	}
	where := "on PATH"
	if dir != "" {
		where = "next to lit in " + dir + " or on PATH"
	}
	return "", fmt.Errorf("the terminal UI %s was not found %s; install it with: %s", name, where, tuiInstall)
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0
}
