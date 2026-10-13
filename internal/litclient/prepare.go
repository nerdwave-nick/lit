package litclient

import (
	"context"
	"sync/atomic"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// Prepared is a previewed durable transaction. It fences every affected record,
// including relationship mirrors and structural guards the caller never named,
// at the revision observed by the preview. Its content cannot be changed after
// Prepare, and it can be submitted at most once.
type Prepared struct {
	plan   protocol.Prepared
	tokens []protocol.ClaimToken
	used   atomic.Bool
}

// Intent returns a copy of the previewed intent: its operations, Targets (every
// changed record with the revision it is fenced at) and Guards (records read
// structurally, fenced without change).
func (p *Prepared) Intent() protocol.Intent {
	var i protocol.Intent
	_ = protocol.Decode([]byte(p.plan.CanonicalJSON), &i)
	return i
}

// RequiredClaims returns a copy of the claim state of every issue the
// transaction needs ownership policy for, as observed by the preview.
func (p *Prepared) RequiredClaims() []protocol.RequiredClaim {
	out := make([]protocol.RequiredClaim, len(p.plan.RequiredClaims))
	for n, r := range p.plan.RequiredClaims {
		out[n] = protocol.RequiredClaim{IssueID: r.IssueID}
		if r.Claim != nil {
			c := *r.Claim
			out[n].Claim = &c
		}
	}
	return out
}

// RequestHash is the canonical hash of the previewed intent.
func (p *Prepared) RequestHash() string { return p.plan.RequestHash }

// Prepare previews req (POST /v1/transaction-previews). It is a read: nothing is
// written or reserved. A zero SchemaVersion means version 1. The preview must
// belong to this session's service and client and its canonical encoding must
// match its hash, or it is refused as invalid_response.
func (s *Session) Prepare(ctx context.Context, req protocol.PrepareRequest) (*Prepared, error) {
	defer s.bind(ctx)()
	if req.SchemaVersion == 0 {
		req.SchemaVersion = 1
	}
	var plan protocol.Prepared
	if e := s.Call("POST", "/v1/transaction-previews", req, nil, &plan, false); e != nil {
		return nil, e
	}
	b, hash, e := protocol.Canonical(plan.Intent)
	if e != nil || hash != plan.RequestHash || plan.SchemaVersion != 1 || plan.ServiceID != s.Meta.ServiceID || plan.ClientID != s.Client.ClientID || string(b) != plan.CanonicalJSON {
		return nil, protocol.E(500, "invalid_response", "invalid prepared transaction")
	}
	// Only this Prepared holds the decoded plan; accessors hand out copies.
	return &Prepared{plan: plan, tokens: ownedTokens(plan)}, nil
}

// ownedTokens are the claim tokens of the required claims the preparing client
// owned when the preview was taken.
func ownedTokens(p protocol.Prepared) []protocol.ClaimToken {
	out := []protocol.ClaimToken{}
	for _, r := range p.RequiredClaims {
		if r.Claim != nil && r.Claim.OwnerClientID == p.ClientID {
			out = append(out, protocol.ClaimToken{IssueID: r.IssueID, Token: r.Claim.Token})
		}
	}
	return out
}

// Commit submits exactly the previewed transaction p, with the claim tokens of
// the RequiredClaims this client owned at preview. If any affected record
// changed after the preview, the service refuses it (revision_conflict); it is
// never prepared again. Once sent, p is consumed even when the commit fails:
// committing it again is refused with prepared_already_submitted, so an
// uncertain outcome is never replayed. A Prepared from another client is
// refused with wrong_client (or wrong_service) without sending anything.
func (s *Session) Commit(ctx context.Context, p *Prepared) (protocol.MutationResult, error) {
	var result protocol.MutationResult
	if p == nil || p.plan.CanonicalJSON == "" {
		return result, protocol.E(400, "invalid_arguments", "commit requires a transaction returned by Prepare")
	}
	if p.plan.ServiceID != s.Meta.ServiceID {
		return result, protocol.E(409, "wrong_service", "prepared transaction belongs to another service")
	}
	if p.plan.ClientID != s.Client.ClientID {
		return result, protocol.E(409, "wrong_client", "prepared transaction belongs to another client")
	}
	if !p.used.CompareAndSwap(false, true) {
		return result, protocol.E(409, "prepared_already_submitted", "prepared transaction was already submitted; prepare it again")
	}
	defer s.bind(ctx)()
	body := protocol.DurableRequest{SchemaVersion: 1, Intent: p.plan.Intent, RequestHash: p.plan.RequestHash, Claims: p.tokens}
	if e := s.Call("POST", "/v1/transactions", body, nil, &result, true); e != nil {
		return protocol.MutationResult{}, e
	}
	return result, nil
}
