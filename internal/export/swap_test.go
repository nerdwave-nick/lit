package export

import (
	"os"
	"path/filepath"
	"testing"
)

// The rename fallback must swap both trees and leave nothing beside them,
// as on a file system without an atomic exchange.
func TestSwapByRenamesExchangesTheDirectories(t *testing.T) {
	parent := t.TempDir()
	a, b := filepath.Join(parent, "staging"), filepath.Join(parent, "export")
	for dir, name := range map[string]string{a: "new.md", b: "old.md"} {
		if e := os.MkdirAll(dir, 0o755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(dir, name), nil, 0o644); e != nil {
			t.Fatal(e)
		}
	}
	if e := swapByRenames(a, b); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(b, "new.md")); e != nil {
		t.Fatalf("export does not hold the new tree: %v", e)
	}
	if _, e := os.Stat(filepath.Join(a, "old.md")); e != nil {
		t.Fatalf("staging does not hold the previous tree: %v", e)
	}
	if entries, _ := os.ReadDir(parent); len(entries) != 2 {
		t.Fatalf("left beside the export: %v", entries)
	}
}
