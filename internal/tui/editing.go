package tui

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/draftsview"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/edit"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
)

// editing is the root model's body editing: the open drafts of this session
// and the draft panel (offer, preview, conflicts, diff, discard). Model
// copies share it; it belongs to the UI goroutine. Requests run in commands
// through edit.Editor, which uses the lifecycle's gate.
type editing struct {
	ed     *edit.Editor
	getenv func(string) string
	goos   string
	isFile func(string) bool
	// exec suspends the program into the editor (tea.ExecProcess).
	exec func(*exec.Cmd, tea.ExecCallback) tea.Cmd
	// post delivers a watcher's message through the lifecycle's forwarder;
	// the watcher never calls Program.Send itself.
	post  func(key string, msg tea.Msg)
	susp  *suspension // the editor holding the terminal, if any
	panel *draftPanel
	open  map[string]*openDraft // by issue ID
	note  string
	busy  bool // an edit request is running
}

// openDraft is a draft being edited in this session: in the suspended
// editor, or detached in any editor. Its file is watched meanwhile.
type openDraft struct {
	d        drafts.Draft
	detached bool
	stop     func()
	changed  time.Time
}

type (
	editStartedMsg struct {
		s         edit.Started
		detached  bool
		id, title string
	}
	editorDoneMsg struct {
		d   drafts.Draft
		err error
	}
	previewMsg   struct{ p edit.Preview }
	appliedMsg   struct{ a edit.Applied }
	comparedMsg  struct{ c edit.Compared }
	discardedMsg struct {
		d drafts.Draft
		r edit.Discarded
	}
	reloadedMsg struct {
		d   drafts.Draft
		err error
	}
	draftChangedMsg struct {
		id string
		at time.Time
	}
)

// WithEditor edits issue bodies through e: the edit and edit_detached
// actions, the draft panel and the open drafts' footer lines.
func (m Model) WithEditor(e *edit.Editor) Model {
	m.ed, m.dv = nil, nil
	if e != nil {
		m.ed = &editing{ed: e, getenv: os.Getenv, goos: runtime.GOOS, isFile: edit.IsFile, open: map[string]*openDraft{}, susp: &suspension{}}
		m.ed.exec = m.ed.susp.exec
		var g draftsview.Gate
		if m.l != nil {
			m.ed.post, g = m.l.Post, m.l
		}
		m.dv = newDraftsState(e, g)
	}
	m.wireClaims()
	return m
}

// wireClaims connects the browser (recreated by WithPrefs) to the claims and
// the editor: the menu offers edit actions only with an editor and a
// selected issue.
func (m Model) wireClaims() {
	m.wireClaimItems()
	if m.b == nil {
		return
	}
	b, claimItem, ed := m.b, m.b.menuItem, m.ed
	b.menuItem = func(bd keys.Binding) (keys.Binding, bool) {
		if bd.Action == keys.Drafts {
			return bd, ed != nil
		}
		if bd.Action == keys.Edit || bd.Action == keys.EditDetached {
			_, selected := b.tree.Selected()
			return bd, ed != nil && selected && b.project != nil
		}
		if claimItem != nil {
			return claimItem(bd)
		}
		return bd, true
	}
}

// editAction edits the selected issue's body: its existing draft is offered
// (never overwritten); otherwise the issue is claimed first and a draft
// started, then edited in the editor or detached.
func (m Model) editAction(detached bool) tea.Cmd {
	e, b := m.ed, m.b
	if b == nil || b.project == nil || b.switching {
		return nil
	}
	issue, ok := b.tree.Selected()
	switch {
	case !ok:
		e.note = "Select an issue first."
		return nil
	case e.busy:
		e.note = "Wait for the running edit action to finish."
		return nil
	}
	d, found, err := e.ed.Existing(b.project.ID, issue.ID)
	switch {
	case err != nil:
		e.note = "Cannot read the drafts: " + describe(err) + "."
		return nil
	case found:
		return m.openBodyDraft(d) // a changed revision opens as a conflict
	}
	e.busy, e.note = true, "Claiming "+titled(issue.Title)+" for editing…"
	ed, project, id, title := e.ed, b.project.ID, issue.ID, issue.Title
	return func() tea.Msg {
		return editStartedMsg{s: ed.Start(context.Background(), project, id, title), detached: detached, id: id, title: title}
	}
}

