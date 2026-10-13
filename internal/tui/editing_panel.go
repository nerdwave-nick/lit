package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/lit/internal/tui/claims"
	"github.com/nerdwave-nick/lit/internal/tui/drafts"
	"github.com/nerdwave-nick/lit/internal/tui/edit"
	"github.com/nerdwave-nick/lit/internal/tui/keys"
	"github.com/nerdwave-nick/lit/internal/tui/render/cells"
	"github.com/nerdwave-nick/lit/internal/tui/render/sanitize"
)

// draftPanel is the modal panel about one draft: a heading, sanitized text,
// a diff that scrolls, and the choices that apply (menu keys move, Enter
// chooses, Escape keeps the draft and closes the panel).
type draftPanel struct {
	d       drafts.Draft
	heading string
	text    []string
	diff    []string // raw; sanitized when drawn
	choices []choice
	cursor  int
	scroll  int
	// preview is the exact transaction Apply commits, for a ready preview.
	preview *edit.Preview
	// stale reports that the draft file changed after the preview.
	stale bool
}

type choiceKind int

const (
	chCommit     choiceKind = iota // commit the shown preview
	chPreview                      // preview (again) before applying
	chEdit                         // resume in the editor
	chDetached                     // resume detached
	chDiff                         // diff against the current body
	chReload                       // record the current revision, keep the text
	chKeep                         // close; the draft stays
	chDiscard                      // ask before discarding
	chDiscardNow                   // discard
	chTitle                        // change a new issue's title
)

type choice struct {
	kind  choiceKind
	label string
}

var choiceLabels = map[choiceKind]string{
	chCommit:     "Apply this change",
	chPreview:    "Preview and apply",
	chEdit:       "Resume in the editor",
	chDetached:   "Edit detached (shows the path)",
	chDiff:       "Diff against the current body",
	chReload:     "Reload: record the current revision, keep the draft's text",
	chKeep:       "Keep the draft for later",
	chDiscard:    "Discard the draft",
	chDiscardNow: "Discard",
	chTitle:      "Change the title",
}

func choices(kinds ...choiceKind) []choice {
	out := make([]choice, len(kinds))
	for i, k := range kinds {
		out[i] = choice{k, choiceLabels[k]}
	}
	return out
}

// diffPage is how far page up and page down scroll the diff.
const diffPage = 10

// panelKey handles a menu-context action in the draft panel.
func (m Model) panelKey(a keys.Action) tea.Cmd {
	p := m.ed.panel
	switch a {
	case keys.Up:
		p.cursor = max(p.cursor-1, 0)
	case keys.Down:
		p.cursor = min(p.cursor+1, len(p.choices)-1)
	case keys.Top:
		p.cursor = 0
	case keys.Bottom:
		p.cursor = len(p.choices) - 1
	case keys.PageDown:
		p.scroll = max(min(p.scroll+diffPage, len(p.diff)-1), 0)
	case keys.PageUp:
		p.scroll = max(p.scroll-diffPage, 0)
	case keys.Cancel:
		return m.choose(chKeep)
	case keys.Open:
		return m.choose(p.choices[p.cursor].kind)
	}
	return nil
}

// choose runs a panel choice on the panel's draft.
func (m Model) choose(k choiceKind) tea.Cmd {
	e := m.ed
	p := e.panel
	d, ed := p.d, e.ed
	switch k {
	case chKeep:
		e.panel = nil
		e.note = "Kept " + draftOf(d.Kind, d.Title) + ": " + path(d) + ". " + m.resumeHint(d.Kind)
		return nil
	case chTitle:
		e.panel = nil
		m.retitlePrompt(d)
		return nil
	case chDiscard:
		e.panel = m.confirmPanel(d)
		return nil
	case chPreview:
		return e.previewCmd(d)
	case chCommit:
		if m.claimLostSince(*p.preview) {
			return e.previewCmd(d) // show the warning before an optimistic apply
		}
	}
	if e.busy {
		e.note = "Wait for the running edit action to finish."
		return nil
	}
	e.busy, e.panel = true, nil
	ctx := context.Background()
	switch k {
	case chCommit:
		pv := *p.preview
		e.note = "Applying " + draftOf(d.Kind, d.Title) + "…"
		return func() tea.Msg { return appliedMsg{ed.Apply(ctx, pv)} }
	case chEdit, chDetached:
		detached := k == chDetached
		e.note = "Resuming " + draftOf(d.Kind, d.Title) + "…"
		return func() tea.Msg {
			return editStartedMsg{s: ed.Resume(ctx, d), detached: detached, t: edit.TargetOf(d)}
		}
	case chDiff:
		return func() tea.Msg { return comparedMsg{ed.DiffCurrent(ctx, d)} }
	case chReload:
		e.note = "Reloading " + nameOf(d.Kind, d.Title) + "…"
		return func() tea.Msg {
			nd, err := ed.Reload(ctx, d)
			return reloadedMsg{nd, err}
		}
	case chDiscardNow:
		e.note = "Discarding " + draftOf(d.Kind, d.Title) + "…"
		return func() tea.Msg { return discardedMsg{d, ed.Discard(ctx, d)} }
	}
	e.busy = false
	return nil
}

