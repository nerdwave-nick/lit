package detail

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render"
)

func (x *harness) shows(s string) bool { return strings.Contains(strings.Join(x.plain(), "\n"), s) }

func renders(msgs []tea.Msg) []tea.Msg {
	var out []tea.Msg
	for _, m := range msgs {
		if _, ok := m.(renderedMsg); ok {
			out = append(out, m)
		}
	}
	return out
}

// The placeholder appears only once a render has been pending for the delay.
func TestPlaceholderOnlyAfterTheDelay(t *testing.T) {
	x := newHarness(t, 80, 10, render.Dark, ansi.WcWidth)
	results := renders(x.collect(x.m.SetSections(doc("# Slow body"))))
	if len(results) != 1 {
		t.Fatalf("renders %v", results)
	}
	if x.shows(Placeholder) || !x.m.Pending() {
		t.Fatalf("placeholder before the delay, or not pending:\n%s", x.m.View())
	}
	if fire[placeholderMsg](x) != 1 || !x.shows(Placeholder) {
		t.Fatalf("no placeholder after the delay:\n%s", x.m.View())
	}
	x.deliver(results...)
	if x.shows(Placeholder) || !x.shows("Slow body") || x.m.Pending() {
		t.Fatalf("after the render:\n%s", x.m.View())
	}

	// A render that finishes within the delay never shows the placeholder.
	x.run(x.m.SetSections(Section{RecordID: "issue-2", Revision: 1, Content: "# Fast body"}))
	fire[placeholderMsg](x)
	if x.shows(Placeholder) || !x.shows("Fast body") {
		t.Fatalf("fast render:\n%s", x.m.View())
	}
}

// Until the style is known nothing renders, but the placeholder still
// appears after the delay.
func TestNothingRendersBeforeTheStyleIsKnown(t *testing.T) {
	svc := render.NewService(render.DefaultCacheBytes)
	x := &harness{t: t, m: New(svc, keys.Default()), svc: svc}
	x.m.after = func(_ time.Duration, msg tea.Msg) tea.Cmd { x.timers = append(x.timers, msg); return nil }
	x.run(x.m.SetSize(80, 10))
	if msgs := renders(x.collect(x.m.SetSections(doc("# Body")))); len(msgs) != 0 {
		t.Fatalf("rendered before the style was set: %v", msgs)
	}
	fire[placeholderMsg](x)
	if !x.shows(Placeholder) {
		t.Fatalf("no placeholder while waiting for the style:\n%s", x.m.View())
	}
	x.run(x.m.SetStyle(render.Light))
	if !x.shows("Body") || x.shows(Placeholder) {
		t.Fatalf("after the style was set:\n%s", x.m.View())
	}
}

// Results for a width, style or record no longer wanted are dropped.
func TestStaleResultsAreDiscarded(t *testing.T) {
	x := newHarness(t, 80, 30, render.Dark, ansi.WcWidth)
	x.show(doc(sectionsBody(3)))

	x.run(x.m.SetSize(130, 30))
	r130 := renders(x.collect(firstTimer(x)))
	x.run(x.m.SetSize(140, 30))
	r140 := renders(x.collect(firstTimer(x)))
	if len(r130) != 1 || len(r140) != 1 {
		t.Fatalf("renders %v %v", r130, r140)
	}
	x.deliver(r140...)
	want := x.m.View()
	x.deliver(r130...)
	if x.m.View() != want {
		t.Errorf("a stale 130-column result replaced the 140-column one")
	}
	job := render.Job{RecordID: "issue-1", Revision: 1, Content: sectionsBody(3), Width: 130, Style: render.Dark}
	if _, ok := x.svc.Lookup(job); !ok {
		t.Error("the stale result is not cached")
	}

	old := renders(x.collect(x.m.SetSections(Section{RecordID: "issue-a", Revision: 1, Content: "# Record A"})))
	x.show(Section{RecordID: "issue-b", Revision: 1, Content: "# Record B"})
	x.deliver(old...)
	if x.shows("Record A") || !x.shows("Record B") {
		t.Errorf("a result for the previous record was shown:\n%s", x.m.View())
	}
}

// firstTimer fires the pending debounce and returns the command it starts
// without running it.
func firstTimer(x *harness) tea.Cmd {
	for i, msg := range x.timers {
		if _, ok := msg.(debounceMsg); ok {
			x.timers = append(x.timers[:i:i], x.timers[i+1:]...)
			return x.m.Update(msg)
		}
	}
	x.t.Fatal("no debounce timer")
	return nil
}

// Resizes are debounced: only the last width of a burst is rendered, while
// the previous render stays on screen clipped to the new width. A width
// already cached shows at once.
func TestResizeIsDebounced(t *testing.T) {
	x := newHarness(t, 80, 10, render.Dark, ansi.WcWidth)
	x.show(doc(sectionsBody(3)))
	for _, w := range []int{90, 100, 110} {
		if msgs := renders(x.collect(x.m.SetSize(w, 10))); len(msgs) != 0 {
			t.Fatalf("resize to %d rendered at once", w)
		}
		x.checkFits("stale", w, 10, ansi.WcWidth)
		if !x.shows("Section 0") {
			t.Fatalf("the previous render is not shown while debouncing:\n%s", x.m.View())
		}
	}
	var rendered []tea.Msg
	for len(x.timers) > 0 {
		rendered = append(rendered, renders(x.collect(firstTimer(x)))...)
	}
	if len(rendered) != 1 || rendered[0].(renderedMsg).res.Key.Width != 110 {
		t.Fatalf("debounced renders %v", rendered)
	}
	x.deliver(rendered...)
	if x.m.Pending() {
		t.Fatal("still pending")
	}
	x.run(x.m.SetSize(80, 10))
	if x.m.Pending() || len(x.timers) != 0 {
		t.Errorf("a cached width is pending or debounced: %v", x.timers)
	}
}

// Several sections (a body and its comments) render independently into one
// scrolling view, and the anchor follows content across sections.
func TestSectionsRenderIntoOneView(t *testing.T) {
	x := newHarness(t, 80, 10, render.Dark, ansi.WcWidth)
	body := Section{RecordID: "issue-1", Revision: 1, Content: sectionsBody(5)}
	comment := Section{RecordID: "comment-1", Revision: 1, Content: "Comment text with a **bold** word.\n\n" + strings.Repeat("More comment.\n\n", 12)}
	x.show(body)
	x.show(body, comment)
	x.press("end")
	if !x.shows("More comment") {
		t.Fatalf("the comment is not shown:\n%s", x.m.View())
	}
	x.press("home")
	x.scrollUntil(func(l string) bool { return strings.Contains(l, "Comment text") })
	longer := body
	longer.Revision, longer.Content = 2, sectionsBody(8)
	x.show(longer, comment)
	if !strings.Contains(x.top(), "Comment text") {
		t.Errorf("the anchor in the comment was lost: %q", x.top())
	}
}
