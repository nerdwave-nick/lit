//go:build unix

package hook

import (
	"os/exec"
	"syscall"
)

// ownProcessGroup lets a timed out command be killed together with everything
// it started, so that a stuck status line cannot pile up processes.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
