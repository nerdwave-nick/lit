package tui

import (
	"context"
	"fmt"
	"sort"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/draftsview"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/edit"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
)

// draftsState is the root model's view of the unapplied drafts: the cached
// list for the launch banner and the tree marks, the Drafts view, the quit
// prompt, and the drafts opened in this session. Model copies share it; it
// belongs to the UI goroutine. It exists with an editor.
type draftsState struct {
	store   drafts.Store
	gate    draftsview.Gate // nil without a lifecycle
	service string
	all     []drafts.Draft
	err     error
	gen     int
	loaded  bool // the launch's list has arrived
	banner  bool // the launch banner is shown, until the Drafts view opens
	view    *draftsview.Model
	prompt  *draftsview.Prompt
	// opened are the drafts started or resumed in this session, by
	// draftsview.Key: quitting while one still exists asks first.
	opened map[string]drafts.Draft
}

type (
	draftsLoadedMsg struct {
		gen int
		l   draftsview.Loaded
	}
	// draftCheckedMsg is an issue-body draft compared with its issue before
	// it is offered, so a changed revision opens as a conflict.
	draftCheckedMsg struct{ c edit.Compared }
)

// draftOpeners open a draft of their kind, chosen in the Drafts view or by
// Apply now in the quit prompt. Kinds without an opener get a panel that
// can discard or keep them. Kinds the editor edits (edit.Editor.Register)
// open through openBodyDraft, which compares the draft with its own record
// first; later kinds (new issues, specifications, milestone descriptions)
// add theirs once registered.
var draftOpeners = map[drafts.Kind]func(Model, drafts.Draft) tea.Cmd{
	drafts.IssueBody:   Model.openBodyDraft,
	drafts.CommentNew:  Model.openBodyDraft,
	drafts.CommentBody: Model.openBodyDraft,
	drafts.ProjectSpec: Model.openBodyDraft,
	drafts.IssueNew:    Model.openBodyDraft,
}

func newDraftsState(e *edit.Editor, g draftsview.Gate) *draftsState {
	return &draftsState{store: e.Store(), gate: g, opened: map[string]drafts.Draft{}}
}

// loadDrafts reloads the cached list of this service's drafts.
func (m Model) loadDrafts() tea.Cmd {
	dv := m.dv
	if dv == nil {
		return nil
	}
	dv.gen++
	gen, g, store, service := dv.gen, dv.gate, dv.store, dv.service
	return func() tea.Msg {
		return draftsLoadedMsg{gen, draftsview.Load(context.Background(), g, store, service)}
	}
}

// draftsWatch notes what editing did to the drafts, without consuming the
// message: a started or resumed draft is open in this session, and every
// change of the drafts reloads the list.
func (m Model) draftsWatch(msg tea.Msg) tea.Cmd {
	if m.dv == nil {
		return nil
	}
	switch msg := msg.(type) {
	case editStartedMsg:
		if msg.s.Err == nil {
			m.dv.opened[draftsview.Key(msg.s.Draft)] = msg.s.Draft
		}
	case appliedMsg, discardedMsg, reloadedMsg:
	default:
		return nil
	}
	return m.loadDrafts()
}

// draftsUpdate handles the drafts messages and, while the Drafts view is
// open, its keys. The draft panel shown over the view takes its keys first.
func (m Model) draftsUpdate(msg tea.Msg) (tea.Cmd, bool) {
	dv := m.dv
	if dv == nil {
		return nil, false
	}
	switch msg := msg.(type) {
	case draftsLoadedMsg:
		if msg.gen == dv.gen {
			dv.all, dv.err = msg.l.Drafts, msg.l.Err
			if msg.l.Service != "" {
				dv.service = msg.l.Service
			}
			if !dv.loaded {
				dv.loaded, dv.banner = true, len(dv.all) > 0
			}
			if dv.view != nil {
				dv.view.Set(m.draftItems(), dv.err)
			}
			m.markTree()
		}
		return nil, true
	case draftCheckedMsg:
		m.ed.busy = false
		m.ed.panel = m.checkedPanel(msg.c)
		return nil, true
	case tea.KeyPressMsg:
		if dv.view == nil || keys.IsShutdown(msg.Key()) {
			return nil, false
		}
		action, _ := m.prefs.Keymap.Lookup(keys.Menu, msg.Key())
		switch action {
		case keys.Quit:
			return m.quit(), true
		case keys.UncertainRequests:
			return nil, false
		}
		chosen, closed := dv.view.Do(action)
		switch {
		case closed:
			dv.view = nil
		case chosen != nil:
			return m.openDraft(chosen.Draft), true
		}
		return nil, true
	case tea.MouseMsg:
		return nil, dv.view != nil
	}
	return nil, false
}

