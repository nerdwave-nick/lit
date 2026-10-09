package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/browse"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/detail"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/projects"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tree"
)

// Refresh timing (Proposals). Background refresh runs RefreshInterval after
// the previous load ended, or ten times that load's duration when longer, so
// a slow server sees proportionally fewer reads. A refresh deferred by a
// running mutation retries after RefreshRetry.
const (
	RefreshInterval = 30 * time.Second
	RefreshRetry    = time.Second
	// ProjectPage is how many projects the switcher reads per page.
	ProjectPage = 50
)

// source is what the browser needs from the lifecycle: the gate, and
// whether a mutation or shutdown is running.
type source interface {
	browse.Doer
	Mutating() bool
	Started() bool
}

type focusPane int

const (
	paneTree focusPane = iota
	paneDetail
)

// browser is the project being browsed: its tree, the switcher, the focus
// and the load and refresh state. Model copies share it; it belongs to the
// UI goroutine.
type browser struct {
	src   source
	km    keys.Keymap
	after func(time.Duration, tea.Msg) tea.Cmd

	tree      *tree.Model
	switcher  *projects.Model
	switching bool
	project   *protocol.Project
	memory    map[string]tree.State // per project, for switching back
	focus     focusPane
	menu      *actionMenu
	method    cells.Method

	gen      int  // the current load; older results are dropped
	loading  bool // a load of the current project is running
	again    bool // a refresh was asked for while loading: run one more
	waiting  bool // refresh is deferred until the running mutation ends
	timer    int  // the live refresh timer; older ones are ignored
	loaded   bool // a load has been shown for the current project
	last     browse.Issues
	lastAt   time.Time
	note     string
	selected string // the issue whose detail is shown
	wide     bool   // the detail is laid out beside the tree
	full     fullDetail
	choose1  bool // choose the only project of the first page
}

type (
	projectsMsg struct {
		page litclient.Page[protocol.Project]
		err  error
	}
	issuesMsg struct {
		gen    int
		issues browse.Issues
	}
	refreshMsg struct{ timer int }
)

func newBrowser(src source, km keys.Keymap) *browser {
	return &browser{src: src, km: km, tree: tree.New(), switcher: projects.New(), memory: map[string]tree.State{}, method: cells.Default,
		after: func(d time.Duration, msg tea.Msg) tea.Cmd { return tea.Tick(d, func(time.Time) tea.Msg { return msg }) }}
}

func (b *browser) view() keys.Context {
	switch {
	case b.switching:
		return keys.Menu
	case b.project == nil:
		return keys.Global
	case b.focus == paneDetail:
		return keys.Detail
	}
	return keys.Tree
}

func (b *browser) setPlain(plain bool) {
	b.tree.SetPlain(plain)
	b.switcher.SetPlain(plain)
}

func (b *browser) setMethod(m cells.Method) {
	b.method = m
	b.tree.SetMethod(m)
	b.switcher.SetMethod(m)
}

// openSwitcher shows the switcher and loads its first page. first marks the
// launch, when a single project is chosen without asking.
func (b *browser) openSwitcher(first bool) tea.Cmd {
	b.switching, b.choose1, b.menu = true, first, nil
	current := ""
	if b.project != nil {
		current = b.project.ID
	}
	b.switcher.Reset(current)
	return b.loadProjects("")
}

func (b *browser) loadProjects(cursor string) tea.Cmd {
	src := b.src
	return func() tea.Msg {
		page, err := browse.LoadProjects(context.Background(), src, cursor, ProjectPage)
		return projectsMsg{page, err}
	}
}

// choose browses p, restoring its remembered tree state.
func (b *browser) choose(p protocol.Project, d *detail.Model) tea.Cmd {
	b.switching = false
	if b.project != nil && b.project.ID == p.ID {
		return nil
	}
	if b.project != nil {
		b.memory[b.project.ID] = b.tree.State()
	}
	b.project, b.focus, b.loaded, b.note, b.selected = &p, paneTree, false, "", ""
	b.tree.Restore(b.memory[p.ID])
	b.tree.SetFocused(true)
	b.loading = false
	return tea.Batch(d.SetSections(), b.refresh())
}

