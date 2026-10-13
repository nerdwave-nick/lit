package main

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// legacyPackages hold the legacy file store's code. Only lit-migrate, the
// one-time setup import, contains a frozen copy; lit-server must never link
// it, nor a revived internal/store (see the SQLite storage specification).
var legacyPackages = []string{
	"github.com/nerdwave-nick/nerdwave-workflows/cmd/lit-migrate/internal/legacy",
	"github.com/nerdwave-nick/nerdwave-workflows/cmd/lit-migrate/internal/legacystore",
	"github.com/nerdwave-nick/nerdwave-workflows/internal/store",
}

func deps(t *testing.T, pkg string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", pkg)
	cmd.Env = append(os.Environ(), "GOOS=linux", "CGO_ENABLED=0")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", pkg, err)
	}
	return strings.Fields(string(out))
}

func TestLitServerDoesNotLinkTheLegacyStore(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command unavailable")
	}
	for _, dep := range deps(t, ".") {
		if slices.Contains(legacyPackages, dep) || strings.HasPrefix(dep, "github.com/nerdwave-nick/nerdwave-workflows/cmd/lit-migrate") {
			t.Errorf("lit-server links legacy package %s", dep)
		}
	}
	// The check would notice: lit-migrate does link the frozen copy.
	migrate := deps(t, "../lit-migrate")
	for _, pkg := range legacyPackages[:2] {
		if !slices.Contains(migrate, pkg) {
			t.Errorf("lit-migrate does not link %s", pkg)
		}
	}
}
