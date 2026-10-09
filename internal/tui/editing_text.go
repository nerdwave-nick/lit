package tui

import (
	"fmt"
	"sort"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/claims"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/edit"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/sanitize"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tree"
)

// offerPanel offers an existing draft instead of starting a new one.
func (m Model) offerPanel(d drafts.Draft) *draftPanel {
	return &draftPanel{d: d, heading: titled(d.Title) + " has a draft",
		text: []string{
			fmt.Sprintf("Started %s at revision %d: %s", d.Created().Local().Format("2006-01-02 15:04"), d.Revision, path(d)),
			"A draft is never overwritten: resume, diff, apply or discard it.",
		},
		choices: choices(chEdit, chDetached, chPreview, chDiff, chDiscard, chKeep)}
}

// previewPanel shows a preview: the diff and affected records to confirm,
// or why there is nothing to apply. The draft is kept in every case.
func (m Model) previewPanel(p edit.Preview) *draftPanel {
	d := p.Draft
	t := titled(d.Title)
	panel := &draftPanel{d: d}
	kept := "Nothing was written; the draft is kept: " + path(d) + "."
	switch {
	case p.Err != nil:
		panel.heading = "Cannot preview the draft of " + t
		panel.text = []string{describe(p.Err) + ".", kept}
		panel.choices = choices(chPreview, chEdit, chDiff, chKeep, chDiscard)
	case p.Conflict:
		panel.heading = fmt.Sprintf("%s changed since the draft was started: revision %d → %d", t, d.Revision, p.Current.Revision)
		panel.text = append(m.claimText(p), kept, "Diff it against the current body, or reload to apply it over the new revision.")
		panel.choices = choices(chDiff, chReload, chEdit, chKeep, chDiscard)
	case p.Unchanged:
		panel.heading = "The draft of " + t + " has no changes"
		panel.text = []string{fmt.Sprintf("It matches revision %d. Discarding it ends the edit and releases an edit claim.", d.Revision)}
		panel.choices = choices(chDiscardNow, chEdit, chKeep)
	case p.Foreign != nil:
		panel.heading = "Cannot apply the draft of " + t
		panel.text = []string{t + " is claimed by " + owner(p.Foreign) + ".", kept}
		panel.choices = choices(chDiff, chReload, chKeep, chDiscard)
	default:
		panel.heading = "Apply the draft of " + t + "?"
		panel.preview = &p
		panel.text = append(m.claimText(p), "Affected records:")
		panel.text = append(panel.text, m.affected(p)...)
		panel.text = append(panel.text, fmt.Sprintf("Diff against revision %d:", d.Revision))
		panel.diff = p.Diff()
		panel.choices = m.previewChoices(panel)
	}
	return panel
}

func (m Model) previewChoices(p *draftPanel) []choice {
	if p.stale {
		return choices(chPreview, chEdit, chDetached, chKeep, chDiscard)
	}
	return choices(chCommit, chEdit, chDetached, chKeep, chDiscard)
}

// claimText describes the edit claim before confirming: a lost or missing
// claim makes the apply optimistic.
func (m Model) claimText(p edit.Preview) []string {
	t := titled(p.Draft.Title)
	optimistic := fmt.Sprintf("Applying is optimistic: it is refused if the issue changed after revision %d or another client claims it.", p.Draft.Revision)
	switch {
	case !p.Held:
		return []string{"Warning: this session holds no claim on " + t + " (it was lost or released). " + optimistic}
	case p.Hold.State == claims.Lost:
		return []string{"Warning: the claim on " + t + " was lost: " + sanitize.Title(p.Hold.Reason) + ". " + optimistic}
	case p.Hold.State == claims.Paused:
		return []string{"The claim on " + t + " is held, but its renewal is paused: answer the idle question to keep it."}
	case p.Draft.Claim == drafts.ClaimExplicit:
		return []string{"Your claim on " + t + " is held; it is kept after applying."}
	}
	return []string{"The edit claim on " + t + " is held; applying releases it."}
}

// affected lists the preview's records with their fenced revisions.
func (m Model) affected(p edit.Preview) []string {
	var out []string
	for i, r := range p.Affected {
		what := "(read)"
		if i < len(p.Prepared.Intent().Targets) {
			what = "(changed)"
		}
		rev := "new"
		if r.ExpectedRevision != nil {
			rev = fmt.Sprintf("at revision %d", *r.ExpectedRevision)
		}
		out = append(out, fmt.Sprintf("  %s %s %s", m.recordName(r), rev, what))
	}
	return out
}

func (m Model) recordName(r protocol.ObjectRef) string {
	switch {
	case r.Type == "issues" && m.b != nil:
		if s, ok := m.b.tree.Issue(r.ID); ok {
			return "issue " + titled(s.Title)
		}
	case r.Type == "projects" && m.b != nil && m.b.project != nil && m.b.project.ID == r.ID:
		return "project " + titled(m.b.project.Title)
	}
	return sanitize.Title(r.Type) + " " + short(r.ID)
}

