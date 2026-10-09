package edit

import (
	"context"
	"sync"
	"unicode/utf8"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/claims"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/recovery"
)

// Gate is the lifecycle's mutation gate (lifecycle.Lifecycle): Do for reads,
// Mutate for writes. Shutdown waits for a call in it and refuses new ones.
type Gate interface {
	Do(ctx context.Context, fn func(ctx context.Context, s *litclient.Session) error) error
	Mutate(ctx context.Context, fn func(ctx context.Context, s *litclient.Session) error) error
}

// Holder is the claim holder (claims.Holder) that keeps edit claims renewed.
type Holder interface {
	Claim(ctx context.Context, id, title string) claims.Result
	Release(ctx context.Context, id string) claims.Result
	Snapshot() claims.Msg
	Touch()
}

// Editor edits issue bodies through TUI drafts, claiming the issue on edit.
// Its methods are safe for concurrent use; those taking a context make
// requests through the gate and belong in a tea.Cmd. Nothing here talks to
// the terminal.
type Editor struct {
	g     Gate
	h     Holder
	store drafts.Store

	mu    sync.Mutex
	edits map[string]bool // issues whose claim an edit of this session acquired
}

// New returns the editor using the gate g, the holder h and the draft store.
func New(g Gate, h Holder, store drafts.Store) *Editor {
	return &Editor{g: g, h: h, store: store, edits: map[string]bool{}}
}

// Store is the draft store.
func (e *Editor) Store() drafts.Store { return e.store }

// Touch records draft activity (a change of a draft file): it postpones the
// claims' idle pause.
func (e *Editor) Touch() { e.h.Touch() }

// Started is the result of Start or Resume.
type Started struct {
	Draft drafts.Draft
	// Claim is the claim-on-edit result; Kind says whether the claim belongs
	// to the edit (released on save or cancel) or was the user's already.
	Claim claims.Result
	Kind  drafts.ClaimState
	// Owner is another client's claim that refused the edit.
	Owner *protocol.Claim
	Err   error
}

// Existing returns the draft of issue's body in project, if there is one.
func (e *Editor) Existing(project, issue string) (drafts.Draft, bool, error) {
	return e.store.Find(project, drafts.IssueBody, issue)
}

// Start starts an edit of issue's body: it claims the issue first (another
// client's claim refuses the edit and is named; nothing is forced), then
// reads the issue and records a draft holding exactly its body at the read
// revision. An existing draft is never overwritten: Start refuses with
// draft_exists before claiming, and the caller offers Resume, Diff or
// Discard instead.
func (e *Editor) Start(ctx context.Context, project, issue, title string) Started {
	if _, ok, err := e.Existing(project, issue); err != nil || ok {
		if err == nil {
			err = protocol.E(409, "draft_exists", "this issue already has a draft; resume, diff or discard it")
		}
		return Started{Err: err}
	}
	r := e.claim(ctx, issue, title)
	if r.Err != nil {
		return r
	}
	var got protocol.Issue
	var service string
	r.Err = e.g.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
		var err error
		got, err = readIssue(ctx, s, project, issue)
		service = s.Meta.ServiceID
		return err
	})
	if r.Err == nil {
		r.Draft, r.Err = e.store.Create(drafts.Draft{Kind: drafts.IssueBody, ServiceID: service, ProjectID: project, RecordID: issue, IssueID: issue,
			Title: got.Title, Revision: got.Revision, Base: got.Body, Claim: r.Kind}, got.Body)
	}
	if r.Err != nil {
		e.release(ctx, issue) // the edit did not start: give back its claim
	}
	return r
}

// claim is claim-on-edit: the claim belongs to the edit unless this session
// already held it explicitly (a hold the edit did not take).
func (e *Editor) claim(ctx context.Context, issue, title string) Started {
	e.mu.Lock()
	ours := e.edits[issue]
	e.mu.Unlock()
	_, held := e.h.Snapshot().Hold(issue)
	r := Started{Claim: e.h.Claim(ctx, issue, title)}
	if r.Err, r.Owner = r.Claim.Err, r.Claim.Owner; r.Err != nil {
		return r
	}
	r.Kind = drafts.ClaimExplicit
	if ours || !held {
		r.Kind = drafts.ClaimEdit
		e.mu.Lock()
		e.edits[issue] = true
		e.mu.Unlock()
	}
	return r
}

// release releases issue's claim if an edit of this session acquired it.
func (e *Editor) release(ctx context.Context, issue string) (bool, error) {
	e.mu.Lock()
	ours := e.edits[issue]
	delete(e.edits, issue)
	e.mu.Unlock()
	if _, held := e.h.Snapshot().Hold(issue); !ours || !held {
		return false, nil
	}
	r := e.h.Release(ctx, issue)
	return r.Err == nil, r.Err
}

func readIssue(ctx context.Context, s *litclient.Session, project, id string) (protocol.Issue, error) {
	got, err := litclient.Get[protocol.Issue](ctx, s, project, id)
	if err == nil && len(got) != 1 {
		err = protocol.E(500, "invalid_response", "the issue read returned no single issue")
	}
	if err != nil {
		return protocol.Issue{}, err
	}
	return got[0], nil
}

// Resume continues d: it claims the issue again when this session does not
// hold it (a lost or released claim is reacquired, rereading the issue
// first), and keeps the draft's recorded revision and content.
func (e *Editor) Resume(ctx context.Context, d drafts.Draft) Started {
	r := e.claim(ctx, d.IssueID, d.Title)
	r.Draft = d
	if r.Err == nil && d.Claim != r.Kind {
		d.Claim = r.Kind
		r.Draft, r.Err = e.store.Save(d)
	}
	return r
}

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
