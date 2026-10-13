// Package comments is lit-tui's comment editing: the comment_new and
// comment_body draft kinds for the editor (internal/tui/edit), and the
// chooser that picks an issue's comment to edit.
//
// A new comment is written in a TUI draft and applied as a claim-exempt
// append (internal/service/claim_policy.go): it takes no claim, so another
// client's claim never blocks it. The draft's generated ID becomes the
// comment's ID, so a draft is created at most once: after an uncertain
// apply, a preview finds the comment if it was created. Editing an existing
// comment claims the owning issue (claim-on-edit) and applies the previewed
// update at the comment's recorded revision.
package comments

import (
	"context"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/tui/drafts"
	"github.com/nerdwave-nick/lit/internal/tui/edit"
)

// Register makes e edit comments: new comments and existing comments'
// bodies.
func Register(e *edit.Editor) {
	e.Register(drafts.CommentNew, edit.Kind{Read: readNew, Request: create})
	e.Register(drafts.CommentBody, edit.Kind{Read: readComment, Request: update})
}

// readNew reports whether the new comment (the draft's ID) exists already.
func readNew(ctx context.Context, s *litclient.Session, d drafts.Draft) (edit.Record, error) {
	page, err := litclient.List[protocol.Comment](ctx, s, litclient.Query{IssueID: d.IssueID, ID: []string{d.RecordID}})
	if err != nil {
		return edit.Record{}, err
	}
	for _, c := range page.Items {
		if c.ID == d.RecordID {
			return edit.Record{Exists: true, Revision: c.Revision, Content: c.Body}, nil
		}
	}
	return edit.Record{}, nil
}

// create appends the comment, with the draft's ID as the comment's.
func create(d drafts.Draft, content string) protocol.PrepareRequest {
	req := litclient.CreateComment(d.ProjectID, d.IssueID, content)
	req.Items[0].ID = d.RecordID
	return req
}

// readComment reads the existing comment; the draft's title stays the
// issue's.
func readComment(ctx context.Context, s *litclient.Session, d drafts.Draft) (edit.Record, error) {
	got, err := litclient.Get[protocol.Comment](ctx, s, "", d.RecordID)
	if err == nil && (len(got) != 1 || got[0].IssueID != d.IssueID) {
		err = protocol.E(500, "invalid_response", "the comment read returned no single comment of the issue")
	}
	if err != nil {
		return edit.Record{}, err
	}
	return edit.Record{Exists: true, Revision: got[0].Revision, Content: got[0].Body}, nil
}

// update replaces the comment's body, fenced at the draft's revision.
func update(d drafts.Draft, content string) protocol.PrepareRequest {
	return litclient.UpdateComment(d.RecordID, d.Revision, content)
}

// New is the target of a new comment on issue (titled title) in project.
func New(project, issue, title string) edit.Target {
	return edit.Target{Kind: drafts.CommentNew, Project: project, Issue: issue, Title: title}
}

// Edit is the target of editing comment c of the issue titled title.
func Edit(project string, c protocol.Comment, title string) edit.Target {
	return edit.Target{Kind: drafts.CommentBody, Project: project, Record: c.ID, Issue: c.IssueID, Title: title}
}
