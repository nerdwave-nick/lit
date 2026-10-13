package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/tui/detail"
	"github.com/nerdwave-nick/lit/internal/tui/keys"
	"github.com/nerdwave-nick/lit/internal/tui/lifecycle"
	"github.com/nerdwave-nick/lit/internal/tui/recovery"
	"github.com/nerdwave-nick/lit/internal/tui/render/cells"
	"github.com/nerdwave-nick/lit/internal/tui/uncertain"
)

// openUncertain opens the read-only uncertain-requests view over whatever is
// shown, the exit report and the launch panel included, and starts its check.
func (m Model) openUncertain() (Model, tea.Cmd) {
	if m.b != nil {
		m.b.menu = nil
	}
	m.help = false
	m.uv = uncertain.New(uncertainLoader(m.l, m.life))
	return m, m.uv.Load()
}

// uncertainLoader lists this session's retained requests and those of the
// orphans found at launch. While running, this session's are checked now
// through the gate (read-only transaction status); after shutdown the result
// it checked is shown, since the gate is closed. Orphans show what recovery
// found at launch. Nothing is replayed, moved or deleted.
func uncertainLoader(l *lifecycle.Lifecycle, life lifecycle.UI) uncertain.Loader {
	orphans := life.Orphans()
	exit, stopped := life.Exit()
	return func(ctx context.Context) []uncertain.Group {
		var groups []uncertain.Group
		if l != nil {
			st := l.Status()
			own := uncertain.Group{Title: "This lit-tui session · client " + short(st.Client) + " · instance " + short(st.Instance)}
			switch {
			case stopped:
				own.Checked, own.Items = "checked at exit", uncertain.FromRequests(exit.Requests)
			default:
				own.Checked = "checked now"
				err := l.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
					items, err := uncertain.Own(ctx, s, l.Instance())
					own.Items = items
					return err
				})
				if err != nil {
					own.Checked = "not checked: " + recovery.Describe(err)
					if own.Items == nil { // the gate refused: list what is retained locally
						own.Items = uncertain.FromRequests(l.Outstanding().Requests)
						for i := range own.Items {
							own.Items[i].Status = uncertain.NotChecked
						}
					}
				}
			}
			groups = append(groups, own)
		}
		for _, o := range orphans {
			title := "Orphaned lit-tui session " + short(o.Entry.ID)
			if o.ClientID != "" {
				title += " · client " + short(o.ClientID)
			}
			groups = append(groups, uncertain.Group{Title: title, Checked: "checked at launch, " + string(o.Outcome) + ", not disconnected",
				Items: uncertain.FromRequests(o.Requests)})
		}
		return groups
	}
}

// uncertainKey gives the open view its messages and key presses before
// anything else, so the exit report and prompts below it wait. Ctrl-C and
// quit close it and go on as usual.
func (m Model) uncertainKey(msg tea.Msg) (Model, tea.Cmd, bool) {
	if m.uv == nil {
		return m, nil, false
	}
	if m.uv.Update(msg) {
		return m, nil, true
	}
	switch msg := msg.(type) {
	case tea.MouseWheelMsg: // nothing behind the view takes the mouse
		for range detail.WheelLines {
			switch msg.Mouse().Button {
			case tea.MouseWheelUp:
				m.uv.Do(keys.Up)
			case tea.MouseWheelDown:
				m.uv.Do(keys.Down)
			}
		}
		return m, nil, true
	case tea.MouseClickMsg:
		return m, nil, true
	}
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil, false
	}
	action, _ := m.prefs.Keymap.Lookup(keys.Menu, k.Key())
	if keys.IsShutdown(k.Key()) || action == keys.Quit {
		m.uv = nil
		return m, nil, false
	}
	cmd, closed := m.uv.Do(action)
	if closed {
		m.uv = nil
	}
	return m, cmd, true
}

func (m Model) uncertainView() string {
	method := cells.Default
	if m.b != nil {
		method = m.b.method
	}
	return m.uv.View(method, m.width, m.height, m.prefs.Keymap)
}
