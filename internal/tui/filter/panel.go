package filter

import (
	"slices"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
)

// Result reports what a panel action did.
type Result struct {
	Changed bool // the criteria changed; read them with Criteria
	Search  bool // the user chose to edit the search text
	Close   bool // the panel was closed
}

type rowKind int

const (
	rowState rowKind = iota
	rowClaimed
	rowLabel
	rowAssignee
	rowSearch
	rowClear
)

type row struct {
	kind  rowKind
	value string // the label or assignee
}

// Panel lists every filter as a row: open is the menu context's open key and
// changes the selected row, cancel closes the panel. Changes apply at once.
type Panel struct {
	crit           Criteria
	rows           []row
	cursor, offset int
}

// NewPanel shows c, offering the labels and assignees of issues (and those
// c selects, so they can be removed).
func NewPanel(c Criteria, issues []protocol.IssueSummary) *Panel {
	labels, assignees := slices.Clone(c.Labels), append([]string{""}, c.Assignees...)
	for _, i := range issues {
		labels = append(labels, i.Labels...)
		assignees = append(assignees, assignee(i))
	}
	p := &Panel{crit: c, rows: []row{{kind: rowState}, {kind: rowClaimed}}}
	for _, l := range sorted(labels) {
		p.rows = append(p.rows, row{rowLabel, l})
	}
	for _, a := range sorted(assignees) {
		p.rows = append(p.rows, row{rowAssignee, a})
	}
	p.rows = append(p.rows, row{kind: rowSearch}, row{kind: rowClear})
	return p
}

func sorted(vs []string) []string {
	slices.Sort(vs)
	return slices.Compact(vs)
}

// Criteria returns the filters as the panel has changed them.
func (p *Panel) Criteria() Criteria { return p.crit }

// Do performs a menu-context action.
func (p *Panel) Do(a keys.Action) Result {
	switch a {
	case keys.Up:
		p.cursor = max(p.cursor-1, 0)
	case keys.Down:
		p.cursor = min(p.cursor+1, len(p.rows)-1)
	case keys.Top, keys.PageUp:
		p.cursor = 0
	case keys.Bottom, keys.PageDown:
		p.cursor = len(p.rows) - 1
	case keys.Cancel:
		return Result{Close: true}
	case keys.Open:
		return p.change(p.rows[p.cursor])
	}
	return Result{}
}

func (p *Panel) change(r row) Result {
	c := &p.crit
	switch r.kind {
	case rowState:
		c.State = next(c.State, "", "open", "closed")
	case rowClaimed:
		c.Claimed = next(c.Claimed, "", Claimed, Unclaimed)
	case rowLabel:
		c.Labels = toggle(c.Labels, r.value)
	case rowAssignee:
		c.Assignees = toggle(c.Assignees, r.value)
	case rowSearch:
		return Result{Search: true}
	case rowClear:
		*c = Criteria{}
	}
	return Result{Changed: true}
}

// next returns the value after v in cycle.
func next(v string, cycle ...string) string {
	return cycle[(slices.Index(cycle, v)+1)%len(cycle)]
}

func toggle(set []string, v string) []string {
	if i := slices.Index(set, v); i >= 0 {
		return slices.Delete(slices.Clone(set), i, i+1)
	}
	return sorted(append(slices.Clone(set), v))
}

func (p *Panel) text(r row) string {
	c := p.crit
	check := func(set []string, v string) string {
		if slices.Contains(set, v) {
			return "[x] "
		}
		return "[ ] "
	}
	switch r.kind {
	case rowState:
		return "State: " + or(c.State, "any")
	case rowClaimed:
		return "Claimed: " + or(c.Claimed, "any")
	case rowLabel:
		return "Label: " + check(c.Labels, r.value) + Name(r.value)
	case rowAssignee:
		return "Assignee: " + check(c.Assignees, r.value) + Name(r.value)
	case rowSearch:
		if c.Search == "" {
			return "Search: none"
		}
		return "Search: " + Quote(c.Search)
	}
	return "Clear all filters"
}

func or(v, empty string) string {
	if v == "" {
		return empty
	}
	return v
}

// Lines draws the panel in exactly h lines of exactly w cells: a title,
// then the rows, scrolled to show the selected one.
func (p *Panel) Lines(method cells.Method, w, h int, plain bool) []string {
	if h <= 0 {
		return nil
	}
	out := []string{fit(method, "Filters (labels and assignees: any of; kinds combine)", w)}
	room := h - 1
	p.offset = min(max(p.offset, p.cursor-room+1), p.cursor)
	p.offset = max(min(p.offset, len(p.rows)-room), 0)
	for i := p.offset; i < len(p.rows) && len(out) < h; i++ {
		marker := "  "
		if i == p.cursor && plain {
			marker = "> "
		}
		line := fit(method, marker+p.text(p.rows[i]), w)
		if i == p.cursor && !plain {
			line = "\x1b[7m" + line + "\x1b[m"
		}
		out = append(out, line)
	}
	for len(out) < h {
		out = append(out, strings.Repeat(" ", w))
	}
	return out
}

// fit clips s to w cells and pads it.
func fit(method cells.Method, s string, w int) string {
	s = cells.Clip(method, s, w, "…")
	return s + strings.Repeat(" ", max(w-cells.Width(method, s), 0))
}
