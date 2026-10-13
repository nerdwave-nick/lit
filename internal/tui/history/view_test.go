package history

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/keys"
	"github.com/nerdwave-nick/lit/internal/tui/render/cells"
)

var sgr = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// loaded is a view showing entries, as if their page had arrived.
func loaded(t *testing.T, entries ...litclient.HistoryEntry) *Model {
	t.Helper()
	m := &Model{choices: []Target{{Type: "issues", ID: issueID, Label: "issue \"Wide 表 🙂\x1b[2J\""}}}
	m.show()
	m.stop()
	if _, ok := m.Update(pageMsg{m: m, gen: m.gen, page: litclient.HistoryPage{Items: entries}}); !ok {
		t.Fatal("page not taken")
	}
	return m
}

func wideEntry() litclient.HistoryEntry {
	body := strings.Repeat("漢字かな🙂 mixed words and emoji ", 12)
	return entry(1, 2, protocol.DurableActor{ClientID: clientID, Name: "Ünïcode 名前", Kind: "human"},
		[]protocol.Operation{{Type: "issues", ID: issueID, Kind: "update"}},
		diffs("title", `"old"`, `"`+strings.Repeat("长标题", 30)+`"`), litclient.BodyHunk{Before: "same\n" + body, After: "same\n" + body + "!"})
}

// Every width yields exactly h lines of at most w cells, under both
// measuring methods, and wrapping keeps every grapheme.
func TestViewFitsTheScreenAtEveryWidth(t *testing.T) {
	m := loaded(t, wideEntry())
	for _, method := range []cells.Method{ansi.WcWidth, ansi.GraphemeWidth} {
		for _, w := range []int{20, 33, 57, 80, 131} {
			for _, h := range []int{3, 10, 40} {
				v := m.View(method, w, h, keys.Default(), false)
				ls := strings.Split(v, "\n")
				if len(ls) != h {
					t.Fatalf("w %d h %d: %d lines", w, h, len(ls))
				}
				for i, l := range ls {
					if got := cells.Width(method, l); got > w {
						t.Fatalf("w %d: line %d is %d cells: %q", w, i, got, l)
					}
				}
			}
			// Unclipped, the body still holds every character of the title.
			all := sgr.ReplaceAllString(m.View(method, w, 0, keys.Default(), false), "")
			joined := strings.ReplaceAll(strings.ReplaceAll(all, "\n", ""), " ", "")
			if !strings.Contains(joined, strings.Repeat("长标题", 30)) {
				t.Fatalf("w %d: wrapping lost text:\n%s", w, all)
			}
		}
	}
}

// Styled views colour removed and added lines with SGR only; plain views
// have no escape sequences at all. Tracker text never adds any.
func TestViewStylesOnlyWithSGR(t *testing.T) {
	e := entry(1, 2, protocol.DurableActor{ClientID: clientID, Name: "x\x1b]0;t\x07", Kind: "agent"}, nil, nil,
		litclient.BodyHunk{Before: "a\nold\x1b[31m", After: "a\nnew\u009b2J"})
	m := loaded(t, e)
	styled := m.View(cells.Default, 80, 20, keys.Default(), false)
	if !strings.Contains(styled, "\x1b[31m    - old␛[31m\x1b[m") || !strings.Contains(styled, "\x1b[32m    + new⟨U+009B⟩2J\x1b[m") {
		t.Fatalf("styled:\n%q", styled)
	}
	if rest := sgr.ReplaceAllString(styled, ""); strings.ContainsAny(rest, "\x1b\x07\u009b") {
		t.Fatalf("controls beyond SGR:\n%q", rest)
	}
	plain := m.View(cells.Default, 80, 20, keys.Default(), true)
	if strings.Contains(plain, "\x1b") || !strings.Contains(plain, "    - old␛[31m") || !strings.Contains(plain, "History · issue \"Wide 表 🙂␛[2J\"") {
		t.Fatalf("plain:\n%q", plain)
	}
}

// Scrolling is bounded by the body, and the footer names the keys.
func TestViewScrollsWithinTheBody(t *testing.T) {
	var es []litclient.HistoryEntry
	for i := int64(1); i <= 8; i++ {
		e := entry(i-1, i, claude, nil, diffs("title", `"a"`, `"b"`))
		e.RequestHash = strings.Repeat(string(rune('a'+i)), 64)
		es = append(es, e)
	}
	m := loaded(t, es...)
	top := m.View(cells.Default, 80, 6, keys.Default(), true)
	if !strings.Contains(top, "8 entries") || !strings.HasSuffix(top, "esc cancel · down down · up up · end bottom · r refresh") {
		t.Fatalf("top:\n%s", top)
	}
	m.Do(keys.Bottom)
	bottom := strings.Split(m.View(cells.Default, 80, 6, keys.Default(), true), "\n")
	if !strings.Contains(bottom[4], `title: "a" → "b"`) || !strings.Contains(bottom[2], "#8 · ") {
		t.Fatalf("bottom:\n%s", strings.Join(bottom, "\n"))
	}
	m.Do(keys.Down) // past the end stays at the end
	if again := strings.Split(m.View(cells.Default, 80, 6, keys.Default(), true), "\n"); again[2] != bottom[2] {
		t.Fatalf("scrolled past the end: %q", again[2])
	}
	m.Do(keys.Top)
	if v := m.View(cells.Default, 80, 6, keys.Default(), true); v != top {
		t.Fatalf("top again:\n%s", v)
	}
}

// A long chooser scrolls so the cursor stays visible.
func TestChooserKeepsTheCursorVisible(t *testing.T) {
	targets := []Target{{Type: "issues", ID: issueID, Label: "issue"}}
	for i := 0; i < 30; i++ {
		targets = append(targets, Target{Type: "comments", ID: commentID, Label: "comment " + string(rune('A'+i%26)) + strings.Repeat("x", i)})
	}
	m, _ := New(nil, targets...)
	for i := 0; i < 25; i++ {
		m.Do(keys.Down)
	}
	v := m.View(cells.Default, 80, 10, keys.Default(), true)
	if !strings.Contains(v, "> comment Y"+strings.Repeat("x", 24)) || len(strings.Split(v, "\n")) != 10 {
		t.Fatalf("cursor not visible:\n%s", v)
	}
	m.Do(keys.Top)
	if v := m.View(cells.Default, 80, 10, keys.Default(), true); !strings.Contains(v, "> issue") {
		t.Fatalf("top:\n%s", v)
	}
}
