package workflowskills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallThroughMacOSSystemTemporaryPaths(t *testing.T) {
	for _, base := range []string{"/tmp", "/var/tmp"} {
		t.Run(base, func(t *testing.T) {
			parent, err := os.MkdirTemp(base, "lit-macos-install-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(parent) })
			t.Setenv("TMPDIR", parent)
			for range 2 {
				if err := Install(Bundle(), parent, []string{"codex", "claude"}); err != nil {
					t.Fatal(err)
				}
			}
			for _, host := range []string{"codex", "claude"} {
				if _, err := os.Stat(filepath.Join(parent, "."+host, "skills", "lit", "SKILL.md")); err != nil {
					t.Fatal(err)
				}
			}
			// A user-created alias remains forbidden, even below a system alias.
			alias := filepath.Join(parent, "redirect")
			if err := os.Symlink(parent, alias); err != nil {
				t.Fatal(err)
			}
			if err := Install(Bundle(), alias, []string{"codex"}); err == nil {
				t.Fatal("accepted a user-created symlink")
			}
		})
	}
}
