package service

import "github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"

// validateLinks checks the links of the records in state: a changed issue's
// counterparts must exist and mirror each link, and an unchanged issue's links
// to a changed issue must still be mirrored. Unchanged issues outside state
// keep their stored, mirrored links. No changed issue may lie on a blocking
// cycle; state holds every issue those issues transitively block.
func validateLinks(state RecordState, changed map[string]bool) error {
	for _, k := range sortedKeys(state) {
		p, ok := state[k].(protocol.Issue)
		if !ok {
			continue
		}
		for _, pair := range []struct {
			ids     []string
			reverse string
		}{{p.Blocks, "blocked_by"}, {p.BlockedBy, "blocks"}, {p.Related, "related"}} {
			for _, id := range pair.ids {
				v, ok := state[recordKey("issues", id)]
				if id == p.ID || !ok && changed[k] {
					return invalid("missing or self-linked issue")
				}
				if !ok {
					continue
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
		p, ok := state[recordKey("issues", id)].(protocol.Issue)
		if done[id] || !ok {
			return nil
		}
		visiting[id] = true
		for _, next := range p.Blocks {
			if e := visit(next); e != nil {
				return e
			}
		}
		delete(visiting, id)
		done[id] = true
		return nil
	}
	for _, k := range sortedKeys(changed) {
		if p, ok := state[k].(protocol.Issue); ok {
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
func (s *Server) prepareLinks(req protocol.PrepareRequest, c protocol.Client) (protocol.Prepared, error) {
	out := protocol.Prepared{}
	if req.SchemaVersion != 0 && req.SchemaVersion != 1 {
		return out, invalid("unsupported schema_version")
	}
	if len(req.Items) == 0 {
		return out, invalid("empty batch")
	}
	if len(req.Items) > s.Config.Limits.ExplicitItems {
		return out, protocol.E(413, "limit_exceeded", "too many items")
	}
	project := req.Project
	if project == "" && c.ProjectID != nil {
		project = *c.ProjectID
	}
	ops := map[string]protocol.Operation{}
	revs := map[string]int64{}
	add := func(p protocol.Issue, other, relation string) {
		op, ok := ops[p.ID]
		if !ok {
			op = protocol.Operation{Type: "issues", ID: p.ID, Kind: "update"}
		}
		members := &op.Add
		if req.Operation == "issue.unlink" {
			members = &op.Remove
		}
		switch relation {
		case "blocks":
			members.Blocks = union(members.Blocks, []string{other})
		case "blocked-by":
			members.BlockedBy = union(members.BlockedBy, []string{other})
		case "related":
			members.Related = union(members.Related, []string{other})
		}
		ops[p.ID] = op
		revs[p.ID] = p.Revision
	}
	for _, item := range req.Items {
		if e := item.ValidateResourceShape("issues", "link"); e != nil {
			return out, invalid(e.Error())
		}
		if item.From == "" || len(item.To) == 0 {
			return out, invalid("link requires from and to")
		}
		if item.Relation != "blocks" && item.Relation != "blocked-by" && item.Relation != "related" {
			return out, invalid("invalid relation")
		}
		from, e := s.resolveIssue(item.From, project)
		if e != nil {
			return out, e
		}
		seen := map[string]bool{}
		for _, target := range item.To {
			to, e := s.resolveIssue(target, project)
			if e != nil {
				return out, e
			}
			if from.ID == to.ID {
				return out, invalid("self link")
			}
			if seen[to.ID] {
				continue
			}
			seen[to.ID] = true
			reverse := item.Relation
			if reverse == "blocks" {
				reverse = "blocked-by"
			} else if reverse == "blocked-by" {
				reverse = "blocks"
			}
			add(from, to.ID, item.Relation)
			add(to, from.ID, reverse)
		}
	}
	i := protocol.Intent{Force: req.Force, SchemaVersion: 1, Operation: "transaction", ServiceID: s.DB.ServiceID(), Actor: protocol.DurableActor{ClientID: c.ClientID, Name: c.Actor.Name, Kind: c.Actor.Kind}}
	for _, id := range sortedKeys(ops) {
		v := revs[id]
		i.Operations = append(i.Operations, ops[id])
		i.Targets = append(i.Targets, protocol.ObjectRef{Type: "issues", ID: id, ExpectedRevision: &v})
	}
	return s.prepareRecordIntent(i, c)
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

func validateRelationshipRoute(ops []protocol.Operation, id string) error {
	found := false
	for _, op := range ops {
		if op.Type != "issues" || op.Kind != "update" || !same(op.Set, protocol.ProjectSet{}) || len(op.Add.Labels)+len(op.Remove.Labels)+len(op.Add.RepositoryRefs)+len(op.Remove.RepositoryRefs) > 0 {
			return invalid("route accepts only relationship changes")
		}
		count := 0
		for _, members := range [][]string{op.Add.Blocks, op.Add.BlockedBy, op.Add.Related, op.Remove.Blocks, op.Remove.BlockedBy, op.Remove.Related} {
			for _, other := range members {
				count++
				if op.ID != id && other != id {
					return invalid("relationship does not involve route issue")
				}
			}
		}
		if count == 0 {
			return invalid("empty relationship change")
		}
		found = found || op.ID == id
	}
	if !found {
		return invalid("route issue missing from intent")
	}
	return nil
}
