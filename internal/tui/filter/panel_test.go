package filter

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/keys"
	"github.com/nerdwave-nick/lit/internal/tui/render/cells"
)

func issues() []protocol.IssueSummary {
	return []protocol.IssueSummary{
		summary("a", "open", []string{"ui", "bug"}, "nick", true),
		summary("b", "closed", []string{"bug\x1b[31m"}, "", false),
		summary("c", "open", []string{}, "anna", false),
	}
}

// at moves the cursor to the row containing s.
func at(t *testing.T, p *Panel, s string) {
	t.Helper()
	p.Do(keys.Top)
	for range 50 {
		lines := p.Lines(cells.Default, 60, 40, true)
		for _, l := range lines {
			if strings.HasPrefix(l, ">") && strings.Contains(l, s) {
				return
			}
		}
		p.Do(keys.Down)
	}
	t.Fatalf("no row %q:\n%s", s, strings.Join(p.Lines(cells.Default, 60, 40, true), "\n"))
}

// fitLines asserts exactly h lines of exactly w cells.
func fitLines(t *testing.T, lines []string, w, h int) {
	t.Helper()
	if len(lines) != h {
		t.Fatalf("%d lines, want %d", len(lines), h)
	}
	for n, l := range lines {
		if got := cells.Width(cells.Default, l); got != w {
			t.Fatalf("line %d is %d cells, want %d: %q", n, got, w, ansi.Strip(l))
		}
	}
}

// Every filter is a row; open changes it at once: the state and claim
// cycle, labels and assignees (from the loaded issues, sorted, with
// unassigned first) toggle, and the last row clears everything.
func TestPanelChangesEveryFilter(t *testing.T) {
	p := NewPanel(Criteria{Search: "renewal"}, issues())
	view := strings.Join(p.Lines(cells.Default, 60, 20, true), "\n")
	for _, want := range []string{"State: any", "Claimed: any", "Label: [ ] bug", "Label: [ ] bug␛[31m", "Label: [ ] ui", "Assignee: [ ] unassigned",
		"Assignee: [ ] anna", "Assignee: [ ] nick", `Search: "renewal"`, "Clear all filters"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "\x1b") || strings.Index(view, "unassigned") > strings.Index(view, "anna") || strings.Index(view, "anna") > strings.Index(view, "nick") {
		t.Fatalf("rows not sanitized or sorted:\n%s", view)
	}

	at(t, p, "State")
	for _, want := range []string{"open", "closed", ""} {
		if r := p.Do(keys.Open); !r.Changed || p.Criteria().State != want {
			t.Fatalf("state %q, want %q (%+v)", p.Criteria().State, want, r)
		}
	}
	at(t, p, "Claimed")
	for _, want := range []string{Claimed, Unclaimed, ""} {
		if p.Do(keys.Open); p.Criteria().Claimed != want {
			t.Fatalf("claimed %q, want %q", p.Criteria().Claimed, want)
		}
	}
	at(t, p, "Label: [ ] ui")
	p.Do(keys.Open)
	at(t, p, "Assignee: [ ] unassigned")
	p.Do(keys.Open)
	if c := p.Criteria(); !slices.Equal(c.Labels, []string{"ui"}) || !slices.Equal(c.Assignees, []string{""}) || c.Search != "renewal" {
		t.Fatalf("criteria %+v", c)
	}
	if view := strings.Join(p.Lines(cells.Default, 60, 20, true), "\n"); !strings.Contains(view, "Label: [x] ui") || !strings.Contains(view, "Assignee: [x] unassigned") {
		t.Fatalf("toggles not shown:\n%s", view)
	}
	at(t, p, "Label: [x] ui")
	p.Do(keys.Open)
	if c := p.Criteria(); len(c.Labels) != 0 {
		t.Fatalf("label not toggled off: %+v", c)
	}
	at(t, p, "Search")
	if r := p.Do(keys.Open); !r.Search || r.Changed {
		t.Fatalf("search row: %+v", r)
	}
	at(t, p, "Clear all")
	if r := p.Do(keys.Open); !r.Changed || p.Criteria().Active() {
		t.Fatalf("clear: %+v %+v", r, p.Criteria())
	}
	if r := p.Do(keys.Cancel); !r.Close {
		t.Fatalf("cancel: %+v", r)
	}
}

