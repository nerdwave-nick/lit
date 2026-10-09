package detail

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
)

var (
	codeA = "A" + digits(240)
	codeB = "B" + strings.Repeat("abcdefghij", 24)
)

// wideBody has prose around two wide code blocks, a wide table, a code block
// that fits, CJK and emoji.
var wideBody = "# Wide blocks\n\n" +
	"Intro prose that is long enough to wrap at sixty columns and must never scroll sideways whatever happens to the blocks.\n\n" +
	"```text\n" + codeA + "\nshort\n```\n\n" +
	"Middle prose between the blocks, also long enough to wrap at narrow widths.\n\n" +
	"```text\n" + codeB + "\n```\n\n" +
	"| id | title | state | assignee | labels | parent | blocked by | created | updated | notes about the record |\n" +
	"|----|-------|-------|----------|--------|--------|------------|---------|---------|------------------------|\n" +
	"| 9f01ee0b | Plan the manual lit TUI with a long title | open | — | planner:map | — | — | 2026-10-01T12:41 | 2026-10-08T15:47 | the map is an index of decisions |\n\n" +
	"```go\nfunc fits() {}\n```\n\n" +
	"日本語の段落はスペースを含まないので、折り返しは文字単位で行われる必要があります。これは長い一文です。\n\n" +
	"Emoji: 👨\u200d👩\u200d👧\u200d👦 family, 🇩🇪 flag, 🧑🏽\u200d💻 coder, ✅ done, ⚠️ warning (VS16) and more words to wrap.\n\n" +
	"| 名前 | 状態 | 説明 |\n|---|---|---|\n| 計画 | 進行中 | 端末ユーザーインターフェースの設計を決定する長い説明文がここに続きます 🚧 |\n"

func sectionsBody(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "## Section %d\n\n", i)
		fmt.Fprintf(&b, "Paragraph%d alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega and more words.\n\n", i)
		fmt.Fprintf(&b, "```go\nfunc section%d() {}\n```\n\n", i)
	}
	return b.String()
}

func doc(content string) Section { return Section{RecordID: "issue-1", Revision: 1, Content: content} }

// Every line fits the pane exactly, at every width, in every style, under
// both width methods, wherever the viewport is scrolled.
func TestEveryLineFitsThePane(t *testing.T) {
	for _, method := range []cells.Method{ansi.WcWidth, ansi.GraphemeWidth} {
		for _, style := range []render.Style{render.Dark, render.Light, render.ASCII} {
			for _, w := range []int{60, 80, 120, 200} {
				const h = 12
				x := newHarness(t, w, h, style, method)
				x.show(doc(wideBody))
				what := fmt.Sprintf("method %v %s w%d", method, style, w)
				for page := 0; page < 40; page++ {
					x.checkFits(what, w, h, method)
					for i := 0; i < 40; i++ {
						x.press("right")
					}
					x.checkFits(what+" scrolled right", w, h, method)
					x.press("tab")
					x.checkFits(what+" next block", w, h, method)
					x.press("pgdown")
				}
			}
		}
	}
}

func TestProseWrapsAndWideBlocksStayUnwrapped(t *testing.T) {
	x := newHarness(t, 60, 200, render.Dark, ansi.WcWidth)
	x.show(doc(wideBody))
	view := strings.Join(x.plain(), "\n")
	if !strings.Contains(view, "Intro prose that is long enough") || strings.Contains(view, "must never scroll sideways whatever happens to the blocks.") {
		t.Errorf("prose did not wrap at 60 columns:\n%s", view)
	}
	if !strings.Contains(view, codeA[:50]) || strings.Contains(view, codeA[100:120]) {
		t.Errorf("the wide code line is not one clipped line:\n%s", view)
	}
}

// rowsOf returns the indexes of the plain lines containing s.
func rowsOf(ls []string, s string) []int {
	var out []int
	for i, l := range ls {
		if strings.Contains(l, s) {
			out = append(out, i)
		}
	}
	return out
}

