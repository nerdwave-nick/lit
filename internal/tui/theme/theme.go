// Package theme decides lit-tui's render style: it detects the terminal
// background with tea.RequestBackgroundColor, falls back to dark after a
// timeout, follows a late answer, and lets the theme preference win over
// detection. Without colour (NO_COLOR, or a colour profile with no colours
// such as TERM=dumb) it selects the ASCII style.
package theme

import (
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/prefs"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render"
)

// DefaultTimeout is how long the detector waits for the terminal to report
// its background before it falls back to dark.
const DefaultTimeout = 300 * time.Millisecond

// Source says where the current background decision comes from.
type Source uint8

const (
	Pending  Source = iota // still waiting for the terminal
	Forced                 // the theme preference
	Detected               // the terminal's answer
	Fallback               // no answer before the timeout: dark
)

func (s Source) String() string { return [...]string{"pending", "forced", "detected", "fallback"}[s] }

// Detector tracks the background and colour support of one program. It
// belongs to the UI goroutine.
type Detector struct {
	id      int64
	timeout time.Duration
	dark    bool
	source  Source
	noColor bool
}

type timeoutMsg struct{ id int64 }

var ids atomic.Int64

// New returns a detector for the theme preference. auto detects; light and
// dark are forced and ready at once.
func New(pref prefs.Theme, timeout time.Duration) *Detector {
	d := &Detector{id: ids.Add(1), timeout: timeout, dark: true}
	switch pref {
	case prefs.ThemeLight:
		d.dark, d.source = false, Forced
	case prefs.ThemeDark:
		d.source = Forced
	}
	return d
}

// Init starts detection: the background query and its timeout. It returns
// nil when the theme is forced.
func (d *Detector) Init() tea.Cmd {
	if d.source == Forced {
		return nil
	}
	id := d.id
	return tea.Batch(tea.RequestBackgroundColor, tea.Tick(d.timeout, func(time.Time) tea.Msg { return timeoutMsg{id} }))
}

// Update handles the background answer, the timeout, the colour profile and
// the environment. It reports whether Style or Ready changed, so the caller
// re-renders.
func (d *Detector) Update(msg tea.Msg) bool {
	ready, style := d.Ready(), d.Style()
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		if d.source != Forced {
			d.dark, d.source = msg.IsDark(), Detected
		}
	case timeoutMsg:
		if msg.id == d.id && d.source == Pending {
			d.source = Fallback
		}
	case tea.ColorProfileMsg:
		if msg.Profile == colorprofile.NoTTY || msg.Profile == colorprofile.Ascii {
			d.noColor = true
		}
	case tea.EnvMsg:
		if msg.Getenv("NO_COLOR") != "" { // https://no-color.org: present and not empty
			d.noColor = true
		}
	}
	return d.Ready() != ready || d.Style() != style
}

// Ready reports whether the style is decided: forced, answered or timed out.
func (d *Detector) Ready() bool { return d.source != Pending }

// Style is the render style to use once Ready.
func (d *Detector) Style() render.Style { return render.StyleFor(d.dark, d.noColor) }

// Source says where the background decision comes from.
func (d *Detector) Source() Source { return d.source }