// A selected label or assignee no loaded issue has any more stays offered,
// so it can be removed.
func TestPanelKeepsSelectedValuesThatNoIssueHas(t *testing.T) {
	p := NewPanel(Criteria{Labels: []string{"gone"}, Assignees: []string{"left"}}, issues())
	view := strings.Join(p.Lines(cells.Default, 60, 20, true), "\n")
	if !strings.Contains(view, "Label: [x] gone") || !strings.Contains(view, "Assignee: [x] left") {
		t.Fatalf("selected values missing:\n%s", view)
	}
}

// The panel fits its pane and scrolls to keep the selected row visible;
// styled, the selected row is in reverse video.
func TestPanelFitsAndScrolls(t *testing.T) {
	var many []protocol.IssueSummary
	for n := range 40 {
		many = append(many, summary(fmt.Sprint(n), "open", []string{fmt.Sprintf("label-%02d-with-a-long-name", n)}, "", false))
	}
	p := NewPanel(Criteria{}, many)
	fitLines(t, p.Lines(cells.Default, 24, 8, true), 24, 8)
	p.Do(keys.Bottom)
	lines := p.Lines(cells.Default, 24, 8, true)
	fitLines(t, lines, 24, 8)
	if !strings.HasPrefix(lines[7], ">") || !strings.Contains(lines[7], "Clear all") {
		t.Fatalf("the last row is not shown selected:\n%s", strings.Join(lines, "\n"))
	}
	p.Do(keys.PageUp)
	if lines := p.Lines(cells.Default, 24, 8, false); !strings.Contains(strings.Join(lines, "\n"), "\x1b[7m") {
		t.Fatal("styled selection not highlighted")
	}
}

func press(in *Input, km keys.Keymap, ks ...tea.KeyPressMsg) (submit, cancel bool) {
	for _, k := range ks {
		submit, cancel = in.Key(k, km)
	}
	return submit, cancel
}

func text(s string) []tea.KeyPressMsg {
	var out []tea.KeyPressMsg
	for _, r := range s {
		out = append(out, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return out
}

// The input owns typed text, including keys bound to actions elsewhere;
// backspace deletes, ctrl+u clears, enter submits and esc cancels; pasted
// line breaks become spaces.
func TestInputOwnsTypedText(t *testing.T) {
	km := keys.Default()
	in := NewInput("old")
	if in.Value() != "old" {
		t.Fatalf("initial %q", in.Value())
	}
	press(in, km, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if s, c := press(in, km, text("q/jk ?mxé")...); s || c || in.Value() != "q/jk ?mxé" {
		t.Fatalf("typed %q (submit %v, cancel %v)", in.Value(), s, c)
	}
	press(in, km, tea.KeyPressMsg{Code: tea.KeyBackspace}, tea.KeyPressMsg{Code: tea.KeyBackspace})
	in.Paste("a\r\nb")
	if in.Value() != "q/jk ?ma b" {
		t.Fatalf("after backspace and paste %q", in.Value())
	}
	press(in, km, tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}, tea.KeyPressMsg{Code: tea.KeyLeft})
	if in.Value() != "q/jk ?ma b" {
		t.Fatalf("control keys changed the text: %q", in.Value())
	}
	if s, c := press(in, km, tea.KeyPressMsg{Code: tea.KeyEnter}); !s || c {
		t.Fatalf("enter: submit %v cancel %v", s, c)
	}
	if s, c := press(in, km, tea.KeyPressMsg{Code: tea.KeyEscape}); s || !c {
		t.Fatalf("esc: submit %v cancel %v", s, c)
	}
}

// The input line fits its width, shows the end of a long text and the keys,
// and sanitizes what it shows.
func TestInputLine(t *testing.T) {
	km := keys.Default()
	in := NewInput("find \x1b[31m this")
	line := in.Line(cells.Default, 60, km)
	if cells.Width(cells.Default, line) != 60 || strings.Contains(line, "\x1b[31m") || !strings.Contains(line, "Search: find ␛[31m this") ||
		!strings.Contains(line, "enter search") || !strings.Contains(line, "esc cancel") {
		t.Fatalf("line %q", line)
	}
	in = NewInput(strings.Repeat("x", 100) + "END")
	if line := in.Line(cells.Default, 40, km); cells.Width(cells.Default, line) != 40 || !strings.Contains(line, "END") {
		t.Fatalf("long line %q", line)
	}
}
