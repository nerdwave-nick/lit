package quick

import (
	"context"
	"slices"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// Gate is the lifecycle's session gate: Do for reads, Mutate for the commit
// (it pauses background refresh meanwhile).
type Gate interface {
	Do(ctx context.Context, fn func(ctx context.Context, s *litclient.Session) error) error
	Mutate(ctx context.Context, fn func(ctx context.Context, s *litclient.Session) error) error
}

// Lookup finds a loaded issue's summary by UUID.
type Lookup func(id string) (protocol.IssueSummary, bool)

// Affected is one record the previewed transaction fences: a target it
// changes, or a guard it reads structurally without changing.
type Affected struct {
	Type     string // "issues", or another record type
	ID       string
	Title    string // "" when not known
	Revision int64  // the revision it is fenced at; 0 when unfenced
	Changed  bool   // a target (changed), not a guard
	Self     bool   // the issue the action started from
	// Claim is the issue's live claim at preview, if any; for another
	// client's claim it names the owner's actor when it could be read.
	Claim *protocol.Claim
	Mine  bool // Claim belongs to this client: its token is passed on
}

// Foreign reports a live claim held by another client.
func (a Affected) Foreign() bool { return a.Claim != nil && !a.Mine }

// Preview is a previewed quick action. Err is the preview's failure, such as
// claim_conflict when another client holds a claim on an affected issue;
// then there is nothing to commit.
type Preview struct {
	Change   Change
	Prepared *litclient.Prepared
	Affected []Affected
	Err      error
}

// Refused reports whether the preview may not be committed.
func (p Preview) Refused() bool { return p.Err != nil || p.Prepared == nil }

// Prepare previews c through g (a read: nothing is written or reserved). It
// lists every target and guard of the previewed intent with its title and
// fenced revision, and the claims of this client it passes on. If another
// client holds a claim on an affected issue, the service refuses the preview
// with claim_conflict before anything is written; the refusal then lists the
// issues the change touches (c.Touches) with their claims, naming owners.
func Prepare(ctx context.Context, g Gate, c Change, lookup Lookup) Preview {
	p := Preview{Change: c}
	err := g.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
		prepared, err := s.Prepare(ctx, c.Request)
		if litclient.Code(err) == "claim_conflict" {
			p.Affected = touched(ctx, s, c, lookup)
		}
		if err != nil {
			return err
		}
		p.Prepared = prepared
		p.Affected = affectedBy(prepared, c.Issue, s.Client.ClientID)
		nameUnknown(ctx, s, p.Affected, lookup)
		return nil
	})
	if err != nil {
		return Preview{Change: c, Affected: p.Affected, Err: err}
	}
	return p
}

// affectedBy lists the targets, then the guards, of the prepared intent,
// with the claims the preview observed; the action's own issue comes first.
func affectedBy(p *litclient.Prepared, self, client string) []Affected {
	claims := map[string]*protocol.Claim{}
	for _, r := range p.RequiredClaims() {
		claims[r.IssueID] = r.Claim
	}
	intent := p.Intent()
	var out []Affected
	add := func(refs []protocol.ObjectRef, changed bool) {
		for _, r := range refs {
			a := Affected{Type: r.Type, ID: r.ID, Changed: changed, Self: r.Type == "issues" && r.ID == self}
			if r.ExpectedRevision != nil {
				a.Revision = *r.ExpectedRevision
			}
			if c := claims[r.ID]; r.Type == "issues" && c != nil {
				a.Claim, a.Mine = c, c.OwnerClientID == client
			}
			out = append(out, a)
		}
	}
	add(intent.Targets, true)
	add(intent.Guards, false)
	slices.SortStableFunc(out, func(a, b Affected) int {
		switch {
		case a.Self == b.Self:
			return 0
		case a.Self:
			return -1
		}
		return 1
	})
	return out
}

