package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/detail"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/filter"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/recovery"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/sanitize"
)

// filtering is the current project's filters and search. The search text in
// crit is applied only once the service has answered it (found); a search
// that fails is reported and leaves the previous one in effect.
type filtering struct {
	crit    filter.Criteria
	found   filter.Found  // the answer for crit.Search
	gen     int           // the current search; older answers are dropped
	running string        // the text being searched, or ""
	failed  *filter.Found // the last search that failed
	panel   *filter.Panel
	input   *filter.Input
}

type searchMsg struct {
	gen   int
	found filter.Found
}

// modal reports whether the filter panel or the search input has the keys.
func (f *filtering) modal() bool { return f.panel != nil || f.input != nil }

// filterAction performs a tree action of the filters, if action is one.
func (b *browser) filterAction(action keys.Action, d *detail.Model) (tea.Cmd, bool) {
	switch action {
	case keys.Search:
		b.openSearch()
	case keys.Filter:
		b.filt.panel = filter.NewPanel(b.filt.crit, b.last.Items)
	case keys.ClearFilters:
		return b.clearFilters(d), true
	default:
		return nil, false
	}
	return nil, true
}

func (b *browser) openSearch() { b.filt.input = filter.NewInput(b.filt.crit.Search) }

// resetFilters drops every filter and any running search.
func (b *browser) resetFilters() { b.filt = filtering{gen: b.filt.gen + 1} }

func (b *browser) clearFilters(d *detail.Model) tea.Cmd {
	b.resetFilters()
	return b.applyFilters(d)
}

// applyFilters filters the tree by the current criteria.
func (b *browser) applyFilters(d *detail.Model) tea.Cmd {
	crit, found := b.filt.crit, b.filt.found.Issues
	var match func(protocol.IssueSummary) bool
	if crit.Active() {
		match = func(i protocol.IssueSummary) bool { return crit.Match(i, found) }
	}
	if b.tree.SetFilter(match).Selection {
		return b.showSelected(d, false)
	}
	return nil
}

// search asks the service for text in the current project, through the gate.
func (b *browser) search(text string) tea.Cmd {
	if b.project == nil {
		return nil
	}
	b.filt.gen++
	b.filt.running = text
	gen, src, project := b.filt.gen, b.src, b.project.ID
	return func() tea.Msg { return searchMsg{gen, filter.Search(context.Background(), src, project, text)} }
}

// researchAfterLoad runs the applied search again after a load, so its
// matches follow the current content.
func (b *browser) researchAfterLoad() tea.Cmd {
	if b.filt.crit.Search == "" || b.filt.running != "" {
		return nil
	}
	return b.search(b.filt.crit.Search)
}

func (b *browser) searched(msg searchMsg, d *detail.Model) tea.Cmd {
	if msg.gen != b.filt.gen {
		return nil
	}
	b.filt.running = ""
	if msg.found.Err != nil {
		b.filt.failed = &msg.found
		return nil
	}
	b.filt.failed = nil
	b.filt.crit.Search, b.filt.found = msg.found.Query, msg.found
	return b.applyFilters(d)
}

// panelKey handles a menu-context action in the filter panel.
func (b *browser) panelKey(a keys.Action, d *detail.Model) tea.Cmd {
	r := b.filt.panel.Do(a)
	switch {
	case r.Search:
		b.filt.panel = nil
		b.openSearch()
	case r.Close:
		b.filt.panel = nil
	case r.Changed:
		c := b.filt.panel.Criteria()
		if c.Search == "" && b.filt.crit.Search != "" {
			b.filt.gen++ // the search was cleared: drop its answer and any rerun
			b.filt.found, b.filt.running, b.filt.failed = filter.Found{}, "", nil
		}
		b.filt.crit = c
		return b.applyFilters(d)
	}
	return nil
}

// inputKey handles a key press in the search input. An empty search clears
// the search.
func (b *browser) inputKey(msg tea.KeyPressMsg, d *detail.Model) tea.Cmd {
	submit, cancel := b.filt.input.Key(msg, b.km)
	switch {
	case cancel:
		b.filt.input = nil
	case submit:
		text := b.filt.input.Value()
		b.filt.input = nil
		if text != "" {
			return b.search(text)
		}
		b.filt.gen++
		b.filt.crit.Search, b.filt.found, b.filt.running, b.filt.failed = "", filter.Found{}, "", nil
		return b.applyFilters(d)
	}
	return nil
}

// filterUpdate handles the filters' own messages.
func (b *browser) filterUpdate(msg tea.Msg, d *detail.Model) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case searchMsg:
		return b.searched(msg, d), true
	case tea.PasteMsg:
		if b.filt.input != nil {
			b.filt.input.Paste(msg.Content)
			return nil, true
		}
	}
	return nil, false
}

// filterLine describes the filters in effect, how many loaded issues match
// (out of how many, only for a consistent load), the search's progress or
// failure, and the key that clears them; "" when nothing is filtered.
func (b *browser) filterLine() string {
	f := b.filt
	var parts []string
	if f.crit.Active() {
		parts = append(parts, "Filter: "+f.crit.Summary())
		if b.last.Consistent {
			parts = append(parts, fmt.Sprintf("%d of %d issues match", b.tree.Matches(), len(b.last.Items)))
		} else {
			parts = append(parts, fmt.Sprintf("%d loaded issues match (partial load)", b.tree.Matches()))
		}
		if f.found.Truncated {
			parts = append(parts, fmt.Sprintf("search stopped after %d results", f.found.Results))
		}
	}
	if f.running != "" {
		parts = append(parts, "searching "+filter.Quote(f.running)+"…")
	}
	if f.failed != nil {
		parts = append(parts, "search "+filter.Quote(f.failed.Query)+" failed: "+sanitize.Title(recovery.Describe(f.failed.Err)))
	}
	if len(parts) == 0 {
		return ""
	}
	if !f.crit.Active() {
		parts[0] = "Filter: " + parts[0]
	}
	if hint := b.km.Hint(keys.Tree, keys.ClearFilters); hint != "" && f.crit.Active() {
		parts = append(parts, hint)
	}
	return strings.Join(parts, " · ")
}
