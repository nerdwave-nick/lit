// Package filter is how lit-tui narrows the issue tree: filter criteria by
// state, label, assignee and claim, a content search with lit grep's
// semantics, the filter panel and the search input. The tree keeps the
// ancestors of every match visible (tree.Model.SetFilter).
package filter

import (
	"slices"
	"strings"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/render/sanitize"
)

// Claim filter values.
const (
	Claimed   = "claimed"
	Unclaimed = "unclaimed"
)

// Criteria are the active filters. Kinds combine with AND; the values of one
// kind (labels, assignees) with OR. Zero values do not filter.
type Criteria struct {
	State     string   // "open" or "closed"
	Claimed   string   // Claimed or Unclaimed
	Labels    []string // any of these labels
	Assignees []string // any of these assignees; "" is unassigned
	Search    string   // literal text the service found (see Search)
}

// Active reports whether any filter is set.
func (c Criteria) Active() bool {
	return c.State != "" || c.Claimed != "" || len(c.Labels) > 0 || len(c.Assignees) > 0 || c.Search != ""
}

// Match reports whether c accepts i; found holds the issues the search found.
func (c Criteria) Match(i protocol.IssueSummary, found map[string]bool) bool {
	switch {
	case c.State != "" && i.State != c.State:
		return false
	case c.Claimed != "" && i.Claimed != (c.Claimed == Claimed):
		return false
	case len(c.Labels) > 0 && !slices.ContainsFunc(i.Labels, func(l string) bool { return slices.Contains(c.Labels, l) }):
		return false
	case len(c.Assignees) > 0 && !slices.Contains(c.Assignees, assignee(i)):
		return false
	case c.Search != "" && !found[i.ID]:
		return false
	}
	return true
}

func assignee(i protocol.IssueSummary) string {
	if i.Assignee == nil {
		return ""
	}
	return *i.Assignee
}

// Summary describes the filters in one line, for example
// `open · claimed · label bug, ui · assignee unassigned · search "renewal"`.
func (c Criteria) Summary() string {
	var parts []string
	for _, v := range []string{c.State, c.Claimed} {
		if v != "" {
			parts = append(parts, v)
		}
	}
	if len(c.Labels) > 0 {
		parts = append(parts, "label "+names(c.Labels))
	}
	if len(c.Assignees) > 0 {
		parts = append(parts, "assignee "+names(c.Assignees))
	}
	if c.Search != "" {
		parts = append(parts, "search "+Quote(c.Search))
	}
	return strings.Join(parts, " · ")
}

// Quote shows a search text, sanitized, in double quotes.
func Quote(text string) string { return `"` + sanitize.Title(text) + `"` }

// Name shows a label or assignee; "" is unassigned.
func Name(v string) string {
	if v == "" {
		return "unassigned"
	}
	return sanitize.Title(v)
}

func names(vs []string) string {
	out := make([]string, len(vs))
	for n, v := range vs {
		out[n] = Name(v)
	}
	return strings.Join(out, ", ")
}
