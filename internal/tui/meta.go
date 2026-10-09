package tui

import (
	"strconv"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
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
		field("Children", list(i.ChildIDs, title, ""))
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

func list(items []string, f func(string) string, empty string) string {
	if len(items) == 0 {
		return empty
	}
	out := make([]string, len(items))
	for n, it := range items {
		out[n] = f(it)
	}
	return strings.Join(out, ", ")
}

// escape backslash-escapes the ASCII punctuation that forms Markdown
// syntax, so tracker text renders as literal text, and keeps it on one
// line. Only escapes the renderer removes are used: others would show their
// backslash.
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r':
			b.WriteRune(' ')
		case r < 0x80 && strings.ContainsRune("\\`*_{}[]<>()#+-.!|", r):
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// code is s as a code span; code spans take no escapes, so backticks are
// dropped.
func code(s string) string { return "`" + strings.ReplaceAll(s, "`", "") + "`" }

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