// The focused wide block scrolls on its own: prose and other blocks stay,
// clipped lines show overflow markers, and every block keeps its offset.
func TestFocusedBlockScrollsHorizontallyOnItsOwn(t *testing.T) {
	const w = 60
	x := newHarness(t, w, 40, render.Dark, ansi.WcWidth)
	x.show(doc(wideBody))
	before := x.plain()
	rowA, rowB := rowsOf(before, codeA[:20]), rowsOf(before, codeB[:20])
	if len(rowA) != 1 || len(rowB) != 1 {
		t.Fatalf("code rows %v %v:\n%s", rowA, rowB, strings.Join(before, "\n"))
	}
	a, b := rowA[0], rowB[0]
	if l := before[a]; !strings.HasPrefix(l, "▌") || !strings.HasSuffix(l, "›") {
		t.Errorf("focused clipped line lacks the focus or overflow marker: %q", l)
	}
	if l := before[a+1]; !strings.HasPrefix(l, "▌") || strings.HasSuffix(l, "›") {
		t.Errorf("a short line of the focused block: %q", l)
	}
	if l := before[b]; strings.HasPrefix(l, "▌") || !strings.HasSuffix(l, "›") {
		t.Errorf("unfocused clipped line: %q", l)
	}

	x.press("right")
	after := x.plain()
	for i := range before {
		if i != a && i != a+1 && after[i] != before[i] {
			t.Errorf("line %d moved:\n%q\n%q", i, before[i], after[i])
		}
	}
	// View column c shows the line's column c+ScrollStep; the code starts at
	// the line's column start.
	start := len([]rune(before[a][:strings.Index(before[a], "A")]))
	j := 2 + ScrollStep - start
	if got, want := after[a], "▌‹"+codeA[j:j+w-3]+"›"; got != want {
		t.Errorf("scrolled line\n%q, want\n%q", got, want)
	}

	for i := 0; i < 100; i++ {
		x.press("right")
	}
	end := x.plain()
	if !strings.HasSuffix(end[a], codeA[len(codeA)-20:]) || !strings.HasPrefix(end[a], "▌‹") {
		t.Errorf("scrolled to the end: %q", end[a])
	}

	x.press("tab")
	focused := x.plain()
	if strings.HasPrefix(focused[a], "▌") || !strings.HasPrefix(focused[b], "▌") {
		t.Errorf("tab did not move the focus to the next block:\n%q\n%q", focused[a], focused[b])
	}
	x.press("right")
	if got := x.plain(); got[a] != focused[a] || got[b] == focused[b] {
		t.Errorf("the next block did not scroll on its own:\n%q\n%q", got[a], got[b])
	}
	x.press("shift+tab")
	for i := 0; i < 100; i++ {
		x.press("left")
	}
	if got := x.plain(); got[a] != before[a] || got[b] == before[b] {
		t.Errorf("each block must keep its own offset:\n%q\n%q", got[a], got[b])
	}
}

// Prose never scrolls sideways: with no wide block on screen, left and right
// change nothing.
func TestProseNeverScrollsSideways(t *testing.T) {
	x := newHarness(t, 60, 10, render.Dark, ansi.WcWidth)
	x.show(doc("# Title\n\n" + strings.Repeat("Words that wrap and wrap again. ", 30) + "\n"))
	before := x.plain()
	x.press("right", "right", "tab", "right")
	if after := x.plain(); !slices.Equal(after, before) {
		t.Errorf("prose moved:\n%s\n---\n%s", strings.Join(before, "\n"), strings.Join(after, "\n"))
	}
}

// Without styling the focus and overflow markers are plain characters.
func TestPlainStyleUsesTextMarkers(t *testing.T) {
	x := newHarness(t, 60, 40, render.ASCII, ansi.WcWidth)
	x.show(doc(wideBody))
	ls := x.plain()
	a := rowsOf(ls, codeA[:20])[0]
	if !strings.HasPrefix(ls[a], "> ") || !strings.HasSuffix(ls[a], "$") {
		t.Errorf("plain focused line: %q", ls[a])
	}
	x.press("right")
	if l := x.plain()[a]; !strings.HasPrefix(l, ">$") {
		t.Errorf("plain scrolled line: %q", l)
	}
	if raw := x.lines()[a]; !strings.HasPrefix(raw, ">$") || !strings.HasSuffix(strings.TrimRight(raw, " "), "$") && !strings.Contains(raw, "$\x1b") {
		t.Errorf("plain markers are styled: %q", raw)
	}
}

// When the focused block scrolls out of view, the focus moves to the first
// wide block on screen.
func TestFocusFollowsVerticalScroll(t *testing.T) {
	x := newHarness(t, 60, 4, render.Dark, ansi.WcWidth)
	x.show(doc(wideBody))
	x.scrollUntil(func(l string) bool { return strings.Contains(l, codeB[:20]) })
	if l := x.plain()[0]; !strings.HasPrefix(l, "▌") {
		t.Errorf("the block on screen is not focused: %q", l)
	}
}

