// Package tui is lit-tui, the terminal UI for people using lit. Only lit-tui
// links it: the Charm stack must stay out of lit and lit-server.
package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// Model is the root Bubble Tea model.
type Model struct {
	endpoint string
}

// NewModel returns the root model for the service at endpoint.
func NewModel(endpoint string) Model { return Model{endpoint: endpoint} }

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m Model) View() tea.View {
	v := tea.NewView("lit-tui\n\nService: " + m.endpoint + "\n\nq quit")
	v.AltScreen = true
	return v
}

// Run runs the program until the user quits or ctx ends. Bubble Tea restores
// the terminal before Run returns.
func Run(ctx context.Context, m Model, opts ...tea.ProgramOption) error {
	_, err := tea.NewProgram(m, append([]tea.ProgramOption{tea.WithContext(ctx)}, opts...)...).Run()
	return err
}
