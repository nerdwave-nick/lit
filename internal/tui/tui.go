// Package tui is lit-tui, the terminal UI for people using lit. Only lit-tui
// links it: the Charm stack must stay out of lit and lit-server.
package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/detail"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/history"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/lifecycle"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/prefs"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/theme"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/uncertain"
)

// Model is the root Bubble Tea model.
type Model struct {
	endpoint      string
	prefs         prefs.Prefs
	help          bool // the help overlay is shown
	theme         *theme.Detector
	detail        *detail.Model
	doc           *detail.Section // shown in the detail viewport when set
	width, height int
	life          lifecycle.UI // session line, recovery notes and exit prompts
	b             *browser     // the project tree; nil without a lifecycle
	l             *lifecycle.Lifecycle
	uv            *uncertain.Model // the uncertain-requests view, when open
	hv            *history.Model   // the history view, when open
	cl            *claimsView      // held claims and the idle question; nil without a holder
	ed            *editing         // body drafts and the draft panel; nil without an editor
	dv            *draftsState     // the Drafts view, launch banner and quit prompt; nil without an editor
}

// NewModel returns the root model for the service at endpoint, with the
// default preferences.
func NewModel(endpoint string) Model { return Model{endpoint: endpoint}.WithPrefs(prefs.Default()) }

// WithPrefs returns m using the effective preferences p.
func (m Model) WithPrefs(p prefs.Prefs) Model {
	m.prefs = p
	m.theme = theme.New(p.Theme, theme.DefaultTimeout)
	m.detail = detail.New(render.NewService(render.DefaultCacheBytes), p.Keymap)
	if m.b != nil {
		m.b = newBrowser(m.b.src, p.Keymap)
		m.wireClaims()
	}
	return m
}

// WithDocument shows a Markdown document in the detail viewport instead of
// the project tree. Tests and demos use it.
func (m Model) WithDocument(s detail.Section) Model {
	m.doc = &s
	return m
}

// WithLifecycle routes quit and Ctrl-C into l's orderly shutdown, shows its
// status, recovery notes and exit prompts, and browses projects through its
// gate. Without it quit is direct and nothing is loaded.
func (m Model) WithLifecycle(l *lifecycle.Lifecycle) Model {
	m.life = lifecycle.NewUI(l)
	if l != nil {
		m.b, m.l = newBrowser(l, m.prefs.Keymap), l
		m.wireClaims()
	}
	return m
}

// withTimers replaces the browser's refresh timers (tests).
func (m Model) withTimers(after func(time.Duration, tea.Msg) tea.Cmd) Model {
	if m.b != nil {
		m.b.after = after
	}
	return m
}

// Prefs returns the effective preferences: theme, mouse and keymap.
func (m Model) Prefs() prefs.Prefs { return m.prefs }

// Init starts background detection, the recovery scan and either the
// document's render or the project list.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.theme.Init(), m.life.Init(), m.loadDrafts()}
	if m.theme.Ready() {
		cmds = append(cmds, m.setStyle())
	}
	switch {
	case m.doc != nil:
		cmds = append(cmds, m.detail.SetSections(*m.doc))
	case m.b != nil:
		cmds = append(cmds, m.b.openSwitcher(true))
	}
	return tea.Batch(cmds...)
}

// context is the keymap context of the focused view.
func (m Model) context() keys.Context {
	if m.help || m.b != nil && (m.b.menu != nil || m.b.filt.panel != nil || m.b.qa.m != nil) || m.ed != nil && m.ed.modal() ||
		m.dv != nil && (m.dv.view != nil || m.dv.prompt != nil) {
		return keys.Menu
	}
	return m.view()
}

// view is the keymap context of the view under the help overlay and the
// action menu.
func (m Model) view() keys.Context {
	switch {
	case m.doc != nil:
		return keys.Detail
	case m.b == nil:
		return keys.Global
	}
	return m.b.view()
}

