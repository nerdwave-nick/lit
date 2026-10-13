package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/nwcli"
)

// renderAllHelp renders --help for the root, every group and every command.
func renderAllHelp(t *testing.T) string {
	t.Helper()
	var all strings.Builder
	grammar.Walk(func(path []string, _ *nwcli.Command) {
		var out, errOut bytes.Buffer
		argv := append(append([]string{}, path...), "--help")
		if code := Run(argv, &out, &errOut); code != 0 || errOut.Len() != 0 {
			t.Errorf("%v: %d %s", argv, code, &errOut)
		}
		all.WriteString("== lit " + strings.Join(argv, " ") + "\n" + out.String())
	})
	return all.String()
}

// TestHelpGolden pins every help page. After an intended help change, review
// the diff and regenerate with LIT_UPDATE_GOLDEN=1.
func TestHelpGolden(t *testing.T) {
	t.Setenv("LIT_STATE_DIR", "/dev/null/no-state")
	golden := filepath.Join("testdata", "help.golden")
	got := renderAllHelp(t)
	if os.Getenv("LIT_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		gotLines, wantLines := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gotLines) && i < len(wantLines); i++ {
			if gotLines[i] != wantLines[i] {
				t.Fatalf("help differs at line %d:\n got: %q\nwant: %q", i+1, gotLines[i], wantLines[i])
			}
		}
		t.Fatalf("help differs in length: %d vs %d lines", len(gotLines), len(wantLines))
	}
}
