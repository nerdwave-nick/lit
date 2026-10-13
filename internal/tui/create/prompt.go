package create

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/lit/internal/tui/keys"
	"github.com/nerdwave-nick/lit/internal/tui/render/cells"
	"github.com/nerdwave-nick/lit/internal/tui/render/sanitize"
)

// Prompt is the inline title field of a new issue. It owns typed text,
// including letters bound elsewhere (q, j, /, …): only the menu context's
// open key (submit) and cancel key, Backspace and Ctrl-U (clear) act on it.
// It refuses, before anything is sent, a title lit-server would refuse for
// its form or because a loaded issue uses it. It belongs to the UI
// goroutine.
type Prompt struct {
	heading string
	info    []string
	taken   []string // titles of the loaded issues
	text    []rune
	note    string
}

// Typed is what a key press did: Submit with the trimmed Title, or Cancel.
type Typed struct {
	Title          string
	Submit, Cancel bool
}

// NewPrompt opens the prompt under heading, filled with title. taken are
// the titles of the loaded issues, which a new title must not repeat
// (ignoring case); info lines are shown under the field.
func NewPrompt(heading, title string, taken []string, info ...string) *Prompt {
	return &Prompt{heading: heading, info: info, taken: taken, text: []rune(title)}
}

// Key handles a key press.
func (p *Prompt) Key(k tea.KeyPressMsg, km keys.Keymap) Typed {
	if p.typed(k) {
		p.note = ""
		return Typed{}
	}
	switch a, _ := km.Lookup(keys.Menu, k.Key()); a {
	case keys.Cancel:
		return Typed{Cancel: true}
	case keys.Open:
		title := strings.TrimSpace(p.Value())
		if p.note = p.check(title); p.note != "" {
			return Typed{}
		}
		return Typed{Title: title, Submit: true}
	}
	return Typed{}
}

// check refuses a title of the wrong form or one a loaded issue uses.
func (p *Prompt) check(title string) string {
	if problem := titleProblem(title); problem != "" {
		return "Not a valid title: " + problem + "."
	}
	for _, t := range p.taken {
		if strings.EqualFold(t, title) {
			return "An issue titled “" + sanitize.Title(t) + "” exists in this project already: issue titles are unique, ignoring case."
		}
	}
	return ""
}

// typed edits the text with k: printable text, Backspace or Ctrl-U. It
// reports whether k was one of those.
func (p *Prompt) typed(k tea.KeyPressMsg) bool {
	key := k.Key()
	switch {
	case key.Text != "" && key.Mod&^tea.ModShift == 0 && printable(key.Text):
		p.text = append(p.text, []rune(key.Text)...)
	case key.Code == tea.KeyBackspace && key.Mod == 0:
		if len(p.text) > 0 {
			p.text = p.text[:len(p.text)-1]
		}
	case key.Code == 'u' && key.Mod == tea.ModCtrl:
		p.text = nil
	default:
		return false
	}
	return true
}

func printable(s string) bool {
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// Paste inserts pasted text; line breaks and tabs become spaces, and other
// control characters are dropped.
func (p *Prompt) Paste(s string) {
	s = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\t", " ").Replace(s)
	p.text = append(p.text, []rune(strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, s))...)
	p.note = ""
}

// Value is the typed text.
func (p *Prompt) Value() string { return string(p.text) }

// Lines draws the prompt in at most h lines of at most w cells: the
// heading, the field, the info lines and a refusal's note.
func (p *Prompt) Lines(method cells.Method, w, h int) []string {
	out := []string{p.heading, "", "Title: " + sanitize.Title(p.Value()) + "▏", ""}
	out = append(out, p.info...)
	if p.note != "" {
		out = append(out, "", p.note)
	}
	var lines []string
	for _, l := range out {
		lines = append(lines, wrap(method, l, w)...)
	}
	return lines[:min(len(lines), max(h, 0))]
}

// Hint names the prompt's keys.
func (p *Prompt) Hint(km keys.Keymap) string {
	return first(km, keys.Open) + " continue · " + first(km, keys.Cancel) + " cancel · backspace delete · ctrl+u clear"
}

func first(km keys.Keymap, a keys.Action) string {
	if k := km.Keys(keys.Menu, a); len(k) > 0 {
		return k[0]
	}
	return string(a)
}

// wrap breaks s into lines of at most w cells at spaces; a word longer than
// w is clipped.
func wrap(method cells.Method, s string, w int) []string {
	if w <= 0 || cells.Width(method, s) <= w {
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
			out = append(out, cells.Clip(method, line, w, "…"))
			line = word
		}
	}
	return append(out, cells.Clip(method, line, w, "…"))
}