func (m Model) setStyle() tea.Cmd {
	style := m.theme.Style()
	if m.b != nil {
		m.b.setPlain(style == render.ASCII)
	}
	return m.detail.SetStyle(style)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if m.theme.Update(msg) && m.theme.Ready() {
		cmds = append(cmds, m.setStyle())
	}
	m.touchClaims(msg)
	cmds = append(cmds, m.draftsWatch(msg))
	m, cmd, handled := m.uncertainKey(msg)
	if handled {
		return m, tea.Batch(append(cmds, cmd, m.relayout())...)
	}
	if life, cmd, handled := m.life.Update(msg, m.prefs.Keymap); handled {
		m.life = life
		return m, tea.Batch(append(cmds, cmd, m.relayout())...)
	}
	if cmd, handled := m.promptUpdate(msg); handled {
		return m, tea.Batch(append(cmds, cmd, m.relayout())...)
	}
	if cmd, handled := m.claimsUpdate(msg); handled {
		return m, tea.Batch(append(cmds, cmd, m.relayout())...)
	}
	if m, cmd, handled = m.historyKey(msg); handled {
		return m, tea.Batch(append(cmds, cmd, m.relayout())...)
	}
	if cmd, handled := m.editUpdate(msg); handled {
		return m, tea.Batch(append(cmds, cmd, m.relayout())...)
	}
	if cmd, handled := m.draftsUpdate(msg); handled {
		return m, tea.Batch(append(cmds, cmd, m.relayout())...)
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.b == nil {
			cmds = append(cmds, m.detail.SetSize(msg.Width, max(0, msg.Height-2)))
		}
	case tea.ModeReportMsg:
		if m.b != nil && msg.Mode == ansi.ModeUnicodeCore {
			m.b.setMethod(cells.MethodForUnicodeCore(msg.Value))
		}
		cmds = append(cmds, m.detail.Update(msg))
	case tea.KeyPressMsg:
		if keys.IsShutdown(msg.Key()) {
			return m, m.life.Interrupt()
		}
		var cmd tea.Cmd
		m, cmd = m.key(msg)
		cmds = append(cmds, cmd)
	case tea.MouseClickMsg, tea.MouseWheelMsg:
		if m.b != nil && !m.help {
			cmds = append(cmds, m.mouse(msg.(tea.MouseMsg)))
		} else {
			cmds = append(cmds, m.detail.Update(msg))
		}
	default:
		if m.b != nil {
			if cmd, handled := m.b.update(msg, m.detail); handled {
				if _, loaded := msg.(issuesMsg); loaded {
					m.markTree() // drafts whose issue changed are marked
				}
				cmds = append(cmds, cmd)
				break
			}
		}
		cmds = append(cmds, m.detail.Update(msg))
	}
	return m, tea.Batch(append(cmds, m.relayout())...)
}

// key handles a key press: help first, then the action menu, then the view.
func (m Model) key(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.b != nil && m.b.qa.m != nil { // an open quick action owns the keys
		return m, m.b.quickKey(msg)
	}
	if m.b != nil && m.b.filt.input != nil { // the search input owns typed keys
		return m, m.b.inputKey(msg, m.detail)
	}
	action, _ := m.prefs.Keymap.Lookup(m.context(), msg.Key())
	switch {
	case action == keys.Quit:
		return m, m.quit()
	case action == keys.UncertainRequests:
		return m.openUncertain()
	case m.help:
		if action == keys.Help || action == keys.Cancel {
			m.help = false
		}
		return m, nil
	case action == keys.Help:
		m.help = true
		return m, nil
	case m.b != nil && m.b.menu != nil:
		chosen, ok := m.b.menuKey(action)
		if !ok {
			return m, nil
		}
		return m.dispatch(chosen, nil)
	case m.b != nil && m.b.filt.panel != nil:
		return m, m.b.panelKey(action, m.detail)
	}
	return m.dispatch(action, msg)
}

// dispatch performs action in the current view; msg is the key press, or
// nil when the action menu chose it.
func (m Model) dispatch(action keys.Action, msg tea.Msg) (Model, tea.Cmd) {
	switch {
	case action == keys.Quit:
		return m, m.quit()
	case action == keys.Help:
		m.help = true
		return m, nil
	case m.cl != nil && (action == keys.Claim || action == keys.Release):
		return m, m.claimAction(action)
	case action == keys.UncertainRequests:
		return m.openUncertain()
	case action == keys.History:
		return m.openHistory()
	case m.b != nil && isQuick(m.view(), action):
		return m, m.b.quickAction(action)
	case m.ed != nil && (action == keys.Edit || action == keys.EditDetached):
		return m, m.editAction(action == keys.EditDetached)
	case m.dv != nil && action == keys.Drafts:
		return m.openDraftsView()
	case m.ed != nil && (action == keys.NewComment || action == keys.EditComment):
		return m, m.commentAction(action)
	case m.b != nil:
		return m, m.b.dispatch(m.view(), action, msg, m.detail)
	case msg != nil:
		return m, m.detail.Update(msg)
	}
	return m, nil
}

// Run runs the program until the user quits or ctx ends. Bubble Tea restores
// the terminal before Run returns.
func Run(ctx context.Context, m Model, opts ...tea.ProgramOption) error {
	_, err := tea.NewProgram(m, append([]tea.ProgramOption{tea.WithContext(ctx)}, opts...)...).Run()
	return err
}
