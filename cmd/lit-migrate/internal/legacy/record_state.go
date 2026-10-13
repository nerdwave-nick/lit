// Frozen copy of the legacy lit-server service at c5af73e (internal/service/record_transaction.go);
// see doc.go. Do not change it.

package legacy

import (
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// RecordState is one serialized snapshot, shared by transaction expansion and
// final-state validation. Future relationship/claim slices extend this boundary.
type RecordState map[string]any

func recordKey(typ, id string) string { return typ + "/" + id }
func (s *Server) ReadRecordState() (RecordState, error) {
	out := RecordState{}
	for _, typ := range []string{"projects", "issues", "comments", "milestones"} {
		rs, e := s.Records(typ)
		if e != nil {
			return nil, e
		}
		for _, r := range rs {
			_, id, _, _, _ := recordIdentity(r)
			out[recordKey(typ, id)] = r
		}
	}
	return out, nil
}
func cloneState(in RecordState) RecordState {
	out := RecordState{}
	for k, v := range in {
		b := recordFields(v)
		raw := mustJSON(b)
		typ, _, _, _, _ := recordIdentity(v)
		out[k], _ = decodeRecord(typ, raw)
	}
	return out
}
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
func applyOperation(state RecordState, op protocol.Operation) error {
	key := recordKey(op.Type, op.ID)
	old, exists := state[key]
	if (op.Kind == "create") == exists {
		return protocol.E(409, "revision_conflict", "operation existence differs")
	}
	switch op.Type {
	case "projects":
		p := protocol.Project{SchemaVersion: 1, ID: op.ID, RepositoryRefs: []string{}, IssueIDs: []string{}}
		if exists {
			p = old.(protocol.Project)
		} else if op.Set.Title == nil {
			return invalid("creation needs title")
		}
		if op.Set.Title != nil {
			p.Title = *op.Set.Title
		}
		if op.Set.Description != nil {
			p.Description = *op.Set.Description
		}
		if op.Set.RepositoryRefs != nil {
			p.RepositoryRefs = append([]string{}, (*op.Set.RepositoryRefs)...)
		}
		p.RepositoryRefs = changeSet(p.RepositoryRefs, op.Add.RepositoryRefs, op.Remove.RepositoryRefs)
		state[key] = p
	case "issues":
		p := protocol.Issue{SchemaVersion: 1, ID: op.ID, State: "open", Labels: []string{}, ChildIDs: []string{}, CommentIDs: []string{}, Blocks: []string{}, BlockedBy: []string{}, Related: []string{}}
		if exists {
			p = old.(protocol.Issue)
		} else if op.Set.Title == nil || op.Set.ProjectID == nil {
			return invalid("issue creation needs title and project")
		}
		if op.Set.Title != nil {
			p.Title = *op.Set.Title
		}
		if op.Set.Body != nil {
			p.Body = *op.Set.Body
		}
		if op.Set.State != nil {
			p.State = *op.Set.State
		}
		if op.Set.Labels != nil {
			p.Labels = append([]string{}, (*op.Set.Labels)...)
		}
		p.Labels = changeSet(p.Labels, op.Add.Labels, op.Remove.Labels)
		p.Blocks = changeSet(p.Blocks, op.Add.Blocks, op.Remove.Blocks)
		p.BlockedBy = changeSet(p.BlockedBy, op.Add.BlockedBy, op.Remove.BlockedBy)
		p.Related = changeSet(p.Related, op.Add.Related, op.Remove.Related)
		if op.Set.Assignee != nil {
			p.Assignee = *op.Set.Assignee
		}
		if op.Set.ProjectID != nil {
			p.ProjectID = *op.Set.ProjectID
		}
		if op.Set.ParentID != nil {
			p.ParentID = *op.Set.ParentID
		}
		state[key] = p
	case "comments":
		p := protocol.Comment{SchemaVersion: 1, ID: op.ID}
		if exists {
			p = old.(protocol.Comment)
		} else if op.Set.IssueID == nil || op.Set.Author == nil {
			return invalid("comment creation needs issue and author")
		}
		if op.Set.Body != nil {
			p.Body = *op.Set.Body
		}
		if op.Set.Author != nil {
			p.Author = *op.Set.Author
		}
		if op.Set.IssueID != nil {
			p.IssueID = *op.Set.IssueID
		}
		state[key] = p
	case "milestones":
		p := protocol.Milestone{SchemaVersion: 1, ID: op.ID, IssueIDs: []string{}}
		if exists {
			p = old.(protocol.Milestone)
		} else if op.Set.Title == nil || op.Set.ProjectID == nil {
			return invalid("milestone creation needs title and project")
		}
		if op.Set.Title != nil {
			p.Title = *op.Set.Title
		}
		if op.Set.Body != nil {
			p.Body = *op.Set.Body
		}
		if op.Set.ProjectID != nil {
			p.ProjectID = *op.Set.ProjectID
		}
		if op.Set.IssueIDs != nil {
			p.IssueIDs = append([]string{}, (*op.Set.IssueIDs)...)
		}
		p.IssueIDs = changeSet(p.IssueIDs, op.Add.IssueIDs, op.Remove.IssueIDs)
		state[key] = p
	default:
		return invalid("unsupported operation")
	}
	return nil
}
func (s *Server) finalState(before RecordState, ops []protocol.Operation) (RecordState, error) {
	after := cloneState(before)
	for _, op := range ops {
		if e := applyOperation(after, op); e != nil {
			return nil, e
		}
	}
	// Membership is derived solely from final ownership, never argument order.
	for k, r := range after {
		switch p := r.(type) {
		case protocol.Project:
			p.IssueIDs = []string{}
			after[k] = p
		case protocol.Milestone:
			// Milestone membership is explicit and independent of project issue ownership.
		case protocol.Issue:
			p.ChildIDs = []string{}
			p.CommentIDs = []string{}
			after[k] = p
		}
	}
	for _, k := range sortedKeys(after) {
		switch p := after[k].(type) {
		case protocol.Issue:
			projectKey := recordKey("projects", p.ProjectID)
			v, ok := after[projectKey]
			if !ok {
				return nil, invalid("issue project does not exist")
			}
			project := v.(protocol.Project)
			project.IssueIDs = append(project.IssueIDs, p.ID)
			after[projectKey] = project
			if p.ParentID != nil {
				parentKey := recordKey("issues", *p.ParentID)
				v, ok := after[parentKey]
				if !ok {
					return nil, invalid("parent does not exist")
				}
				parent := v.(protocol.Issue)
				if parent.ProjectID != p.ProjectID {
					return nil, invalid("parent must belong to the same project")
				}
				parent.ChildIDs = append(parent.ChildIDs, p.ID)
				after[parentKey] = parent
			}
		case protocol.Comment:
			issueKey := recordKey("issues", p.IssueID)
			v, ok := after[issueKey]
			if !ok {
				return nil, invalid("comment owner does not exist")
			}
			issue := v.(protocol.Issue)
			issue.CommentIDs = append(issue.CommentIDs, p.ID)
			after[issueKey] = issue
		case protocol.Milestone:
			project, ok := after[recordKey("projects", p.ProjectID)].(protocol.Project)
			if !ok {
				return nil, invalid("milestone project does not exist")
			}
			_ = project
			for _, id := range p.IssueIDs {
				issue, ok := after[recordKey("issues", id)].(protocol.Issue)
				if !ok || issue.ProjectID != p.ProjectID {
					return nil, invalid("milestone members must exist in the same project")
				}
			}
		}
	}
	for k, r := range after {
		switch p := r.(type) {
		case protocol.Project:
			sort.Strings(p.IssueIDs)
			after[k] = p
		case protocol.Issue:
			sort.Strings(p.ChildIDs)
			sort.Strings(p.CommentIDs)
			after[k] = p
		}
	}
	if e := s.validateRecordState(after); e != nil {
		return nil, e
	}
	return after, nil
}
func (s *Server) validateRecordState(state RecordState) error {
	if e := validateLinks(state); e != nil {
		return e
	}
	titles := map[string]bool{}
	for _, r := range state {
		switch p := r.(type) {
		case protocol.Project:
			if e := s.validateProject(p); e != nil {
				return e
			}
			key := "projects/" + strings.ToLower(p.Title)
			if titles[key] {
				return invalid("duplicate project title")
			}
			titles[key] = true
		case protocol.Issue:
			if e := validateIssueTitle(p.Title); e != nil {
				return e
			}
			if !utf8.ValidString(p.Body) || (p.State != "open" && p.State != "closed") || !sortedSet(p.Labels, false) || p.Assignee != nil && (strings.TrimSpace(*p.Assignee) == "" || !utf8.ValidString(*p.Assignee)) {
				return invalid("invalid issue fields")
			}
			key := p.ProjectID + "/" + IssueTitleKey(p.Title)
			if titles[key] {
				return invalid("duplicate issue title")
			}
			titles[key] = true
			for _, members := range [][]string{p.ChildIDs, p.CommentIDs, p.Blocks, p.BlockedBy, p.Related} {
				if !sortedSet(members, true) {
					return invalid("invalid issue relationships")
				}
			}
			seen := map[string]bool{p.ID: true}
			parent := p.ParentID
			for parent != nil {
				if seen[*parent] {
					return invalid("parent cycle")
				}
				seen[*parent] = true
				v, ok := state[recordKey("issues", *parent)]
				if !ok {
					return invalid("missing parent")
				}
				owner := v.(protocol.Issue)
				if owner.ProjectID != p.ProjectID {
					return invalid("parent project differs")
				}
				parent = owner.ParentID
			}
		case protocol.Comment:
			if strings.TrimSpace(p.Author) == "" || !utf8.ValidString(p.Author) || !utf8.ValidString(p.Body) {
				return invalid("invalid comment fields")
			}
		case protocol.Milestone:
			if e := validateIssueTitle(p.Title); e != nil {
				return e
			}
			if !utf8.ValidString(p.Body) || !protocol.ValidUUID(p.ProjectID) || !sortedSet(p.IssueIDs, true) {
				return invalid("invalid milestone fields")
			}
			key := "milestones/" + p.ProjectID + "/" + IssueTitleKey(p.Title)
			if titles[key] {
				return invalid("duplicate milestone title")
			}
			titles[key] = true
			for _, id := range p.IssueIDs {
				issue, ok := state[recordKey("issues", id)].(protocol.Issue)
				if !ok || issue.ProjectID != p.ProjectID {
					return invalid("milestone members must exist in the same project")
				}
			}
		}
	}
	return nil
}
func stateTitleIndex(state RecordState) (TitleIndex, error) {
	ps := []protocol.Project{}
	for _, r := range state {
		if p, ok := r.(protocol.Project); ok {
			ps = append(ps, p)
		}
	}
	idx, e := titleIndex(ps)
	if e != nil {
		return idx, e
	}
	for _, r := range state {
		if p, ok := r.(protocol.Issue); ok {
			if idx.Issues[p.ProjectID] == nil {
				idx.Issues[p.ProjectID] = map[string]string{}
			}
			key := IssueTitleKey(p.Title)
			if _, exists := idx.Issues[p.ProjectID][key]; exists {
				return idx, invalid("duplicate issue title")
			}
			idx.Issues[p.ProjectID][key] = p.ID
		}
	}
	return idx, nil
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
func nextRecordTime(state RecordState) string {
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, r := range state {
		_, _, _, _, updated := recordIdentity(r)
		t, e := time.Parse(historyTimeFormat, updated)
		if e == nil && !now.After(t) {
			now = t.Add(time.Microsecond)
		}
	}
	return now.Format(historyTimeFormat)
}
