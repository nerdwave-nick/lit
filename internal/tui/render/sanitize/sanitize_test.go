package sanitize

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"testing/quick"
	"unicode/utf8"
)

func TestBodyMakesControlsVisible(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"plain", "hello *world*", "hello *world*"},
		{"escape", "a\x1b[2Jb", "a␛[2Jb"},
		{"bell", "ding\x07", "ding␇"},
		{"lone CR", "rm -rf ~\rls", "rm -rf ~␍ls"},
		{"CRLF line ending", "a\r\nb\r\n", "a\nb\n"},
		{"NUL", "a\x00b", "a␀b"},
		{"DEL", "a\x7fb", "a␡b"},
		{"C1 CSI rune", "\u009b31m", "⟨U+009B⟩31m"},
		{"raw 0x9b byte", "a\x9bb", "a�b"},
		{"bidi override", "‮evil.txt‬", "⟨U+202E⟩evil.txt⟨U+202C⟩"},
		{"bidi isolate and marks", "⁦x⁩‎‏؜", "⟨U+2066⟩x⟨U+2069⟩⟨U+200E⟩⟨U+200F⟩⟨U+061C⟩"},
		{"tab stops", "a\tb\n\tc", "a   b\n    c"},
		{"tab after wide rune", "日\tx", "日  x"},
		{"OSC 52", "\x1b]52;c;Zm9v\x07", "␛]52;c;Zm9v␇"},
		{"decimal reference to ESC", "&#27;[8msecret", "␛[8msecret"},
		{"hex reference to ESC", "&#x1B;[2J", "␛[2J"},
		{"reference without semicolon", "&#27[8m", "␛[8m"},
		{"named bidi mark", "a&rlm;b", "a⟨U+200F⟩b"},
		{"named tab", "a&Tab;b", "a   b"},
		{"harmless references stay", "&lt;b&gt; &amp;#27; &#65;", "&lt;b&gt; &amp;#27; &#65;"},
		{"emoji ZWJ sequence stays", "👨‍👩‍👧", "👨‍👩‍👧"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Body(tc.in); got != tc.want {
				t.Errorf("Body(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTitleIsSingleLine(t *testing.T) {
	if got, want := Title("Fix\r\nthe\nbug\x1b]0;PWN\x07"), "Fix␤the␤bug␛]0;PWN␇"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
}

func TestRenderedKeepsOnlySGRAndOSC8(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"SGR kept", "\x1b[38;2;1;2;3mx\x1b[0m", "\x1b[38;2;1;2;3mx\x1b[0m"},
		{"OSC 8 BEL kept", "\x1b]8;id=1;https://e.org\x07t\x1b]8;;\x07", "\x1b]8;id=1;https://e.org\x07t\x1b]8;;\x07"},
		{"OSC 8 ST kept", "\x1b]8;;https://e.org\x1b\\t\x1b]8;;\x1b\\", "\x1b]8;;https://e.org\x1b\\t\x1b]8;;\x1b\\"},
		{"erase display marked", "a\x1b[2Jb", "a␛[2Jb"},
		{"OSC 52 marked", "\x1b]52;c;Zm9v\x07", "␛]52;c;Zm9v␇"},
		{"private SGR-like marked", "\x1b[?25l", "␛[?25l"},
		{"bare controls marked", "a\rb\x07\t", "a␍b␇␉"},
		{"C1 and bidi marked", "\u009b‮", "⟨U+009B⟩⟨U+202E⟩"},
		{"newline kept", "a\nb", "a\nb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Rendered(tc.in); got != tc.want {
				t.Errorf("Rendered(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Hostile is arbitrary text biased towards controls, references and markup.
type Hostile string

var pieces = []string{
	"\x1b", "\x1b[", "\x1b]", "\x1b\\", "\x07", "\r", "\r\n", "\n", "\t", "\x00", "\x7f", "\x9b", "\xff",
	"\u0085", "\u009b", "\u009c", "‮", "⁦", "‏", "؜", "‍",
	"&#27;", "&#x1b;", "&#27", "&#x9b;", "&#8238;", "&rlm;", "&lrm;", "&Tab;", "&amp;", "&#13;", "&#7;", "&",
	"#", "*", "_", "`", "```", "\n```\n", "    ", "> ", "- ", "1. ", "|", "[", "](", ")", "<", ">", "\\",
	"日本", "👨‍👩‍👧", "🇩🇪", "é", "x", "word ", "https://e.org",
}

func (Hostile) Generate(r *rand.Rand, size int) reflect.Value {
	var b strings.Builder
	for n := r.Intn(size + 1); n > 0; n-- {
		if r.Intn(4) == 0 {
			b.WriteRune(rune(r.Intn(0x3000)))
		} else {
			b.WriteString(pieces[r.Intn(len(pieces))])
		}
	}
	return reflect.ValueOf(Hostile(b.String()))
}

// Forbidden reports the first rune that must never reach the terminal from
// content: C0 controls other than newline, DEL, C1 controls, bidi controls
// and invalid UTF-8.
func Forbidden(s string) (rune, bool) {
	for i, r := range s {
		if r == utf8.RuneError && !strings.HasPrefix(s[i:], "�") {
			return r, true
		}
		if r != '\n' && isControl(r) {
			return r, true
		}
	}
	return 0, false
}

func TestPropertyBodyAndTitleOutputNoControls(t *testing.T) {
	cfg := &quick.Config{MaxCount: 2000, Rand: rand.New(rand.NewSource(1))}
	if err := quick.Check(func(h Hostile) bool {
		body, title := Body(string(h)), Title(string(h))
		if r, bad := Forbidden(body); bad {
			t.Logf("Body(%q) keeps %U", h, r)
			return false
		}
		if r, bad := Forbidden(title); bad || strings.Contains(title, "\n") {
			t.Logf("Title(%q) keeps %U or a newline", h, r)
			return false
		}
		return Body(body) == body // sanitizing is idempotent
	}, cfg); err != nil {
		t.Error(err)
	}
}
