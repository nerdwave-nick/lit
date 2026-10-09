//go:build !windows

package cli

import (
	"io"
	"os"
	"syscall"
)

// launchTUI replaces this process with lit-tui, so that signals and the
// terminal belong to the TUI directly. It returns only if the exec fails.
func launchTUI(path string, args []string, _, _ io.Writer) (int, error) {
	return 0, syscall.Exec(path, append([]string{path}, args...), os.Environ())
}
