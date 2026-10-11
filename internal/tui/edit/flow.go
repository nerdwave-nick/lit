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

// Editor edits records through TUI drafts: issue bodies, and the kinds
// registered with Register. Claiming kinds claim the owning issue on edit.
// Its methods are safe for concurrent use; those taking a context make
// requests through the gate and belong in a tea.Cmd. Nothing here talks to
// the terminal.
type Editor struct {
	g     Gate
	h     Holder
	store drafts.Store

	mu    sync.Mutex
	edits map[string]map[string]bool // per issue whose claim an edit of this session acquired: the drafts using it
	kinds map[drafts.Kind]Kind
}

// New returns the editor using the gate g, the holder h and the draft store.
// It edits issue bodies; Register adds other draft kinds.
func New(g Gate, h Holder, store drafts.Store) *Editor {
	return &Editor{g: g, h: h, store: store, edits: map[string]map[string]bool{}, kinds: map[drafts.Kind]Kind{drafts.IssueBody: issueBody}}
}

// Store is the draft store.
func (e *Editor) Store() drafts.Store { return e.store }

// Touch records draft activity (a change of a draft file): it postpones the
// claims' idle pause.
func (e *Editor) Touch() { e.h.Touch() }

// Hold is this session's hold on issue's claim now, if any.
func (e *Editor) Hold(issue string) (claims.Hold, bool) { return e.h.Snapshot().Hold(issue) }

// Started is the result of Open, Start or Resume.
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

// Start starts an edit of issue's body: Open of the issue_body draft. It
// claims the issue first (another client's claim refuses the edit and is
// named; nothing is forced), then reads the issue and records a draft
// holding exactly its body at the read revision. An existing draft is never
// overwritten: Start refuses with draft_exists before claiming, and the
// caller offers Resume, Diff or Discard instead.
func (e *Editor) Start(ctx context.Context, project, issue, title string) Started {
	return e.Open(ctx, Target{Kind: drafts.IssueBody, Project: project, Record: issue, Issue: issue, Title: title})
}

// claim is claim-on-edit for the draft key: the claim belongs to the edit
// unless this session already held it explicitly (a hold no edit took).
// Several drafts of one issue (its body and comments) share its edit claim.
func (e *Editor) claim(ctx context.Context, issue, title, key string) Started {
	e.mu.Lock()
	ours := len(e.edits[issue]) > 0
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
		if e.edits[issue] == nil {
			e.edits[issue] = map[string]bool{}
		}
		e.edits[issue][key] = true
		e.mu.Unlock()
	}
	return r
}

// release ends the draft key's use of issue's claim. The claim is released
// when an edit of this session acquired it and no other draft of this
// session still uses it; a claim held explicitly is kept.
func (e *Editor) release(ctx context.Context, issue, key string) (bool, error) {
	e.mu.Lock()
	ours := e.edits[issue][key]
	delete(e.edits[issue], key)
	others := len(e.edits[issue])
	if others == 0 {
		delete(e.edits, issue)
	}
	e.mu.Unlock()
	if _, held := e.h.Snapshot().Hold(issue); !ours || others > 0 || !held {
		return false, nil
	}
	r := e.h.Release(ctx, issue)
	return r.Err == nil, r.Err
}

// Resume continues d: for a claiming kind it claims the owning issue again
// when this session does not hold it (a lost or released claim is
// reacquired, rereading the issue first). It keeps the draft's recorded
// revision and content. A claim-exempt or unowned kind takes no claim.
func (e *Editor) Resume(ctx context.Context, d drafts.Draft) Started {
	if !d.Kind.Claims() {
		return Started{Draft: d, Kind: drafts.ClaimNone}
	}
	r := e.claim(ctx, d.IssueID, d.Title, d.Key())
	r.Draft = d
	if r.Err == nil && d.Claim != r.Kind {
		d.Claim = r.Kind
		r.Draft, r.Err = e.store.Save(d)
	}
	return r
}
