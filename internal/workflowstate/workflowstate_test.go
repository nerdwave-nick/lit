package workflowstate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPlatformStateDefaults(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	for _, tc := range []struct{ platform, xdg, local, want string }{{"linux", "relative", "", filepath.Join(home, ".local", "state", "lit", "local")}, {"linux", home, "", filepath.Join(home, "lit", "local")}, {"darwin", "", "", filepath.Join(home, "Library", "Application Support", "lit", "local")}, {"windows", "", home, filepath.Join(home, "lit", "local")}} {
		got, e := platformDefault(tc.platform, home, tc.xdg, tc.local)
		if e != nil || got != tc.want {
			t.Fatal(got, e, tc)
		}
	}
	if _, e := platformDefault("windows", home, "", ""); e == nil {
		t.Fatal("missing LOCALAPPDATA accepted")
	}
}

func TestDirHonorsTheOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	absolute := filepath.Join(t.TempDir(), "state")
	for _, tc := range []struct{ env, want string }{
		{absolute, absolute},
		{"~/state", filepath.Join(home, "state")},
		{"~", home},
	} {
		t.Setenv("LIT_WORKFLOW_STATE_DIR", tc.env)
		if got, err := Dir(); err != nil || got != tc.want {
			t.Errorf("%q: %q %v, want %q", tc.env, got, err, tc.want)
		}
	}
	t.Setenv("LIT_WORKFLOW_STATE_DIR", "")
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "xdg"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "local"))
	want, err := platformDefault(runtime.GOOS, home, filepath.Join(home, "xdg"), filepath.Join(home, "local"))
	if got, e := Dir(); err != nil || e != nil || got != want {
		t.Errorf("default: %q %v, want %q %v", got, e, want, err)
	}
}

func TestTildeExpansion(t *testing.T) {
	home, e := os.UserHomeDir()
	if e != nil {
		t.Fatal(e)
	}
	for input, want := range map[string]string{"~": home, "~/work": filepath.Join(home, "work")} {
		got, e := Expand(input)
		if e != nil || got != want {
			t.Fatalf("%q got %q want %q: %v", input, got, want, e)
		}
	}
}