// refresh loads the current project now, or once the running load or
// mutation has finished. Requests while a load runs coalesce into one.
func (b *browser) refresh() tea.Cmd {
	switch {
	case b.project == nil:
		return nil
	case b.loading:
		b.again = true
		return nil
	case b.src.Mutating():
		b.waiting = true
		return b.schedule(RefreshRetry)
	}
	b.gen++
	b.loading, b.again, b.waiting = true, false, false
	gen, src, id := b.gen, b.src, b.project.ID
	return func() tea.Msg { return issuesMsg{gen, browse.LoadIssues(context.Background(), src, id)} }
}

// schedule replaces the refresh timer.
func (b *browser) schedule(d time.Duration) tea.Cmd {
	b.timer++
	return b.after(d, refreshMsg{b.timer})
}

// update handles the browser's own messages.
func (b *browser) update(msg tea.Msg, d *detail.Model) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case projectsMsg:
		b.switcher.AddPage(msg.page, msg.err)
		if b.choose1 && msg.err == nil && len(msg.page.Items) == 1 && msg.page.NextCursor == nil {
			return b.choose(msg.page.Items[0], d), true
		}
		b.choose1 = false
		return nil, true
	case issuesMsg:
		if msg.gen != b.gen {
			return nil, true
		}
		return b.loadedIssues(msg.issues, d), true
	case refreshMsg:
		if msg.timer != b.timer || b.src.Started() {
			return nil, true
		}
		return b.refresh(), true
	case detailMsg:
		return b.loadedFull(msg, d), true
	}
	return nil, false
}

func (b *browser) loadedIssues(got browse.Issues, d *detail.Model) tea.Cmd {
	b.loading = false
	shown := got.Err == nil || got.Pages > 0
	if shown {
		b.last, b.lastAt, b.loaded = got, time.Now(), true
		b.note = ""
		if ch := b.tree.SetIssues(got.Items); ch.Missing {
			b.note = "The previously selected issue is no longer listed."
		}
	} else {
		b.last.Err = got.Err
	}
	cmds := []tea.Cmd{b.showSelected(d, true)}
	if b.again {
		cmds = append(cmds, b.refresh())
	} else {
		cmds = append(cmds, b.schedule(max(RefreshInterval, 10*got.Elapsed)))
	}
	return tea.Batch(cmds...)
}

// dispatch performs action in the view ctx; msg is the key press, or nil
// when the action menu chose it.
func (b *browser) dispatch(ctx keys.Context, action keys.Action, msg tea.Msg, d *detail.Model) tea.Cmd {
	switch action {
	case keys.ActionMenu:
		if ctx != keys.Menu { // the switcher is itself a menu
			b.openMenu(ctx)
		}
		return nil
	case keys.Refresh:
		if b.switching {
			return b.openSwitcher(false)
		}
		return b.refresh()
	case keys.SwitchProject:
		if b.switching && b.project != nil {
			b.switching = false
			return nil
		}
		return b.openSwitcher(false)
	}
	switch ctx {
	case keys.Menu:
		r := b.switcher.Do(action)
		return b.switched(r, d)
	case keys.Tree:
		ch := b.tree.Do(action)
		if ch.Open {
			b.focus = paneDetail
			b.tree.SetFocused(false)
			return b.showSelected(d, false)
		}
		if ch.Selection {
			return b.showSelected(d, false)
		}
	case keys.Detail:
		if action == keys.Back {
			b.focus = paneTree
			b.tree.SetFocused(true)
			return nil
		}
		if msg != nil {
			return d.Update(msg)
		}
	}
	return nil
}

func (b *browser) switched(r projects.Result, d *detail.Model) tea.Cmd {
	switch {
	case r.Chosen != nil:
		return b.choose(*r.Chosen, d)
	case r.More != nil:
		return b.loadProjects(*r.More)
	case r.Cancel:
		b.switching = false
	}
	return nil
}
