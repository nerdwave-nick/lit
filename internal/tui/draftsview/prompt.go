package draftsview

import (
	"strconv"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/sanitize"
)

// Choice is an answer to the quit prompt.
type Choice int

const (
	// ApplyNow cancels quitting and previews the first open draft's apply.
	ApplyNow Choice = iota
	// Quit quits: the drafts are kept and the claims released.
	Quit
	// Cancel keeps running.
	Cancel
)

var choiceLabels = []string{ApplyNow: "Apply now", Quit: "Quit", Cancel: "Cancel"}

// Prompt is the one prompt shown when quitting with open drafts.
type Prompt struct {
	open   []drafts.Draft
	cursor Choice
}

// NewPrompt asks about quitting with the open drafts, oldest first.
func NewPrompt(open []drafts.Draft) *Prompt { return &Prompt{open: open} }

// Drafts are the open drafts the prompt names.
func (p *Prompt) Drafts() []drafts.Draft { return append([]drafts.Draft(nil), p.open...) }

// Message is the prompt's question: "N open draft(s) (titles). Drafts are
// kept; claims will be released."
func (p *Prompt) Message() string {
	titles := make([]string, len(p.open))
	for i, d := range p.open {
		titles[i] = "“" + sanitize.Title(d.Title) + "”"
	}
	what := "1 open draft"
	if len(p.open) != 1 {
		what = strconv.Itoa(len(p.open)) + " open drafts"
	}
	return what + " (" + strings.Join(titles, ", ") + "). Drafts are kept; claims will be released."
}

// Do performs a menu-context action; it reports the choice once made. The
// quit action chooses Quit and cancel chooses Cancel.
func (p *Prompt) Do(a keys.Action) (Choice, bool) {
	switch a {
	case keys.Up, keys.Top:
		p.cursor = max(p.cursor-1, ApplyNow)
		if a == keys.Top {
			p.cursor = ApplyNow
		}
	case keys.Down, keys.Bottom:
		p.cursor = min(p.cursor+1, Cancel)
		if a == keys.Bottom {
			p.cursor = Cancel
		}
	case keys.Open:
		return p.cursor, true
	case keys.Quit:
		return Quit, true
	case keys.Cancel:
		return Cancel, true
	}
	return 0, false
}

// View draws the prompt as lines of at most w cells: the question, the
// choices "[Apply now] [Quit] [Cancel]" with the selected one highlighted
// (named on its own line when plain), and the key hint.
func (p *Prompt) View(method cells.Method, w int, km keys.Keymap, plain bool) string {
	buttons := make([]string, len(choiceLabels))
	for i, l := range choiceLabels {
		buttons[i] = "[" + l + "]"
		if Choice(i) == p.cursor && !plain {
			buttons[i] = "\x1b[7m" + buttons[i] + "\x1b[m"
		}
	}
	out := wrap(method, p.Message(), w)
	out = append(out, "", strings.Join(buttons, " "))
	if plain {
		out = append(out, "> "+choiceLabels[p.cursor])
	}
	out = append(out, "", clip(method, km.Hint(keys.Menu, keys.Open, keys.Cancel), w))
	return strings.Join(out, "\n")
}

// wrap breaks s at spaces into lines of at most w cells.
func wrap(method cells.Method, s string, w int) []string {
	if w <= 0 {
		return []string{s}
	}
	var out []string
	line := ""
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case cells.Width(method, line+" "+word) <= w:
			line += " " + word
		default:
			out = append(out, clip(method, line, w))
			line = word
		}
	}
	return append(out, clip(method, line, w))
}