// failedPanel says why an apply failed; the draft is kept.
func (m Model) failedPanel(a edit.Applied) *draftPanel {
	d := a.Draft
	t := titled(d.Title)
	panel := &draftPanel{d: d, heading: "Cannot apply the draft of " + t, choices: choices(chDiff, chReload, chKeep, chDiscard)}
	kept := "Nothing was written; the draft is kept: " + path(d) + "."
	switch code := litclient.Code(a.Err); {
	case a.Uncertain != nil:
		panel.heading = "The outcome of applying the draft of " + t + " is uncertain"
		panel.text = []string{"Request " + a.Uncertain.ID + ", evidence " + sanitize.Title(a.Uncertain.Path) + ".",
			"It is never replayed. Check it with " + firstKey(m, keys.Global, keys.UncertainRequests) + " (uncertain requests) or lit transactions status.",
			"The draft and the claim are kept: " + path(d) + "."}
		panel.choices = choices(chDiff, chKeep)
	case code == "claim_conflict" && a.Owner != nil:
		panel.text = []string{t + " is claimed by " + owner(a.Owner) + ".", kept}
	case code == "revision_conflict":
		panel.text = []string{t + " changed after the preview.", kept}
	default:
		panel.text = []string{describe(a.Err) + ".", kept}
	}
	return panel
}

// diffPanel compares the draft with the issue's current body.
func (m Model) diffPanel(c edit.Compared) *draftPanel {
	d := c.Draft
	panel := &draftPanel{d: d, heading: "The draft of " + titled(d.Title) + " against the current body",
		choices: choices(chPreview, chEdit, chKeep, chDiscard)}
	switch {
	case c.Err != nil:
		panel.text = []string{"Cannot compare: " + describe(c.Err) + ".", "The draft is kept: " + path(d) + "."}
	case c.Current.Revision != d.Revision:
		panel.text = []string{fmt.Sprintf("Current revision %d; the draft records revision %d.", c.Current.Revision, d.Revision)}
		panel.choices = choices(chReload, chEdit, chKeep, chDiscard)
	default:
		panel.text = []string{fmt.Sprintf("Current revision %d, as the draft records.", c.Current.Revision)}
	}
	if panel.diff = c.Diff; c.Err == nil && len(c.Diff) == 0 {
		panel.text = append(panel.text, "No differences.")
	}
	return panel
}

func (m Model) confirmPanel(d drafts.Draft) *draftPanel {
	return &draftPanel{d: d, heading: "Discard the draft of " + titled(d.Title) + "?",
		text:    []string{"Its text is deleted: " + path(d) + ".", "An edit claim is released; a claim you took yourself is kept."},
		choices: []choice{{chDiscardNow, "Discard"}, {chKeep, "Keep the draft"}}}
}

func appliedNote(a edit.Applied) string {
	note := "Applied the draft of " + titled(a.Draft.Title)
	for _, it := range a.Result.Items {
		if it.Type == "issues" && it.ID == a.Draft.IssueID {
			note += fmt.Sprintf(": revision %d → %d", it.BeforeRevision, it.Revision)
		}
	}
	note += "."
	if a.Released {
		note += " Released the edit claim."
	}
	if a.Err != nil {
		note += " The draft could not be removed: " + describe(a.Err) + "."
	}
	return note
}

func discardNote(d drafts.Draft, r edit.Discarded) string {
	if r.Err != nil {
		return "Cannot discard the draft of " + titled(d.Title) + ": " + describe(r.Err) + "."
	}
	note := "Discarded the draft of " + titled(d.Title) + "."
	if r.Released {
		note += " Released the edit claim."
	}
	return note
}

// editLines are the footer's open detached drafts and the last note.
func (m Model) editLines() []string {
	e := m.ed
	if e == nil {
		return nil
	}
	var out []string
	var open []*openDraft
	for _, o := range e.open {
		if o.detached {
			open = append(open, o)
		}
	}
	sort.Slice(open, func(i, j int) bool { return open[i].d.CreatedAt < open[j].d.CreatedAt })
	for _, o := range open {
		line := "Draft open: " + titled(o.d.Title)
		if !o.changed.IsZero() {
			line += " · changed " + o.changed.Format("15:04:05")
		}
		out = append(out, line+" · "+m.editKey()+" applies it", "  "+path(o.d))
	}
	if e.note != "" {
		out = append(out, e.note)
	}
	return out
}

func owner(c *protocol.Claim) string {
	return tree.ClaimOwner(c, true) + " until " + sanitize.Title(c.ExpiresAt)
}

func path(d drafts.Draft) string { return sanitize.Title(d.Path()) }
