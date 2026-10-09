//go:build windows

package cli

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
)

// launchTUI runs lit-tui as a child on the same console and returns its exit
// code. Windows has no process replacement. The console delivers Ctrl-C to
// both processes; lit ignores it while it waits, so the TUI alone handles it.
func launchTUI(path string, args []string, out, errOut io.Writer) (int, error) {
	cmd := exec.Command(path, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, out, errOut
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	return 0, err
}