// draftPanelView draws the panel full screen: heading, text, choices, the
// visible part of the diff and the key hint.
func (m Model) draftPanelView() string {
	p, km := m.ed.panel, m.prefs.Keymap
	method, plain := cells.Default, false
	if m.b != nil {
		method, plain = m.b.method, m.b.tree.Plain()
	}
	out := []string{sanitize.Title(p.heading), ""}
	out = append(out, p.text...)
	out = append(out, "")
	for i, c := range p.choices {
		line := "  " + c.text(p.d)
		if i == p.cursor {
			line = "> " + c.text(p.d)
			if !plain {
				line = "\x1b[7m" + line + "\x1b[m"
			}
		}
		out = append(out, line)
	}
	hint := km.Hint(keys.Menu, keys.Open, keys.Cancel, keys.PageDown, keys.PageUp)
	room := m.height - len(out) - 2
	if len(p.diff) > 0 && room > 1 {
		out = append(out, "")
		end := min(p.scroll+room-1, len(p.diff))
		for _, l := range p.diff[p.scroll:end] {
			out = append(out, diffLine(l, plain))
		}
		if end < len(p.diff) {
			out = append(out, "… "+plural(len(p.diff)-end, "more line")+" ("+menuKey(km, keys.PageDown)+" scrolls)")
		}
	}
	for len(out) < m.height-1 {
		out = append(out, "")
	}
	if m.height > 1 && len(out) > m.height-1 {
		out = out[:m.height-1]
	}
	out = append(out, hint)
	for i, l := range out {
		out[i] = pad(method, l, m.width)
	}
	return strings.Join(out, "\n")
}

// diffLine is a sanitized diff line, coloured by its kind unless plain.
func diffLine(l string, plain bool) string {
	s := sanitize.Title(l)
	if plain || l == "" {
		return s
	}
	switch l[0] {
	case '+':
		return "\x1b[32m" + s + "\x1b[m"
	case '-':
		return "\x1b[31m" + s + "\x1b[m"
	case '@':
		return "\x1b[36m" + s + "\x1b[m"
	}
	return s
}

func (e *editing) previewCmd(d drafts.Draft) tea.Cmd {
	e.busy, e.panel = true, nil
	e.note = "Previewing " + draftOf(d.Kind, d.Title) + "…"
	ed := e.ed
	return func() tea.Msg { return previewMsg{ed.Preview(context.Background(), d)} }
}

// applied shows an apply's result: on success the draft is gone and the
// tree reloads; on failure the draft is kept and a panel says why.
func (m Model) applied(a edit.Applied) tea.Cmd {
	e := m.ed
	if a.Err != nil && len(a.Result.Items) == 0 {
		e.panel = m.failedPanel(a)
		return nil
	}
	e.untrack(a.Draft.Key())
	e.panel, e.note = nil, appliedNote(a)
	if a.Draft.Kind == drafts.IssueNew {
		e.note = m.createdNote(a)
		m.selectCreated(a)
	}
	return m.refreshTree()
}

// claimLostSince reports that p was previewed while this session held the
// edit claim and the claim has been lost or released since.
func (m Model) claimLostSince(p edit.Preview) bool {
	if p.Lost() || p.Exempt {
		return false
	}
	h, ok := m.ed.ed.Hold(p.Draft.IssueID)
	return !ok || h.State == claims.Lost
}
