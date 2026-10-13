package litclient

import "github.com/nerdwave-nick/lit/internal/protocol"

// The functions below build the preview requests for single record mutations.
// project scopes issue and milestone references ("" uses the client's selected
// project). A positive revision fences the named record at that revision; zero
// leaves the preview to fix the current one. Creation IDs are generated here.
// Every request still goes through Prepare and Commit.

// IssueChange lists the issue fields an update sets. Nil fields are unchanged.
// Clear names optional fields to clear, such as "assignee" or "parent".
type IssueChange struct {
	Title, Body, State, Assignee, Parent *string
	AddLabels, RemoveLabels, Clear       []string
}

// ProjectChange lists the project fields an update sets.
type ProjectChange struct {
	Title, Description                         *string
	AddRepositories, RemoveRepositories, Clear []string
}

// MilestoneChange lists the milestone fields an update sets. Issue membership
// changes take issue UUIDs.
type MilestoneChange struct {
	Title, Body                    *string
	AddIssues, RemoveIssues, Clear []string
}

func expected(revision int64) *int64 {
	if revision <= 0 {
		return nil
	}
	return &revision
}

func request(operation, project string, item protocol.ProjectInput) protocol.PrepareRequest {
	return protocol.PrepareRequest{SchemaVersion: 1, Operation: operation, Project: project, Items: []protocol.ProjectInput{item}}
}

// CreateIssue previews a new issue; parent is an issue reference or "".
func CreateIssue(project, title, body, parent string) protocol.PrepareRequest {
	item := protocol.ProjectInput{ID: protocol.UUID(), Title: &title, Content: &body}
	if parent != "" {
		item.Parent = &parent
	}
	return request("issue.create", project, item)
}

// UpdateIssue previews changes to one issue.
func UpdateIssue(project, issue string, revision int64, c IssueChange) protocol.PrepareRequest {
	item := protocol.ProjectInput{Target: issue, ExpectedRevision: expected(revision), Parent: c.Parent, Clear: c.Clear}
	item.Set.Title, item.Set.Body, item.Set.State = c.Title, c.Body, c.State
	if c.Assignee != nil {
		item.Set.Assignee = &c.Assignee
	}
	item.Add.Labels, item.Remove.Labels = c.AddLabels, c.RemoveLabels
	return request("issue.update", project, item)
}

// CloseIssue previews closing one issue; closing ends its claim.
func CloseIssue(project, issue string, revision int64) protocol.PrepareRequest {
	return request("issue.close", project, protocol.ProjectInput{Target: issue, ExpectedRevision: expected(revision)})
}

// ReopenIssue previews reopening one issue.
func ReopenIssue(project, issue string, revision int64) protocol.PrepareRequest {
	return request("issue.reopen", project, protocol.ProjectInput{Target: issue, ExpectedRevision: expected(revision)})
}

// LinkIssues previews adding relation ("blocks", "blocked-by" or "related")
// from one issue to others. Both ends of every link change.
func LinkIssues(project, from, relation string, to ...string) protocol.PrepareRequest {
	return request("issue.link", project, protocol.ProjectInput{From: from, Relation: relation, To: to})
}

// UnlinkIssues previews removing relation from one issue to others.
func UnlinkIssues(project, from, relation string, to ...string) protocol.PrepareRequest {
	return request("issue.unlink", project, protocol.ProjectInput{From: from, Relation: relation, To: to})
}

// CreateComment previews a new comment on issue. New comments need no claim.
func CreateComment(project, issue, body string) protocol.PrepareRequest {
	return request("comment.create", project, protocol.ProjectInput{ID: protocol.UUID(), Issue: issue, Content: &body})
}

// UpdateComment previews replacing a comment's body.
func UpdateComment(comment string, revision int64, body string) protocol.PrepareRequest {
	item := protocol.ProjectInput{Target: comment, ExpectedRevision: expected(revision)}
	item.Set.Body = &body
	return request("comment.update", "", item)
}

// CreateProject previews a new project with its shared specification.
func CreateProject(title, description string, repositories ...string) protocol.PrepareRequest {
	return protocol.PrepareRequest{SchemaVersion: 1, Operation: "project.create", Items: []protocol.ProjectInput{{ID: protocol.UUID(), Title: &title, Content: &description, RepositoryRefs: repositories}}}
}

// UpdateProject previews changes to one project.
func UpdateProject(project string, revision int64, c ProjectChange) protocol.PrepareRequest {
	item := protocol.ProjectInput{Target: project, ExpectedRevision: expected(revision), Clear: c.Clear}
	item.Set.Title, item.Set.Description = c.Title, c.Description
	item.Add.RepositoryRefs, item.Remove.RepositoryRefs = c.AddRepositories, c.RemoveRepositories
	return protocol.PrepareRequest{SchemaVersion: 1, Operation: "project.update", Items: []protocol.ProjectInput{item}}
}

// CreateMilestone previews a new milestone with optional member issue UUIDs.
func CreateMilestone(project, title, body string, issues ...string) protocol.PrepareRequest {
	return request("milestone.create", project, protocol.ProjectInput{ID: protocol.UUID(), Title: &title, Content: &body, IssueIDs: issues})
}

// UpdateMilestone previews changes to one milestone.
func UpdateMilestone(project, milestone string, revision int64, c MilestoneChange) protocol.PrepareRequest {
	item := protocol.ProjectInput{Target: milestone, ExpectedRevision: expected(revision), Clear: c.Clear}
	item.Set.Title, item.Set.Body = c.Title, c.Body
	item.Add.IssueIDs, item.Remove.IssueIDs = c.AddIssues, c.RemoveIssues
	return request("milestone.update", project, item)
}
