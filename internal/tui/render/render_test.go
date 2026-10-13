package render

import (
	"fmt"
	"math/rand"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"testing/quick"
	"unicode/utf8"

	"charm.land/glamour/v2"
	"github.com/charmbracelet/x/ansi"
)

var (
	sgrRe  = regexp.MustCompile(`\x1b\[[0-9;:]*m`)
	osc8Re = regexp.MustCompile(`\x1b\]8;[^\x07\x1b]*;[^\x07\x1b]*(\x07|\x1b\\)`)
)

// leaked returns every control rune left after removing SGR and OSC 8.
func leaked(s string) []string {
	s = osc8Re.ReplaceAllString(sgrRe.ReplaceAllString(s, ""), "")
	var bad []string
	for i, r := range s {
		switch {
		case r == '\n':
		case r == utf8.RuneError && !strings.HasPrefix(s[i:], "�"),
			r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f,
			r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069, r == 0x200e, r == 0x200f, r == 0x061c:
			bad = append(bad, fmt.Sprintf("%U", r))
		}
	}
	return bad
}

func joined(d *Doc) string {
	var ls []string
	for _, l := range d.Lines() {
		ls = append(ls, l.Text)
	}
	return strings.Join(ls, "\n")
}

func mustRender(t testing.TB, j Job) *Doc {
	t.Helper()
	d, err := Render(j)
	if err != nil {
		t.Fatalf("Render(%q…): %v", j.Content[:min(30, len(j.Content))], err)
	}
	return d
}

var styles = []Style{Dark, Light, ASCII}

func TestRenderedOutputHasOnlyRendererSGRAndOSC8(t *testing.T) {
	for _, f := range fixtures() {
		for _, st := range styles {
			d := mustRender(t, Job{Content: f.Body, Width: 80, Style: st})
			if len(d.Blocks) == 0 {
				t.Fatalf("%s/%v: no blocks", f.Title, st)
			}
			if bad := leaked(joined(d)); len(bad) > 0 {
				t.Errorf("%s/%v: controls reach the terminal: %v", f.Title, st, bad)
			}
		}
	}
}

func TestEscapesAndReferencesShowAsMarkers(t *testing.T) {
	body := escapeBody + "\nReference conceal: &#27;[8msecret&#27;[0m and `&#x1b;]52;c;eA==&#7;`.\n"
	plain := ansi.Strip(joined(mustRender(t, Job{Content: body, Width: 100, Style: Dark})))
	for _, want := range []string{"␛]52;c;", "␛]8;;https://evil.example/", "␛[2J", "␇", "␍", "⟨U+009B⟩", "⟨U+202E⟩", "␛[8msecret", "␛]52;c;eA==␇"} {
		if !strings.Contains(plain, want) {
			t.Errorf("marker %q missing from render", want)
		}
	}
}

// Hostile is Markdown-shaped text full of controls and references.
type Hostile string

var pieces = []string{
	"\x1b", "\x1b[8m", "\x1b]8;;https://evil/\x1b\\", "\x07", "\r", "\r\n", "\n", "\n\n", "\t", "\x00", "\x9b", "\xff",
	"\u009b", "\u009c", "‮", "⁦", "&#27;", "&#x1b;[2J", "&#27", "&#x9b;", "&rlm;", "&Tab;", "&amp;", "&#13;", "&#7;",
	"# ", "## ", "*", "**", "_", "`", "\n```\n", "\n```go\n", "\n```nosuch\n", "\n    ", "\n> ", "\n- ", "\n1. ", "\n| a | b |\n|---|---|\n| ",
	" | ", "[link](https://e.org/", ")", "<https://e.org>", "<b>", "</b>", "\\", "~~", "---", "[^1]", "\n[r]: https://e.org\n",
	"日本語", "👨‍👩‍👧", "🇩🇪", "word ", "longwordwithoutspaces", " ",
}

