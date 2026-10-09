package theme

import (
	"image/color"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/prefs"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render"
)

var (
	darkBg  = tea.BackgroundColorMsg{Color: color.RGBA{0x10, 0x10, 0x10, 0xff}}
	lightBg = tea.BackgroundColorMsg{Color: color.RGBA{0xf8, 0xf8, 0xf0, 0xff}}
)

func expect(t *testing.T, d *Detector, ready bool, style render.Style, source Source) {
	t.Helper()
	if d.Ready() != ready || ready && d.Style() != style || d.Source() != source {
		t.Fatalf("ready %v style %s source %s, want %v %s %s", d.Ready(), d.Style(), d.Source(), ready, style, source)
	}
}

func TestAutoDetectsTheBackground(t *testing.T) {
	for _, tc := range []struct {
		msg   tea.BackgroundColorMsg
		style render.Style
	}{{darkBg, render.Dark}, {lightBg, render.Light}} {
		d := New(prefs.ThemeAuto, DefaultTimeout)
		if d.Init() == nil {
			t.Fatal("auto must query the background")
		}
		expect(t, d, false, 0, Pending)
		if !d.Update(tc.msg) {
			t.Error("answer reported no change")
		}
		expect(t, d, true, tc.style, Detected)
		if d.Update(timeoutMsg{d.id}) {
			t.Error("timeout after the answer changed the style")
		}
		expect(t, d, true, tc.style, Detected)
	}
}

// With no answer the detector falls back to dark after the timeout, and a
// late answer still switches the style.
func TestTimeoutFallsBackToDarkAndALateAnswerWins(t *testing.T) {
	d := New(prefs.ThemeAuto, DefaultTimeout)
	other := New(prefs.ThemeAuto, DefaultTimeout)
	if d.Update(timeoutMsg{other.id}) {
		t.Error("another detector's timeout counted")
	}
	expect(t, d, false, 0, Pending)
	if !d.Update(timeoutMsg{d.id}) {
		t.Error("timeout reported no change")
	}
	expect(t, d, true, render.Dark, Fallback)
	if !d.Update(lightBg) {
		t.Error("late light answer reported no change")
	}
	expect(t, d, true, render.Light, Detected)
	if d.Update(lightBg) {
		t.Error("a repeated answer reported a change")
	}
}

// A late answer that agrees with the fallback changes nothing visible.
func TestLateAnswerMatchingTheFallbackIsNoChange(t *testing.T) {
	d := New(prefs.ThemeAuto, DefaultTimeout)
	d.Update(timeoutMsg{d.id})
	if d.Update(darkBg) {
		t.Error("late dark answer after the dark fallback reported a change")
	}
	expect(t, d, true, render.Dark, Detected)
}

func TestTheThemePreferenceWinsOverDetection(t *testing.T) {
	for _, tc := range []struct {
		pref  prefs.Theme
		style render.Style
		bg    tea.BackgroundColorMsg
	}{{prefs.ThemeLight, render.Light, darkBg}, {prefs.ThemeDark, render.Dark, lightBg}} {
		d := New(tc.pref, DefaultTimeout)
		if d.Init() != nil {
			t.Errorf("%s: a forced theme must not query", tc.pref)
		}
		expect(t, d, true, tc.style, Forced)
		if d.Update(tc.bg) {
			t.Errorf("%s: an answer changed a forced theme", tc.pref)
		}
		expect(t, d, true, tc.style, Forced)
	}
}

// No colour selects ASCII, whatever the background or preference.
func TestNoColourSelectsASCII(t *testing.T) {
	for _, tc := range []struct {
		name  string
		msg   tea.Msg
		ascii bool
	}{
		{"NO_COLOR=1", tea.EnvMsg{"TERM=xterm-256color", "NO_COLOR=1"}, true},
		{"NO_COLOR=yes", tea.EnvMsg{"NO_COLOR=yes"}, true},
		{"empty NO_COLOR", tea.EnvMsg{"NO_COLOR="}, false},
		{"no NO_COLOR", tea.EnvMsg{"TERM=xterm-256color"}, false},
		{"profile NoTTY (TERM=dumb)", tea.ColorProfileMsg{Profile: colorprofile.NoTTY}, true},
		{"profile Ascii", tea.ColorProfileMsg{Profile: colorprofile.Ascii}, true},
		{"profile ANSI256", tea.ColorProfileMsg{Profile: colorprofile.ANSI256}, false},
		{"profile TrueColor", tea.ColorProfileMsg{Profile: colorprofile.TrueColor}, false},
	} {
		for _, pref := range []prefs.Theme{prefs.ThemeDark, prefs.ThemeLight} {
			d := New(pref, DefaultTimeout)
			changed := d.Update(tc.msg)
			if got := d.Style() == render.ASCII; got != tc.ascii || changed != tc.ascii {
				t.Errorf("%s, %s: style %s changed %v, want ascii %v", tc.name, pref, d.Style(), changed, tc.ascii)
			}
		}
	}
	d := New(prefs.ThemeAuto, DefaultTimeout)
	d.Update(tea.EnvMsg{"NO_COLOR=1"})
	if d.Ready() {
		t.Error("NO_COLOR made auto ready before the background is known")
	}
	d.Update(lightBg)
	expect(t, d, true, render.ASCII, Detected)
}
