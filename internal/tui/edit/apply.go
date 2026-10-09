package edit

import (
	"context"
	"unicode/utf8"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/claims"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/recovery"
)

// Preview is an apply's preview: the draft's content, the issue now, and the
// exact transaction that Apply commits.
type Preview struct {
	Draft   drafts.Draft
	Content string
	// Current is the issue as read for the preview.
	Current *protocol.Issue
	// Prepared is the previewed update of the body at the draft's recorded
	// revision; Affected lists every record it changes (targets) and reads
	// structurally (guards), each with its fenced revision.
	Prepared *litclient.Prepared
	Affected []protocol.ObjectRef
	// Hold is the edit claim's state now; Held is false when this session
	// holds no claim on the issue.
	Hold claims.Hold
	Held bool
	// Unchanged: the draft equals the recorded content; nothing to apply.
	Unchanged bool
	// Conflict: the issue's revision changed since the draft's.
	Conflict bool
	// Foreign is another client's claim on an affected issue: applying is
	// refused before anything is written.
	Foreign *protocol.Claim
	Err     error
}

// Diff is the change the draft makes to the recorded content.
func (p Preview) Diff() []string { return drafts.Diff(p.Draft.Base, p.Content) }

// Lost reports that the edit claim was lost (or is no longer held), so an
// apply is optimistic: fenced by the recorded revision only.
func (p Preview) Lost() bool { return !p.Held || p.Hold.State == claims.Lost }

// Ready reports whether Apply can commit the preview.
func (p Preview) Ready() bool {
	return p.Err == nil && p.Prepared != nil && !p.Unchanged && !p.Conflict && p.Foreign == nil
}

// Preview reads the draft and the issue and previews updating the body to
// the draft's content at the recorded revision. Nothing is written.
func (e *Editor) Preview(ctx context.Context, d drafts.Draft) Preview {
	p := Preview{Draft: d}
	p.Hold, p.Held = e.h.Snapshot().Hold(d.IssueID)
	if p.Content, p.Err = e.store.Read(d); p.Err != nil {
		return p
	}
	if !utf8.ValidString(p.Content) {
		p.Err = protocol.E(400, "invalid_arguments", "the draft is not valid UTF-8")
		return p
	}
	p.Err = e.g.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
		if d.ServiceID != s.Meta.ServiceID {
			return protocol.E(409, "wrong_service", "the draft belongs to another lit service")
		}
		got, err := readIssue(ctx, s, d.ProjectID, d.IssueID)
		if err != nil {
			return err
		}
		p.Current = &got
		p.Conflict, p.Unchanged = got.Revision != d.Revision, p.Content == d.Base
		if p.Conflict || p.Unchanged {
			return nil
		}
		body := p.Content
		p.Prepared, err = s.Prepare(ctx, litclient.UpdateIssue(d.ProjectID, d.IssueID, d.Revision, litclient.IssueChange{Body: &body}))
		switch litclient.Code(err) {
		case "":
		case "revision_conflict":
			p.Conflict = true
			return nil
		case "claim_conflict": // the preview is refused: name the owner
			if p.Foreign, _ = ownerOf(ctx, s, d.IssueID, nil); p.Foreign != nil {
				return nil
			}
			return err
		default:
			return err
		}
		intent := p.Prepared.Intent()
		p.Affected = append(append(p.Affected, intent.Targets...), intent.Guards...)
		for _, rc := range p.Prepared.RequiredClaims() {
			if rc.Claim != nil && rc.Claim.OwnerClientID != s.Mapping.ClientID {
				p.Foreign, err = ownerOf(ctx, s, rc.IssueID, rc.Claim)
				return err
			}
		}
		return nil
	})
	return p
}

// ownerOf is issue's live claim as a claim snapshot names it (with its
// owner's actor), or fallback when it is no longer another client's.
func ownerOf(ctx context.Context, s *litclient.Session, issue string, fallback *protocol.Claim) (*protocol.Claim, error) {
	snap, err := s.SnapshotClaims(ctx, litclient.ClaimSelection{Issues: []string{issue}})
	if err != nil {
		return fallback, err
	}
	if len(snap.Items) == 1 && snap.Items[0].Claim != nil && snap.Items[0].Claim.OwnerClientID != s.Mapping.ClientID {
		return snap.Items[0].Claim, nil
	}
	return fallback, nil
}

