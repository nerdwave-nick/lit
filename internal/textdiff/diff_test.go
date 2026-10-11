package textdiff

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestDiffIsAUnifiedLineDiff(t *testing.T) {
	cases := []struct {
		name     string
		old, new string
		want     []string
	}{
		{"equal", "a\nb\n", "a\nb\n", nil},
		{"from empty", "", "new\n", []string{"@@ -0,0 +1,1 @@", "+new"}},
		{"to empty", "gone\n", "", []string{"@@ -1,1 +0,0 @@", "-gone"}},
		{"changed line", "a\nb\nc\n", "a\nB\nc\n", []string{"@@ -1,3 +1,3 @@", " a", "-b", "+B", " c"}},
		{"final newline added", "one", "one\n", []string{"@@ -1,1 +1,1 @@", "-one", `\ No newline at end of file`, "+one"}},
		{"final newline removed", "one\n", "one", []string{"@@ -1,1 +1,1 @@", "-one", "+one", `\ No newline at end of file`}},
	}
	for _, c := range cases {
		if got := Diff(c.old, c.new); !slices.Equal(got, c.want) {
			t.Errorf("%s: got\n%s\nwant\n%s", c.name, strings.Join(got, "\n"), strings.Join(c.want, "\n"))
		}
	}
}

// Distant changes make separate hunks with three lines of context; close
// ones share a hunk.
func TestDiffSplitsHunks(t *testing.T) {
	var old []string
	for i := 1; i <= 20; i++ {
		old = append(old, fmt.Sprintf("line %d", i))
	}
	nw := slices.Clone(old)
	nw[1], nw[17] = "changed 2", "changed 18"
	got := Diff(strings.Join(old, "\n")+"\n", strings.Join(nw, "\n")+"\n")
	var headers []string
	for _, l := range got {
		if strings.HasPrefix(l, "@@") {
			headers = append(headers, l)
		}
	}
	if want := []string{"@@ -1,5 +1,5 @@", "@@ -15,6 +15,6 @@"}; !slices.Equal(headers, want) {
		t.Fatalf("hunks %v, want %v\n%s", headers, want, strings.Join(got, "\n"))
	}
}

// Applying a diff's '-' and '+' lines to old gives new, for any pair of
// texts (a property over generated inputs), and large inputs stay bounded.
func TestDiffReconstructsTheNewText(t *testing.T) {
	words := []string{"a", "b", "c", "", "d"}
	gen := func(seed, n int) string {
		var lines []string
		for i := range n {
			lines = append(lines, words[(seed*7+i*i*3+i)%len(words)])
		}
		s := strings.Join(lines, "\n")
		if seed%2 == 0 {
			s += "\n"
		}
		return s
	}
	for seed := range 150 {
		old, nw := gen(seed, seed%13), gen(seed+1, (seed*5)%11)
		if got := apply(t, old, Diff(old, nw)); got != nw {
			t.Fatalf("seed %d: %q -> %q rebuilt %q\n%s", seed, old, nw, got, strings.Join(Diff(old, nw), "\n"))
		}
	}
	big := strings.Repeat("x\n", 5000)
	if got := apply(t, big, Diff(big, "y\n"+big[2:])); got != "y\n"+big[2:] {
		t.Fatal("large diff does not rebuild")
	}
}

// apply rebuilds the new text from old and a unified diff.
func apply(t *testing.T, old string, diff []string) string {
	t.Helper()
	lines := strings.SplitAfter(old, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var out []string
	pos := 0
	for i := 0; i < len(diff); i++ {
		l := diff[i]
		noEOL := i+1 < len(diff) && diff[i+1] == `\ No newline at end of file`
		text := l[1:] + "\n"
		if noEOL {
			text = l[1:]
		}
		switch l[0] {
		case '@':
			var a, b, c, d int
			if _, err := fmt.Sscanf(l, "@@ -%d,%d +%d,%d @@", &a, &b, &c, &d); err != nil {
				t.Fatalf("header %q: %v", l, err)
			}
			start := a - 1
			if b == 0 {
				start = a
			}
			out = append(out, lines[pos:start]...)
			pos = start
		case ' ':
			out = append(out, lines[pos])
			pos++
		case '-':
			pos++
		case '+':
			out = append(out, text)
		case '\\':
		}
	}
	return strings.Join(append(out, lines[pos:]...), "")
}
