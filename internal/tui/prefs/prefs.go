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
	"time"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/keys"
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
	// EnvClaimIdle sets ClaimIdle, as a Go duration such as 30m.
	EnvClaimIdle = "LIT_TUI_CLAIM_IDLE"
	// EnvClaimLease sets ClaimLease, as a Go duration such as 15m.
	EnvClaimLease = "LIT_TUI_CLAIM_LEASE"
)

// DefaultClaimIdle is how long claims are renewed without input or a draft
// change before the TUI asks whether to keep holding them (Proposal).
// MinClaimIdle is the shortest accepted period.
const (
	DefaultClaimIdle = 30 * time.Minute
	MinClaimIdle     = time.Second
)

// DefaultClaimLease is how long each claim the TUI takes or renews lasts
// (Proposal); it is renewed after two thirds of it. A claim lasts at least
// MinClaimLease and at most the service's MaxClaimLease.
const (
	DefaultClaimLease = 15 * time.Minute
	MinClaimLease     = time.Second
	MaxClaimLease     = time.Hour
)

// Prefs are the effective preferences.
type Prefs struct {
	Theme  Theme
	Mouse  bool // click-to-select and wheel scrolling; off keeps native selection
	Keymap keys.Keymap
	File   string // the config file read, or "" when none was
	// ClaimIdle is how long held claims are renewed without activity before
	// renewal pauses and the TUI asks whether to keep holding them.
	ClaimIdle time.Duration
	// ClaimLease is the lease of each claim taken or renewed.
	ClaimLease time.Duration
}

// Default returns the preferences used without flags, environment or file.
func Default() Prefs {
	return Prefs{Theme: ThemeAuto, Keymap: keys.Default(), ClaimIdle: DefaultClaimIdle, ClaimLease: DefaultClaimLease}
}

// Flags are lit-tui's preference flags; nil means not given.
type Flags struct {
	Config, Theme, Mouse  *string
	ClaimIdle, ClaimLease *string
}

// file is the schema_version 1 config file.
type file struct {
	SchemaVersion int                            `json:"schema_version"`
	Theme         *string                        `json:"theme"`
	Mouse         *bool                          `json:"mouse"`
	Keymap        map[string]map[string][]string `json:"keymap"`
	ClaimIdle     *string                        `json:"claim_idle"`
	ClaimLease    *string                        `json:"claim_lease"`
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
	for _, src := range []struct {
		name  string
		value *string
	}{{EnvClaimIdle, env(EnvClaimIdle)}, {"--claim-idle", f.ClaimIdle}} {
		if err := p.applyClaimIdle(src.name, src.value); err != nil {
			return p, err
		}
	}
	for _, src := range []struct {
		name  string
		value *string
	}{{EnvClaimLease, env(EnvClaimLease)}, {"--claim-lease", f.ClaimLease}} {
		if err := p.applyClaimLease(src.name, src.value); err != nil {
			return p, err
		}
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
	if err := p.applyClaimIdle("claim_idle", c.ClaimIdle); err != nil {
		return fail(err)
	}
	if err := p.applyClaimLease("claim_lease", c.ClaimLease); err != nil {
		return fail(err)
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

// applyClaimIdle overrides ClaimIdle from one source, named in errors; nil
// means not given.
func (p *Prefs) applyClaimIdle(source string, value *string) error {
	return duration(source, value, MinClaimIdle, 0, &p.ClaimIdle)
}

// applyClaimLease overrides ClaimLease from one source.
func (p *Prefs) applyClaimLease(source string, value *string) error {
	return duration(source, value, MinClaimLease, MaxClaimLease, &p.ClaimLease)
}

// duration parses a Go duration of at least least and, unless most is 0,
// at most most into dst; nil means not given.
func duration(source string, value *string, least, most time.Duration, dst *time.Duration) error {
	if value == nil {
		return nil
	}
	d, err := time.ParseDuration(*value)
	switch {
	case err != nil:
		return fmt.Errorf("%s: invalid duration %q (want a Go duration such as 30m or 1h)", source, *value)
	case d < least:
		return fmt.Errorf("%s: %s is shorter than %s", source, *value, least)
	case most > 0 && d > most:
		return fmt.Errorf("%s: %s is longer than %s", source, *value, most)
	}
	*dst = d
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
