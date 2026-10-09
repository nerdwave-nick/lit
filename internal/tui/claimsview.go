package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/claims"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
)

// claimsView is the root model's view of the claim holder: its latest state
// and the last action's note. Model copies share it; it belongs to the UI
// goroutine. The holder itself renews on its own goroutine.
type claimsView struct {
	h     *claims.Holder
	state claims.Msg
	note  string
}

// claimDoneMsg is the result of an explicit claim action, with the holder's
// state right after it.
type claimDoneMsg struct {
	action keys.Action // Claim, Release, or Open for keep holding
	r      claims.Result
	state  claims.Msg
}

// WithClaims shows and acts on h's claims: the claim and release actions on
// the selected issue, lost and paused badges, and the idle question.
func (m Model) WithClaims(h *claims.Holder) Model {
	m.cl = nil
	if h != nil {
		m.cl = &claimsView{h: h, state: h.Snapshot()}
	}
	m.wireClaims()
	return m
}

// wireClaimItems connects the browser (recreated by WithPrefs) to the
// claims. Without a holder the menu offers no claim actions.
func (m Model) wireClaimItems() {
	switch {
	case m.b == nil:
		return
	case m.cl == nil:
		m.b.menuItem = func(bd keys.Binding) (keys.Binding, bool) {
			return bd, bd.Action != keys.Claim && bd.Action != keys.Release
		}
		return
	}
	b, cl := m.b, m.cl
	b.menuItem = func(bd keys.Binding) (keys.Binding, bool) {
		issue, ok := b.tree.Selected()
		if !ok || b.project == nil {
			return bd, bd.Action != keys.Claim && bd.Action != keys.Release
		}
		held, has := cl.state.Hold(issue.ID)
		switch bd.Action {
		case keys.Claim:
			if has && held.State != claims.Holding {
				bd.Description = "reacquire the claim (rereads the issue first)"
			}
			return bd, !has || held.State != claims.Holding
		case keys.Release:
			return bd, has
		}
		return bd, true
	}
	m.markTree()
}

// markTree shows paused and lost holds and the drafts as tree badges.
func (m Model) markTree() {
	if m.b == nil {
		return
	}
	marks := m.draftMarks()
	for _, h := range m.holds() {
		if h.State != claims.Holding {
			if d := marks[h.IssueID]; d != "" {
				marks[h.IssueID] = string(h.State) + ", " + d
			} else {
				marks[h.IssueID] = string(h.State)
			}
		}
	}
	m.b.tree.SetMarks(marks)
}

func (m Model) holds() []claims.Hold {
	if m.cl == nil {
		return nil
	}
	return m.cl.state.Holds
}

// touchClaims records TUI input as activity, postponing the idle question.
func (m Model) touchClaims(msg tea.Msg) {
	switch msg.(type) {
	case tea.KeyPressMsg, tea.MouseMsg:
		if m.cl != nil {
			m.cl.h.Touch()
		}
	}
}

// setState keeps the newest of the holder's states.
func (m Model) setState(s claims.Msg) {
	if s.Seq >= m.cl.state.Seq {
		m.cl.state = s
		m.markTree()
	}
}

// claimsUpdate handles the holder's state, action results and, while the
// idle question is shown, its keys.
func (m Model) claimsUpdate(msg tea.Msg) (tea.Cmd, bool) {
	cl := m.cl
	if cl == nil {
		return nil, false
	}
	switch msg := msg.(type) {
	case claims.Msg:
		m.setState(msg)
		return nil, true
	case claimDoneMsg:
		m.setState(msg.state)
		cl.note = resultNote(msg.action, msg.r)
		if m.b != nil && msg.action != keys.Open {
			return m.b.refresh(), true
		}
		return nil, true
	case tea.KeyPressMsg:
		if !cl.state.Asking || keys.IsShutdown(msg.Key()) {
			return nil, false
		}
		action, _ := m.prefs.Keymap.Lookup(keys.Menu, msg.Key())
		switch action {
		case keys.Open:
			cl.state.Asking = false
			h := cl.h
			return func() tea.Msg {
				r := h.KeepHolding(context.Background())
				return claimDoneMsg{action: keys.Open, r: r, state: h.Snapshot()}
			}, true
		case keys.Cancel:
			cl.h.LetLapse()
			m.setState(cl.h.Snapshot())
			cl.note = "Claim renewal stays paused: the claims lapse when their leases end and are then shown as lost."
		case keys.Quit:
			return m.quit(), true
		}
		return nil, true
	}
	return nil, false
}

// claimAction claims or releases the selected issue.
func (m Model) claimAction(action keys.Action) tea.Cmd {
	cl, b := m.cl, m.b
	if b == nil || b.project == nil || b.switching {
		return nil
	}
	issue, ok := b.tree.Selected()
	if !ok {
		cl.note = "Select an issue first."
		return nil
	}
	h, id, title := cl.h, issue.ID, issue.Title
	if _, held := cl.state.Hold(id); action == keys.Release && !held {
		cl.note = "This session holds no claim on " + titled(title) + "."
		return nil
	}
	verb := "Claiming "
	if action == keys.Release {
		verb = "Releasing "
	}
	cl.note = verb + titled(title) + "…"
	return func() tea.Msg {
		var r claims.Result
		if action == keys.Release {
			r = h.Release(context.Background(), id)
		} else {
			r = h.Claim(context.Background(), id, title)
		}
		if r.Title == "" {
			r.Title = title
		}
		return claimDoneMsg{action: action, r: r, state: h.Snapshot()}
	}
}