// touched lists the issues c touches, with their live claims, for a preview
// the service refused with claim_conflict. Revisions are those last loaded.
func touched(ctx context.Context, s *litclient.Session, c Change, lookup Lookup) []Affected {
	var out []Affected
	for _, id := range c.Touches {
		a := Affected{Type: "issues", ID: id, Changed: true, Self: id == c.Issue}
		if lookup != nil {
			if i, ok := lookup(id); ok {
				a.Revision = i.Revision
			}
		}
		out = append(out, a)
	}
	if snap, err := s.SnapshotClaims(ctx, litclient.ClaimSelection{Issues: c.Touches}); err == nil {
		for _, r := range snap.Items {
			for n := range out {
				if out[n].ID == r.IssueID && r.Claim != nil {
					claim := *r.Claim
					out[n].Claim, out[n].Mine = &claim, claim.OwnerClientID == s.Client.ClientID
				}
			}
		}
	}
	nameUnknown(ctx, s, out, lookup)
	return out
}

// nameUnknown titles the affected issues: loaded ones by lookup, others
// (for example in another project) read by UUID, best effort.
func nameUnknown(ctx context.Context, s *litclient.Session, affected []Affected, lookup Lookup) {
	var unknown []string
	for n := range affected {
		a := &affected[n]
		if a.Type != "issues" {
			continue
		}
		if lookup != nil {
			if i, ok := lookup(a.ID); ok {
				a.Title = i.Title
				continue
			}
		}
		unknown = append(unknown, a.ID)
	}
	if len(unknown) == 0 {
		return
	}
	issues, err := litclient.Get[protocol.Issue](ctx, s, "", unknown...)
	if err != nil {
		return
	}
	for _, i := range issues {
		for n := range affected {
			if affected[n].ID == i.ID {
				affected[n].Title = i.Title
			}
		}
	}
}

// Outcome is the result of committing a preview. On claim_conflict, Owners
// lists the affected issues another client claimed, as read afterwards.
type Outcome struct {
	Result protocol.MutationResult
	Err    error
	Owners []Affected
}

// Commit submits exactly p's Prepared once, through the mutation gate, with
// the tokens of the claims this client owned at preview. A refused preview is
// never sent.
func Commit(ctx context.Context, g Gate, p Preview) Outcome {
	if p.Refused() {
		err := p.Err
		if err == nil {
			err = protocol.E(400, "invalid_arguments", "nothing was previewed")
		}
		return Outcome{Err: err, Owners: foreignOf(p.Affected)}
	}
	var out Outcome
	out.Err = g.Mutate(ctx, func(ctx context.Context, s *litclient.Session) error {
		r, err := s.Commit(ctx, p.Prepared)
		out.Result = r
		return err
	})
	if litclient.Code(out.Err) == "claim_conflict" {
		out.Owners = owners(ctx, g, p)
	}
	return out
}

func foreignOf(affected []Affected) []Affected {
	var out []Affected
	for _, a := range affected {
		if a.Foreign() {
			out = append(out, a)
		}
	}
	return out
}

// owners reads the live claims of the issues that needed claim policy after
// a claim_conflict and returns those another client holds, titled as in p.
func owners(ctx context.Context, g Gate, p Preview) []Affected {
	var ids []string
	for _, r := range p.Prepared.RequiredClaims() {
		ids = append(ids, r.IssueID)
	}
	var out []Affected
	_ = g.Do(ctx, func(ctx context.Context, s *litclient.Session) error {
		snap, err := s.SnapshotClaims(ctx, litclient.ClaimSelection{Issues: ids})
		if err != nil {
			return err
		}
		for _, r := range snap.Items {
			if r.Claim == nil || r.Claim.OwnerClientID == s.Client.ClientID {
				continue
			}
			a := Affected{Type: "issues", ID: r.IssueID, Claim: r.Claim}
			for _, known := range p.Affected {
				if known.ID == r.IssueID {
					a.Title, a.Revision, a.Changed, a.Self = known.Title, known.Revision, known.Changed, known.Self
				}
			}
			out = append(out, a)
		}
		return nil
	})
	return out
}
