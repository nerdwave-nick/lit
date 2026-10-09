package cells

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

var methods = []Method{ansi.WcWidth, ansi.GraphemeWidth}

var samples = []string{
	"abcdefghij",
	"日本語のテキストです",
	"x 👨‍👩‍👧‍👦 y and more text",
	"⚠️ warn 🇩🇪 flag 🧑🏽‍💻 coder",
	"\x1b[31mred 日本\x1b[0m plain \x1b]8;;https://e.org\x07link\x1b]8;;\x07 end",
	"mixed ascii 中文 and emoji ✅ done",
}

func name(m Method) string { return map[Method]string{ansi.WcWidth: "wc", ansi.GraphemeWidth: "grapheme"}[m] }

func TestWidthMatchesRendererMeasure(t *testing.T) {
	for _, m := range methods {
		for _, s := range samples {
			if got, want := Width(m, s), m.StringWidth(s); got != want {
				t.Errorf("%s Width(%q) = %d, want %d", name(m), s, got, want)
			}
		}
	}
}

func TestClipNeverExceedsWidth(t *testing.T) {
	for _, m := range methods {
		for _, s := range samples {
			full := Width(m, s)
			for n := 0; n <= full+2; n++ {
				for _, tail := range []string{"", "…", "→"} {
					got := Clip(m, s, n, tail)
					w := Width(m, got)
					if w > n {
						t.Errorf("%s Clip(%q, %d, %q) = %q has width %d", name(m), s, n, tail, got, w)
					}
					if n >= full && got != s {
						t.Errorf("%s Clip(%q, %d) changed a fitting string to %q", name(m), s, n, got)
					}
					if n < full && n >= Width(m, tail) && w != n {
						t.Errorf("%s Clip(%q, %d, %q) = %q has width %d, want exactly %d", name(m), s, n, tail, got, w, n)
					}
				}
			}
		}
	}
}

func TestCutSelectsColumns(t *testing.T) {
	for _, m := range methods {
		if got := Cut(m, "abcdefghij", 3, 6); got != "def" {
			t.Errorf("%s Cut(abcdefghij, 3, 6) = %q, want def", name(m), got)
		}
		if got := ansi.Strip(Cut(m, "\x1b[31mabcdefghij\x1b[0m", 3, 6)); got != "def" {
			t.Errorf("%s styled Cut = %q, want def", name(m), got)
		}
		// A wide rune split by an edge becomes spaces, so columns stay aligned.
		if got := Cut(m, "a日本語b", 2, 6); got != " 本 " {
			t.Errorf("%s Cut(a日本語b, 2, 6) = %q, want %q", name(m), got, " 本 ")
		}
	}
}

func TestCutWidthIsExactAndKeepsEscapes(t *testing.T) {
	for _, m := range methods {
		for _, s := range samples {
			full := Width(m, s)
			for left := 0; left <= full; left++ {
				for right := left; right <= full+1; right++ {
					got := Cut(m, s, left, right)
					if w, want := Width(m, got), min(right, full)-left; w != want {
						t.Fatalf("%s Cut(%q, %d, %d) = %q width %d, want %d", name(m), s, left, right, got, w, want)
					}
				}
			}
			// Concatenating adjacent cuts restores the visible text.
			var b strings.Builder
			for c := 0; c < full; c += 3 {
				b.WriteString(ansi.Strip(Cut(m, s, c, c+3)))
			}
			if !strings.Contains(s, "👨") && !strings.Contains(s, "日") && !strings.Contains(s, "中") && !strings.Contains(s, "🧑") && !strings.Contains(s, "⚠") {
				if got := b.String(); got != ansi.Strip(s) {
					t.Errorf("%s cuts of %q rejoin to %q", name(m), s, got)
				}
			}
		}
	}
	// SGR and OSC 8 state survives a cut so styling and links stay balanced.
	got := Cut(ansi.WcWidth, "\x1b[31mabc\x1b[0m def", 4, 7)
	if !strings.Contains(got, "\x1b[31m") || !strings.Contains(got, "\x1b[0m") {
		t.Errorf("cut dropped escapes: %q", got)
	}
}

func TestMethodForUnicodeCore(t *testing.T) {
	for _, tc := range []struct {
		v    ansi.ModeSetting
		want Method
	}{
		{ansi.ModeNotRecognized, ansi.WcWidth},
		{ansi.ModePermanentlyReset, ansi.WcWidth},
		{ansi.ModeReset, ansi.GraphemeWidth},
		{ansi.ModeSet, ansi.GraphemeWidth},
		{ansi.ModePermanentlySet, ansi.GraphemeWidth},
	} {
		if got := MethodForUnicodeCore(tc.v); got != tc.want {
			t.Errorf("MethodForUnicodeCore(%v) = %s, want %s", tc.v, name(got), name(tc.want))
		}
	}
}

// TestXAnsiWcWidthBugsStillPresent pins the upstream bugs this package works
// around (x/ansi truncate.go, still present in v0.11.9). When it fails after
// a dependency update, upstream fixed them and the workaround can be revisited.
func TestXAnsiWcWidthBugsStillPresent(t *testing.T) {
	if got := ansi.WcWidth.Cut("abcdefghij", 3, 6); got == "def" {
		t.Errorf("x/ansi WcWidth.Cut is fixed upstream (got %q)", got)
	}
	s := "x 👨‍👩‍👧‍👦 y and more text" // fits 22 cells by grapheme, not by wcwidth
	got := ansi.WcWidth.Truncate(s, 22, "")
	if ansi.WcWidth.StringWidth(got) <= 22 {
		t.Errorf("x/ansi WcWidth.Truncate is fixed upstream (got width %d)", ansi.WcWidth.StringWidth(got))
	}
	t.Log(fmt.Sprintf("x/ansi WcWidth.Cut(abcdefghij,3,6)=%q; WcWidth.Truncate to 22 gives %d cells", ansi.WcWidth.Cut("abcdefghij", 3, 6), ansi.WcWidth.StringWidth(got)))
}
