// Package cells measures, clips and cuts styled terminal text by display
// cells, the same way Bubble Tea's renderer lays out the screen.
//
// Bubble Tea measures with wcwidth (ansi.WcWidth) unless the terminal
// reports grapheme-cluster support (mode 2027), after which it switches to
// ansi.GraphemeWidth. Callers track that switch with MethodForUnicodeCore and
// pass the current method to every helper here.
//
// x/ansi's own WcWidth.Truncate and WcWidth.Cut are wrong (truncate.go,
// v0.11.8 and still in v0.11.9): Truncate returns its input unchanged
// whenever it fits by grapheme width, and Cut truncates from the right twice,
// so WcWidth.Cut("abcdefghij", 3, 6) is "abc". Clip and Cut here are correct
// for both methods.
package cells

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Method selects how cell widths are measured.
type Method = ansi.Method

// Default is the method Bubble Tea uses until a terminal reports mode 2027.
const Default = ansi.WcWidth

// MethodForUnicodeCore returns the method Bubble Tea's renderer switches to
// for a mode 2027 (ansi.ModeUnicodeCore) report value, mirroring tea.go:
// a recognised mode (set or reset) means grapheme clustering is supported.
func MethodForUnicodeCore(v ansi.ModeSetting) Method {
	if v == ansi.ModeReset || v == ansi.ModeSet || v == ansi.ModePermanentlySet {
		return ansi.GraphemeWidth
	}
	return ansi.WcWidth
}

// Width returns the display width of s in cells under m, ignoring escape
// sequences.
func Width(m Method, s string) int { return m.StringWidth(s) }

// Clip returns s limited to n cells. A string that fits is returned
// unchanged; otherwise it is cut and tail (for example an overflow marker)
// is appended so the result is exactly n cells wide. Escape sequences after
// the cut are kept, so styles and hyperlinks are still closed.
func Clip(m Method, s string, n int, tail string) string {
	if n <= 0 {
		return Cut(m, s, 0, 0)
	}
	if m.StringWidth(s) <= n {
		return s
	}
	tw := m.StringWidth(tail)
	if tw > n {
		return Cut(m, s, 0, n)
	}
	return Cut(m, s, 0, n-tw) + tail
}

// Cut returns the cells [left, right) of s. A wide grapheme split by either
// edge is replaced by spaces for its visible part, so the result is exactly
// min(right, Width(s)) - left cells wide and columns stay aligned when
// scrolling horizontally. Every escape sequence in s is kept, wherever it
// is, so styles and hyperlinks stay balanced.
func Cut(m Method, s string, left, right int) string {
	left = max(left, 0)
	var b strings.Builder
	b.Grow(len(s))
	var state byte
	col := 0
	for len(s) > 0 {
		seq, w, n, next := m.DecodeSequenceInString(s, state, nil)
		state = next
		s = s[n:]
		if w == 0 {
			if seq != "" && seq[0] == 0x1b || col >= left && col < right {
				b.WriteString(seq) // escape sequences anywhere; zero-width text when visible
			}
			continue
		}
		start, end := col, col+w
		col = end
		switch {
		case end <= left || start >= right:
		case start >= left && end <= right:
			b.WriteString(seq)
		default:
			b.WriteString(strings.Repeat(" ", min(end, right)-max(start, left)))
		}
	}
	return b.String()
}
