// Package tui is lit-tui, the terminal UI for people using lit. Only lit-tui
// links it: the Charm stack must stay out of lit and lit-server.
package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/detail"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/prefs"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/theme"
)

// Model is the root Bubble Tea model.
type Model struct {
	endpoint string
	prefs    prefs.Prefs
	help     bool // the help overlay is shown
	theme    *theme.Detector
	detail   *detail.Model
	doc      *detail.Section // shown in the detail viewport when set
	width    int
}

// NewModel returns the root model for the service at endpoint, with the
// default preferences.
func NewModel(endpoint string) Model { return Model{endpoint: endpoint}.WithPrefs(prefs.Default()) }

// WithPrefs returns m using the effective preferences p.
func (m Model) WithPrefs(p prefs.Prefs) Model {
	m.prefs = p
	m.theme = theme.New(p.Theme, theme.DefaultTimeout)
	m.detail = detail.New(render.NewService(render.DefaultCacheBytes), p.Keymap)
	return m
}

// WithDocument shows a Markdown document in the detail viewport. The issue
// detail (T10) replaces this with loaded issues; tests and demos use it now.
func (m Model) WithDocument(s detail.Section) Model {
	m.doc = &s
	return m
}

// Prefs returns the effective preferences: theme, mouse and keymap.
func (m Model) Prefs() prefs.Prefs { return m.prefs }

// Init starts background detection and, with a document, its render.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.theme.Init()}
	if m.theme.Ready() {
		cmds = append(cmds, m.detail.SetStyle(m.theme.Style()))
	}
	if m.doc != nil {
		cmds = append(cmds, m.detail.SetSections(*m.doc))
	}
	return tea.Batch(cmds...)
}

// context is the keymap context of the focused view.
func (m Model) context() keys.Context {
	if m.help {
		return keys.Menu
	}
	return m.view()
}

// view is the keymap context of the view under the help overlay.
func (m Model) view() keys.Context {
	if m.doc != nil {
		return keys.Detail
	}
	return keys.Global
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if m.theme.Update(msg) && m.theme.Ready() {
		cmds = append(cmds, m.detail.SetStyle(m.theme.Style()))
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		cmds = append(cmds, m.detail.SetSize(msg.Width, max(0, msg.Height-2)))
	case tea.KeyPressMsg:
		if keys.IsShutdown(msg.Key()) {
			return m, tea.Quit
		}
		action, _ := m.prefs.Keymap.Lookup(m.context(), msg.Key())
		switch action {
		case keys.Quit:
			return m, tea.Quit
		case keys.Help:
			m.help = !m.help
		case keys.Cancel:
			m.help = false
		default:
			if !m.help {
				cmds = append(cmds, m.detail.Update(msg))
			}
		}
	default:
		cmds = append(cmds, m.detail.Update(msg))
	}
	return m, tea.Batch(cmds...)
}

func (m Model) View() tea.View {
	km := m.prefs.Keymap
	content := "lit-tui\n\nService: " + m.endpoint + "\n\n" + km.Hint(keys.Global, keys.Quit, keys.Help)
	switch {
	case m.help:
		content = strings.Join(km.Help(m.view()), "\n") + "\n\n" + km.Hint(keys.Menu, keys.Cancel)
	case m.doc != nil:
		content = cells.Clip(cells.Default, "lit-tui · "+m.endpoint, m.width, "…") + "\n" + m.detail.View() + "\n" +
			cells.Clip(cells.Default, km.Hint(keys.Detail, keys.NextBlock, keys.Quit, keys.Help), m.width, "…")
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
