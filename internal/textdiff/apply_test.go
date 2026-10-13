package textdiff

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/quick"
)

// Diffs keep body bytes exact: CR bytes, a missing final newline, empty
// bodies, Unicode and invalid UTF-8 survive Diff and Apply unchanged.
func TestApplyRebuildsTheNewTextByteExactly(t *testing.T) {
	cases := []struct {
		name     string
		old, new string
		want     []string
	}{
		{"both empty", "", "", nil},
		{"crlf line changed", "a\r\nb\r\nc\r\n", "a\r\nB\r\nc\r\n", []string{"@@ -1,3 +1,3 @@", " a\r", "-b\r", "+B\r", " c\r"}},
		{"crlf to lf", "a\r\nb\r\n", "a\nb\n", []string{"@@ -1,2 +1,2 @@", "-a\r", "-b\r", "+a", "+b"}},
		{"lone cr stays inside its line", "a\rb\n", "a\rc\n", []string{"@@ -1,1 +1,1 @@", "-a\rb", "+a\rc"}},
		{"no final newline on either side", "a\nb", "a\nc", []string{"@@ -1,2 +1,2 @@", " a", "-b", NoNewline, "+c", NoNewline}},
		{"cr before a missing final newline", "a\r\nb\r", "a\r\nb\r\n", []string{"@@ -1,2 +1,2 @@", " a\r", "-b\r", NoNewline, "+b\r"}},
		{"context without final newline", "a\nb", "A\nb", []string{"@@ -1,2 +1,2 @@", "-a", "+A", " b", NoNewline}},
		{"from empty", "", "x\ny", []string{"@@ -0,0 +1,2 @@", "+x", "+y", NoNewline}},
		{"to empty", "x\ny\n", "", []string{"@@ -1,2 +0,0 @@", "-x", "-y"}},
		{"only a newline removed", "\n", "", []string{"@@ -1,1 +0,0 @@", "-"}},
		{"unicode", "Straße\n雪 ❄️\nnaïve é\n", "Strasse\n雪 ❄️\nnaïve é\n", []string{"@@ -1,3 +1,3 @@", "-Straße", "+Strasse", " 雪 ❄️", " naïve é"}},
		{"invalid utf-8", "\xff\xfe\n", "\xff\n", []string{"@@ -1,1 +1,1 @@", "-\xff\xfe", "+\xff"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Diff(c.old, c.new)
			if !slices.Equal(d, c.want) {
				t.Fatalf("diff %q, want %q", d, c.want)
			}
			if got, err := Apply(d, c.old); err != nil || got != c.new {
				t.Fatalf("Apply = %q, %v; want %q", got, err, c.new)
			}
		})
	}
}

// Apply is strict: a diff applies only to the exact base it was made from.
func TestApplyRefusesAnotherBase(t *testing.T) {
	cases := []struct {
		name           string
		old, new, base string
	}{
		{"context differs", "a\nb\nc\n", "a\nB\nc\n", "x\nb\nc\n"},
		{"removed line differs", "a\nb\nc\n", "a\nB\nc\n", "a\nX\nc\n"},
		{"line ending differs", "a\r\nb\r\n", "a\r\nB\r\n", "a\nb\n"},
		{"final newline differs", "a\nb", "a\nc", "a\nb\n"},
		{"base has a line before", "", "new\n", "old\n"},
		{"base has a line after", "a\nb\n", "a\nB\n", "a\nb\nc\n"},
		{"base is shorter", "a\nb\nc\n", "a\nb\nC\n", "a\nb\n"},
		{"base is empty", "a\n", "b\n", ""},
		{"lines shifted", lines10(0), strings.Replace(lines10(0), "8\n", "eight\n", 1), "0\n" + lines10(0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Diff(c.old, c.new)
			if got, err := Apply(d, c.old); err != nil || got != c.new {
				t.Fatalf("on its own base Apply = %q, %v; want %q", got, err, c.new)
			}
			got, err := Apply(d, c.base)
			if !errors.Is(err, ErrApply) {
				t.Fatalf("Apply = %q, %v; want ErrApply", got, err)
			}
		})
	}
}

func lines10(from int) string {
	var b strings.Builder
	for i := from; i < from+10; i++ {
		fmt.Fprintf(&b, "%d\n", i)
	}
	return b.String()
}

