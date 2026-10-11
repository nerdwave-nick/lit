package service

import (
	"strings"
	"unicode/utf8"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

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

// titleKey is the key under which a record's title must be unique, if any.
func titleKey(r any) (string, bool) {
	switch p := r.(type) {
	case protocol.Project:
		return "projects/" + strings.ToLower(p.Title), true
	case protocol.Issue:
		return p.ProjectID + "/" + IssueTitleKey(p.Title), true
	case protocol.Milestone:
		return "milestones/" + p.ProjectID + "/" + IssueTitleKey(p.Title), true
	}
	return "", false
}

// titleTaken reports whether another record holds r's title in the final
// state: another record in state, or a stored record that state does not hold.
func (s *Server) titleTaken(state RecordState, holders map[string][]string, r any) (bool, error) {
	key, _ := titleKey(r)
	if len(holders[key]) > 1 {
		return true, nil
	}
	typ, own, _, _, _ := recordIdentity(r)
	var project, title string
	switch p := r.(type) {
	case protocol.Project:
		title = p.Title
	case protocol.Issue:
		project, title = p.ProjectID, p.Title
	case protocol.Milestone:
		project, title = p.ProjectID, p.Title
	}
	id, found, e := s.q().IDByTitle(s.ctx(), typ, project, title)
	if e != nil || !found || id == own {
		return false, e
	}
	_, inState := state[recordKey(typ, id)]
	return !inState, nil
}

// validateRecordState validates the changed records of a final state: their
// fields, unique titles, parent chains and links. Unchanged records were
// validated when they were written.
func (s *Server) validateRecordState(state RecordState, changed map[string]bool) error {
	if e := validateLinks(state, changed); e != nil {
		return e
	}
	holders := map[string][]string{}
	for k, r := range state {
		if key, ok := titleKey(r); ok {
			holders[key] = append(holders[key], k)
		}
	}
	for _, k := range sortedKeys(changed) {
		if e := s.validateRecord(state, k); e != nil {
			return e
		}
		if _, ok := titleKey(state[k]); !ok {
			continue
		}
		taken, e := s.titleTaken(state, holders, state[k])
		if e != nil {
			return e
		}
		if taken {
			typ, _, _, _, _ := recordIdentity(state[k])
			return invalid("duplicate " + strings.TrimSuffix(typ, "s") + " title")
		}
	}
	return nil
}

func (s *Server) validateRecord(state RecordState, k string) error {
	switch p := state[k].(type) {
	case protocol.Project:
		return s.validateProject(p)
	case protocol.Issue:
		if e := validateIssueTitle(p.Title); e != nil {
			return e
		}
		if !utf8.ValidString(p.Body) || (p.State != "open" && p.State != "closed") || !sortedSet(p.Labels, false) || p.Assignee != nil && (strings.TrimSpace(*p.Assignee) == "" || !utf8.ValidString(*p.Assignee)) {
			return invalid("invalid issue fields")
		}
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
			owner, ok := state[recordKey("issues", *parent)].(protocol.Issue)
			if !ok {
				return invalid("missing parent")
			}
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
		for _, id := range p.IssueIDs {
			issue, ok := state[recordKey("issues", id)].(protocol.Issue)
			if !ok || issue.ProjectID != p.ProjectID {
				return invalid("milestone members must exist in the same project")
			}
		}
	}
	return nil
}
