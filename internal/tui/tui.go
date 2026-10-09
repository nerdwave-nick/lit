// Package tui is lit-tui, the terminal UI for people using lit. Only lit-tui
// links it: the Charm stack must stay out of lit and lit-server.
package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/prefs"
)

// Model is the root Bubble Tea model.
type Model struct {
	endpoint string
	prefs    prefs.Prefs
	help     bool // the help overlay is shown
}

// NewModel returns the root model for the service at endpoint, with the
// default preferences.
func NewModel(endpoint string) Model { return Model{endpoint: endpoint, prefs: prefs.Default()} }

// WithPrefs returns m using the effective preferences p.
func (m Model) WithPrefs(p prefs.Prefs) Model {
	m.prefs = p
	return m
}

// Prefs returns the effective preferences: theme, mouse and keymap.
func (m Model) Prefs() prefs.Prefs { return m.prefs }

func (m Model) Init() tea.Cmd { return nil }

// context is the keymap context of the focused view.
func (m Model) context() keys.Context {
	if m.help {
		return keys.Menu
	}
	return keys.Global
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	press, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if keys.IsShutdown(press.Key()) {
		return m, tea.Quit
	}
	action, _ := m.prefs.Keymap.Lookup(m.context(), press.Key())
	switch action {
	case keys.Quit:
		return m, tea.Quit
	case keys.Help:
		m.help = !m.help
	case keys.Cancel:
		m.help = false
	}
	return m, nil
}

func (m Model) View() tea.View {
	km := m.prefs.Keymap
	content := "lit-tui\n\nService: " + m.endpoint + "\n\n" + km.Hint(keys.Global, keys.Quit, keys.Help)
	if m.help {
		content = strings.Join(km.Help(keys.Global), "\n") + "\n\n" + km.Hint(keys.Menu, keys.Cancel)
	}
	v := tea.NewView(content)
	v.AltScreen = true
	if m.prefs.Mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

// Run runs the program until the user quits or ctx ends. Bubble Tea restores
// the terminal before Run returns.
func Run(ctx context.Context, m Model, opts ...tea.ProgramOption) error {
	_, err := tea.NewProgram(m, append([]tea.ProgramOption{tea.WithContext(ctx)}, opts...)...).Run()
	return err
}