func (Hostile) Generate(r *rand.Rand, size int) reflect.Value {
	var b strings.Builder
	for n := r.Intn(size*3 + 1); n > 0; n-- {
		if r.Intn(5) == 0 {
			b.WriteRune(rune(r.Intn(0x3000)))
		} else {
			b.WriteString(pieces[r.Intn(len(pieces))])
		}
	}
	return reflect.ValueOf(Hostile(b.String()))
}

func TestPropertyRenderEmitsOnlySGRAndOSC8(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	cfg := &quick.Config{MaxCount: 150, Rand: r}
	if testing.Short() {
		cfg.MaxCount = 30
	}
	if err := quick.Check(func(h Hostile, w uint8) bool {
		j := Job{Content: string(h), Width: 10 + int(w)%140, Style: Style(int(w) % 3)}
		d, err := Render(j)
		if err != nil {
			t.Logf("Render(%q): %v", h, err)
			return false
		}
		if bad := leaked(joined(d)); len(bad) > 0 {
			t.Logf("Render(%q, w=%d, %v) leaks %v", h, j.Width, j.Style, bad)
			return false
		}
		return true
	}, cfg); err != nil {
		t.Error(err)
	}
}

func TestProseWrapsWideBlocksStayUnwrapped(t *testing.T) {
	for _, f := range fixtures() {
		for _, w := range []int{40, 80, 200} {
			for _, st := range styles {
				d := mustRender(t, Job{Content: f.Body, Width: w, Style: st})
				for bi, b := range d.Blocks {
					if b.Kind.Wraps() {
						for _, l := range b.Lines {
							if lw := ansi.StringWidth(l); lw > w {
								t.Errorf("%s w=%d %v block %d: prose line width %d > %d: %q", f.Title, w, st, bi, lw, w, ansi.Strip(l))
							}
						}
					}
				}
			}
		}
	}
	long := "Prose.\n\n```go\nfunc main() { fmt.Println(\"" + strings.Repeat("x", 120) + "\") }\n```\n"
	d := mustRender(t, Job{Content: long, Width: 60, Style: Dark})
	if len(d.Blocks) != 2 || d.Blocks[1].Kind != Code {
		t.Fatalf("blocks = %+v", kinds(d))
	}
	if got := d.Blocks[1].Width(ansi.GraphemeWidth); got <= 120 || len(d.Blocks[1].Lines) != 1 {
		t.Errorf("code block wrapped: width %d, %d lines", got, len(d.Blocks[1].Lines))
	}
	if !strings.Contains(ansi.Strip(d.Blocks[1].Lines[0]), strings.Repeat("x", 120)) {
		t.Errorf("code line split: %q", ansi.Strip(d.Blocks[1].Lines[0]))
	}
	if wt := mustRender(t, Job{Content: wideTableBody, Width: 60, Style: Dark}); kinds(wt) != "PPTP" || wt.Blocks[2].Width(ansi.GraphemeWidth) <= 60 {
		t.Errorf("wide table: kinds %s width %d", kinds(wt), wt.Blocks[2].Width(ansi.GraphemeWidth))
	}
}

func kinds(d *Doc) string {
	var b strings.Builder
	for _, bl := range d.Blocks {
		b.WriteByte("PCT"[bl.Kind])
	}
	return b.String()
}

