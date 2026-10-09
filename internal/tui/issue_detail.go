package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/detail"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/issue"
)

// fullDetail is the issue detail's load of the selection's body and
// comments. A load starts when the detail of a new selection is shown (wide
// layouts show it beside the tree; narrow ones when it opens) and on every
// reload. A newer load cancels the older one, and a result that is not for
// the current load (generation, issue and revision) is dropped.
type fullDetail struct {
	gen     int
	id      string
	rev     int64         // the summary revision the load started at
	got     *issue.Detail // the last load for the selection; nil before it
	running bool
	cancel  context.CancelFunc
}

type detailMsg struct {
	gen int
	id  string
	rev int64
	got issue.Detail
}

// detailShown reports whether the detail is on screen.
func (b *browser) detailShown() bool { return b.wide || b.focus == paneDetail }

// sections are the detail of the selection sel: its metadata, then, once
// the detail is shown, its body and comments or a loading note.
func (b *browser) sections(sel protocol.IssueSummary) []detail.Section {
	secs := []detail.Section{{RecordID: sel.ID, Revision: sel.Revision, Content: issueMarkdown(sel, b.project, b.tree)}}
	if b.full.got == nil && !b.full.running {
		return secs
	}
	return append(secs, issue.Sections(sel.ID, b.full.got)...)
}

// loadFull starts loading sel's body and comments, cancelling an older load.
func (b *browser) loadFull(sel protocol.IssueSummary) tea.Cmd {
	b.stopFull()
	ctx, cancel := context.WithCancel(context.Background())
	b.full.gen++
	b.full.id, b.full.rev, b.full.running, b.full.cancel = sel.ID, sel.Revision, true, cancel
	gen, src, project := b.full.gen, b.src, b.project.ID
	return func() tea.Msg {
		defer cancel()
		return detailMsg{gen, sel.ID, sel.Revision, issue.Load(ctx, src, project, sel.ID)}
	}
}

func (b *browser) stopFull() {
	if b.full.cancel != nil {
		b.full.cancel()
	}
	b.full.running, b.full.cancel = false, nil
}

// dropFull forgets the load of a previous selection.
func (b *browser) dropFull() {
	b.stopFull()
	b.full.got = nil
}

// loadedFull shows a load's result if it is still the one wanted.
func (b *browser) loadedFull(msg detailMsg, d *detail.Model) tea.Cmd {
	sel, ok := b.tree.Selected()
	if msg.gen != b.full.gen || msg.id != b.full.id || msg.rev != b.full.rev || !ok || sel.ID != msg.id {
		return nil
	}
	b.full.running, b.full.cancel = false, nil
	b.full.got = &msg.got
	return d.SetSections(b.sections(sel)...)
}
