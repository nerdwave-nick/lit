// Package prefs loads lit-tui's preferences: flags, then environment, then the
// JSON config file, then defaults, as lit-server does. Changes apply on the
// next launch.
package prefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
)

// Theme selects the light or dark style. Auto uses background detection.
type Theme string

const (
	ThemeAuto  Theme = "auto"
	ThemeLight Theme = "light"
	ThemeDark  Theme = "dark"
)

// Environment variables read by Load.
const (
	EnvConfig = "LIT_TUI_CONFIG"
	EnvTheme  = "LIT_TUI_THEME"
	EnvMouse  = "LIT_TUI_MOUSE"
)

// Prefs are the effective preferences.
type Prefs struct {
	Theme  Theme
	Mouse  bool // click-to-select and wheel scrolling; off keeps native selection
	Keymap keys.Keymap
	File   string // the config file read, or "" when none was
}

// Default returns the preferences used without flags, environment or file.
func Default() Prefs { return Prefs{Theme: ThemeAuto, Keymap: keys.Default()} }

// Flags are lit-tui's preference flags; nil means not given.
type Flags struct {
	Config, Theme, Mouse *string
}

// file is the schema_version 1 config file.
type file struct {
	SchemaVersion int                            `json:"schema_version"`
	Theme         *string                        `json:"theme"`
	Mouse         *bool                          `json:"mouse"`
	Keymap        map[string]map[string][]string `json:"keymap"`
}

// DefaultPath returns ${XDG_CONFIG_HOME:-$HOME/.config}/lit/tui.json. A
// relative XDG_CONFIG_HOME is ignored.
func DefaultPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "lit", "tui.json"), nil
}

// Load returns the effective preferences and validates them. An explicitly
// selected config file (--config or LIT_TUI_CONFIG) must exist; the default
// one may be missing.
func Load(f Flags) (Prefs, error) {
	p := Default()
	path, explicit := os.Getenv(EnvConfig), true
	switch {
	case f.Config != nil:
		path = *f.Config
	case path == "":
		explicit = false
		path, _ = DefaultPath() // without a home directory there is no default file
	}
	if path != "" || explicit {
		if err := p.readFile(path, explicit); err != nil {
			return p, err
		}
	}
	if err := p.apply(EnvTheme, env(EnvTheme), EnvMouse, env(EnvMouse)); err != nil {
		return p, err
	}
	if err := p.apply("--theme", f.Theme, "--mouse", f.Mouse); err != nil {
		return p, err
	}
	return p, nil
}

// readFile applies the config file at path. A missing default file is no
// error; a missing explicit one is.
func (p *Prefs) readFile(path string, explicit bool) error {
	b, err := os.ReadFile(path)
	switch {
	case err != nil && !explicit && errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("config: %w", err)
	}
	fail := func(err error) error {
		lines := strings.Split(err.Error(), "\n")
		for i := range lines {
			lines[i] = "config " + path + ": " + lines[i]
		}
		return errors.New(strings.Join(lines, "\n"))
	}
	var c file
	if err := protocol.Decode(b, &c); err != nil {
		return fail(err)
	}
	if c.SchemaVersion != 1 {
		return fail(fmt.Errorf("unsupported schema_version %d (want 1)", c.SchemaVersion))
	}
	if c.Theme != nil {
		t, err := parseTheme(*c.Theme)
		if err != nil {
			return fail(fmt.Errorf("theme: %w", err))
		}
		p.Theme = t
	}
	if c.Mouse != nil {
		p.Mouse = *c.Mouse
	}
	k, err := keys.New(c.Keymap)
	if err != nil {
		return fail(err)
	}
	p.Keymap, p.File = k, path
	return nil
}

// apply overrides the theme and mouse preferences given by one source, named
// in errors; nil means not given.
func (p *Prefs) apply(themeSource string, theme *string, mouseSource string, mouse *string) error {
	if theme != nil {
		t, err := parseTheme(*theme)
		if err != nil {
			return fmt.Errorf("%s: %w", themeSource, err)
		}
		p.Theme = t
	}
	if mouse != nil {
		switch *mouse {
		case "true", "false":
			p.Mouse = *mouse == "true"
		default:
			return fmt.Errorf("%s: invalid value %q (want true or false)", mouseSource, *mouse)
		}
	}
	return nil
}

func parseTheme(s string) (Theme, error) {
	switch t := Theme(s); t {
	case ThemeAuto, ThemeLight, ThemeDark:
		return t, nil
	}
	return "", fmt.Errorf("invalid theme %q (want auto, light or dark)", s)
}

// env returns a set, non-empty environment variable, or nil.
func env(name string) *string {
	if v := os.Getenv(name); v != "" {
		return &v
	}
	return nil
}
