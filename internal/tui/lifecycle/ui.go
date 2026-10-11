package lifecycle

import (
	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/recovery"
)

// ReportMsg delivers the launch's registry scan.
type ReportMsg struct {
	Report recovery.Report
	Err    error
}

// RecoveredMsg delivers the recovery of one orphan the user confirmed.
type RecoveredMsg struct {
	Orphan recovery.Orphan
	OK     bool
	Err    error
}

// UI is the root model's view of the lifecycle: the session line, one-line
// recovery notes, the prompts for orphans the user must decide about, and
// the exit report that needs a keypress.
type UI struct {
	l      *Lifecycle
	status Status
	notes  []string
	panel  []recovery.Orphan // uncertain orphans, shown until dismissed
	ask    []recovery.Orphan // orphans awaiting the user's decision
	kept   []recovery.Orphan // orphans with retained requests, for the uncertain-requests view
}

// NewUI returns the UI for l; a nil l (tests, demos) quits directly.
func NewUI(l *Lifecycle) UI {
	u := UI{l: l}
	if l != nil {
		u.status = l.Status()
	}
	return u
}

// Init starts the launch's orphan recovery.
func (u UI) Init() tea.Cmd {
	if u.l == nil {
		return nil
	}
	l := u.l
	return func() tea.Msg {
		r, err := l.Recover()
		return ReportMsg{Report: r, Err: err}
	}
}

// Update handles lifecycle messages and, while a prompt or the exit report is
// shown, key presses. handled reports that the root model must not.
func (u UI) Update(msg tea.Msg, km keys.Keymap) (next UI, cmd tea.Cmd, handled bool) {
	switch msg := msg.(type) {
	case StatusMsg:
		u.status = Status(msg)
		return u, nil, true
	case ReportMsg:
		if msg.Report.Live > 0 {
			u.notes = append(u.notes, plural(msg.Report.Live, "other lit-tui session is", "other lit-tui sessions are")+" running: left untouched.")
		}
		u.absorb(msg.Report.Orphans)
		if msg.Err != nil {
			u.notes = append(u.notes, "Recovery of orphaned sessions stopped: "+recovery.Describe(msg.Err))
		}
		return u, nil, true
	case RecoveredMsg:
		switch {
		case msg.Err != nil:
			u.notes = append(u.notes, "Recovery stopped: "+recovery.Describe(msg.Err))
		case !msg.OK:
			u.notes = append(u.notes, "Session "+short(msg.Orphan.Entry.ID)+" is in use again: left untouched.")
		default:
			u.absorb([]recovery.Orphan{msg.Orphan})
		}
		return u, nil, true
	case tea.KeyPressMsg:
		return u.key(msg, km)
	}
	return u, nil, false
}

func (u UI) key(msg tea.KeyPressMsg, km keys.Keymap) (UI, tea.Cmd, bool) {
	shutdown := keys.IsShutdown(msg.Key())
	action, _ := km.Lookup(keys.Menu, msg.Key())
	switch {
	case u.status.Phase == Stopped && u.status.Result.Outcome != Clean:
		if action == keys.UncertainRequests && len(u.status.Result.Requests) > 0 {
			return u, nil, false // the root model opens the view
		}
		return u, tea.Quit, true // the second keypress leaves
	case u.status.Phase != Running:
		if shutdown {
			return u, u.Interrupt(), true
		}
		return u, nil, true
	case shutdown:
		return u, nil, false
	}
	switch {
	case len(u.ask) > 0:
		o := u.ask[0]
		switch action {
		case keys.Open:
			u.ask = u.ask[1:]
			l := u.l
			return u, func() tea.Msg {
				if l == nil {
					return RecoveredMsg{Orphan: o}
				}
				got, ok, err := l.RecoverEntry(o.Entry)
				return RecoveredMsg{Orphan: got, OK: ok, Err: err}
			}, true
		case keys.Cancel:
			u.ask = u.ask[1:]
			u.notes = append(u.notes, "Kept orphaned session "+short(o.Entry.ID)+": it was not disconnected.")
		}
		return u, nil, true
	case len(u.panel) > 0:
		switch action {
		case keys.Cancel:
			u.panel = u.panel[1:]
		case keys.UncertainRequests:
			return u, nil, false // the root model opens the view
		}
		return u, nil, true
	}
	return u, nil, false
}

// absorb turns recovered orphans into notes, prompts and panels.
func (u *UI) absorb(orphans []recovery.Orphan) {
	cleaned := 0
	for _, o := range orphans {
		if len(o.Requests) > 0 {
			u.kept = append(u.kept, o)
		}
		switch o.Outcome {
		case recovery.Disconnected:
			cleaned++
		case recovery.Uncertain:
			u.panel = append(u.panel, o)
		case recovery.Ask:
			u.ask = append(u.ask, o)
		default:
			what := "kept for a later launch"
			if o.Removed {
				what = "entry removed"
			}
			u.notes = append(u.notes, "Orphaned session "+describe(o)+": unknown ("+what+"): "+o.Reason)
		}
	}
	if cleaned > 0 {
		u.notes = append(u.notes, "Cleaned up "+plural(cleaned, "orphaned lit-tui session", "orphaned lit-tui sessions")+": disconnected, leftover claims released.")
	}
}

// Quit is the quit action: it starts the orderly shutdown.
func (u UI) Quit() tea.Cmd {
	if u.l == nil {
		return tea.Quit
	}
	u.l.Shutdown("quit")
	return nil
}

// Interrupt is Ctrl-C: the first starts the orderly shutdown, a second
// forces an exit.
func (u UI) Interrupt() tea.Cmd {
	if u.l == nil {
		return tea.Quit
	}
	u.l.Interrupt(130)
	return nil
}

// Orphans are the launch's orphans that retained requests, kept after their
// panel is closed, for the uncertain-requests view.
func (u UI) Orphans() []recovery.Orphan { return append([]recovery.Orphan(nil), u.kept...) }

// Exit is shutdown's result once it has ended.
func (u UI) Exit() (Result, bool) { return u.status.Result, u.status.Phase == Stopped }
