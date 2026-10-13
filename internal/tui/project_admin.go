package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/detail"
	"github.com/nerdwave-nick/lit/internal/tui/keys"
	"github.com/nerdwave-nick/lit/internal/tui/projectadmin"
	"github.com/nerdwave-nick/lit/internal/tui/quick"
)

// Project administration (internal/tui/projectadmin): new_project in the
// project switcher creates a project and browses it; edit_repositories
// changes the current project's repository list through a quick preview;
// edit_spec edits its specification through a project_spec draft, which
// the draft panel previews, applies, reloads, diffs and discards.

type (
	// newProjectMsg carries the service's title prefixes for the form.
	newProjectMsg struct {
		prefixes []string
		err      error
	}
	// repositoriesMsg is the project read for the repositories form.
	repositoriesMsg struct {
		p   protocol.Project
		err error
	}
)

// projectAction runs new_project (only in the switcher) or
// edit_repositories (on the current project).
func (b *browser) projectAction(a keys.Action) tea.Cmd {
	src := b.src
	switch {
	case b.qa.m != nil:
		return nil
	case a == keys.NewProject:
		if !b.switching {
			return nil
		}
		return func() tea.Msg {
			p, err := projectadmin.Prefixes(context.Background(), src)
			return newProjectMsg{p, err}
		}
	case b.project == nil || b.switching:
		return nil
	}
	b.menu, b.qa.note = nil, "Reading the repositories of "+titled(b.project.Title)+"…"
	id := b.project.ID
	return func() tea.Msg {
		p, err := projectadmin.Read(context.Background(), src, id)
		return repositoriesMsg{p, err}
	}
}

// adminUpdate opens the forms once their reads arrive, and browses a
// project once it was created.
func (b *browser) adminUpdate(msg tea.Msg, d *detail.Model) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case newProjectMsg:
		switch {
		case msg.err != nil:
			b.qa.note = "Cannot create a project: " + describe(msg.err) + "."
		case b.switching && b.qa.m == nil:
			b.qa.note, b.qa.m = "", projectadmin.NewProject(quick.Env{Gate: b.src}, msg.prefixes)
		}
	case repositoriesMsg:
		switch {
		case msg.err != nil:
			b.qa.note = "Cannot read the repositories: " + describe(msg.err) + "."
		case b.project != nil && b.project.ID == msg.p.ID && !b.switching && b.qa.m == nil:
			b.qa.note = ""
			b.qa.m = projectadmin.Repositories(quick.Env{Gate: b.src, Project: msg.p.ID, ProjectTitle: msg.p.Title}, msg.p)
		}
	case projectadmin.Created:
		if msg.Err != nil {
			b.qa.note = "Created the project, but cannot read it (" + describe(msg.Err) + "); " + firstKeyOf(b.km, keys.Global, keys.SwitchProject) + " lists it."
			return nil, true
		}
		b.qa.note = "Created " + titled(msg.Project.Title) + ": " + firstKeyOf(b.km, keys.Tree, keys.EditSpec) + " writes its specification, " +
			firstKeyOf(b.km, keys.Tree, keys.EditRepositories) + " its repositories."
		return b.choose(msg.Project, d), true
	default:
		return nil, false
	}
	return nil, true
}

// specAction edits the current project's specification: its existing
// draft is offered (never overwritten); otherwise a draft is started at the
// project's current revision, without a claim, and opened in the editor.
func (m Model) specAction() tea.Cmd {
	e, b := m.ed, m.b
	switch {
	case b == nil || b.project == nil || b.switching:
		return nil
	case e.busy:
		e.note = "Wait for the running edit action to finish."
		return nil
	}
	return m.openTarget(projectadmin.Spec(*b.project), false)
}

func firstKeyOf(km keys.Keymap, ctx keys.Context, a keys.Action) string {
	if k := km.Keys(ctx, a); len(k) > 0 {
		return k[0]
	}
	return string(a)
}