func TestTopLevelBlockStructure(t *testing.T) {
	src := "# Heading\n\nA paragraph.\n\n```go\nx := 1\n```\n\n    indented code\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n" +
		"- item\n\n  ```\n  nested code wraps with its list\n  ```\n\n> quote\n\n---\n"
	d := mustRender(t, Job{Content: src, Width: 80, Style: Dark})
	if got, want := kinds(d), "PPCCTPPP"; got != want {
		t.Errorf("block kinds = %s, want %s", got, want)
	}
	for _, b := range d.Blocks {
		if len(b.Lines) == 0 || strings.TrimSpace(ansi.Strip(b.Lines[0])) == "" || strings.TrimSpace(ansi.Strip(b.Lines[len(b.Lines)-1])) == "" {
			t.Errorf("block %v has leading/trailing blank lines: %q", b.Kind, b.Lines)
		}
	}
	lines := d.Lines()
	if n := len(lines); n == 0 || lines[0].Block != 0 || lines[n-1].Block != len(d.Blocks)-1 {
		t.Fatalf("Lines() block indexes wrong: %+v", lines)
	}
	total := len(d.Blocks) - 1
	for _, b := range d.Blocks {
		total += len(b.Lines)
	}
	if len(lines) != total {
		t.Errorf("Lines() = %d lines, want %d (one blank line between blocks)", len(lines), total)
	}
	// Code blocks keep the document margin that prose has.
	if code := ansi.Strip(d.Blocks[2].Lines[0]); !strings.HasPrefix(code, "  ") {
		t.Errorf("code block lost the document margin: %q", code)
	}
}

func TestReferenceLinksSurviveBlockSplit(t *testing.T) {
	d := mustRender(t, Job{Content: cjkBody, Width: 120, Style: Dark})
	if !strings.Contains(joined(d), "github.com/nerdwave-nick/lit") {
		t.Error("reference link destination lost")
	}
}

const goCode = "```go\nfunc main() { return \"x\" }\n```\n"

func TestCodeThemesAreIsolatedPerStyle(t *testing.T) {
	darkKeyword, lightKeyword := "\x1b[38;2;0;170;255m", "\x1b[38;2;39;158;252m" // glamour's dark/light chroma Keyword colours
	dark1 := joined(mustRender(t, Job{Content: goCode, Width: 80, Style: Dark}))
	light := joined(mustRender(t, Job{Content: goCode, Width: 80, Style: Light}))
	dark2 := joined(mustRender(t, Job{Content: goCode, Width: 80, Style: Dark}))
	if !strings.Contains(dark1, darkKeyword+"func") {
		t.Errorf("dark render lacks the dark keyword colour: %q", dark1)
	}
	if !strings.Contains(light, lightKeyword+"func") || strings.Contains(light, darkKeyword) {
		t.Errorf("light after dark does not use light colours: %q", light)
	}
	if dark1 != dark2 {
		t.Error("dark render changed after a light render")
	}
	// The stock bug this avoids: Glamour registers every style's chroma theme
	// under the fixed name "charm", so whichever renders first wins.
	stock := func(s string) string {
		r, _ := glamour.NewTermRenderer(glamour.WithStandardStyle(s), glamour.WithWordWrap(80))
		out, _ := r.Render(goCode)
		return regexp.MustCompile(`(\x1b\[[0-9;]*m)+func`).FindString(out) // the keyword's colour
	}
	sd, sl := stock("dark"), stock("light")
	t.Logf("stock glamour keyword SGR: dark %q, light after dark %q (same = %v)", sd, sl, sd == sl)
}

func TestASCIIStyleHasNoColour(t *testing.T) {
	out := joined(mustRender(t, Job{Content: polyglotBody + "\n# Title\n\n*em* **strong** [l](https://e.org)\n", Width: 80, Style: ASCII}))
	if c := regexp.MustCompile(`\x1b\[[0-9;]*[34]8;`).FindString(out); c != "" {
		t.Errorf("ASCII style emits colour %q", c)
	}
}

func TestUnlabelledCodeUsesChromaGuess(t *testing.T) {
	src := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n"
	if got := resolveLexer("", src); got != "Go" {
		t.Errorf("guess for unlabelled Go = %q, want Go", got)
	}
	if got := resolveLexer("nosuchlang", "#!/bin/sh\necho hi\n"); got != "Bash" {
		t.Errorf("guess for unknown language = %q, want Bash", got)
	}
	if got := resolveLexer("golang", ""); got != "Go" {
		t.Errorf("alias lookup = %q, want Go", got)
	}
	colours := func(s string) int {
		seen := map[string]bool{}
		for _, m := range regexp.MustCompile(`\x1b\[38;2;[0-9;]+m`).FindAllString(s, -1) {
			seen[m] = true
		}
		return len(seen)
	}
	for _, body := range []string{"```\n" + src + "```\n", "```nosuchlang\n" + src + "```\n", "- list\n\n  ```\n  " + strings.ReplaceAll(src, "\n", "\n  ") + "```\n"} {
		if n := colours(joined(mustRender(t, Job{Content: body, Width: 80, Style: Dark}))); n < 3 {
			t.Errorf("guessed code has %d token colours, want highlighting: %q", n, body)
		}
	}
}

