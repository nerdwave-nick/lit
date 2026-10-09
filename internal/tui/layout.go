package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/detail"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/recovery"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/sanitize"
)

// Layout constants (Proposals). At WideBreakpoint columns and more, the tree
// and the detail are side by side; the tree takes two fifths, within
// [MinTree, MaxTree] columns.
const (
	WideBreakpoint = 100
	MinTree        = 30
	MaxTree        = 64
	separator      = "│"
	crumbSep       = " › "
)

// geometry is where the browser's parts go on screen.
type geometry struct {
	wide           bool
	top, height    int // body rows: first screen row and count
	treeW, detailX int // wide: tree width and the detail's first column
	detailW        int
}

func (m Model) geometry() geometry {
	b := m.b
	footer := len(m.footer())
	crumb := 0
	if b.project != nil && !b.switching {
		crumb = 1
	}
	g := geometry{top: 1 + crumb, wide: m.width >= WideBreakpoint}
	g.height = max(m.height-g.top-footer-1, 0)
	g.treeW, g.detailW = m.width, m.width
	if g.wide {
		g.treeW = min(max(m.width*2/5, MinTree), MaxTree)
		g.detailX = g.treeW + 1
		g.detailW = m.width - g.detailX
	}
	return g
}

// relayout sizes the parts after every update.
func (m Model) relayout() tea.Cmd {
	if m.b == nil {
		return nil
	}
	g := m.geometry()
	m.b.tree.SetSize(g.treeW, g.height)
	m.b.switcher.SetSize(m.width, g.height)
	cmd := m.detail.SetSize(g.detailW, g.height)
	if g.wide != m.b.wide {
		m.b.wide = g.wide
		if g.wide && m.b.project != nil {
			cmd = tea.Batch(cmd, m.b.showSelected(m.detail, false))
		}
	}
	return cmd
}

// mouse handles clicks and the wheel (only delivered with the mouse
// preference on): in the switcher, the tree, or the detail beside it.
func (m Model) mouse(msg tea.MouseMsg) tea.Cmd {
	b, g, ms := m.b, m.geometry(), msg.Mouse()
	y := ms.Y - g.top
	if y < 0 || y >= g.height || b.menu != nil || b.filt.modal() {
		return nil
	}
	_, wheel := msg.(tea.MouseWheelMsg)
	step := 0
	switch ms.Button {
	case tea.MouseWheelUp:
		step = -detail.WheelLines
	case tea.MouseWheelDown:
		step = detail.WheelLines
	}
	switch {
	case b.switching && wheel:
		b.switcher.Wheel(step)
	case b.switching:
		return b.switched(b.switcher.Click(y), m.detail)
	case b.project == nil:
	case g.wide && ms.X >= g.detailX || !g.wide && b.focus == paneDetail:
		if !wheel {
			b.focus = paneDetail
			b.tree.SetFocused(false)
			return nil
		}
		return m.detail.Update(msg)
	case wheel:
		if step != 0 && b.tree.Wheel(step).Selection {
			return b.showSelected(m.detail, false)
		}
	default:
		b.focus = paneTree
		b.tree.SetFocused(true)
		if b.tree.Click(y).Selection {
			return b.showSelected(m.detail, false)
		}
	}
	return nil
}

// footer is the session line, the recovery notes and the browser's note.
func (m Model) footer() []string {
	lines := m.life.Lines()
	if m.b != nil && m.b.note != "" {
		lines = append(lines, m.b.note)
	}
	if m.b != nil && m.b.project != nil && !m.b.switching {
		if l := m.b.filterLine(); l != "" {
			lines = append(lines, l)
		}
	}
	return append(append(lines, m.claimLines()...), m.editLines()...)
}

