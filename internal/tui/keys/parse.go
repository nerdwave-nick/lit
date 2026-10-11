package keys

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// modifiers in Bubble Tea's keystroke order.
var modifiers = []string{"ctrl", "alt", "shift", "meta", "hyper", "super"}

// names are the named keys, as Bubble Tea's keystrokes spell them.
var names = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range []string{"enter", "tab", "backspace", "esc", "space", "up", "down", "left", "right",
		"home", "end", "pgup", "pgdown", "insert", "delete"} {
		m[n] = true
	}
	for i := 1; i <= 20; i++ {
		m[fmt.Sprintf("f%d", i)] = true
	}
	return m
}()

// ParseKey returns the canonical name of a key such as "ctrl+k", "G" or
// "pgdown": modifiers (ctrl, alt, shift, meta, hyper, super) joined by "+",
// then one printable character or a key name. Modifier and key names ignore
// case; an upper-case letter means shift, and shift plus a letter alone is
// written as the upper-case letter.
func ParseKey(s string) (string, error) {
	switch s {
	case "":
		return "", errors.New("empty key")
	case " ":
		return "", errors.New(`write "space"`)
	}
	var parts []string
	switch {
	case s == "+":
		parts = []string{"+"}
	case strings.HasSuffix(s, "++"):
		parts = append(strings.Split(s[:len(s)-2], "+"), "+")
	default:
		parts = strings.Split(s, "+")
	}
	mods := map[string]bool{}
	for _, p := range parts[:len(parts)-1] {
		m := strings.ToLower(p)
		switch {
		case p == "":
			return "", errors.New("empty modifier")
		case !slices.Contains(modifiers, m):
			return "", fmt.Errorf("unknown modifier %q", p)
		case mods[m]:
			return "", fmt.Errorf("repeated modifier %q", m)
		}
		mods[m] = true
	}
	base := parts[len(parts)-1]
	var name string
	switch {
	case base == "":
		return "", errors.New("missing key after the modifiers")
	case utf8.RuneCountInString(base) == 1:
		r, _ := utf8.DecodeRuneInString(base)
		if r == ' ' {
			return "", errors.New(`write "space"`)
		}
		if !unicode.IsPrint(r) {
			return "", errors.New("not a printable character; use a key name")
		}
		if lower := unicode.ToLower(r); lower != r {
			mods["shift"], r = true, lower
		}
		if upper := unicode.ToUpper(r); mods["shift"] && len(mods) == 1 && upper != r {
			return string(upper), nil
		}
		name = string(r)
	default:
		name = strings.ToLower(base)
		if !names[name] {
			return "", fmt.Errorf("unknown key name %q", base)
		}
	}
	var b strings.Builder
	for _, m := range modifiers {
		if mods[m] {
			b.WriteString(m + "+")
		}
	}
	b.WriteString(name)
	return b.String(), nil
}

// IsShutdown reports whether key is the reserved Ctrl-C.
func IsShutdown(key tea.Key) bool { return key.Keystroke() == Shutdown }

// canonical returns the canonical names a pressed key may be bound under: its
// text (such as "G") and its keystroke (such as "shift+g" or "ctrl+d").
func canonical(key tea.Key) []string {
	var out []string
	for _, s := range []string{key.String(), key.Keystroke()} {
		if c, err := ParseKey(s); err == nil && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}
