package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/create"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/edit"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
)

// Creating issues: new_issue asks inline for the title of a top-level
// issue, new_child for a child of the selection. Submitting the title
// starts an issue_new draft (it takes no claim) and suspends into the
// editor on the body; the draft panel then previews the one transaction
// that creates the issue (and changes a child's parent) and applies exactly
// that preview. The new issue is selected in the reloaded tree; its labels,
// assignee and links are set afterwards with the quick actions.

// titlePrompt is the open title prompt: of a new issue (under parent, or
// top-level), or of a kept draft whose title changes.
type titlePrompt struct {
	p      *create.Prompt
	parent string        // the new issue's parent, "" for a top-level issue
	draft  *drafts.Draft // the kept draft to retitle, if any
}

type retitledMsg struct {
	d   drafts.Draft
	err error
}

// createAction opens the title prompt of a new issue: top-level for
// new_issue, a child of the selection for new_child.
func (m Model) createAction(a keys.Action) tea.Cmd {
	e, b := m.ed, m.b
	if b == nil || b.project == nil || b.switching {
		return nil
	}
	if e.busy {
		e.note = "Wait for the running edit action to finish."
		return nil
	}
	heading, parent := "New issue in "+titled(b.project.Title), ""
	var info []string
	if a == keys.NewChild {
		sel, ok := b.tree.Selected()
		if !ok {
			e.note = "Select the parent issue first."
			return nil
		}
		heading, parent = "New child of "+titled(sel.Title), sel.ID
		if c := sel.Claim; c != nil && !m.ownClaim(c) {
			info = append(info, titled(sel.Title)+" is claimed by "+owner(c)+": a new child changes its parent, so creating it is refused while that claim lasts.")
		}
	}
	info = append(info, firstKey(m, keys.Menu, keys.Open)+" opens your editor on the body's draft. Nothing is created until you apply its preview. Set the labels ("+
		firstKey(m, keys.Tree, keys.SetLabels)+"), the assignee ("+firstKey(m, keys.Tree, keys.SetAssignee)+") and links ("+firstKey(m, keys.Tree, keys.Link)+") afterwards with the quick actions.")
	b.menu, e.note = nil, ""
	e.title = &titlePrompt{p: create.NewPrompt(heading, "", m.loadedTitles(), info...), parent: parent}
	return nil
}

// retitlePrompt opens the title prompt for the kept draft d.
func (m Model) retitlePrompt(d drafts.Draft) {
	heading := "Change the title of " + nameOf(d.Kind, d.Title)
	m.ed.title = &titlePrompt{p: create.NewPrompt(heading, d.Title, m.loadedTitles(), "The draft keeps its body: "+path(d)+"."), draft: &d}
}

// createUpdate gives the title prompt its keys and pastes before anything
// below it (typed letters are text there), and takes a retitle's result.
// Ctrl-C passes through and shuts down as usual.
func (m Model) createUpdate(msg tea.Msg) (tea.Cmd, bool) {
	e := m.ed
	if e == nil {
		return nil, false
	}
	if r, ok := msg.(retitledMsg); ok {
		e.busy = false
		if r.err != nil {
			e.note = "Cannot change the title of " + draftOf(r.d.Kind, r.d.Title) + ": " + describe(r.err) + ". The draft is kept."
			return nil, true
		}
		if o := e.open[r.d.Key()]; o != nil {
			o.d = r.d
		}
		return tea.Batch(e.previewCmd(r.d), m.loadDrafts()), true
	}
	t := e.title
	if t == nil {
		return nil, false
	}
	switch msg := msg.(type) {
	case tea.PasteMsg:
		t.p.Paste(msg.Content)
		return nil, true
	case tea.MouseMsg:
		return nil, true
	case tea.KeyPressMsg:
		if keys.IsShutdown(msg.Key()) {
			return nil, false
		}
		r := t.p.Key(msg, m.prefs.Keymap)
		switch {
		case r.Cancel && t.draft != nil:
			e.title, e.note = nil, "The title is unchanged; the draft is kept: "+path(*t.draft)+"."
		case r.Cancel:
			e.title = nil
		case r.Submit && t.draft != nil:
			e.title, e.busy = nil, true
			store, d := e.ed.Store(), *t.draft
			return func() tea.Msg {
				nd, err := create.Retitle(store, d, r.Title)
				return retitledMsg{nd, err}
			}, true
		case r.Submit && m.b != nil && m.b.project != nil:
			e.title = nil
			return m.openTarget(create.Target(m.b.project.ID, t.parent, r.Title), false), true
		}
		return nil, true
	}
	return nil, false
}

