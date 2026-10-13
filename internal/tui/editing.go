package tui

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/tui/comments"
	"github.com/nerdwave-nick/lit/internal/tui/drafts"
	"github.com/nerdwave-nick/lit/internal/tui/draftsview"
	"github.com/nerdwave-nick/lit/internal/tui/edit"
	"github.com/nerdwave-nick/lit/internal/tui/keys"
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
	// chooser picks the comment to edit (edit_comment).
	chooser *comments.Chooser
	open    map[string]*openDraft // by draft key (kind and record)
	note    string
	busy    bool // an edit request is running

	// title asks for a new issue's title (new_issue, new_child).
	title *titlePrompt
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
		s        edit.Started
		detached bool
		t        edit.Target
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
		key string // the draft's key
		at  time.Time
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
		if bd.Action == keys.EditSpec || bd.Action == keys.EditRepositories {
			return bd, ed != nil && b.project != nil
		}
		if bd.Action == keys.Edit || bd.Action == keys.EditDetached || bd.Action == keys.NewComment || bd.Action == keys.EditComment {
			_, selected := b.tree.Selected()
			return bd, ed != nil && selected && b.project != nil
		}
		if bd.Action == keys.NewIssue || bd.Action == keys.NewChild {
			_, selected := b.tree.Selected()
			return bd, ed != nil && b.project != nil && (selected || bd.Action == keys.NewIssue)
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
	return m.openTarget(edit.Target{Kind: drafts.IssueBody, Project: b.project.ID, Record: issue.ID, Issue: issue.ID, Title: issue.Title}, detached)
}

// openTarget starts a draft of t: its existing draft is offered (never
// overwritten); otherwise a claiming kind claims the owning issue first,
// and the draft is edited in the editor or detached.
func (m Model) openTarget(t edit.Target, detached bool) tea.Cmd {
	e := m.ed
	d, found, err := e.ed.ExistingFor(t)
	switch {
	case err != nil:
		e.note = "Cannot read the drafts: " + describe(err) + "."
		return nil
	case found:
		return m.openBodyDraft(d) // a changed revision opens as a conflict
	}
	e.busy, e.note = true, "Claiming "+titled(t.Title)+" for editing…"
	if !t.Kind.Claims() {
		e.note = "Starting " + draftOf(t.Kind, t.Title) + "…"
	}
	ed := e.ed
	return func() tea.Msg {
		return editStartedMsg{s: ed.Open(context.Background(), t), detached: detached, t: t}
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
		if o := e.open[msg.d.Key()]; o != nil && !o.detached {
			e.untrack(msg.d.Key())
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
		e.untrack(msg.d.Key())
		e.note = discardNote(msg.d, msg.r)
		return m.refreshTree(), true
	case draftChangedMsg:
		if o := e.open[msg.key]; o != nil {
			o.changed = msg.at
		}
		if p := e.panel; p != nil && p.d.Key() == msg.key && p.preview != nil {
			p.stale = true
			p.choices = m.previewChoices(p)
		}
		return nil, true
	case tea.KeyPressMsg:
		if e.panel == nil && e.chooser == nil || keys.IsShutdown(msg.Key()) {
			return nil, false
		}
		action, _ := m.prefs.Keymap.Lookup(keys.Menu, msg.Key())
		switch action {
		case keys.Quit:
			return m.quit(), true
		case keys.UncertainRequests:
			return nil, false
		}
		if e.chooser != nil {
			return m.chooserKey(action), true
		}
		return m.panelKey(action), true
	case tea.MouseMsg:
		return nil, e.modal()
	}
	return m.commentUpdate(msg)
}

// modal reports that a draft panel or the comment chooser owns the keys.
func (e *editing) modal() bool { return e.panel != nil || e.chooser != nil || e.title != nil }

// started opens the started edit: the editor, or the detached draft's path.
func (m Model) started(msg editStartedMsg) tea.Cmd {
	e, s, t := m.ed, msg.s, msg.t
	switch {
	case s.Err != nil && s.Owner != nil:
		e.note = "Cannot edit " + nameOf(t.Kind, t.Title) + ": claimed by " + owner(s.Owner) + ". Nothing was changed."
		return nil
	case litclient.Code(s.Err) == "draft_exists":
		if d, ok, _ := e.ed.ExistingFor(t); ok {
			e.panel = m.offerPanel(d)
			return nil
		}
		fallthrough
	case s.Err != nil:
		e.note = "Cannot edit " + nameOf(t.Kind, t.Title) + ": " + describe(s.Err) + "."
		return nil
	}
	e.track(s.Draft, msg.detached)
	if msg.detached {
		e.note = "Editing " + nameOf(s.Draft.Kind, s.Draft.Title) + " detached: save the draft file below in any editor, then " + m.keyFor(s.Draft.Kind) + " applies it."
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
	key := d.Key()
	if o := e.open[key]; o != nil {
		o.d, o.detached = d, o.detached || detached
		return
	}
	ed, post := e.ed, e.post
	stop := edit.Watch(d.Path(), edit.WatchEvery, func() {
		ed.Touch()
		if post != nil {
			post("draft:"+key, draftChangedMsg{key: key, at: time.Now()})
		}
	})
	e.open[key] = &openDraft{d: d, detached: detached, stop: stop}
}

// untrack stops watching the draft with key.
func (e *editing) untrack(key string) {
	if o := e.open[key]; o != nil {
		o.stop()
		delete(e.open, key)
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
