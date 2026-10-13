// Frozen copy of the legacy lit-server service at c5af73e (internal/service/record_transaction.go);
// see doc.go. Do not change it.

package legacy

import (
	store "github.com/nerdwave-nick/lit/cmd/lit-migrate/internal/legacystore"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

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
func (s *Server) recordTransaction(i protocol.Intent, hash string, c protocol.Client, tokens ...protocol.ClaimToken) ([]store.Write, protocol.MutationResult, error) {
	result := protocol.MutationResult{Outcome: "already_satisfied", RequestHash: hash, Items: []protocol.ChangedObject{}}
	if i.Actor.ClientID != c.ClientID {
		return nil, result, protocol.E(409, "wrong_client", "transaction client differs")
	}
	if i.ServiceID != s.Store.Identity.ServiceID {
		return nil, result, protocol.E(409, "wrong_service", "transaction service differs")
	}
	if i.Actor.Name != c.Actor.Name || i.Actor.Kind != c.Actor.Kind {
		return nil, result, protocol.E(409, "actor_changed", "actor differs")
	}
	if logicalMutationCount(i.Operations) > s.Config.Limits.ExplicitItems || len(i.Targets)+len(i.Guards) > s.Config.Limits.ExpandedRecords {
		return nil, result, protocol.E(413, "limit_exceeded", "transaction item limit exceeded")
	}
	before, e := s.ReadRecordState()
	if e != nil {
		return nil, result, e
	}
	if e = checkRecordPreconditions(before, i); e != nil {
		return nil, result, e
	}
	after, e := s.finalState(before, i.Operations)
	if e != nil {
		return nil, result, e
	}
	if e = validateReferences(before, after, i); e != nil {
		return nil, result, e
	}
	claimWrites, e := s.claimPolicy(before, after, i, c, tokens)
	if e != nil {
		return nil, result, e
	}
	changed := changedKeys(before, after)
	if len(changed) == 0 {
		return claimWrites, result, nil
	}
	if len(changed) > s.Config.Limits.ExpandedRecords {
		return nil, result, protocol.E(413, "limit_exceeded", "affected record limit exceeded")
	}
	stamp := nextRecordTime(before)
	for _, k := range changed {
		after[k] = stampRecord(after[k], stamp)
		typ, id, rev, _, _ := recordIdentity(after[k])
		result.Items = append(result.Items, protocol.ChangedObject{Type: typ, ID: id, BeforeRevision: rev - 1, Revision: rev})
	}
	result.Outcome = "applied"
	writes := claimWrites
	for _, k := range changed {
		r := after[k]
		_, id, rev, _, _ := recordIdentity(r)
		h := ProjectHistory{SchemaVersion: 1, OwnerID: id, BeforeRevision: rev - 1, Revision: rev, Timestamp: stamp, RequestHash: hash, Intent: i, Results: result.Items, Differences: recordDiff(before[k], r), BodyHunks: []BodyHunk{}}
		oldBody := ""
		if before[k] != nil {
			oldBody = recordBody(before[k])
		}
		if oldBody != recordBody(r) {
			h.BodyHunks = append(h.BodyHunks, BodyHunk{Before: oldBody, After: recordBody(r)})
		}
		writes = append(writes, recordWrites(r)...)
		writes = append(writes, store.JSONWrite(k+"/history/"+stamp+"-"+hash+".json", h))
	}
	idx, e := stateTitleIndex(after)
	if e != nil {
		return nil, result, e
	}
	writes = append(writes, store.JSONWrite("indexes/titles.json", idx))
	return writes, result, nil
}
func (s *Server) prepareRecordIntent(i protocol.Intent, c protocol.Client) (protocol.Prepared, error) {
	out := protocol.Prepared{}
	before, e := s.ReadRecordState()
	if e != nil {
		return out, e
	}
	after, e := s.finalState(before, i.Operations)
	if e != nil {
		return out, e
	}
	present := map[string]bool{}
	for _, r := range i.Targets {
		present[recordKey(r.Type, r.ID)] = true
	}
	for _, k := range changedKeys(before, after) {
		if present[k] {
			continue
		}
		typ, id, _, _, _ := recordIdentity(after[k])
		v := recordRevision(before[k])
		i.Targets = append(i.Targets, protocol.ObjectRef{Type: typ, ID: id, ExpectedRevision: &v})
	}
	for _, r := range i.Targets {
		present[recordKey(r.Type, r.ID)] = true
	}
	for _, k := range sortedKeys(requiredGuards(before, after, i.Operations)) {
		if present[k] {
			continue
		}
		old, exists := before[k]
		if !exists {
			continue
		}
		typ, id, rev, _, _ := recordIdentity(old)
		i.Guards = append(i.Guards, protocol.ObjectRef{Type: typ, ID: id, ExpectedRevision: &rev})
	}
	b, hash, e := protocol.Canonical(i)
	if e != nil {
		return out, invalid(e.Error())
	}
	protocol.Decode(b, &i)
	if _, _, e = s.recordTransaction(i, hash, c); e != nil {
		return out, e
	}
	required, e := s.preparedClaims(before, after, i)
	if e != nil {
		return out, e
	}
	return protocol.Prepared{SchemaVersion: 1, ServiceID: s.Store.Identity.ServiceID, ClientID: c.ClientID, Intent: i, CanonicalJSON: string(b), RequestHash: hash, RequiredClaims: required}, nil
}
