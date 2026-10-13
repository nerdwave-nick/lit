package service

import "github.com/nerdwave-nick/lit/internal/protocol"

// Structural reads are explicit guards unless the same owner is a target.
func requiredGuards(before, after RecordState, ops []protocol.Operation) map[string]bool {
	needed := map[string]bool{}
	var visitIssue func(RecordState, string, map[string]bool)
	visitIssue = func(state RecordState, id string, seen map[string]bool) {
		key := recordKey("issues", id)
		if seen[key] {
			return
		}
		seen[key] = true
		if _, exists := before[key]; exists {
			needed[key] = true
		}
		if v, ok := state[key]; ok {
			p := v.(protocol.Issue)
			needed[recordKey("projects", p.ProjectID)] = true
			if p.ParentID != nil {
				visitIssue(state, *p.ParentID, seen)
			}
			for _, next := range p.Blocks {
				visitIssue(state, next, seen)
			}
		}
	}
	for _, op := range ops {
		for _, state := range []RecordState{before, after} {
			switch op.Type {
			case "milestones":
				if v, ok := state[recordKey("milestones", op.ID)]; ok {
					m := v.(protocol.Milestone)
					if _, exists := before[recordKey("projects", m.ProjectID)]; exists {
						needed[recordKey("projects", m.ProjectID)] = true
					}
					for _, id := range m.IssueIDs {
						if _, exists := before[recordKey("issues", id)]; exists {
							needed[recordKey("issues", id)] = true
						}
					}
				} else if op.Kind == "create" && op.Set.ProjectID != nil {
					if _, exists := before[recordKey("projects", *op.Set.ProjectID)]; exists {
						needed[recordKey("projects", *op.Set.ProjectID)] = true
					}
				}
			case "issues":
				visitIssue(state, op.ID, map[string]bool{})
			case "comments":
				if v, ok := state[recordKey(op.Type, op.ID)]; ok {
					visitIssue(state, v.(protocol.Comment).IssueID, map[string]bool{})
				}
			}
		}
	}
	return needed
}
func validateReferences(before, after RecordState, i protocol.Intent) error {
	required := map[string]bool{}
	for _, op := range i.Operations {
		required[recordKey(op.Type, op.ID)] = true
	}
	for _, k := range changedKeys(before, after) {
		required[k] = true
	}
	seen := map[string]bool{}
	for _, r := range i.Targets {
		k := recordKey(r.Type, r.ID)
		if !required[k] {
			return protocol.E(409, "affected_set_changed", "unexpected target")
		}
		seen[k] = true
	}
	if len(seen) != len(required) {
		return protocol.E(409, "affected_set_changed", "missing affected owner")
	}
	guards := map[string]bool{}
	for _, r := range i.Guards {
		k := recordKey(r.Type, r.ID)
		if seen[k] {
			return invalid("target also declared as guard")
		}
		guards[k] = true
	}
	for k := range requiredGuards(before, after, i.Operations) {
		if !seen[k] && !guards[k] {
			return protocol.E(409, "affected_set_changed", "missing structural guard")
		}
	}
	return checkRecordPreconditions(before, i)
}
func checkRecordPreconditions(before RecordState, i protocol.Intent) error {
	for _, r := range append(append([]protocol.ObjectRef{}, i.Targets...), i.Guards...) {
		k := recordKey(r.Type, r.ID)
		old, exists := before[k]
		if r.ExpectedRevision == nil {
			if exists {
				return protocol.E(409, "revision_conflict", "creation ID exists")
			}
		} else if !exists || recordRevision(old) != *r.ExpectedRevision {
			return protocol.E(409, "revision_conflict", "record revision changed")
		}
	}
	return nil
}
