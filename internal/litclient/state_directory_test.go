package litclient

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStateDirDefaultAndOverride(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("XDG_STATE_HOME", root)
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("LIT_STATE_DIR", "")
	want := filepath.Join(root, "lit")
	if runtime.GOOS == "darwin" {
		want = filepath.Join(root, "Library", "Application Support", "lit")
	}
	got, err := StateDir()
	if err != nil || got != want {
		t.Fatalf("StateDir() = %q, %v; want %q", got, err, want)
	}
	override := filepath.Join(root, "explicit")
	t.Setenv("LIT_STATE_DIR", override)
	got, err = StateDir()
	if err != nil || got != override {
		t.Fatalf("override = %q, %v", got, err)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("path resolution wrote state: %v, %v", entries, err)
	}
}

func TestStateDirPlatforms(t *testing.T) {
	home, base := t.TempDir(), t.TempDir()
	for _, tc := range []struct {
		name, platform, xdg, local, want string
		wantErr                          bool
	}{
		{"linux default", "linux", "", "", filepath.Join(home, ".local", "state", "lit"), false},
		{"linux relative ignored", "linux", "relative", "", filepath.Join(home, ".local", "state", "lit"), false},
		{"linux XDG", "linux", base, "", filepath.Join(base, "lit"), false},
		{"macOS", "darwin", base, base, filepath.Join(home, "Library", "Application Support", "lit"), false},
		{"Windows", "windows", base, base, filepath.Join(base, "lit"), false},
		{"Windows missing local data", "windows", base, "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stateDir(tc.platform, home, tc.xdg, tc.local)
			if got != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("stateDir() = %q, %v; want %q, error %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}
