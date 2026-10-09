package edit

import (
	"context"
	"sync"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/claims"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
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

// Hold is this session's hold on issue's claim now, if any.
func (e *Editor) Hold(issue string) (claims.Hold, bool) { return e.h.Snapshot().Hold(issue) }

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