// loadedTitles are the titles of the loaded issues, which a new issue's
// title must not repeat.
func (m Model) loadedTitles() []string {
	if m.b == nil {
		return nil
	}
	out := make([]string, 0, len(m.b.last.Items))
	for _, i := range m.b.last.Items {
		out = append(out, i.Title)
	}
	return out
}

// ownClaim reports whether c belongs to this session.
func (m Model) ownClaim(c *protocol.Claim) bool {
	return m.l != nil && c.OwnerClientID == m.l.Status().Client
}

// titleLines draw the title prompt in the browser's body.
func (m Model) titleLines(h int) []string {
	lines := m.ed.title.p.Lines(m.b.method, m.width, h)
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines[:h]
}

// selectCreated selects a just-created issue once a load lists it, and
// closes the Drafts view it may have been created from, so the tree shows
// it.
func (m Model) selectCreated(a edit.Applied) {
	if a.Draft.Kind != drafts.IssueNew || m.b == nil {
		return
	}
	m.b.want = a.Draft.RecordID
	if m.dv != nil {
		m.dv.view = nil
	}
}

// selectWanted selects the wanted issue (a just-created one) when items
// list it; until then it stays wanted, since a load that started before
// the creation does not list it yet.
func (b *browser) selectWanted(items []protocol.IssueSummary) {
	for _, i := range items {
		if b.want != "" && i.ID == b.want {
			b.tree.Select(b.want)
			b.want = ""
			return
		}
	}
}

// createdNote is the footer note after an issue was created.
func (m Model) createdNote(a edit.Applied) string {
	d := a.Draft
	note := "Created " + titled(d.Title)
	if d.ParentID != "" {
		note += " under " + m.issueName(d.ParentID)
	}
	note += ". Set its labels (" + firstKey(m, keys.Tree, keys.SetLabels) + "), assignee (" + firstKey(m, keys.Tree, keys.SetAssignee) +
		") or links (" + firstKey(m, keys.Tree, keys.Link) + ") with the quick actions."
	if a.Err != nil {
		note += " The draft could not be removed: " + describe(a.Err) + "."
	}
	return note
}

// newIssueText describes a new issue's preview before confirming: it takes
// no claim, and a child changes its parent.
func (m Model) newIssueText(p edit.Preview) []string {
	if p.Draft.ParentID == "" {
		return []string{"New issues take no claim."}
	}
	parent := m.issueName(p.Draft.ParentID)
	return []string{fmt.Sprintf("New issues take no claim. Creating it changes the children of %s: another client's claim on %s refuses it.", parent, parent)}
}

// issueName is a loaded issue's quoted title, or "issue <id8>".
func (m Model) issueName(id string) string {
	if m.b != nil {
		if s, ok := m.b.tree.Issue(id); ok {
			return titled(s.Title)
		}
	}
	return "issue " + short(id)
}

// claimedName names the issue c claims for a refusal of d: d's own title
// for its owning issue, otherwise the claimed issue (a new child's parent).
func (m Model) claimedName(c *protocol.Claim, d drafts.Draft) string {
	if c.IssueID == "" || c.IssueID == d.IssueID {
		return titled(d.Title)
	}
	return m.issueName(c.IssueID)
}
