// Package workflowstate locates the machine-local workflow state directory.
// The workflow-session adapter and the checkout preferences keep their records
// in separate subdirectories of it; neither reads the other's.
package workflowstate

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Dir returns the workflow state directory: LIT_WORKFLOW_STATE_DIR (a leading ~
// and relative paths are expanded), otherwise the platform default under the
// user's state location. It creates nothing.
func Dir() (string, error) {
	if p := os.Getenv("LIT_WORKFLOW_STATE_DIR"); p != "" {
		return Expand(p)
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	return platformDefault(runtime.GOOS, home, os.Getenv("XDG_STATE_HOME"), os.Getenv("LOCALAPPDATA"))
}

// Expand resolves a leading ~ to the home directory and makes the path absolute.
func Expand(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		h, e := os.UserHomeDir()
		if e != nil {
			return "", e
		}
		if path == "~" {
			path = h
		} else {
			path = filepath.Join(h, strings.TrimPrefix(path, "~/"))
		}
	}
	return filepath.Abs(path)
}

func platformDefault(platform, home, xdg, local string) (string, error) {
	switch platform {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "lit", "local"), nil
	case "windows":
		if local == "" {
			return "", errors.New("LOCALAPPDATA is required on Windows")
		}
		return filepath.Join(local, "lit", "local"), nil
	default:
		if !filepath.IsAbs(xdg) {
			xdg = filepath.Join(home, ".local", "state")
		}
		return filepath.Join(xdg, "lit", "local"), nil
	}
}
