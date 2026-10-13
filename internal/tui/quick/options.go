package quick

import (
	"slices"
	"strings"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/render/sanitize"
)

// Pickers offer what the loaded project already uses (Proposal): its
// labels and assignees, and typed new values; its issues for parents and
// links. Issues of other projects are not offered.

func statePicker(i protocol.IssueSummary) *picker {
	p := &picker{title: "State of " + quoted(i.Title)}
	for _, s := range []string{"open", "closed"} {
		label := s
		if s == i.State {
			label += " (current)"
		}
		p.options = append(p.options, option{label: label, value: s})
	}
	if i.State == "open" {
		p.cursor = 1
	}
	return p
}

func labelsPicker(i protocol.IssueSummary, issues []protocol.IssueSummary) *picker {
	p := &picker{title: "Labels of " + quoted(i.Title) + " (enter toggles)", multi: true, create: "add the new label ", original: map[string]bool{}}
	p.options = append(p.options, option{kind: optSubmit, label: "Preview the change"})
	var all []string
	for _, other := range issues {
		all = append(all, other.Labels...)
	}
	all = append(all, i.Labels...)
	slices.Sort(all)
	for _, l := range slices.Compact(all) {
		on := slices.Contains(i.Labels, l)
		p.original[l] = on
		p.options = append(p.options, option{label: sanitize.Title(l), value: l, checked: on})
	}
	if len(p.options) > 1 {
		p.cursor = 1
	}
	return p
}

func assigneePicker(i protocol.IssueSummary, issues []protocol.IssueSummary) *picker {
	p := &picker{title: "Assignee of " + quoted(i.Title), create: "assign to "}
	current := ""
	if i.Assignee != nil {
		current = *i.Assignee
	}
	names := []string{}
	for _, other := range append(slices.Clone(issues), i) {
		if other.Assignee != nil {
			names = append(names, *other.Assignee)
		}
	}
	slices.Sort(names)
	label := func(text, value string) string {
		if value == current {
			return text + " (current)"
		}
		return text
	}
	p.options = append(p.options, option{label: label("unassigned", ""), value: ""})
	for _, n := range slices.Compact(names) {
		p.options = append(p.options, option{label: label(sanitize.Title(n), n), value: n})
	}
	return p
}

// candidates are the project's issues other than i, in title order, with
// excluded removed.
func candidates(i protocol.IssueSummary, issues []protocol.IssueSummary, excluded map[string]bool) []option {
	var out []option
	for _, other := range issues {
		if other.ID == i.ID || excluded[other.ID] {
			continue
		}
		label := sanitize.Title(other.Title)
		if other.State == "closed" {
			label += " (closed)"
		}
		out = append(out, option{label: label, value: other.ID})
	}
	slices.SortStableFunc(out, func(a, b option) int { return strings.Compare(strings.ToLower(a.label), strings.ToLower(b.label)) })
	return out
}

func parentPicker(i protocol.IssueSummary, issues []protocol.IssueSummary) *picker {
	p := &picker{title: "Parent of " + quoted(i.Title) + " (type to filter)"}
	if i.ParentID != nil {
		p.options = append(p.options, option{label: "no parent (make it a root)", value: ""})
	}
	// A descendant cannot become the parent; the current one stays listed.
	children := map[string][]string{}
	for _, other := range issues {
		if other.ParentID != nil {
			children[*other.ParentID] = append(children[*other.ParentID], other.ID)
		}
	}
	excluded := map[string]bool{}
	for queue := slices.Clone(children[i.ID]); len(queue) > 0; queue = queue[1:] {
		if !excluded[queue[0]] {
			excluded[queue[0]] = true
			queue = append(queue, children[queue[0]]...)
		}
	}
	for _, o := range candidates(i, issues, excluded) {
		if i.ParentID != nil && o.value == *i.ParentID {
			o.label += " (current)"
		}
		p.options = append(p.options, o)
	}
	return p
}

func relationPicker(i protocol.IssueSummary) *picker {
	p := &picker{title: "Link " + quoted(i.Title)}
	for _, r := range []string{BlockedBy, Blocks, Related} {
		p.options = append(p.options, option{label: quoted(i.Title) + " " + RelationText(r) + " …", value: r})
	}
	return p
}

// linked are i's current links by relation.
func linked(i protocol.IssueSummary) map[string][]string {
	return map[string][]string{Blocks: i.Blocks, BlockedBy: i.BlockedBy, Related: i.Related}
}

func linkTargetPicker(i protocol.IssueSummary, relation string, issues []protocol.IssueSummary) *picker {
	p := &picker{title: quoted(i.Title) + " " + RelationText(relation) + " … (type to filter)"}
	excluded := map[string]bool{}
	for _, id := range linked(i)[relation] {
		excluded[id] = true
	}
	p.options = candidates(i, issues, excluded)
	return p
}

// unlinkPicker lists i's links; a value is the relation, a space and the
// other issue's UUID.
func unlinkPicker(i protocol.IssueSummary, title func(string) string) *picker {
	p := &picker{title: "Unlink " + quoted(i.Title)}
	for _, r := range []string{BlockedBy, Blocks, Related} {
		for _, id := range linked(i)[r] {
			p.options = append(p.options, option{label: RelationText(r) + " " + title(id), value: r + " " + id})
		}
	}
	return p
}