func (m Model) View() tea.View {
	km := m.prefs.Keymap
	lines := strings.Join(m.life.Lines(), "\n")
	var content string
	switch overlay := m.life.Overlay(km); {
	case m.uv != nil:
		content = m.uncertainView()
	case overlay != "":
		content = strings.TrimRight(overlay+"\n\n"+lines, "\n")
	case m.claimsPrompt() != "":
		content = strings.TrimRight(m.claimsPrompt()+"\n\n"+strings.Join(m.footer(), "\n"), "\n")
	case m.hv != nil:
		content = m.historyView()
	case m.ed != nil && m.ed.panel != nil:
		content = m.draftPanelView()
	case m.help:
		content = strings.Join(km.Help(m.view()), "\n") + "\n\n" + km.Hint(keys.Menu, keys.Cancel)
	case m.doc != nil:
		content = cells.Clip(cells.Default, "lit-tui · "+m.endpoint, m.width, "…") + "\n" + m.detail.View() + "\n" +
			cells.Clip(cells.Default, km.Hint(keys.Detail, keys.NextBlock, keys.Quit, keys.Help), m.width, "…")
	case m.b != nil:
		content = m.browserView()
	default:
		content = "lit-tui\n\nService: " + m.endpoint + "\n\n" + km.Hint(keys.Global, keys.Quit, keys.Help)
		if lines != "" {
			content += "\n\n" + lines
		}
	}
	v := tea.NewView(content)
	v.AltScreen = true
	if m.prefs.Mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

func (m Model) browserView() string {
	b, g := m.b, m.geometry()
	out := []string{m.clip(m.header())}
	if g.top > 1 {
		out = append(out, m.crumb())
	}
	switch {
	case b.menu != nil:
		out = append(out, b.menu.lines(b.method, m.width, g.height, b.tree.Plain())...)
	case b.filt.panel != nil:
		out = append(out, b.filt.panel.Lines(b.method, m.width, g.height, b.tree.Plain())...)
	case b.switching:
		out = append(out, splitLines(b.switcher.View(), g.height)...)
	case b.project == nil:
		body := []string{"No project selected.", "", "Press " + firstKey(m, keys.Global, keys.SwitchProject) + " to choose a project."}
		for len(body) < g.height {
			body = append(body, "")
		}
		out = append(out, body[:g.height]...)
	case g.wide:
		left, right := splitLines(b.tree.View(), g.height), splitLines(m.detail.View(), g.height)
		for i := range g.height {
			out = append(out, left[i]+separator+right[i])
		}
	case b.focus == paneDetail:
		out = append(out, splitLines(m.detail.View(), g.height)...)
	default:
		out = append(out, splitLines(b.tree.View(), g.height)...)
	}
	for _, l := range m.footer() {
		out = append(out, m.clip(l))
	}
	return strings.Join(append(out, m.clip(m.hint())), "\n")
}

// splitLines returns exactly h lines of a pane view.
func splitLines(v string, h int) []string {
	lines := strings.Split(v, "\n")
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines[:h]
}

// header names the project and the load state. Counts are shown only for a
// consistent load; a paged load says it is partial.
func (m Model) header() string {
	b := m.b
	parts := []string{"lit"}
	if b.project == nil {
		return strings.Join(append(parts, "choose a project"), " · ")
	}
	parts = append(parts, sanitize.Title(b.project.Title))
	switch {
	case !b.loaded && b.last.Err != nil:
		parts = append(parts, "load failed: "+sanitize.Title(recovery.Describe(b.last.Err)))
	case !b.loaded:
		parts = append(parts, "loading issues…")
	case b.last.Err != nil && b.last.Pages > 0:
		parts = append(parts, "partial: loading stopped ("+sanitize.Title(recovery.Describe(b.last.Err))+"); may have changed between pages")
	case !b.last.Consistent:
		parts = append(parts, "partial: may have changed between pages")
	default:
		parts = append(parts, fmt.Sprintf("%d issues", len(b.last.Items)))
	}
	if b.loaded {
		parts = append(parts, "as of "+b.lastAt.Format("15:04:05"))
		if b.last.Err != nil && b.last.Pages == 0 {
			parts = append(parts, "refresh failed: "+sanitize.Title(recovery.Describe(b.last.Err)))
		}
	}
	switch {
	case b.waiting:
		parts = append(parts, "refresh waits for a running change")
	case b.loading && b.loaded:
		parts = append(parts, "refreshing…")
	}
	return strings.Join(parts, " · ")
}

// crumb is the project, then the selection's ancestors and the selection;
// when too long, the start is elided so the nearest ancestors stay visible.
func (m Model) crumb() string {
	b := m.b
	parts := []string{sanitize.Title(b.project.Title)}
	if issue, ok := b.tree.Selected(); ok {
		for _, a := range b.tree.Path(issue.ID) {
			parts = append(parts, sanitize.Title(a.Title))
		}
	}
	s := strings.Join(parts, crumbSep)
	w := cells.Width(b.method, s)
	if m.width > 0 && w > m.width {
		s = "…" + cells.Cut(b.method, s, w-(m.width-1), w)
	}
	return m.clip(s)
}

func (m Model) hint() string {
	km, ctx := m.prefs.Keymap, m.view()
	switch {
	case m.b.filt.input != nil:
		return m.b.filt.input.Line(m.b.method, m.width, km)
	case m.b.menu != nil || m.b.filt.panel != nil:
		return km.Hint(keys.Menu, keys.Open, keys.Cancel)
	case ctx == keys.Menu:
		return km.Hint(keys.Menu, keys.Open, keys.Cancel, keys.Quit)
	case ctx == keys.Tree:
		return km.Hint(keys.Tree, keys.Open, keys.Expand, keys.Collapse, keys.Search, keys.Filter, keys.SwitchProject, keys.Refresh, keys.ActionMenu, keys.Help, keys.Quit)
	case ctx == keys.Detail:
		return km.Hint(keys.Detail, keys.Back, keys.NextBlock, keys.Refresh, keys.Help, keys.Quit)
	}
	return km.Hint(keys.Global, keys.SwitchProject, keys.Help, keys.Quit)
}

func firstKey(m Model, ctx keys.Context, a keys.Action) string {
	if k := m.prefs.Keymap.Keys(ctx, a); len(k) > 0 {
		return k[0]
	}
	return string(a)
}

// clip fits s to the screen width and pads it; an unknown width (before
// the first resize) keeps s.
func (m Model) clip(s string) string { return pad(m.b.method, s, m.width) }

func pad(method cells.Method, s string, w int) string {
	if w <= 0 {
		return s
	}
	s = cells.Clip(method, s, w, "…")
	return s + strings.Repeat(" ", max(w-cells.Width(method, s), 0))
}