func TestVerticalScrollKeys(t *testing.T) {
	x := newHarness(t, 80, 10, render.Dark, ansi.WcWidth)
	x.show(doc(sectionsBody(30)))
	if x.top() != "## Section 0" && !strings.Contains(x.top(), "Section 0") {
		t.Fatalf("top %q", x.top())
	}
	x.press("end")
	last := x.plain()
	if !slices.ContainsFunc(last, func(l string) bool { return strings.Contains(l, "func section29") }) {
		t.Errorf("end does not show the last block:\n%s", strings.Join(last, "\n"))
	}
	x.press("down")
	if !slices.Equal(x.plain(), last) {
		t.Error("scrolled past the end")
	}
	x.press("home")
	if !strings.Contains(x.top(), "Section 0") {
		t.Errorf("home: top %q", x.top())
	}
	x.press("up")
	if !strings.Contains(x.top(), "Section 0") {
		t.Errorf("scrolled above the top: %q", x.top())
	}
	x.press("pgdown")
	paged := x.top()
	x.press("pgup")
	if paged == x.top() || !strings.Contains(x.top(), "Section 0") {
		t.Errorf("page down/up: %q then %q", paged, x.top())
	}
	x.deliver(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if x.top() == paged || strings.Contains(x.top(), "Section 0") {
		t.Errorf("wheel down did not scroll: %q", x.top())
	}
}

func TestKeysFollowTheKeymap(t *testing.T) {
	km, err := keys.New(map[string]map[string][]string{"detail": {"down": {"n"}}})
	if err != nil {
		t.Fatal(err)
	}
	x := newHarnessWith(t, 80, 10, render.Dark, ansi.WcWidth, km)
	x.show(doc(sectionsBody(10)))
	top := x.top()
	x.press("j", "down")
	if x.top() != top {
		t.Errorf("unbound keys scrolled: %q", x.top())
	}
	x.press("n")
	if x.top() == top {
		t.Error("the rebound key did not scroll")
	}
}

// The scroll position follows the content, not the line number, across
// resizes and re-renders.
func TestScrollAnchorSurvivesResizeAndRerender(t *testing.T) {
	x := newHarness(t, 120, 10, render.Dark, ansi.WcWidth)
	x.show(doc(sectionsBody(30)))
	x.scrollUntil(func(l string) bool { return strings.Contains(l, "Section 12") })
	for _, w := range []int{70, 200, 61, 120} {
		x.resize(w, 10)
		if !strings.Contains(x.top(), "Section 12") {
			t.Fatalf("after resize to %d the top is %q", w, x.top())
		}
	}
	x.run(x.m.SetStyle(render.Light))
	if !strings.Contains(x.top(), "Section 12") {
		t.Fatalf("after a style change the top is %q", x.top())
	}
	x.show(Section{RecordID: "issue-1", Revision: 2, Content: sectionsBody(30)})
	if !strings.Contains(x.top(), "Section 12") {
		t.Fatalf("after a new revision the top is %q", x.top())
	}

	// An anchor inside a code block is exact.
	x.scrollUntil(func(l string) bool { return strings.Contains(l, "func section14") })
	x.resize(70, 10)
	if !strings.Contains(x.top(), "func section14") {
		t.Fatalf("code anchor after resize: %q", x.top())
	}

	// An anchor inside a paragraph keeps its first word on the top line.
	x.scrollUntil(func(l string) bool { return strings.Contains(l, "Paragraph16") })
	x.press("down")
	word := strings.Fields(x.top())[0]
	for _, w := range []int{120, 61, 200} {
		x.resize(w, 10)
		if !slices.Contains(strings.Fields(x.top()), word) {
			t.Fatalf("paragraph anchor %q lost at width %d: top %q", word, w, x.top())
		}
	}
}

// A height change keeps the anchor too.
func TestScrollAnchorSurvivesHeightChanges(t *testing.T) {
	x := newHarness(t, 80, 10, render.Dark, ansi.WcWidth)
	x.show(doc(sectionsBody(30)))
	x.scrollUntil(func(l string) bool { return strings.Contains(l, "Section 9") })
	x.resize(80, 30)
	x.resize(80, 5)
	if !strings.Contains(x.top(), "Section 9") {
		t.Errorf("top %q", x.top())
	}
}