// editUpdate handles the editing messages and, while the draft panel is
// shown, its keys.
func (m Model) editUpdate(msg tea.Msg) (tea.Cmd, bool) {
	e := m.ed
	if e == nil {
		return nil, false
	}
	switch msg.(type) {
	case editStartedMsg, appliedMsg, discardedMsg:
		m.syncClaims() // show the claim an edit took or released at once
	}
	switch msg := msg.(type) {
	case editStartedMsg:
		e.busy = false
		return m.started(msg), true
	case editorDoneMsg:
		if o := e.open[msg.d.IssueID]; o != nil && !o.detached {
			e.untrack(msg.d.IssueID)
		}
		if msg.err != nil {
			e.note = "The editor ended with an error (" + describe(msg.err) + "); the draft is kept: " + path(msg.d) + "."
			return nil, true
		}
		e.ed.Touch()
		return e.previewCmd(msg.d), true
	case previewMsg:
		e.busy = false
		e.panel = m.previewPanel(msg.p)
		return nil, true
	case appliedMsg:
		e.busy = false
		return m.applied(msg.a), true
	case comparedMsg:
		e.busy = false
		e.panel = m.diffPanel(msg.c)
		return nil, true
	case reloadedMsg:
		if msg.err != nil {
			e.busy = false
			e.note = "Cannot reload " + titled(msg.d.Title) + ": " + describe(msg.err) + ". The draft is kept."
			return nil, true
		}
		return e.previewCmd(msg.d), true
	case discardedMsg:
		e.busy = false
		e.untrack(msg.d.IssueID)
		e.note = discardNote(msg.d, msg.r)
		return m.refreshTree(), true
	case draftChangedMsg:
		if o := e.open[msg.id]; o != nil {
			o.changed = msg.at
		}
		if p := e.panel; p != nil && p.d.IssueID == msg.id && p.preview != nil {
			p.stale = true
			p.choices = m.previewChoices(p)
		}
		return nil, true
	case tea.KeyPressMsg:
		if e.panel == nil || keys.IsShutdown(msg.Key()) {
			return nil, false
		}
		action, _ := m.prefs.Keymap.Lookup(keys.Menu, msg.Key())
		switch action {
		case keys.Quit:
			return m.quit(), true
		case keys.UncertainRequests:
			return nil, false
		}
		return m.panelKey(action), true
	case tea.MouseMsg:
		return nil, e.panel != nil
	}
	return nil, false
}

// started opens the started edit: the editor, or the detached draft's path.
func (m Model) started(msg editStartedMsg) tea.Cmd {
	e, s := m.ed, msg.s
	switch {
	case s.Err != nil && s.Owner != nil:
		e.note = "Cannot edit " + titled(msg.title) + ": claimed by " + owner(s.Owner) + ". Nothing was changed."
		return nil
	case litclient.Code(s.Err) == "draft_exists" && m.b != nil && m.b.project != nil:
		if d, ok, _ := e.ed.Existing(m.b.project.ID, msg.id); ok {
			e.panel = m.offerPanel(d)
			return nil
		}
		fallthrough
	case s.Err != nil:
		e.note = "Cannot edit " + titled(msg.title) + ": " + describe(s.Err) + "."
		return nil
	}
	e.track(s.Draft, msg.detached)
	if msg.detached {
		e.note = "Editing " + titled(s.Draft.Title) + " detached: save the draft file below in any editor, then " + m.editKey() + " applies it."
		return m.refreshTree()
	}
	e.note = ""
	return tea.Batch(m.refreshTree(), m.launch(s.Draft))
}

// launch suspends the program into the editor on d's file.
func (m Model) launch(d drafts.Draft) tea.Cmd {
	e := m.ed
	argv, source, err := edit.Lookup(e.getenv, e.goos, e.isFile)
	if err != nil {
		e.note = "Cannot start the editor (" + source + "): " + describe(err) + ". The draft is kept: " + path(d) + "."
		return nil
	}
	e.track(d, false)
	return e.exec(edit.Command(argv, d.Path()), func(err error) tea.Msg { return editorDoneMsg{d: d, err: err} })
}

// track watches d's file while it is open; a change counts as activity for
// the claims and is shown in the footer.
func (e *editing) track(d drafts.Draft, detached bool) {
	if o := e.open[d.IssueID]; o != nil {
		o.d, o.detached = d, o.detached || detached
		return
	}
	ed, post, id := e.ed, e.post, d.IssueID
	stop := edit.Watch(d.Path(), edit.WatchEvery, func() {
		ed.Touch()
		if post != nil {
			post("draft:"+id, draftChangedMsg{id: id, at: time.Now()})
		}
	})
	e.open[id] = &openDraft{d: d, detached: detached, stop: stop}
}

func (e *editing) untrack(id string) {
	if o := e.open[id]; o != nil {
		o.stop()
		delete(e.open, id)
	}
}

// syncClaims shows the holder's current state, which the forwarder would
// otherwise deliver a little later.
func (m Model) syncClaims() {
	if m.cl != nil {
		m.setState(m.cl.h.Snapshot())
	}
}

func (m Model) refreshTree() tea.Cmd {
	if m.b == nil {
		return nil
	}
	return m.b.refresh()
}

func (m Model) editKey() string { return firstKey(m, keys.Tree, keys.Edit) }
