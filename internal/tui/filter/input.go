package filter

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/lit/internal/tui/keys"
	"github.com/nerdwave-nick/lit/internal/tui/render/cells"
	"github.com/nerdwave-nick/lit/internal/tui/render/sanitize"
)

// Input edits the search text. It owns typed text, including letters bound
// elsewhere (q, /, j, …): only the menu context's open (submit) and cancel
// keys, backspace and ctrl+u (clear) act on it.
type Input struct {
	text []rune
}

// NewInput starts editing text.
func NewInput(text string) *Input { return &Input{text: []rune(text)} }

// Key handles a key press and reports whether it submitted or cancelled.
func (in *Input) Key(k tea.KeyPressMsg, km keys.Keymap) (submit, cancel bool) {
	key := k.Key()
	if key.Text != "" && key.Mod&^tea.ModShift == 0 && printable(key.Text) {
		in.text = append(in.text, []rune(key.Text)...)
		return false, false
	}
	switch a, _ := km.Lookup(keys.Menu, key); {
	case a == keys.Open:
		return true, false
	case a == keys.Cancel:
		return false, true
	case key.Code == tea.KeyBackspace && key.Mod == 0:
		if len(in.text) > 0 {
			in.text = in.text[:len(in.text)-1]
		}
	case key.Code == 'u' && key.Mod == tea.ModCtrl:
		in.text = nil
	}
	return false, false
}

func printable(s string) bool {
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// Paste inserts pasted text; line breaks become spaces.
func (in *Input) Paste(s string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
	in.text = append(in.text, []rune(s)...)
}

// Value is the text.
func (in *Input) Value() string { return string(in.text) }

// Line draws the input in exactly w cells, keeping its end visible.
func (in *Input) Line(method cells.Method, w int, km keys.Keymap) string {
	hint := "  " + first(km, keys.Open) + " search · " + first(km, keys.Cancel) + " cancel"
	prompt, text := "Search: ", sanitize.Title(string(in.text))+"▏"
	room := w - cells.Width(method, prompt) - cells.Width(method, hint)
	if tw := cells.Width(method, text); room > 1 && tw > room {
		text = "…" + cells.Cut(method, text, tw-(room-1), tw)
	}
	return fit(method, prompt+text+hint, w)
}

func first(km keys.Keymap, a keys.Action) string {
	if k := km.Keys(keys.Menu, a); len(k) > 0 {
		return k[0]
	}
	return string(a)
}
