package tui

import (
	"fmt"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/keys"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
)

// menuCommands are the actions the action menu offers, in its order
// (Proposal): the view's commands first, then the global ones. Navigation
// stays on its keys. Later screens add their commands (claim, edit, …) here.
var menuCommands = []keys.Action{keys.Open, keys.Back, keys.Refresh, keys.SwitchProject, keys.UncertainRequests, keys.Search, keys.Filter, keys.ClearFilters, keys.Claim, keys.Release,
	keys.EditTitle, keys.SetState, keys.SetLabels, keys.SetAssignee, keys.SetParent, keys.Link, keys.Unlink, keys.Help, keys.Quit, keys.History, keys.Edit, keys.EditDetached,
	keys.NewComment, keys.EditComment}

// actionMenu lists the commands of one view with their effective keys.
type actionMenu struct {
	items  []keys.Binding
	cursor int
}

func (b *browser) openMenu(ctx keys.Context) {
	m := &actionMenu{}
	for _, want := range menuCommands {
		for _, bd := range b.km.Bindings(ctx) {
			if bd.Action == want && (bd.Context == ctx || bd.Context == keys.Global) {
				if b.menuItem != nil {
					var ok bool
					if bd, ok = b.menuItem(bd); !ok {
						break
					}
				}
				m.items = append(m.items, bd)
				break
			}
		}
	}
	b.menu = m
}

// menuKey handles a menu-context action; it returns the chosen command.
func (b *browser) menuKey(a keys.Action) (keys.Action, bool) {
	m := b.menu
	switch a {
	case keys.Up:
		m.cursor = max(m.cursor-1, 0)
	case keys.Down:
		m.cursor = min(m.cursor+1, len(m.items)-1)
	case keys.Top, keys.PageUp:
		m.cursor = 0
	case keys.Bottom, keys.PageDown:
		m.cursor = len(m.items) - 1
	case keys.Cancel, keys.ActionMenu:
		b.menu = nil
	case keys.Open:
		b.menu = nil
		return m.items[m.cursor].Action, true
	}
	return "", false
}

// lines draws the menu in a w×h pane.
func (m *actionMenu) lines(method cells.Method, w, h int, plain bool) []string {
	out := []string{pad(method, "Actions", w)}
	width := 0
	for _, it := range m.items {
		width = max(width, len(it.Description))
	}
	for i, it := range m.items {
		if len(out) >= h {
			break
		}
		k := strings.Join(it.Keys, ", ")
		if k == "" {
			k = "(unbound)"
		}
		marker := "  "
		if i == m.cursor && plain {
			marker = "> "
		}
		line := pad(method, fmt.Sprintf("%s%-*s  %s", marker, width, it.Description, k), w)
		if i == m.cursor && !plain {
			line = "\x1b[7m" + line + "\x1b[m"
		}
		out = append(out, line)
	}
	for len(out) < h {
		out = append(out, strings.Repeat(" ", w))
	}
	return out[:h]
}
