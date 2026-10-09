package tui

import (
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/detail"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/prefs"
)

// drive delivers msgs and runs every command they produce, as Bubble Tea
// would, timers included (they sleep). Messages Bubble Tea answers itself,
// such as the background query, go nowhere.
func drive(t *testing.T, m tea.Model, cmd tea.Cmd, msgs ...tea.Msg) tea.Model {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	queue := []tea.Cmd{cmd}
	for _, msg := range msgs {
		queue = append(queue, func() tea.Msg { return msg })
	}
	for len(queue) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("commands did not settle")
		}
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, msg...)
		default:
			var next tea.Cmd
			m, next = m.Update(msg)
			queue = append(queue, next)
		}
	}
	return m
}

const demoBody = "# Demo heading\n\nSome prose.\n\n```text\n" + "wide-code-line-" + "0123456789abcdefghij0123456789abcdefghij0123456789abcdefghij0123456789abcdefghij0123456789" + "\n```\n"

func demoModel(theme prefs.Theme) Model {
	p := prefs.Default()
	p.Theme = theme
	return NewModel("http://127.0.0.1:1").WithPrefs(p).WithDocument(detail.Section{RecordID: "demo", Revision: 1, Content: demoBody})
}

func plainView(m tea.Model) string { return ansi.Strip(m.View().Content) }

func TestModelShowsADocumentInTheDetailViewport(t *testing.T) {
	m := demoModel(prefs.ThemeDark)
	got := drive(t, m, m.Init(), tea.WindowSizeMsg{Width: 60, Height: 12})
	view := plainView(got)
	if !strings.Contains(view, "Demo heading") || !strings.Contains(view, "wide-code-line") || !strings.Contains(view, "http://127.0.0.1:1") {
		t.Fatalf("view:\n%s", view)
	}
	if lines := strings.Split(got.View().Content, "\n"); len(lines) != 12 {
		t.Errorf("%d lines, want 12:\n%s", len(lines), view)
	}
	// The detail keys reach the viewport: right scrolls the wide code block.
	// (Model copies share the viewport, so view is captured first.)
	scrolled := drive(t, got, nil, tea.KeyPressMsg{Code: tea.KeyRight})
	if plainView(scrolled) == view || !strings.Contains(plainView(scrolled), "‹") {
		t.Errorf("right did not scroll the code block:\n%s", plainView(scrolled))
	}
	if _, cmd := scrolled.Update(tea.KeyPressMsg{Code: 'q', Text: "q"}); !quits(cmd) {
		t.Error("q does not quit with a document shown")
	}
}

// With theme auto, nothing renders until the terminal answers or the
// timeout passes; a late answer re-renders in its style.
func TestModelRendersAfterBackgroundDetection(t *testing.T) {
	m := demoModel(prefs.ThemeAuto)
	start := time.Now()
	dark := drive(t, m, m.Init(), tea.WindowSizeMsg{Width: 60, Height: 12})
	if time.Since(start) < 250*time.Millisecond || !strings.Contains(plainView(dark), "Demo heading") {
		t.Fatalf("the fallback did not wait for the timeout (%v):\n%s", time.Since(start), plainView(dark))
	}
	darkView, darkPlain := dark.View().Content, plainView(dark)
	light := drive(t, dark, nil, tea.BackgroundColorMsg{Color: color.White})
	if light.View().Content == darkView || plainView(light) != darkPlain {
		t.Errorf("a late light answer did not re-render in the light style:\n%q\n%q", darkView, light.View().Content)
	}
}

// A forced theme wins over the terminal's answer.
func TestModelThemePreferenceWinsOverDetection(t *testing.T) {
	m := demoModel(prefs.ThemeDark)
	dark := drive(t, m, m.Init(), tea.WindowSizeMsg{Width: 60, Height: 12})
	darkView := dark.View().Content
	after := drive(t, dark, nil, tea.BackgroundColorMsg{Color: color.White})
	if after.View().Content != darkView {
		t.Error("a light answer changed a forced dark theme")
	}
}

// Help describes the view under the overlay: the detail keys with a document.
func TestModelHelpDescribesTheDetailView(t *testing.T) {
	m := demoModel(prefs.ThemeDark)
	shown := drive(t, m, m.Init(), tea.WindowSizeMsg{Width: 80, Height: 40}, tea.KeyPressMsg{Code: '?', Text: "?"})
	view := plainView(shown)
	if !strings.Contains(view, "Detail") || !strings.Contains(view, "focus the next wide block") || strings.Contains(view, "choose the item") {
		t.Errorf("help:\n%s", view)
	}
}
