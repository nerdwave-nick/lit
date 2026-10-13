package service

import (
	"errors"
	"sort"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/storage"
)

// RecordState holds the records one transaction reads, keyed by "type/id":
// the records it changes, the records they name and their structural closure.
// Validation of the final state reads only these; membership lists are
// computed by storage and never rebuilt here.
type RecordState map[string]any

func recordKey(typ, id string) string { return typ + "/" + id }

// stateLoader reads records into a RecordState once each; missing records
// stay absent.
type stateLoader struct {
	s     *Server
	state RecordState
	tried map[string]bool
	seeds []string // issues whose structural closure is loaded
}

func (l *stateLoader) load(typ, id string) (any, error) {
	k := recordKey(typ, id)
	if l.tried[k] {
		return l.state[k], nil
	}
	l.tried[k] = true
	if !protocol.ValidUUID(id) {
		return nil, nil
	}
	r, e := l.s.q().Record(l.s.ctx(), typ, id)
	if errors.Is(e, storage.ErrRecordNotFound) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	l.state[k] = r
	return r, nil
}

// issues loads issues; seed adds them to the structural closure.
func (l *stateLoader) issues(seed bool, ids ...string) error {
	for _, id := range ids {
		if seed {
			l.seeds = append(l.seeds, id)
		}
		if _, e := l.load("issues", id); e != nil {
			return e
		}
	}
	return nil
}

// loadState reads what the intent's validation needs: its targets and guards;
// each operation's record before the change and every record the operation
// or that record names (project, parent, link counterparts, a comment's
// issue, a milestone's project and members); and the structural closure of
// the issues involved (ancestors, transitively blocked issues, their projects).
// The closure covers the final state too, since every new parent or edge
// starts at a seeded issue.
func (s *Server) loadState(i protocol.Intent) (RecordState, error) {
	l := &stateLoader{s: s, state: RecordState{}, tried: map[string]bool{}}
	for _, r := range append(append([]protocol.ObjectRef{}, i.Targets...), i.Guards...) {
		if _, e := l.load(r.Type, r.ID); e != nil {
			return nil, e
		}
	}
	for _, op := range i.Operations {
		if e := l.operation(op); e != nil {
			return nil, e
		}
	}
	refs, e := s.q().StructuralSet(s.ctx(), l.seeds)
	if e != nil {
		return nil, e
	}
	for _, r := range refs {
		if _, e := l.load(r.Type, r.ID); e != nil {
			return nil, e
		}
	}
	return l.state, nil
}

func (l *stateLoader) operation(op protocol.Operation) error {
	old, e := l.load(op.Type, op.ID)
	if e != nil {
		return e
	}
	projects := []string{}
	if op.Set.ProjectID != nil {
		projects = append(projects, *op.Set.ProjectID)
	}
	switch op.Type {
	case "issues":
		links := [][]string{op.Add.Blocks, op.Remove.Blocks, op.Add.BlockedBy, op.Remove.BlockedBy, op.Add.Related, op.Remove.Related}
		if op.Set.ParentID != nil && *op.Set.ParentID != nil {
			links = append(links, []string{**op.Set.ParentID})
		}
		if p, ok := old.(protocol.Issue); ok {
			projects = append(projects, p.ProjectID)
			// Every current counterpart is read, so that a link changed on one
			// side only is seen as unmirrored. Blocked issues are in the closure.
			if e = l.issues(false, append(append([]string{}, p.BlockedBy...), p.Related...)...); e != nil {
				return e
			}
		}
		if e = l.issues(true, op.ID); e != nil {
			return e
		}
		for _, ids := range links {
			if e = l.issues(true, ids...); e != nil {
				return e
			}
		}
	case "comments":
		owner := ""
		if op.Set.IssueID != nil {
			owner = *op.Set.IssueID
		}
		if p, ok := old.(protocol.Comment); ok {
			owner = p.IssueID
		}
		if e = l.issues(true, owner); e != nil {
			return e
		}
	case "milestones":
		members := [][]string{op.Add.IssueIDs, op.Remove.IssueIDs}
		if op.Set.IssueIDs != nil {
			members = append(members, *op.Set.IssueIDs)
		}
		if p, ok := old.(protocol.Milestone); ok {
			projects = append(projects, p.ProjectID)
			members = append(members, p.IssueIDs)
		}
		for _, ids := range members {
			if e = l.issues(false, ids...); e != nil {
				return e
			}
		}
	}
	for _, id := range projects {
		if _, e = l.load("projects", id); e != nil {
			return e
		}
	}
	return nil
}

func cloneState(in RecordState) RecordState {
	out := RecordState{}
	for k, v := range in {
		typ, _, _, _, _ := recordIdentity(v)
		out[k], _ = decodeRecord(typ, mustJSON(v))
	}
	return out
}
func pointer(v string) *string    { return &v }
func nullable(v *string) **string { return &v }
func changeSet(current, add, remove []string) []string {
	out := union(current, add)
	for _, r := range remove {
		for n, v := range out {
			if v == r {
				out = append(out[:n], out[n+1:]...)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// operationKeys are the records an intent's operations change.
func operationKeys(ops []protocol.Operation) map[string]bool {
	out := map[string]bool{}
	for _, op := range ops {
		out[recordKey(op.Type, op.ID)] = true
	}
	return out
}

// finalState applies the operations to a copy of before and validates the
// changed records and the references they make.
func (s *Server) finalState(before RecordState, ops []protocol.Operation) (RecordState, error) {
	after := cloneState(before)
	for _, op := range ops {
		if e := applyOperation(after, op); e != nil {
			return nil, e
		}
	}
	changed := operationKeys(ops)
	for _, k := range sortedKeys(changed) {
		switch p := after[k].(type) {
		case protocol.Issue:
			if _, ok := after[recordKey("projects", p.ProjectID)]; !ok {
				return nil, invalid("issue project does not exist")
			}
			if p.ParentID != nil {
				parent, ok := after[recordKey("issues", *p.ParentID)].(protocol.Issue)
				if !ok {
					return nil, invalid("parent does not exist")
				}
				if parent.ProjectID != p.ProjectID {
					return nil, invalid("parent must belong to the same project")
				}
			}
		case protocol.Comment:
			if _, ok := after[recordKey("issues", p.IssueID)]; !ok {
				return nil, invalid("comment owner does not exist")
			}
		case protocol.Milestone:
			if _, ok := after[recordKey("projects", p.ProjectID)]; !ok {
				return nil, invalid("milestone project does not exist")
			}
			for _, id := range p.IssueIDs {
				issue, ok := after[recordKey("issues", id)].(protocol.Issue)
				if !ok || issue.ProjectID != p.ProjectID {
					return nil, invalid("milestone members must exist in the same project")
				}
			}
		}
	}
	if e := s.validateRecordState(after, changed); e != nil {
		return nil, e
	}
	return after, nil
}
func changedKeys(before, after RecordState) []string {
	out := []string{}
	for _, k := range sortedKeys(after) {
		if old, exists := before[k]; !exists || !same(old, after[k]) {
			out = append(out, k)
		}
	}
	return out
}
func recordRevision(r any) int64 {
	if r == nil {
		return 0
	}
	_, _, rev, _, _ := recordIdentity(r)
	return rev
}
