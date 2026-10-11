// Package create is lit-tui's issue creation: the issue_new draft kind for
// the editor (internal/tui/edit) and the inline title prompt.
//
// A new issue is created by one previewed transaction: its title, typed
// inline; its body, written in a TUI draft; and, for a child, its parent.
// The draft's generated ID becomes the issue's ID, so a draft creates at
// most one issue: after an uncertain apply, a preview finds the issue if it
// was created and nothing is sent again. A new issue takes no claim, but a
// child's creation changes its parent (the parent's children), so another
// client's claim on the parent refuses it before anything is written, and
// this session's own claim on the parent is passed on. Labels, assignee,
// links and the other metadata are set afterwards with the quick actions
// (internal/tui/quick).
package create

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/edit"
)

// Register makes e create issues from issue_new drafts. A blank body is
// applied: an issue may have none.
func Register(e *edit.Editor) {
	e.Register(drafts.IssueNew, edit.Kind{Read: read, Request: request, Blank: true, Touches: parentOf})
}

// read reports whether the issue (the draft's ID) exists already.
func read(ctx context.Context, s *litclient.Session, d drafts.Draft) (edit.Record, error) {
	page, err := litclient.List[protocol.Issue](ctx, s, litclient.Query{ProjectID: d.ProjectID, ID: []string{d.RecordID}})
	if err != nil {
		return edit.Record{}, err
	}
	for _, i := range page.Items {
		if i.ID == d.RecordID {
			return edit.Record{Exists: true, Title: i.Title, Revision: i.Revision, Content: i.Body}, nil
		}
	}
	return edit.Record{}, nil
}

// request creates the issue with the draft's title, content and parent, and
// the draft's ID as the issue's.
func request(d drafts.Draft, content string) protocol.PrepareRequest {
	req := litclient.CreateIssue(d.ProjectID, d.Title, content, d.ParentID)
	req.Items[0].ID = d.RecordID
	return req
}

// parentOf is the existing issue a creation changes: the parent of a child.
func parentOf(d drafts.Draft) []string {
	if d.ParentID == "" {
		return nil
	}
	return []string{d.ParentID}
}

// Target is a new issue titled title in project, a child of parent (an
// issue UUID) or top-level when parent is "". Opening it starts an empty
// draft under a generated ID and takes no claim.
func Target(project, parent, title string) edit.Target {
	return edit.Target{Kind: drafts.IssueNew, Project: project, Parent: parent, Title: title}
}

// Retitle records title as the title d's issue is created with. The draft's
// file keeps its name: the title part is cosmetic.
func Retitle(store drafts.Store, d drafts.Draft, title string) (drafts.Draft, error) {
	if err := CheckTitle(title); err != nil {
		return d, err
	}
	d.Title = title
	return store.Save(d)
}

// MaxTitle is the longest issue title, in characters.
const MaxTitle = 128

// CheckTitle checks title as lit-server does for an issue title, without
// its uniqueness: nonempty, at most MaxTitle characters, without control
// characters or leading or trailing whitespace.
func CheckTitle(title string) error {
	if problem := titleProblem(title); problem != "" {
		return protocol.E(400, "validation_failed", problem)
	}
	return nil
}

func titleProblem(title string) string {
	switch {
	case strings.TrimSpace(title) == "":
		return "an issue needs a title"
	case strings.TrimSpace(title) != title:
		return "a title cannot start or end with spaces"
	case utf8.RuneCountInString(title) > MaxTitle:
		return "a title has at most 128 characters"
	case strings.IndexFunc(title, unicode.IsControl) >= 0:
		return "a title cannot contain control characters"
	}
	return ""
}