// openDraftsView opens the Drafts view: every unapplied draft of this
// service, to resume, diff or discard. It ends the launch banner.
func (m Model) openDraftsView() (Model, tea.Cmd) {
	if m.b != nil {
		m.b.menu = nil
	}
	m.help = false
	dv := m.dv
	dv.banner = false
	dv.view = draftsview.New()
	dv.view.Set(m.draftItems(), dv.err)
	return m, m.loadDrafts()
}

// draftItems are the cached drafts as the Drafts view lists them: the
// current project's first, each with its project and, when the tree knows
// its issue's revision, whether it changed.
func (m Model) draftItems() []draftsview.Item {
	current := ""
	if m.b != nil && m.b.project != nil {
		current = m.b.project.ID
	}
	items := make([]draftsview.Item, 0, len(m.dv.all))
	for _, d := range m.dv.all {
		it := draftsview.Item{Draft: d, Project: "project " + short(d.ProjectID)}
		if d.ProjectID == current {
			it.Project = "project " + titled(m.b.project.Title)
			it.Current, it.Changed = draftsview.Changed(d, m.revision)
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Draft.ProjectID == current && items[j].Draft.ProjectID != current
	})
	return items
}

// revision is an issue's revision in the loaded tree.
func (m Model) revision(issue string) (int64, bool) {
	if m.b == nil {
		return 0, false
	}
	s, ok := m.b.tree.Issue(issue)
	return s.Revision, ok
}

// draftMarks are the tree badges of the current project's drafts.
func (m Model) draftMarks() map[string]string {
	if m.dv == nil || m.b == nil || m.b.project == nil {
		return map[string]string{}
	}
	var here []drafts.Draft
	for _, d := range m.dv.all {
		if d.ProjectID == m.b.project.ID {
			here = append(here, d)
		}
	}
	return draftsview.Marks(here, m.revision)
}

// openDraft opens d by its kind's opener, or a panel that can discard it.
func (m Model) openDraft(d drafts.Draft) tea.Cmd {
	if open, ok := draftOpeners[d.Kind]; ok {
		return open(m, d)
	}
	m.ed.panel = &draftPanel{d: d, heading: "The " + draftsview.KindLabel(d.Kind) + " draft " + titled(d.Title),
		text: []string{
			fmt.Sprintf("Started %s at revision %d: %s", d.Created().Local().Format("2006-01-02 15:04"), d.Revision, path(d)),
			"This lit-tui cannot resume or diff " + draftsview.KindLabel(d.Kind) + " drafts. Keep it, or discard it.",
		},
		choices: choices(chDiscard, chKeep)}
	return nil
}

// openBodyDraft compares a draft with its record first (an issue body with
// its issue, a comment with the comment): when the record's revision
// changed, the draft opens as a conflict (diff, reload, resume, keep or
// discard); otherwise it is offered (resume, diff, apply, discard). A new
// comment whose comment exists already opens as posted.
func (m Model) openBodyDraft(d drafts.Draft) tea.Cmd {
	e := m.ed
	if e.busy {
		e.note = "Wait for the running edit action to finish."
		return nil
	}
	e.busy, e.note = true, "Checking "+draftOf(d.Kind, d.Title)+"…"
	ed := e.ed
	return func() tea.Msg { return draftCheckedMsg{ed.DiffCurrent(context.Background(), d)} }
}

// checkedPanel is the panel of a draft compared with its own record:
// revisions are compared per kind (a comment draft records the comment's).
func (m Model) checkedPanel(c edit.Compared) *draftPanel {
	d := c.Draft
	m.ed.note = ""
	switch {
	case c.Err != nil:
		p := m.offerPanel(d)
		p.text = append(p.text, "Cannot read the current revision: "+describe(c.Err)+".")
		return p
	case d.Kind.New() && c.Record.Exists:
		return m.previewPanel(edit.Preview{Draft: d, Record: c.Record, Exempt: !d.Kind.Claims(), Created: true})
	case !d.Kind.New() && c.Record.Revision != d.Revision:
		p := edit.Preview{Draft: d, Record: c.Record, Current: c.Current, Exempt: !d.Kind.Claims(), Conflict: true}
		if !p.Exempt {
			p.Hold, p.Held = m.ed.ed.Hold(d.IssueID)
		}
		return m.previewPanel(p)
	}
	return m.offerPanel(d)
}

// draftLines are the footer's launch banner.
func (m Model) draftLines() []string {
	if m.dv == nil || !m.dv.banner || len(m.dv.all) == 0 {
		return nil
	}
	return []string{draftsview.Banner(len(m.dv.all)) + " · " + firstKey(m, keys.Global, keys.Drafts) + " opens the Drafts view"}
}

func (m Model) draftsView() string {
	method, plain := cells.Default, false
	if m.b != nil {
		method, plain = m.b.method, m.b.tree.Plain()
	}
	return m.dv.view.View(method, m.width, m.height, m.prefs.Keymap, plain)
}
