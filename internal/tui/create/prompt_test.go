package create

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/tui/keys"
	"github.com/nerdwave-nick/lit/internal/tui/render/cells"
)

func press(p *Prompt, km keys.Keymap, ks ...string) Typed {
	var r Typed
	for _, k := range ks {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "backspace":
			msg = tea.KeyPressMsg{Code: tea.KeyBackspace}
		case "ctrl+u":
			msg = tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
		default:
			msg = tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
		}
		r = p.Key(msg, km)
	}
	return r
}

func typeText(p *Prompt, km keys.Keymap, s string) {
	for _, r := range s {
		press(p, km, string(r))
	}
}

func screen(p *Prompt) string {
	return strings.Join(p.Lines(cells.Default, 200, 12), "\n")
}

// The prompt owns typed text, including letters bound elsewhere (q, j, /);
// Backspace deletes, Ctrl-U clears, a paste joins its lines, and Enter
// submits the trimmed title.
func TestThePromptOwnsTypedTextAndSubmitsTheTrimmedTitle(t *testing.T) {
	km := keys.Default()
	p := NewPrompt("New issue in “test/one”", "", nil, "Enter opens your editor.")
	typeText(p, km, "qj/ Fix")
	press(p, km, "backspace", "backspace", "backspace")
	if p.Value() != "qj/ " {
		t.Fatalf("value %q", p.Value())
	}
	press(p, km, "ctrl+u")
	p.Paste("  Fix the\nparser ")
	if r := press(p, km, "enter"); !r.Submit || r.Cancel || r.Title != "Fix the parser" {
		t.Fatalf("submit %+v", r)
	}
	if s := screen(p); !strings.Contains(s, "New issue in “test/one”") || !strings.Contains(s, "Title:   Fix the parser ▏") || !strings.Contains(s, "Enter opens your editor.") {
		t.Fatalf("screen:\n%s", s)
	}
	if r := press(p, km, "esc"); !r.Cancel || r.Submit {
		t.Fatalf("cancel %+v", r)
	}
}

// Enter refuses, with a note and without submitting, a title lit-server
// would refuse: empty, too long, or used by a loaded issue (titles are
// unique in a project, ignoring case).
func TestThePromptRefusesTitlesTheServiceWouldRefuse(t *testing.T) {
	km := keys.Default()
	for _, c := range []struct{ typed, note string }{
		{"   ", "an issue needs a title"},
		{strings.Repeat("x", MaxTitle+1), "at most 128 characters"},
		{"root ISSUE", "“Root issue” exists in this project already"},
	} {
		p := NewPrompt("New issue", "", []string{"Root issue", "Other"})
		typeText(p, km, c.typed)
		if r := press(p, km, "enter"); r.Submit {
			t.Errorf("%q submitted", c.typed)
		}
		if s := screen(p); !strings.Contains(s, c.note) {
			t.Errorf("%q: no note %q:\n%s", c.typed, c.note, s)
		}
		typeText(p, km, "x")
		if s := screen(p); strings.Contains(s, c.note) {
			t.Errorf("%q: the note stays after typing", c.typed)
		}
	}
	p := NewPrompt("Change the title", "Kept title", []string{"Other"})
	if r := press(p, km, "enter"); !r.Submit || r.Title != "Kept title" {
		t.Fatalf("an unchanged title of a kept draft: %+v", r)
	}
}

// Control characters typed or pasted never reach the screen, and CheckTitle
// refuses them.
func TestThePromptShowsNoControlCharacters(t *testing.T) {
	p := NewPrompt("New issue", "", nil)
	p.Paste("a\x1b[2Jb\u202e")
	if s := screen(p); strings.ContainsAny(s, "\x1b\u202e") {
		t.Fatalf("control characters on screen: %q", s)
	}
	if err := CheckTitle("a\x07b"); litclient.Code(err) != "validation_failed" {
		t.Fatalf("check: %v", err)
	}
	if err := CheckTitle(" padded"); litclient.Code(err) != "validation_failed" {
		t.Fatalf("check: %v", err)
	}
}
