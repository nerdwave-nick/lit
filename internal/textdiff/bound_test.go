package textdiff

import (
	"fmt"
	"strings"
	"testing"
)

const kb64 = 64 << 10

// body64KB is a 64 KB body of distinct lines lineLen bytes long (with their
// line break), ending in a shorter last line without a line break.
func body64KB(lineLen int) string {
	var b strings.Builder
	for i := 0; b.Len()+lineLen < kb64; i++ {
		l := fmt.Sprintf("%d ", i)
		b.WriteString(l + strings.Repeat("x", max(lineLen-1-len(l), 0)) + "\n")
	}
	b.WriteString(strings.Repeat("z", kb64-b.Len()))
	return b.String()
}

// editEnds changes the first and last lines, so that the differing middle
// is the whole text.
func editEnds(s string) string {
	ls := lines(s)
	ls[0] = "first changed\n"
	ls[len(ls)-1] = "last changed" + ls[len(ls)-1][len(strings.TrimSuffix(ls[len(ls)-1], "\n")):]
	return strings.Join(ls, "")
}

func changes(d []string) (removed, added int) {
	for _, l := range d {
		switch {
		case strings.HasPrefix(l, "-"):
			removed++
		case strings.HasPrefix(l, "+"):
			added++
		}
	}
	return removed, added
}

// The LCS table holds at most maxDiffCells = 2^22 cells: a differing middle
// of up to 2047 lines on each side gets a minimal diff, a larger one is
// replaced as a whole. Both stay exact.
func TestDiffLCSBound(t *testing.T) {
	for _, c := range []struct {
		lines       int
		wantChanged int
	}{
		{2047, 2},    // (2047+1)^2 = 2^22 cells: minimal
		{2048, 2048}, // one more line: the whole middle is replaced
	} {
		var b strings.Builder
		for i := range c.lines {
			fmt.Fprintf(&b, "line %d\n", i)
		}
		old := b.String()
		nw := editEnds(old)
		d := Diff(old, nw)
		if r, a := changes(d); r != c.wantChanged || a != c.wantChanged {
			t.Errorf("%d lines: -%d +%d, want -%d +%d", c.lines, r, a, c.wantChanged, c.wantChanged)
		}
		if got, err := Apply(d, old); err != nil || got != nw {
			t.Errorf("%d lines: Apply = %v", c.lines, err)
		}
	}
}

// A 64 KB body whose lines average 33 bytes or more always gets a minimal
// diff; shorter lines fall back to replacing the differing middle, which
// still applies exactly.
func TestDiff64KBBodies(t *testing.T) {
	for _, c := range []struct {
		lineLen int
		minimal bool
	}{
		{8, false}, {32, false}, {33, true}, {48, true}, {80, true},
	} {
		old := body64KB(c.lineLen)
		nw := editEnds(old)
		if len(old) != kb64 {
			t.Fatalf("body is %d bytes", len(old))
		}
		d := Diff(old, nw)
		r, a := changes(d)
		if minimal := r == 2 && a == 2; minimal != c.minimal {
			t.Errorf("%d-byte lines (%d lines): -%d +%d, minimal %v, want %v", c.lineLen, len(lines(old)), r, a, minimal, c.minimal)
		}
		if got, err := Apply(d, old); err != nil || got != nw {
			t.Errorf("%d-byte lines: Apply = %v", c.lineLen, err)
		}
	}
	one := strings.Repeat("y", kb64) // a single line without a line break
	if got, err := Apply(Diff(one, one[1:]), one); err != nil || got != one[1:] {
		t.Errorf("one 64 KB line: Apply = %v", err)
	}
}

// go test -run '^$' -bench Diff64KB -benchmem ./internal/textdiff
func BenchmarkDiff64KB(b *testing.B) {
	for _, lineLen := range []int{8, 33, 48, 80} {
		old := body64KB(lineLen)
		nw := editEnds(old)
		b.Run(fmt.Sprintf("Diff/%d-byte-lines", lineLen), func(b *testing.B) {
			for b.Loop() {
				Diff(old, nw)
			}
		})
		d := Diff(old, nw)
		b.Run(fmt.Sprintf("Apply/%d-byte-lines", lineLen), func(b *testing.B) {
			for b.Loop() {
				if _, err := Apply(d, old); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