// Apply accepts only well-formed diffs in Diff's own format.
func TestApplyRefusesMalformedDiffs(t *testing.T) {
	const base = "a\nb\nc\n"
	twenty := func() string {
		var b strings.Builder
		for i := 1; i <= 20; i++ {
			fmt.Fprintf(&b, "line %d\n", i)
		}
		return b.String()
	}()
	two := Diff(twenty, strings.NewReplacer("line 2\n", "two\n", "line 18\n", "eighteen\n").Replace(twenty))
	second := slices.IndexFunc(two[1:], func(l string) bool { return strings.HasPrefix(l, "@@") }) + 1
	cases := []struct {
		name string
		base string
		diff []string
	}{
		{"no header", base, []string{" a", "-b", "+B", " c"}},
		{"header without counts", base, []string{"@@ -1 +1 @@", "-a", "+A"}},
		{"header with a section", base, []string{"@@ -1,3 +1,3 @@ x", " a", "-b", "+B", " c"}},
		{"header with a space", base, []string{"@@ -1,3 +1, 3 @@", " a", "-b", "+B", " c"}},
		{"header with a sign", base, []string{"@@ -1,3 +1,+3 @@", " a", "-b", "+B", " c"}},
		{"header line zero", base, []string{"@@ -0,3 +1,3 @@", " a", "-b", "+B", " c"}},
		{"body shorter than header", base, []string{"@@ -1,3 +1,3 @@", " a", "-b", "+B"}},
		{"body longer than header", base, []string{"@@ -1,2 +1,2 @@", " a", "-b", "+B", " c"}},
		{"unknown line", base, []string{"@@ -1,3 +1,3 @@", " a", "*b", "+B", " c"}},
		{"empty line", base, []string{"@@ -1,3 +1,3 @@", " a", "", "-b", "+B"}},
		{"marker without a line", base, []string{"@@ -1,3 +1,3 @@", NoNewline, " a", "-b", "+B", " c"}},
		{"marker twice", "a\nb", []string{"@@ -1,2 +1,2 @@", " a", "-b", NoNewline, NoNewline, "+c", NoNewline}},
		{"marker before more lines", base, []string{"@@ -1,3 +1,3 @@", " a", "-b", "+B", NoNewline, " c"}},
		{"wrong new start", base, []string{"@@ -1,3 +2,3 @@", " a", "-b", "+B", " c"}},
		{"no change", base, []string{"@@ -1,3 +1,3 @@", " a", " b", " c"}},
		{"hunks out of order", twenty, append(slices.Clone(two[second:]), two[:second]...)},
		{"hunk repeated", twenty, append(slices.Clone(two[:second]), two[:second]...)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Apply(c.diff, c.base)
			if !errors.Is(err, ErrApply) {
				t.Fatalf("Apply = %q, %v; want ErrApply", got, err)
			}
		})
	}
	if got, err := Apply(two, twenty); err != nil || !strings.Contains(got, "two\n") {
		t.Fatalf("the unaltered two-hunk diff does not apply: %q, %v", got, err)
	}
}

// body is a generated text: lines from a small alphabet (so that texts share
// lines) with LF or CRLF endings and an optional final newline.
type body string

func (body) Generate(r *rand.Rand, size int) reflect.Value {
	alphabet := []string{"a", "b", "", "\r", "é", "雪 ❄️", "x\ry", "\xff", "@@ -1,1 +1,1 @@", `\ No newline at end of file`}
	var b strings.Builder
	n := r.Intn(size + 1)
	for i := range n {
		b.WriteString(alphabet[r.Intn(len(alphabet))])
		if i < n-1 || r.Intn(2) == 0 {
			b.WriteString([]string{"\n", "\r\n"}[r.Intn(2)])
		}
	}
	return reflect.ValueOf(body(b.String()))
}

// Property: Apply(Diff(a, b), a) == b for any pair of texts.
func TestApplyInvertsDiff(t *testing.T) {
	prop := func(a, b body) bool {
		got, err := Apply(Diff(string(a), string(b)), string(a))
		return err == nil && got == string(b)
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 2000, Rand: rand.New(rand.NewSource(1))}); err != nil {
		t.Fatal(err)
	}
}

// The same property over arbitrary bytes; `go test` runs the seeds.
func FuzzApplyInvertsDiff(f *testing.F) {
	for _, s := range [][2]string{
		{"", ""}, {"a\r\nb", "a\nb\r\n"}, {"\n\n\n", "\n"}, {"x", ""},
		{"Straße\n", "Strasse"}, {"\xff\n\xfe", "\xfe\n"}, {lines10(0), lines10(3)},
	} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		if got, err := Apply(Diff(a, b), a); err != nil || got != b {
			t.Fatalf("Apply(Diff(%q, %q)) = %q, %v", a, b, got, err)
		}
	})
}

// Apply never panics on arbitrary diffs (stored history is replayed by the
// server); it applies them or returns ErrApply.
func FuzzApplyArbitraryDiffs(f *testing.F) {
	f.Add("@@ -1,3 +1,3 @@\n a\n-b\n+B\n c", "a\nb\nc\n")
	f.Add("@@ -1,1 +1,1 @@\n-a\n\\ No newline at end of file\n+b", "a")
	f.Add("@@ -0,0 +1,1 @@\n+x\n@@ -0,0 +1,1 @@\n+y", "")
	f.Add("@@ -2,1 +1,0 @@\n-b", "a\nb")
	f.Fuzz(func(t *testing.T, diff, base string) {
		if _, err := Apply(strings.Split(diff, "\n"), base); err != nil && !errors.Is(err, ErrApply) {
			t.Fatalf("error %v does not wrap ErrApply", err)
		}
	})
}
