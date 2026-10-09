package tui

import (
	"strconv"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/issue"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tree"
)

// issueMarkdown is the detail's metadata section for an issue summary:
// title, state, labels, assignee, claim (owner by name), hierarchy and
// links. Text from the tracker is escaped, so it never forms Markdown, and
// the renderer sanitizes it. The issue detail adds the body and comments.
func issueMarkdown(i protocol.IssueSummary, project *protocol.Project, t *tree.Model) string {
	var b strings.Builder
	b.WriteString("# " + escape(i.Title) + "\n\n")
	field := func(name, value string) { b.WriteString("- **" + name + ":** " + value + "\n") }
	field("State", escape(i.State))
	field("Labels", list(i.Labels, escape, "none"))
	assignee := "unassigned"
	if i.Assignee != nil {
		assignee = escape(*i.Assignee)
	}
	field("Assignee", assignee)
	claim := "unclaimed"
	if i.Claimed && i.Claim != nil {
		claim = escape(tree.ClaimOwner(i.Claim, true)) + ", until " + escape(i.Claim.ExpiresAt)
	}
	field("Claim", claim)
	title := func(id string) string {
		if other, ok := t.Issue(id); ok {
			return escape(other.Title)
		}
		return code(short(id))
	}
	if i.ParentID != nil {
		field("Parent", title(*i.ParentID))
	}
	if len(i.ChildIDs) > 0 {
		field("Children", strconv.Itoa(len(i.ChildIDs))+": "+list(ordered(i.ID, i.ChildIDs, t), title, ""))
	}
	for _, l := range []struct {
		name string
		ids  []string
	}{{"Blocked by", i.BlockedBy}, {"Blocks", i.Blocks}, {"Related", i.Related}} {
		if len(l.ids) > 0 {
			field(l.name, list(l.ids, title, ""))
		}
	}
	field("Updated", escape(i.UpdatedAt)+" (revision "+strconv.FormatInt(i.Revision, 10)+")")
	b.WriteString("\n" + code(i.ID))
	if project != nil {
		b.WriteString(" in " + escape(project.Title))
	}
	b.WriteString("\n")
	return b.String()
}

// maxListed bounds how many related issues a field names.
const maxListed = 8

func list(items []string, f func(string) string, empty string) string {
	if len(items) == 0 {
		return empty
	}
	out := make([]string, 0, min(len(items), maxListed+1))
	for _, it := range items[:min(len(items), maxListed)] {
		out = append(out, f(it))
	}
	if len(items) > maxListed {
		out = append(out, "and "+strconv.Itoa(len(items)-maxListed)+" more")
	}
	return strings.Join(out, ", ")
}

// ordered puts parent's loaded children in tree order, then any not loaded.
func ordered(parent string, ids []string, t *tree.Model) []string {
	if t == nil {
		return ids
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var out []string
	for _, id := range t.Children(parent) {
		if want[id] {
			out = append(out, id)
			delete(want, id)
		}
	}
	for _, id := range ids {
		if want[id] {
			out = append(out, id)
		}
	}
	return out
}

// escape makes tracker text literal Markdown on one line (issue.Escape).
func escape(s string) string { return issue.Escape(s) }

// code is s as a code span; code spans take no escapes, so backticks are
// dropped.
func code(s string) string { return "`" + strings.ReplaceAll(s, "`", "") + "`" }

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
