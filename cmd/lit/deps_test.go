package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The Charm, Glamour and chroma stack costs package init time and binary size,
// so only lit-tui links it; lit and lit-server must not (see the lit-tui spec).
var tuiStack = []string{"charm.land/", "github.com/charmbracelet/", "github.com/alecthomas/chroma", "github.com/yuin/goldmark"}

func linked(t *testing.T, goos, pkg string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", pkg)
	cmd.Env = append(os.Environ(), "GOOS="+goos, "CGO_ENABLED=0")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s (GOOS=%s): %v", pkg, goos, err)
	}
	return strings.Fields(string(out))
}

func tuiPackages(deps []string) []string {
	found := []string{}
	for _, dep := range deps {
		for _, prefix := range tuiStack {
			if strings.HasPrefix(dep, prefix) {
				found = append(found, dep)
			}
		}
	}
	return found
}

func TestOnlyLitTUILinksTheCharmStack(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command unavailable")
	}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, pkg := range []string{"../lit", "../lit-server"} {
			if pkg == "../lit-server" && goos != "linux" {
				continue
			}
			if found := tuiPackages(linked(t, goos, pkg)); len(found) > 0 {
				t.Errorf("%s (GOOS=%s) links TUI packages: %v", pkg, goos, found)
			}
		}
		// The check would notice: lit-tui does link Bubble Tea.
		if found := tuiPackages(linked(t, goos, "../lit-tui")); !strings.Contains(strings.Join(found, " "), "charm.land/bubbletea/v2") {
			t.Errorf("lit-tui (GOOS=%s) does not link Bubble Tea: %v", goos, found)
		}
	}
}
