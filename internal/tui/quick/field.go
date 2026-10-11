package quick

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
)

// field is an inline text field. It owns typed text, including letters bound
// elsewhere (q, j, /, …): only the menu context's open (submit) and cancel
// keys, backspace and ctrl+u (clear) act on it.
type field struct {
	text []rune
}

// key handles a key press and reports whether it submitted or cancelled.
func (f *field) key(k tea.KeyPressMsg, km keys.Keymap) (submit, cancel bool) {
	if f.typed(k) {
		return false, false
	}
	switch a, _ := km.Lookup(keys.Menu, k.Key()); a {
	case keys.Open:
		return true, false
	case keys.Cancel:
		return false, true
	}
	return false, false
}

// typed edits the text with k: printable text, backspace or ctrl+u. It
// reports whether k was one of those.
func (f *field) typed(k tea.KeyPressMsg) bool {
	key := k.Key()
	switch {
	case key.Text != "" && key.Mod&^tea.ModShift == 0 && printable(key.Text):
		f.text = append(f.text, []rune(key.Text)...)
	case key.Code == tea.KeyBackspace && key.Mod == 0:
		if len(f.text) > 0 {
			f.text = f.text[:len(f.text)-1]
		}
	case key.Code == 'u' && key.Mod == tea.ModCtrl:
		f.text = nil
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

// paste inserts pasted text; line breaks become spaces.
func (f *field) paste(s string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(s)
	f.text = append(f.text, []rune(s)...)
}

func (f *field) value() string { return string(f.text) }
