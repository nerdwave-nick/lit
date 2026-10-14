//go:build !unix

package hook

import "os/exec"

// ownProcessGroup does nothing where process groups are not available.
func ownProcessGroup(*exec.Cmd) {}
