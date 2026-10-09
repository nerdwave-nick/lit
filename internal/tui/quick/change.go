// Package quick is lit-tui's quick actions: single optimistic transactions
// that change an issue's title, state, labels, assignee, parent or links
// without a draft. Each action previews its change, lists every record the
// preview fences (relationship mirrors and guards included) with their
// claims, refuses before anything is written when another client holds a
// claim on an affected issue, and otherwise commits exactly the previewed
// transaction once.
package quick

import (
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/sanitize"
)

// Relations an issue link can have, as the service names them.
const (
	Blocks    = "blocks"
	BlockedBy = "blocked-by"
	Related   = "related"
)

// Change is one quick action on one issue: what it does, in words, and the
// preview request that does it.
type Change struct {
	Issue   string // the UUID of the issue the action started from
	Summary string // for example `Close “Child issue”`
	Request protocol.PrepareRequest
	// Touches are the issues the change is known to change: the issue and,
	// for relationships, the other ends whose mirror fields change. They are
	// listed when the service refuses the preview itself.
	Touches []string
}

// quoted is a sanitized title in quotes.
func quoted(title string) string { return "“" + sanitize.Title(title) + "”" }

// fence is the revision the user saw; the preview refuses a newer issue.
func fence(i protocol.IssueSummary) int64 { return i.Revision }

// Title renames issue i.
func Title(project string, i protocol.IssueSummary, title string) Change {
	return Change{i.ID, "Rename " + quoted(i.Title) + " to " + quoted(title),
		litclient.UpdateIssue(project, i.ID, fence(i), litclient.IssueChange{Title: &title}), []string{i.ID}}
}

// State closes or reopens issue i. Closing ends its claim.
func State(project string, i protocol.IssueSummary, state string) Change {
	if state == "closed" {
		return Change{i.ID, "Close " + quoted(i.Title), litclient.CloseIssue(project, i.ID, fence(i)), []string{i.ID}}
	}
	return Change{i.ID, "Reopen " + quoted(i.Title), litclient.ReopenIssue(project, i.ID, fence(i)), []string{i.ID}}
}

// Labels adds and removes labels of issue i.
func Labels(project string, i protocol.IssueSummary, add, remove []string) Change {
	s := "Change the labels of " + quoted(i.Title) + ":"
	for _, l := range add {
		s += " +" + sanitize.Title(l)
	}
	for _, l := range remove {
		s += " −" + sanitize.Title(l)
	}
	return Change{i.ID, s, litclient.UpdateIssue(project, i.ID, fence(i), litclient.IssueChange{AddLabels: add, RemoveLabels: remove}), []string{i.ID}}
}

// Assignee assigns issue i to name, or unassigns it when name is "".
func Assignee(project string, i protocol.IssueSummary, name string) Change {
	if name == "" {
		return Change{i.ID, "Unassign " + quoted(i.Title), litclient.UpdateIssue(project, i.ID, fence(i), litclient.IssueChange{Clear: []string{"assignee"}}), []string{i.ID}}
	}
	return Change{i.ID, "Assign " + quoted(i.Title) + " to " + sanitize.Title(name),
		litclient.UpdateIssue(project, i.ID, fence(i), litclient.IssueChange{Assignee: &name}), []string{i.ID}}
}

// Parent moves issue i under parent (an issue UUID titled title), or makes
// it a root when parent is "".
func Parent(project string, i protocol.IssueSummary, parent, title string) Change {
	touches := []string{i.ID}
	if i.ParentID != nil {
		touches = append(touches, *i.ParentID)
	}
	if parent == "" {
		return Change{i.ID, "Remove the parent of " + quoted(i.Title), litclient.UpdateIssue(project, i.ID, fence(i), litclient.IssueChange{Clear: []string{"parent"}}), touches}
	}
	return Change{i.ID, "Move " + quoted(i.Title) + " under " + quoted(title),
		litclient.UpdateIssue(project, i.ID, fence(i), litclient.IssueChange{Parent: &parent}), append(touches, parent)}
}

// Link links issue i to the issue to (titled title) by relation.
func Link(project string, i protocol.IssueSummary, relation, to, title string) Change {
	return Change{i.ID, "Link: " + quoted(i.Title) + " " + RelationText(relation) + " " + quoted(title), litclient.LinkIssues(project, i.ID, relation, to), []string{i.ID, to}}
}

// Unlink removes the link by relation from issue i to the issue to.
func Unlink(project string, i protocol.IssueSummary, relation, to, title string) Change {
	return Change{i.ID, "Unlink: " + quoted(i.Title) + " " + RelationText(relation) + " " + quoted(title), litclient.UnlinkIssues(project, i.ID, relation, to), []string{i.ID, to}}
}

// RelationText is relation in words, from the issue's side.
func RelationText(relation string) string {
	switch relation {
	case Blocks:
		return "blocks"
	case BlockedBy:
		return "is blocked by"
	}
	return "is related to"
}
