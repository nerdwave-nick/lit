package detail

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nerdwave-nick/lit/internal/tui/keys"
	"github.com/nerdwave-nick/lit/internal/tui/render"
	"github.com/nerdwave-nick/lit/internal/tui/render/cells"
)

// harness drives a Model without a terminal. Commands run synchronously
// (renders included); timers are recorded and fired explicitly.
type harness struct {
	t      *testing.T
	m      *Model
	svc    *render.Service
	timers []tea.Msg
}

func newHarness(t *testing.T, w, h int, style render.Style, method cells.Method) *harness {
	t.Helper()
	return newHarnessWith(t, w, h, style, method, keys.Default())
}

func newHarnessWith(t *testing.T, w, h int, style render.Style, method cells.Method, km keys.Keymap) *harness {
	t.Helper()
	svc := render.NewService(render.DefaultCacheBytes)
	x := &harness{t: t, m: New(svc, km), svc: svc}
	x.m.after = func(_ time.Duration, msg tea.Msg) tea.Cmd {
		x.timers = append(x.timers, msg)
		return nil
	}
	x.m.SetMethod(method)
	x.run(x.m.SetStyle(style))
	x.run(x.m.SetSize(w, h))
	return x
}

// collect runs cmd and every command in its batches and returns the
// messages without delivering them.
func (x *harness) collect(cmd tea.Cmd) []tea.Msg {
	var out []tea.Msg
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
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
			out = append(out, msg)
		}
	}
	return out
}

// deliver hands msgs to the model and runs whatever follows.
func (x *harness) deliver(msgs ...tea.Msg) {
	for _, msg := range msgs {
		x.run(x.m.Update(msg))
	}
}

// run runs cmd to completion, delivering every message it produces.
func (x *harness) run(cmd tea.Cmd) { x.deliver(x.collect(cmd)...) }

// fire delivers every recorded timer of type T.
func fire[T tea.Msg](x *harness) int {
	var keep, due []tea.Msg
	for _, msg := range x.timers {
		if _, ok := msg.(T); ok {
			due = append(due, msg)
		} else {
			keep = append(keep, msg)
		}
	}
	x.timers = keep
	x.deliver(due...)
	return len(due)
}

// show sets the sections and runs the renders and every timer they start.
func (x *harness) show(secs ...Section) {
	x.run(x.m.SetSections(secs...))
	x.settle()
}

// resize resizes and settles the debounce and renders.
func (x *harness) resize(w, h int) {
	x.run(x.m.SetSize(w, h))
	x.settle()
}

func (x *harness) settle() {
	for fire[debounceMsg](x)+fire[placeholderMsg](x) > 0 {
	}
}

var keyNames = map[string]tea.KeyPressMsg{
	"up": {Code: tea.KeyUp}, "down": {Code: tea.KeyDown}, "left": {Code: tea.KeyLeft}, "right": {Code: tea.KeyRight},
	"pgup": {Code: tea.KeyPgUp}, "pgdown": {Code: tea.KeyPgDown}, "home": {Code: tea.KeyHome}, "end": {Code: tea.KeyEnd},
	"tab": {Code: tea.KeyTab}, "shift+tab": {Code: tea.KeyTab, Mod: tea.ModShift},
}

func (x *harness) press(names ...string) {
	for _, n := range names {
		k, ok := keyNames[n]
		if !ok {
			r := []rune(n)[0]
			k = tea.KeyPressMsg{Code: r, Text: n}
		}
		x.run(x.m.Update(k))
	}
}

func (x *harness) lines() []string { return strings.Split(x.m.View(), "\n") }

func (x *harness) plain() []string {
	ls := x.lines()
	for i, l := range ls {
		ls[i] = ansi.Strip(l)
	}
	return ls
}

func (x *harness) top() string { return strings.TrimSpace(x.plain()[0]) }

// scrollUntil presses down until the top line satisfies ok.
func (x *harness) scrollUntil(ok func(string) bool) {
	x.t.Helper()
	for i := 0; i < 2000; i++ {
		if ok(x.top()) {
			return
		}
		x.press("down")
	}
	x.t.Fatalf("never reached the wanted top line; view:\n%s", strings.Join(x.plain(), "\n"))
}

// checkFits fails unless the view has exactly h lines, each exactly w cells.
func (x *harness) checkFits(what string, w, h int, method cells.Method) {
	x.t.Helper()
	ls := x.lines()
	if len(ls) != h {
		x.t.Fatalf("%s: %d lines, want %d", what, len(ls), h)
	}
	for i, l := range ls {
		if got := cells.Width(method, l); got != w {
			x.t.Fatalf("%s: line %d is %d cells, want %d: %q", what, i, got, w, ansi.Strip(l))
		}
	}
}

// digits returns n cells of a non-repeating sequence: 000.001.002…
func digits(n int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "%03d.", i)
	}
	return b.String()[:n]
}
