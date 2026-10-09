package tree

import "github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"

// SetFilter shows only the issues match accepts, each with its ancestors as
// context, expanded so that every match shows; nil shows every issue again.
// The selection stays while it is shown; one the filter hides moves to the
// first match. The filter keeps its own expansion: clearing it restores the
// tree's, with the selection's ancestors expanded. Later SetIssues calls
// apply the same filter.
func (m *Model) SetFilter(match func(protocol.IssueSummary) bool) Change {
	prev, screen := m.selected, m.cursor-m.offset
	m.match = match
	if match == nil {
		m.hits, m.keep, m.fexpanded = nil, nil, nil
		return m.reselect(prev, screen)
	}
	m.fexpanded = map[string]bool{}
	m.refilter()
	if m.shown(prev) {
		return m.reselect(prev, screen)
	}
	m.reindex()
	m.cursor, m.selected = min(0, len(m.rows)-1), ""
	for i, r := range m.rows {
		if m.hits[r.id] {
			m.cursor = i
			break
		}
	}
	if m.cursor >= 0 {
		m.selected = m.rows[m.cursor].id
	}
	m.follow()
	return Change{Selection: m.selected != prev}
}

// Filtered reports whether a filter is set.
func (m *Model) Filtered() bool { return m.match != nil }

// Matches is the number of loaded issues the filter accepts; without a
// filter, every loaded issue.
func (m *Model) Matches() int {
	if m.match == nil {
		return len(m.f.nodes)
	}
	return len(m.hits)
}

// Match reports whether the filter accepts id: false for an issue shown only
// as an ancestor of a match.
func (m *Model) Match(id string) bool {
	if m.match == nil {
		return m.f.nodes[id] != nil
	}
	return m.hits[id]
}

// refilter applies the filter to the loaded issues. Ancestors new to the
// filtered view start expanded; the user's later changes are kept.
func (m *Model) refilter() {
	m.hits, m.keep = m.f.filter(m.match)
	for id := range m.keep {
		if _, set := m.fexpanded[id]; !set && len(m.children(id)) > 0 {
			m.fexpanded[id] = true
		}
	}
}

// shown reports whether id is loaded and not hidden by the filter.
func (m *Model) shown(id string) bool {
	return id != "" && m.f.nodes[id] != nil && (m.keep == nil || m.keep[id])
}

// expansion is the expanded set in effect: the filter's while one is set.
func (m *Model) expansion() map[string]bool {
	if m.match != nil {
		return m.fexpanded
	}
	return m.expanded
}

// children lists id's children in the current view.
func (m *Model) children(id string) []string {
	if n := m.f.nodes[id]; n != nil {
		return kept(n.children, m.keep)
	}
	return nil
}