// Applied is the result of Apply.
type Applied struct {
	Draft  drafts.Draft
	Result protocol.MutationResult
	// Released reports that the edit claim was released; an explicitly held
	// claim is kept.
	Released bool
	// Owner names another client's claim when the commit was refused with
	// claim_conflict.
	Owner *protocol.Claim
	// Uncertain is the retained request of a commit whose outcome is
	// uncertain; it is never replayed.
	Uncertain *recovery.Request
	Err       error
}

// Apply commits exactly p's previewed transaction. On success it removes
// the draft and releases the edit claim (an explicitly held claim is kept).
// On any failure the draft is kept, and nothing is retried or replayed.
func (e *Editor) Apply(ctx context.Context, p Preview) Applied {
	a := Applied{Draft: p.Draft}
	switch {
	case p.Err != nil:
		a.Err = p.Err
	case p.Foreign != nil:
		a.Err, a.Owner = protocol.E(409, "claim_conflict", "another client holds a claim on an affected issue"), p.Foreign
	case !p.Ready():
		a.Err = protocol.E(409, "not_applicable", "the preview has nothing to apply at the recorded revision")
	}
	if a.Err != nil {
		return a
	}
	a.Err = e.g.Mutate(ctx, func(ctx context.Context, s *litclient.Session) error {
		var err error
		a.Result, err = s.Commit(ctx, p.Prepared)
		if req, ok := recovery.UncertainRequest(s.StateDir, err); ok {
			a.Uncertain = &req
		}
		return err
	})
	if litclient.Code(a.Err) == "claim_conflict" {
		_ = e.g.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
			a.Owner, _ = ownerOf(ctx, s, p.Draft.IssueID, nil)
			return nil
		})
	}
	if a.Err != nil {
		return a
	}
	if err := e.store.Discard(p.Draft); err != nil {
		a.Err = err // applied, but the draft could not be removed
	}
	a.Released, _ = e.release(ctx, p.Draft.IssueID)
	return a
}

// Reload records the issue's current revision and body as the draft's base,
// keeping the draft's content: applying it then replaces the current body.
func (e *Editor) Reload(ctx context.Context, d drafts.Draft) (drafts.Draft, error) {
	var got protocol.Issue
	err := e.g.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
		var err error
		got, err = readIssue(ctx, s, d.ProjectID, d.IssueID)
		return err
	})
	if err != nil {
		return d, err
	}
	d.Revision, d.Base, d.Title = got.Revision, got.Body, got.Title
	return e.store.Save(d)
}

// Compared is a draft compared with the issue's current body.
type Compared struct {
	Draft   drafts.Draft
	Content string
	Current *protocol.Issue
	// Diff is the change applying the draft would make to the current body.
	Diff []string
	Err  error
}

// DiffCurrent compares the draft with the issue's current body.
func (e *Editor) DiffCurrent(ctx context.Context, d drafts.Draft) Compared {
	c := Compared{Draft: d}
	if c.Content, c.Err = e.store.Read(d); c.Err != nil {
		return c
	}
	c.Err = e.g.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
		got, err := readIssue(ctx, s, d.ProjectID, d.IssueID)
		c.Current = &got
		return err
	})
	if c.Err == nil {
		c.Diff = drafts.Diff(c.Current.Body, c.Content)
	}
	return c
}

// Discarded is the result of Discard.
type Discarded struct {
	Released bool
	Err      error
}

// Discard cancels the edit: it removes the draft and releases the edit
// claim (an explicitly held claim is kept).
func (e *Editor) Discard(ctx context.Context, d drafts.Draft) Discarded {
	if err := e.store.Discard(d); err != nil {
		return Discarded{Err: err}
	}
	released, err := e.release(ctx, d.IssueID)
	return Discarded{Released: released, Err: err}
}
