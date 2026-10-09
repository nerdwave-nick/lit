package milestones

import (
	"fmt"
	"strings"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/recovery"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/sanitize"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tree"
)

// DescriptionLines is how many lines of the selected milestone's
// description the view shows (Proposal); the editor shows all of it.
const DescriptionLines = 6

// View draws the view as exactly h lines of at most w cells: the header,
// the list, the selected milestone (or its actions), a note and the hint.
// An open title field or its preview takes the body.
func (m *Model) View(method cells.Method, w, h int, km keys.Keymap, plain bool) string {
	out := []string{m.header()}
	var body []string
	hint := km.Hint(keys.Menu, keys.Open, keys.Cancel, keys.Refresh)
	switch {
	case m.qa != nil:
		body, hint = m.qa.Lines(method, w, max(h-3, 0), plain), m.qa.Hint(km)
	default:
		body = m.listLines(method, w, plain, max(h/2-1, 3))
		body = append(append(body, ""), m.selectedLines(method, w, km, plain)...)
	}
	out = append(out, body...)
	tail := []string{hint}
	if m.note != "" {
		tail = append([]string{m.note}, tail...)
	}
	for len(out) < h-len(tail) {
		out = append(out, "")
	}
	if h > len(tail) && len(out) > h-len(tail) {
		out = out[:h-len(tail)]
	}
	out = append(out, tail...)
	for n, l := range out {
		if !strings.HasPrefix(l, "\x1b[7m") {
			out[n] = cells.Clip(method, l, w, "…")
		}
	}
	return strings.Join(out, "\n")
}

func (m *Model) header() string {
	parts := []string{"Milestones of " + quoted(m.env.ProjectTitle)}
	switch {
	case !m.loaded && m.err == nil:
		parts = append(parts, "loading…")
	case m.err != nil:
		parts = append(parts, fmt.Sprintf("%d", len(m.items)), "load failed: "+sanitize.Title(recovery.Describe(m.err)))
	default:
		parts = append(parts, fmt.Sprintf("%d", len(m.items)), "as of "+m.at.Format("15:04:05"))
	}
	if m.loading && m.loaded {
		parts = append(parts, "refreshing…")
	}
	return strings.Join(parts, " · ")
}

// listLines is the "New milestone" row and one row per milestone, scrolled
// to keep the selection within rows lines.
func (m *Model) listLines(method cells.Method, w int, plain bool, rows int) []string {
	all := append([]string{"+ New milestone"}, make([]string, len(m.items))...)
	for n, it := range m.items {
		all[n+1] = quoted(it.Title) + " · " + Progress(it.Progress)
	}
	m.offset = max(min(m.offset, m.cursor), m.cursor-rows+1, 0)
	var out []string
	for n := m.offset; n < len(all) && n < m.offset+rows; n++ {
		out = append(out, row(method, all[n], w, n == m.cursor && m.actions == nil, plain))
	}
	return out
}

// row is a list row; the selected one is marked (plain) or reversed.
func row(method cells.Method, text string, w int, selected, plain bool) string {
	switch {
	case !selected:
		return "  " + text
	case plain:
		return "> " + text
	}
	line := cells.Clip(method, "> "+text, w, "…")
	return "\x1b[7m" + line + strings.Repeat(" ", max(w-cells.Width(method, line), 0)) + "\x1b[m"
}

// Progress describes a milestone's progress in words. An empty milestone
// is an empty scope, never complete.
func Progress(p litclient.MilestoneProgress) string {
	if p.Total == 0 {
		return "no members: an empty scope, not complete"
	}
	s := fmt.Sprintf("%s: %d closed, %d open", plural(p.Total, "member"), p.Closed, p.Open)
	if p.BlockedOpen+p.ClaimedOpen > 0 {
		s += fmt.Sprintf(" (%d blocked, %d claimed)", p.BlockedOpen, p.ClaimedOpen)
	}
	return s
}

// selectedLines describe the selected milestone: revision, description,
// members with their states and open blockers outside it; or its actions.
func (m *Model) selectedLines(method cells.Method, w int, km keys.Keymap, plain bool) []string {
	ms, ok := m.selected()
	if !ok {
		if len(m.items) == 0 && m.loaded {
			return []string{"The project has no milestones yet. Enter on “New milestone” creates one."}
		}
		return []string{"Enter creates a milestone: a named workset of this project's issues."}
	}
	if m.actions != nil {
		out := []string{"Actions for the milestone " + quoted(ms.Title) + ":"}
		for n, label := range actionLabels {
			out = append(out, row(method, label, w, n == *m.actions, plain))
		}
		return out
	}
	out := []string{fmt.Sprintf("%s · revision %d · updated %s", quoted(ms.Title), ms.Revision, when(ms.UpdatedAt))}
	if strings.TrimSpace(ms.Body) == "" {
		out = append(out, "No description.")
	} else {
		lines := strings.Split(strings.TrimRight(sanitize.Body(ms.Body), "\n"), "\n")
		out = append(out, "Description:")
		for _, l := range lines[:min(len(lines), DescriptionLines)] {
			out = append(out, "  "+l)
		}
		if len(lines) > DescriptionLines {
			out = append(out, fmt.Sprintf("  … %s more (the editor shows all of it)", plural(len(lines)-DescriptionLines, "line")))
		}
	}
	if len(ms.IssueIDs) == 0 {
		add := string(keys.SetMilestones)
		if k := km.Keys(keys.Tree, keys.SetMilestones); len(k) > 0 {
			add = k[0]
		}
		return append(out, "No members yet. "+add+" on an issue in the tree adds it.")
	}
	out = append(out, fmt.Sprintf("Members (%d):", len(ms.IssueIDs)))
	for _, id := range ms.IssueIDs {
		out = append(out, "  • "+m.member(id))
	}
	if ext := ms.Progress.ExternalBlockers; len(ext) > 0 {
		names := make([]string, len(ext))
		for n, id := range ext {
			names[n] = m.name(id)
		}
		out = append(out, "Open blockers outside the milestone: "+strings.Join(names, ", "))
	}
	return out
}

// member is a member issue with its state, whether an open blocker blocks
// it, and its claim, as the loaded tree shows them.
func (m *Model) member(id string) string {
	i, ok := m.issue(id)
	if !ok {
		return "issue " + short(id) + " (not loaded)"
	}
	parts := []string{quoted(i.Title), sanitize.Title(i.State)}
	for _, b := range i.BlockedBy {
		if blocker, ok := m.issue(b); !ok || blocker.State == "open" {
			parts = append(parts, "blocked")
			break
		}
	}
	if i.Claim != nil {
		parts = append(parts, "claimed by "+tree.ClaimOwner(i.Claim, true))
	}
	return strings.Join(parts, " · ")
}

func (m *Model) name(id string) string {
	if i, ok := m.issue(id); ok {
		return quoted(i.Title)
	}
	return "issue " + short(id)
}

func (m *Model) issue(id string) (protocol.IssueSummary, bool) {
	for _, i := range m.env.Issues {
		if i.ID == id {
			return i, true
		}
	}
	return protocol.IssueSummary{}, false
}

// when is a record time in local time, or as recorded when it does not
// parse.
func when(at string) string {
	if t, err := time.Parse(time.RFC3339Nano, at); err == nil {
		return t.Local().Format("2006-01-02 15:04")
	}
	return sanitize.Title(at)
}

func quoted(s string) string { return "“" + sanitize.Title(s) + "”" }

func short(id string) string { return id[:min(len(id), 8)] }

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
