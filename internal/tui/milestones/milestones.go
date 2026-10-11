// Package milestones is lit-tui's milestone administration: the
// milestone_description draft kind for the editor (internal/tui/edit), the
// load of the project's milestones with their progress, and the Milestones
// view.
//
// A milestone is an optional named workset of issues in one project.
// Membership is explicit: parents do not bring their children and grouping
// creates no blockers. Progress is the service's read-only projection of
// the members; an empty milestone is an empty scope, not a completed one.
// Milestones have no claims, so every change (creation, title, description,
// membership) is optimistic, fenced at the revision it was read at, and
// commits exactly its preview.
package milestones

import (
	"context"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/edit"
)

// Register makes e edit milestone descriptions.
func Register(e *edit.Editor) {
	e.Register(drafts.MilestoneDescription, edit.Kind{Read: read, Request: update})
}

// read reads the milestone now; the draft's title follows its title.
func read(ctx context.Context, s *litclient.Session, d drafts.Draft) (edit.Record, error) {
	got, err := litclient.Get[protocol.Milestone](ctx, s, d.ProjectID, d.RecordID)
	if err == nil && (len(got) != 1 || got[0].ID != d.RecordID) {
		err = protocol.E(500, "invalid_response", "the milestone read returned no single milestone")
	}
	if err != nil {
		return edit.Record{}, err
	}
	return edit.Record{Exists: true, Title: got[0].Title, Revision: got[0].Revision, Content: got[0].Body}, nil
}

// update replaces the description, fenced at the draft's revision; the
// membership is not touched.
func update(d drafts.Draft, content string) protocol.PrepareRequest {
	return litclient.UpdateMilestone(d.ProjectID, d.RecordID, d.Revision, litclient.MilestoneChange{Body: &content})
}

// Description is the target of editing milestone m's description in
// project (its UUID).
func Description(project string, m protocol.Milestone) edit.Target {
	return edit.Target{Kind: drafts.MilestoneDescription, Project: project, Record: m.ID, Title: m.Title}
}

// page is how many milestones one read asks for.
var page = 100

// Gate is the lifecycle's gate for reads.
type Gate interface {
	Do(ctx context.Context, fn func(ctx context.Context, s *litclient.Session) error) error
}

// Loaded is the result of Load: the milestones read, oldest first, and how
// many pages that took. Err keeps the milestones read before it.
type Loaded struct {
	Milestones []litclient.MilestoneView
	Pages      int
	Err        error
}

// Load reads every milestone of project (a UUID) with its progress, page by
// page, each page one read through g.
func Load(ctx context.Context, g Gate, project string) Loaded {
	var l Loaded
	cursor := ""
	for {
		var p litclient.MilestonePage
		err := g.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
			var err error
			p, err = s.Milestones(ctx, litclient.Query{ProjectID: project, Limit: page, Cursor: cursor})
			return err
		})
		if err != nil {
			l.Err = err
			return l
		}
		l.Pages++
		l.Milestones = append(l.Milestones, p.Items...)
		if p.NextCursor == nil {
			return l
		}
		cursor = *p.NextCursor
	}
}
