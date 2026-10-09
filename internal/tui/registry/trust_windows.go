//go:build windows

package registry

import (
	"path/filepath"
	"strings"
)

// lockTrust distrusts state directories on UNC network shares. Mapped network
// drives are not detected (verification of Windows behavior is T16's).
func lockTrust(dir string) (bool, string) {
	if abs, err := filepath.Abs(dir); err == nil && strings.HasPrefix(filepath.VolumeName(abs), `\\`) {
		return false, "a network share"
	}
	return true, ""
}
