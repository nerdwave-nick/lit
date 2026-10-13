package storage

import (
	"os/exec"
	"strings"
	"testing"
)

// modernc.org/sqlite is generated against one exact modernc.org/libc; another
// libc version can corrupt memory at run time, so the module must select
// exactly the version the driver requires.
func TestModuleSelectsTheDriversLibc(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command unavailable")
	}
	run := func(args ...string) string {
		out, err := exec.Command("go", args...).Output()
		if err != nil {
			t.Fatalf("go %s: %v", strings.Join(args, " "), err)
		}
		return string(out)
	}
	selected := strings.Fields(run("list", "-m", "-f", "{{.Path}}@{{.Version}}", "modernc.org/sqlite", "modernc.org/libc"))
	if len(selected) != 2 {
		t.Fatalf("selected modules %q", selected)
	}
	required := ""
	for _, line := range strings.Split(run("mod", "graph"), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == selected[0] && strings.HasPrefix(f[1], "modernc.org/libc@") {
			required = f[1]
		}
	}
	if required != selected[1] {
		t.Fatalf("%s requires %q but the module selects %s; pin the driver's libc version in go.mod", selected[0], required, selected[1])
	}
}
