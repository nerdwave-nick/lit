package hook

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// script writes an executable shell script and returns its path.
func script(t *testing.T, text string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the original status line command runs through sh")
	}
	path := filepath.Join(t.TempDir(), "original.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+text+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func quickOriginal(t *testing.T, timeout time.Duration) {
	t.Helper()
	old := originalTimeout
	originalTimeout = timeout
	t.Cleanup(func() { originalTimeout = old })
}

const litAlone = "🔥 lit | feat/tracker | claims:2 | impl"

func workingRig(t *testing.T) (*statusRig, []byte) {
	g := newStatusRig(t, nil)
	g.start()
	g.work(2, "impl")
	return g, body(t, "statusline-session.json", nil)
}

func TestStatusLineWithOriginalPutsLitAfterIt(t *testing.T) {
	g, in := workingRig(t)
	dump := filepath.Join(t.TempDir(), "stdin")
	original := script(t, `cat > "$1"; printf 'my line (%s)\n' "$COLUMNS"`)
	s := g.statusline(in, 200, Options{Original: original + " " + dump})
	// 200 columns, minus lit's smallest part ("🔥 c:2 | impl", 13), " | " and two spare.
	if want := "my line (182) | " + litAlone; s.Out != want || s.Code != 0 || s.Err != "" {
		t.Fatalf("\n got %+v\nwant %q", s, want)
	}
	if seen, _ := os.ReadFile(dump); string(seen) != string(in) {
		t.Fatalf("the original must get the host's input as it was:\n got %q\nwant %q", seen, in)
	}
	noFailures(t)
}

func TestStatusLineWithOriginalKeepsItsOwnEnvironmentAndUnknownWidth(t *testing.T) {
	g, in := workingRig(t)
	t.Setenv("LIT_TEST_MARKER", "kept")
	original := script(t, `printf '%s/%s' "$LIT_TEST_MARKER" "${COLUMNS-unset}"`)
	if s := g.statusline(in, 0, Options{Original: original}); s.Out != "kept/ | "+litAlone {
		t.Fatalf("the width is only handed down when the host gave one: %q", s.Out)
	}
}

func TestStatusLineWithOriginalShortensLitBeforeTheOriginal(t *testing.T) {
	g, in := workingRig(t)
	original := script(t, `printf 'xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx'`) // 40 columns, whatever COLUMNS says
	const x = "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	for cols, want := range map[int]string{
		200: x + " | " + litAlone,
		100: x + " | " + litAlone, // 40 + 3 + 39 = 82 fits in 98
		80:  x + " | 🔥 feat/tracker | c:2 | impl",
		60:  x + " | 🔥 c:2 | impl",
		30:  x + " | 🔥 c:2 | impl", // the original is never cut
	} {
		if s := g.statusline(in, cols, Options{Original: original}); s.Out != want {
			t.Errorf("%d columns:\n got %q\nwant %q", cols, s.Out, want)
		}
	}
}

func TestStatusLineWithOriginalPassesTheRemainingWidth(t *testing.T) {
	g, in := workingRig(t)
	original := script(t, `printf '%s' "$COLUMNS"`)
	for cols, want := range map[int]string{100: "82", 50: "32", 10: "1"} {
		s := g.statusline(in, cols, Options{Original: original})
		if !strings.HasPrefix(s.Out, want+" | ") {
			t.Errorf("%d columns: the original should get %s: %q", cols, want, s.Out)
		}
	}
}

func TestStatusLineWithOriginalAddsLitToTheLastLine(t *testing.T) {
	g, in := workingRig(t)
	original := script(t, `printf '\033[32mfirst\033[0m\nsecond \033[1mbold\033[0m\n\n'`)
	want := "\x1b[32mfirst\x1b[0m\nsecond \x1b[1mbold\x1b[0m | " + litAlone
	if s := g.statusline(in, 200, Options{Original: original}); s.Out != want {
		t.Fatalf("\n got %q\nwant %q", s.Out, want)
	}
	// Escape sequences take no room: "second bold" is 11 columns.
	if s := g.statusline(in, 11+3+39+2, Options{Original: original}); !strings.HasSuffix(s.Out, "second \x1b[1mbold\x1b[0m | "+litAlone) {
		t.Fatalf("%q", s.Out)
	}
}

func TestStatusLineWithOriginalWhenLitIsDownOrPending(t *testing.T) {
	g, in := workingRig(t)
	g.server.Close()
	original := script(t, `printf 'mine'`)
	if s := g.statusline(in, 100, Options{Original: original}); s.Out != "mine | ⚫ not lit" {
		t.Fatalf("%q", s.Out)
	}
}

func TestStatusLineShowsLitAloneWhenTheOriginalFails(t *testing.T) {
	quickOriginal(t, 300*time.Millisecond)
	g, in := workingRig(t)
	marker := filepath.Join(t.TempDir(), "survivor")
	for name, original := range map[string]string{
		"exit status":        script(t, `echo partial; exit 3`),
		"no output":          script(t, `true`),
		"blank output":       script(t, `printf ' \n\n'`),
		"missing command":    "/nonexistent/original-status-line",
		"killed by a signal": script(t, `echo partial; kill -9 $$`),
		"too slow":           script(t, `echo late; sleep 5`),
		"slow with children": script(t, `(sleep 1; touch "$1") & sleep 5`) + " " + marker,
	} {
		start := time.Now()
		s := g.statusline(in, 200, Options{Original: original})
		if s.Out != litAlone || s.Code != 0 || s.Err != "" {
			t.Errorf("%s:\n got %+v\nwant %q", name, s, litAlone)
		}
		if took := time.Since(start); took > 1500*time.Millisecond {
			t.Errorf("%s: took %s", name, took)
		}
	}
	time.Sleep(1200 * time.Millisecond)
	if fileExists(marker) {
		t.Error("a timed out original left its children running")
	}
	noFailures(t)
}

func TestStatusLineLitAloneStillFitsTheTerminal(t *testing.T) {
	g, in := workingRig(t)
	original := script(t, `exit 1`)
	for cols, want := range map[int]string{100: litAlone, 36: "🔥 feat/tracker | c:2 | impl", 20: "🔥 c:2 | impl"} {
		if s := g.statusline(in, cols, Options{Original: original}); s.Out != want {
			t.Errorf("%d columns:\n got %q\nwant %q", cols, s.Out, want)
		}
	}
}