func TestLexerLookupsByNameAreMemoised(t *testing.T) {
	resolveLexer("nosuchlang-memo", "x")
	resolveLexer("python", "x")
	lexerMu.Lock()
	miss, okMiss := lexerMemo["nosuchlang-memo"]
	hit, okHit := lexerMemo["python"]
	lexerMu.Unlock()
	if !okMiss || miss != "" || !okHit || hit != "Python" {
		t.Errorf("memo: nosuchlang=(%q,%v) python=(%q,%v)", miss, okMiss, hit, okHit)
	}
}

func TestKeyCoversContentRevisionWidthStyleOptions(t *testing.T) {
	base := Job{RecordID: "r", Revision: 1, Content: "a", Width: 80, Style: Dark}
	seen := map[Key]string{base.Key(): "base"}
	for name, j := range map[string]Job{
		"content":  {RecordID: "r", Revision: 1, Content: "b", Width: 80, Style: Dark},
		"revision": {RecordID: "r", Revision: 2, Content: "a", Width: 80, Style: Dark},
		"width":    {RecordID: "r", Revision: 1, Content: "a", Width: 81, Style: Dark},
		"style":    {RecordID: "r", Revision: 1, Content: "a", Width: 80, Style: Light},
		"options":  {RecordID: "r", Revision: 1, Content: "a", Width: 80, Style: Dark, Options: Options{PreserveNewLines: true}},
		"record":   {RecordID: "s", Revision: 1, Content: "a", Width: 80, Style: Dark},
	} {
		if prev, dup := seen[j.Key()]; dup {
			t.Errorf("%s change gives the same key as %s", name, prev)
		}
		seen[j.Key()] = name
	}
	if base.Key() != (Job{RecordID: "r", Revision: 1, Content: "a", Width: 80, Style: Dark}).Key() {
		t.Error("equal jobs give different keys")
	}
}

func TestPreserveNewLinesOption(t *testing.T) {
	src := "one\ntwo\n"
	a := ansi.Strip(joined(mustRender(t, Job{Content: src, Width: 80})))
	b := ansi.Strip(joined(mustRender(t, Job{Content: src, Width: 80, Options: Options{PreserveNewLines: true}})))
	if strings.Count(strings.TrimSpace(a), "\n") != 0 || strings.Count(strings.TrimSpace(b), "\n") != 1 {
		t.Errorf("soft break: default %q, preserved %q", a, b)
	}
}

// Every line, wrapped or not, starts with the style's document margin, so a
// viewport can keep it in place while it scrolls a wide block.
func TestEveryLineStartsWithTheStyleMargin(t *testing.T) {
	for _, s := range []Style{Dark, Light, ASCII} {
		if s.Margin() < 2 {
			t.Errorf("%s: margin %d, want at least 2 for the focus and overflow markers", s, s.Margin())
		}
		d, err := Render(Job{RecordID: "m", Revision: 1, Content: polyglotBody + wideTableBody, Width: 60, Style: s})
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range d.Blocks {
			for _, l := range b.Lines {
				if plain := ansi.Strip(l); strings.TrimSpace(plain) != "" && !strings.HasPrefix(plain, strings.Repeat(" ", s.Margin())) {
					t.Errorf("%s %s line lacks the %d-cell margin: %q", s, b.Kind, s.Margin(), plain)
				}
			}
		}
	}
}
