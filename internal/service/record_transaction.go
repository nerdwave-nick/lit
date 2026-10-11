package service

import (
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

func stampRecord(r any, stamp string) any {
	switch p := r.(type) {
	case protocol.Project:
		if p.Revision == 0 {
			p.CreatedAt = stamp
		}
		p.Revision++
		p.UpdatedAt = stamp
		return p
	case protocol.Issue:
		if p.Revision == 0 {
			p.CreatedAt = stamp
		}
		p.Revision++
		p.UpdatedAt = stamp
		return p
	case protocol.Comment:
		if p.Revision == 0 {
			p.CreatedAt = stamp
		}
		p.Revision++
		p.UpdatedAt = stamp
		return p
	case protocol.Milestone:
		if p.Revision == 0 {
			p.CreatedAt = stamp
		}
		p.Revision++
		p.UpdatedAt = stamp
		return p
	}
	panic("unknown record")
}

// nextRecordTime is the request's record stamp: the process clock's time,
// kept strictly after the newest committed transaction.
func (s *Server) nextRecordTime() (string, error) {
	now := s.cur.stamp.UTC().Truncate(time.Microsecond)
	newest, e := s.q().NewestStamp(s.ctx())
	if e != nil {
		return "", e
	}
	if t, e := time.Parse(storage.TimeFormat, newest); e == nil && !now.After(t) {
		now = t.Add(time.Microsecond)
	}
	return now.Format(storage.TimeFormat), nil
}

// recordTransaction validates a durable request against the current state
// and returns its writes without applying them: the same engine serves
// preparation, execution and reconciliation.
func (s *Server) recordTransaction(i protocol.Intent, hash string, c protocol.Client, tokens ...protocol.ClaimToken) (changes, protocol.MutationResult, error) {
	result := protocol.MutationResult{Outcome: "already_satisfied", RequestHash: hash, Items: []protocol.ChangedObject{}}
	if i.Actor.ClientID != c.ClientID {
		return changes{}, result, protocol.E(409, "wrong_client", "transaction client differs")
	}
	if i.ServiceID != s.DB.ServiceID() {
		return changes{}, result, protocol.E(409, "wrong_service", "transaction service differs")
	}
	if i.Actor.Name != c.Actor.Name || i.Actor.Kind != c.Actor.Kind {
		return changes{}, result, protocol.E(409, "actor_changed", "actor differs")
	}
	if logicalMutationCount(i.Operations) > s.Config.Limits.ExplicitItems || len(i.Targets)+len(i.Guards) > s.Config.Limits.ExpandedRecords {
		return changes{}, result, protocol.E(413, "limit_exceeded", "transaction item limit exceeded")
	}
	before, e := s.loadState(i)
	if e != nil {
		return changes{}, result, e
	}
	if e = checkRecordPreconditions(before, i); e != nil {
		return changes{}, result, e
	}
	after, e := s.finalState(before, i.Operations)
	if e != nil {
		return changes{}, result, e
	}
	if e = validateReferences(before, after, i); e != nil {
		return changes{}, result, e
	}
	cs, e := s.claimPolicy(before, after, i, c, tokens)
	if e != nil {
		return changes{}, result, e
	}
	changed := changedKeys(before, after)
	if len(changed) == 0 {
		return cs, result, nil
	}
	if len(changed) > s.Config.Limits.ExpandedRecords {
		return changes{}, result, protocol.E(413, "limit_exceeded", "affected record limit exceeded")
	}
	stamp, e := s.nextRecordTime()
	if e != nil {
		return changes{}, result, e
	}
	for _, k := range changed {
		after[k] = stampRecord(after[k], stamp)
		typ, id, rev, _, _ := recordIdentity(after[k])
		result.Items = append(result.Items, protocol.ChangedObject{Type: typ, ID: id, BeforeRevision: rev - 1, Revision: rev})
		cs.records = append(cs.records, after[k])
	}
	result.Outcome = "applied"
	cs.request = &storage.Request{Hash: hash, Intent: i, Timestamp: stamp, Results: result.Items}
	return cs, result, nil
}
func (s *Server) prepareRecordIntent(i protocol.Intent, c protocol.Client) (protocol.Prepared, error) {
	out := protocol.Prepared{}
	before, e := s.loadState(i)
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
	if e = protocol.Decode(b, &i); e != nil {
		return out, e
	}
	if _, _, e = s.recordTransaction(i, hash, c); e != nil {
		return out, e
	}
	required, e := s.preparedClaims(before, after, i)
	if e != nil {
		return out, e
	}
	return protocol.Prepared{SchemaVersion: 1, ServiceID: s.DB.ServiceID(), ClientID: c.ClientID, Intent: i, CanonicalJSON: string(b), RequestHash: hash, RequiredClaims: required}, nil
}
func (s *Server) resolveBatchParent(selector, project string, items []protocol.ProjectInput) (string, error) {
	if protocol.ValidUUID(selector) {
		for _, item := range items {
			if item.ID == selector {
				return selector, nil
			}
		}
	}
	p, e := s.resolveIssue(selector, project)
	return p.ID, e
}
func mergeRecordOperations(a, b protocol.Operation) (protocol.Operation, error) {
	if a.Kind != b.Kind {
		return a, invalid("incompatible operations")
	}
	x, y := recordFields(a.Set), recordFields(b.Set)
	for k, v := range y {
		if old, exists := x[k]; exists && !same(old, v) {
			return a, invalid("contradictory scalar or set assignments")
		}
		x[k] = v
	}
	if e := protocol.Decode(mustJSON(x), &a.Set); e != nil {
		return a, e
	}
	a.Add.Blocks = union(a.Add.Blocks, b.Add.Blocks)
	a.Remove.Blocks = union(a.Remove.Blocks, b.Remove.Blocks)
	a.Add.BlockedBy = union(a.Add.BlockedBy, b.Add.BlockedBy)
	a.Remove.BlockedBy = union(a.Remove.BlockedBy, b.Remove.BlockedBy)
	a.Add.Related = union(a.Add.Related, b.Add.Related)
	a.Remove.Related = union(a.Remove.Related, b.Remove.Related)
	a.Add.Labels = union(a.Add.Labels, b.Add.Labels)
	a.Remove.Labels = union(a.Remove.Labels, b.Remove.Labels)
	a.Add.IssueIDs = union(a.Add.IssueIDs, b.Add.IssueIDs)
	a.Remove.IssueIDs = union(a.Remove.IssueIDs, b.Remove.IssueIDs)
	return a, nil
}
