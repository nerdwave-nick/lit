package service

import (
	"strings"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

func (s *Server) prepareRecords(req protocol.PrepareRequest, c protocol.Client) (protocol.Prepared, error) {
	if strings.HasPrefix(req.Operation, "project.") {
		return s.prepareProjects(req, c)
	}
	out := protocol.Prepared{}
	if req.Operation == "issue.link" || req.Operation == "issue.unlink" {
		return s.prepareLinks(req, c)
	}
	parts := strings.Split(req.Operation, ".")
	if len(parts) != 2 || (parts[0] != "issue" && parts[0] != "comment" && parts[0] != "milestone") || (parts[1] != "create" && parts[1] != "update" && parts[1] != "close" && parts[1] != "reopen") {
		return out, invalid("unsupported operation")
	}
	resource, kind := parts[0]+"s", parts[1]
	if resource == "milestones" && kind != "create" && kind != "update" {
		return out, invalid("unsupported milestone operation")
	}
	if resource == "comments" && kind != "create" && kind != "update" {
		return out, invalid("unsupported comment operation")
	}
	if req.SchemaVersion != 0 && req.SchemaVersion != 1 {
		return out, invalid("unsupported schema_version")
	}
	if req.Force && strings.HasPrefix(req.Operation, "milestone.") {
		return out, invalid("force is inapplicable to milestones")
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
	i := protocol.Intent{Force: req.Force, SchemaVersion: 1, Operation: "transaction", ServiceID: s.DB.ServiceID(), Actor: protocol.DurableActor{ClientID: c.ClientID, Name: c.Actor.Name, Kind: c.Actor.Kind}}
	ops := map[string]protocol.Operation{}
	revisions := map[string]*int64{}
	for _, item := range req.Items {
		shapeKind := kind
		if kind == "close" || kind == "reopen" {
			shapeKind = "update"
		}
		if e := item.ValidateResourceShape(resource, kind); e != nil {
			return out, invalid(e.Error())
		}
		op := protocol.Operation{Type: resource, ID: item.ID, Kind: shapeKind, Set: item.Set, Add: item.Add, Remove: item.Remove}
		var rev *int64
		if kind == "create" {
			if !protocol.ValidUUID(op.ID) {
				return out, invalid("creation needs UUID")
			}
			op.Set.Body = item.Content
			if resource == "issues" {
				op.Set.Title = item.Title
				op.Set.State = item.State
				labels := append([]string{}, item.Labels...)
				op.Set.Labels = &labels
				op.Set.Assignee = nullable(item.Assignee)
				sel := item.Project
				if sel == "" {
					sel = project
				}
				p, e := s.resolveProject(sel)
				if e != nil {
					return out, e
				}
				op.Set.ProjectID = &p.ID
				if item.Parent != nil {
					pid, e := s.resolveBatchParent(*item.Parent, p.ID, req.Items)
					if e != nil {
						return out, e
					}
					op.Set.ParentID = nullable(&pid)
				}
			} else if resource == "milestones" {
				op.Set.Title = item.Title
				sel := item.Project
				if sel == "" {
					sel = project
				}
				p, e := s.resolveProject(sel)
				if e != nil {
					return out, e
				}
				op.Set.ProjectID = &p.ID
				members := append([]string{}, item.IssueIDs...)
				op.Set.IssueIDs = &members
			} else {
				owner, e := s.resolveIssue(item.Issue, project)
				if e != nil {
					return out, e
				}
				op.Set.IssueID = &owner.ID
				op.Set.Author = item.Author
				if op.Set.Author == nil {
					op.Set.Author = &c.Actor.Name
				}
			}
		} else {
			var old any
			var e error
			if resource == "issues" {
				old, e = s.resolveIssue(item.Target, project)
			} else if resource == "milestones" {
				old, e = s.resolveMilestone(item.Target, project)
			} else {
				old, e = s.resolveComment(item.Target)
			}
			if e != nil {
				return out, e
			}
			_, op.ID, _, _, _ = recordIdentity(old)
			r := recordRevision(old)
			rev = &r
			if item.ExpectedRevision != nil {
				rev = item.ExpectedRevision
			}
			if item.Fields["parent"] && item.Parent == nil {
				if op.Set.ParentID != nil && *op.Set.ParentID != nil {
					return out, invalid("contradictory parent assignments")
				}
				op.Set.ParentID = nullable(nil)
			}
			if item.Parent != nil {
				owner := old.(protocol.Issue)
				pid, e := s.resolveBatchParent(*item.Parent, owner.ProjectID, nil)
				if e != nil {
					return out, e
				}
				if op.Set.ParentID != nil && (*op.Set.ParentID == nil || **op.Set.ParentID != pid) {
					return out, invalid("contradictory parent assignments")
				}
				op.Set.ParentID = nullable(&pid)
			}
			clears := map[string]bool{}
			for _, field := range item.Clear {
				if clears[field] {
					return out, invalid("duplicate clear field")
				}
				clears[field] = true
				switch field {
				case "content", "body":
					if op.Set.Body != nil && *op.Set.Body != "" {
						return out, invalid("clear conflicts with body")
					}
					op.Set.Body = pointer("")
				case "labels":
					if resource != "issues" || op.Set.Labels != nil && len(*op.Set.Labels) > 0 {
						return out, invalid("invalid clear labels")
					}
					v := []string{}
					op.Set.Labels = &v
				case "assignee":
					if resource != "issues" || op.Set.Assignee != nil && *op.Set.Assignee != nil {
						return out, invalid("invalid clear assignee")
					}
					op.Set.Assignee = nullable(nil)
				case "parent":
					if resource != "issues" || op.Set.ParentID != nil && *op.Set.ParentID != nil {
						return out, invalid("invalid clear parent")
					}
					op.Set.ParentID = nullable(nil)
				case "issues":
					if resource != "milestones" || op.Set.IssueIDs != nil && len(*op.Set.IssueIDs) > 0 {
						return out, invalid("invalid clear milestone issues")
					}
					v := []string{}
					op.Set.IssueIDs = &v
				default:
					return out, invalid("unknown clear field")
				}
			}
			if kind == "close" || kind == "reopen" {
				state := "closed"
				if kind == "reopen" {
					state = "open"
				}
				if !same(op.Set, protocol.ProjectSet{}) || len(item.Clear) > 0 || len(op.Add.Labels)+len(op.Remove.Labels) > 0 {
					return out, invalid("state convenience command accepts only targets and revisions")
				}
				op.Set.State = &state
			}
		}
		check := i
		check.Operations = []protocol.Operation{op}
		check.Targets = []protocol.ObjectRef{{Type: resource, ID: op.ID, ExpectedRevision: rev}}
		b, _, e := protocol.Canonical(check)
		if e != nil {
			return out, invalid(e.Error())
		}
		var normalized protocol.Intent
		protocol.Decode(b, &normalized)
		op = normalized.Operations[0]
		if old, ok := ops[op.ID]; ok {
			if !same(revisions[op.ID], rev) {
				return out, invalid("incompatible revisions")
			}
			op, e = mergeRecordOperations(old, op)
			if e != nil {
				return out, e
			}
		}
		ops[op.ID] = op
		revisions[op.ID] = rev
	}
	for _, id := range sortedKeys(ops) {
		i.Operations = append(i.Operations, ops[id])
		i.Targets = append(i.Targets, protocol.ObjectRef{Type: resource, ID: id, ExpectedRevision: revisions[id]})
	}
	return s.prepareRecordIntent(i, c)
}
