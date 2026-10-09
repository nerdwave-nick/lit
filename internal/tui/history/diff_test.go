package history

import (
	"strings"
	"testing"
)

// script renders edits as one line each: "  kept", "- removed", "+ added"
// and "…" for an elided run.
func script(es []edit) string {
	var out []string
	for _, e := range es {
		out = append(out, map[kind]string{kept: "  ", removed: "- ", added: "+ ", gap: "…"}[e.kind]+e.text)
	}
	return strings.Join(out, "\n")
}

func TestDiffLinesIsAShortestLineEditScript(t *testing.T) {
	for _, tc := range []struct{ name, before, after, want string }{
		{"created", "", "a\nb", "+ a\n+ b"},
		{"cleared", "a\nb", "", "- a\n- b"},
		{"appended", "a", "a\nb", "  a\n+ b"},
		{"changed middle", "a\nb\nc", "a\nB\nc", "  a\n- b\n+ B\n  c"},
		{"two changes", "a\nb\nc\nd\ne", "x\nb\nc\nd\ny", "- a\n+ x\n  b\n  c\n  d\n- e\n+ y"},
		{"moved line", "a\nb\nc", "b\nc\na", "- a\n  b\n  c\n+ a"},
		{"inserted inside", "a\nc", "a\nb\nc", "  a\n+ b\n  c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := script(diffLines(tc.before, tc.after)); got != tc.want {
				t.Errorf("got\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

// Every edit script turns before into after: kept and removed lines are
// before, kept and added lines are after.
func TestDiffLinesReconstructsBothSides(t *testing.T) {
	pairs := [][2]string{{"", ""}, {"x", "x"}, {"a\nb\nc\nd", "d\nc\nb\na"}, {"one\ntwo\n", "one\nthree\ntwo\n"}, {"\n\n", "\n"}}
	for _, p := range pairs {
		var before, after []string
		for _, e := range diffLines(p[0], p[1]) {
			if e.kind != added {
				before = append(before, e.text)
			}
			if e.kind != removed {
				after = append(after, e.text)
			}
		}
		if strings.Join(before, "\n") != p[0] || strings.Join(after, "\n") != p[1] {
			t.Errorf("%q → %q: sides %q, %q", p[0], p[1], before, after)
		}
	}
}

// Above the cell bound the middle is shown as removed then added.
func TestDiffLinesFallsBackForHugeMiddles(t *testing.T) {
	n := 1100 // 1100² > MaxDiffCells
	a, b := make([]string, n), make([]string, n)
	for i := range a {
		a[i], b[i] = "a"+strings.Repeat("x", i%7), "b"+strings.Repeat("y", i%5)
	}
	es := diffLines("same\n"+strings.Join(a, "\n"), "same\n"+strings.Join(b, "\n"))
	if len(es) != 1+2*n || es[0].kind != kept || es[1].kind != removed || es[n].kind != removed || es[n+1].kind != added {
		t.Fatalf("fallback script: %d edits", len(es))
	}
}

func TestHunksKeepContextAroundChanges(t *testing.T) {
	before := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13"
	after := "1\n2\n3\n4\nfive\n6\n7\n8\n9\n10\n11\ntwelve\n13"
	want := "…\n  3\n  4\n- 5\n+ five\n  6\n  7\n…\n  10\n  11\n- 12\n+ twelve\n  13"
	if got := script(hunks(diffLines(before, after), 2)); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	// A short kept run between changes stays whole.
	if got := script(hunks(diffLines("a\nb\nc\nd", "A\nb\nc\nD"), 2)); got != "- a\n+ A\n  b\n  c\n- d\n+ D" {
		t.Errorf("short run: %s", got)
	}
}
