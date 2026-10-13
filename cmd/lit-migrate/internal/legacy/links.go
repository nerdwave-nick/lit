// Frozen copy of the legacy lit-server service at c5af73e (internal/service/links.go);
// see doc.go. Do not change it.

package legacy

import (
	"github.com/nerdwave-nick/lit/internal/protocol"
)

func validateLinks(state RecordState) error {
	for _, r := range state {
		p, ok := r.(protocol.Issue)
		if !ok {
			continue
		}
		for _, pair := range []struct {
			ids     []string
			reverse string
		}{{p.Blocks, "blocked_by"}, {p.BlockedBy, "blocks"}, {p.Related, "related"}} {
			for _, id := range pair.ids {
				v, ok := state[recordKey("issues", id)]
				if !ok || id == p.ID {
					return invalid("missing or self-linked issue")
				}
				other := v.(protocol.Issue)
				back := other.Related
				if pair.reverse == "blocks" {
					back = other.Blocks
				}
				if pair.reverse == "blocked_by" {
					back = other.BlockedBy
				}
				if !contains(back, p.ID) {
					return invalid("unmirrored issue relationship")
				}
			}
		}
	}
	visiting, done := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return invalid("blocking cycle")
		}
		if done[id] {
			return nil
		}
		visiting[id] = true
		for _, next := range state[recordKey("issues", id)].(protocol.Issue).Blocks {
			if e := visit(next); e != nil {
				return e
			}
		}
		delete(visiting, id)
		done[id] = true
		return nil
	}
	for _, r := range state {
		if p, ok := r.(protocol.Issue); ok {
			if e := visit(p.ID); e != nil {
				return e
			}
		}
	}
	return nil
}
func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Canonical relationship descriptors contain both endpoints. Count each logical
// edge once, separately from ordinary record changes, before expansion limits.
func logicalMutationCount(ops []protocol.Operation) int {
	edges := map[string]bool{}
	ordinary := 0
	for _, op := range ops {
		hasEdge := false
		for _, part := range []struct {
			members protocol.ProjectMembers
			kind    string
		}{{op.Add, "add"}, {op.Remove, "remove"}} {
			for _, rel := range []struct {
				ids  []string
				kind string
			}{{part.members.Blocks, "blocks"}, {part.members.BlockedBy, "blocked-by"}, {part.members.Related, "related"}} {
				for _, other := range rel.ids {
					hasEdge = true
					a, b := op.ID, other
					kind := rel.kind
					if kind == "blocked-by" {
						a, b = b, a
						kind = "blocks"
					}
					if kind == "related" && a > b {
						a, b = b, a
					}
					edges[part.kind+"/"+kind+"/"+a+"/"+b] = true
				}
			}
		}
		if !hasEdge || !same(op.Set, protocol.ProjectSet{}) || len(op.Add.Labels)+len(op.Remove.Labels)+len(op.Add.RepositoryRefs)+len(op.Remove.RepositoryRefs) > 0 {
			ordinary++
		}
	}
	return ordinary + len(edges)
}
