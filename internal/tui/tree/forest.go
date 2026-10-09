package tree

import (
	"slices"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// forest indexes issue summaries as a hierarchy. An issue whose parent is not
// loaded (a partial load), is itself, or closes a parent cycle shows as a
// root, so no loaded issue is ever unreachable.
type forest struct {
	nodes map[string]*node
	roots []string
}

type node struct {
	issue    protocol.IssueSummary
	parent   string // "" for a root
	children []string
	depth    int
}

func build(items []protocol.IssueSummary) *forest {
	f := &forest{nodes: make(map[string]*node, len(items))}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		if _, dup := f.nodes[it.ID]; dup || it.ID == "" {
			continue
		}
		f.nodes[it.ID] = &node{issue: it}
		ids = append(ids, it.ID)
	}
	slices.Sort(ids)
	for _, id := range ids {
		n := f.nodes[id]
		if p := n.issue.ParentID; p != nil && *p != id && f.nodes[*p] != nil {
			n.parent = *p
		}
	}
	f.cutCycles(ids)
	for _, id := range ids {
		if p := f.nodes[id].parent; p != "" {
			f.nodes[p].children = append(f.nodes[p].children, id)
		} else {
			f.roots = append(f.roots, id)
		}
	}
	f.sort(f.roots)
	for _, id := range ids {
		f.sort(f.nodes[id].children)
	}
	f.depths(ids)
	return f
}

// cutCycles walks up from every issue in a stable order; a walk that returns
// to an issue on its own path cuts that issue's parent link.
func (f *forest) cutCycles(ids []string) {
	const (
		unvisited = iota
		onPath
		done
	)
	state := make(map[string]int, len(ids))
	for _, id := range ids {
		var path []string
		cur := id
		for cur != "" && state[cur] == unvisited {
			state[cur] = onPath
			path = append(path, cur)
			cur = f.nodes[cur].parent
		}
		if cur != "" && state[cur] == onPath {
			f.nodes[cur].parent = ""
		}
		for _, p := range path {
			state[p] = done
		}
	}
}

func (f *forest) depths(ids []string) {
	known := make(map[string]bool, len(ids))
	for _, id := range ids {
		var path []string
		cur := id
		for cur != "" && !known[cur] {
			path = append(path, cur)
			cur = f.nodes[cur].parent
		}
		base := -1
		if cur != "" {
			base = f.nodes[cur].depth
		}
		for i := len(path) - 1; i >= 0; i-- {
			base++
			f.nodes[path[i]].depth = base
			known[path[i]] = true
		}
	}
}

// sort orders siblings by creation, then title (issues created together),
// then UUID.
func (f *forest) sort(ids []string) {
	slices.SortFunc(ids, func(a, b string) int {
		x, y := f.nodes[a].issue, f.nodes[b].issue
		if c := strings.Compare(x.CreatedAt, y.CreatedAt); c != 0 {
			return c
		}
		if c := strings.Compare(strings.ToLower(x.Title), strings.ToLower(y.Title)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
}

// path lists id's ancestors from its root, then id itself.
func (f *forest) path(id string) []protocol.IssueSummary {
	var out []protocol.IssueSummary
	for cur := id; cur != ""; cur = f.nodes[cur].parent {
		if f.nodes[cur] == nil {
			return nil
		}
		out = append(out, f.nodes[cur].issue)
	}
	slices.Reverse(out)
	return out
}

// row is one visible line of the tree.
type row struct {
	id    string
	depth int
}

// visible lists the rows shown with expanded open, in pre-order.
func (f *forest) visible(expanded map[string]bool) []row {
	var out []row
	stack := slices.Clone(f.roots)
	slices.Reverse(stack)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := f.nodes[id]
		out = append(out, row{id: id, depth: n.depth})
		if expanded[id] {
			for i := len(n.children) - 1; i >= 0; i-- {
				stack = append(stack, n.children[i])
			}
		}
	}
	return out
}
