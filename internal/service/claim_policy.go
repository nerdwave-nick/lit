package service

import (
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// Work requirements concern changed existing issues and explicit existing issue
// targets; pure structural guards grant no write permission. An existing issue
// that gains or loses a child also needs its owner's consent, although its own
// fields and revision stay unchanged now that membership lists are computed.
// Appending a comment changes no issue and needs no claim.
func claimRequirements(before, after RecordState, ops []protocol.Operation) []string {
	required := map[string]bool{}
	for _, op := range ops {
		if op.Type == "issues" {
			old, existed := before[recordKey("issues", op.ID)].(protocol.Issue)
			if existed {
				required[op.ID] = true
			}
			next, _ := after[recordKey("issues", op.ID)].(protocol.Issue)
			if !same(old.ParentID, next.ParentID) {
				for _, parent := range []*string{old.ParentID, next.ParentID} {
					if parent != nil && before[recordKey("issues", *parent)] != nil {
						required[*parent] = true
					}
				}
			}
		}
		if op.Type == "comments" && op.Kind != "create" {
			if v := before[recordKey("comments", op.ID)]; v != nil {
				required[v.(protocol.Comment).IssueID] = true
			}
		}
	}
	for _, key := range changedKeys(before, after) {
		if old, ok := before[key].(protocol.Issue); ok {
			required[old.ID] = true
		}
	}
	return sortedKeys(required)
}
func (s *Server) claimPolicy(before, after RecordState, i protocol.Intent, c protocol.Client, tokens []protocol.ClaimToken) (changes, error) {
	ids := claimRequirements(before, after, i.Operations)
	required := map[string]bool{}
	for _, id := range ids {
		required[id] = true
	}
	seen := map[string]bool{}
	for _, token := range tokens {
		if !protocol.ValidUUID(token.IssueID) || !protocol.ValidUUID(token.Token) || seen[token.IssueID] || !required[token.IssueID] {
			return changes{}, invalid("invalid or inapplicable claim token")
		}
		seen[token.IssueID] = true
		current, e := s.liveClaim(token.IssueID)
		if e != nil {
			return changes{}, e
		}
		if current == nil || current.Token != token.Token || current.OwnerClientID != c.ClientID {
			return changes{}, protocol.E(409, "claim_conflict", "provided claim token is stale")
		}
	}
	var cs changes
	for _, id := range ids {
		current, e := s.liveClaim(id)
		if e != nil {
			return changes{}, e
		}
		if current != nil && current.OwnerClientID != c.ClientID && !i.Force {
			return changes{}, protocol.E(409, "claim_conflict", "another client owns affected issue")
		}
		closing := false
		for _, op := range i.Operations {
			if op.Type == "issues" && op.ID == id && op.Set.State != nil && *op.Set.State == "closed" {
				closing = true
			}
		}
		if current != nil && (i.Force || closing) {
			cs.release = append(cs.release, id)
		}
	}
	return cs, nil
}
func (s *Server) preparedClaims(before, after RecordState, i protocol.Intent) ([]protocol.RequiredClaim, error) {
	out := []protocol.RequiredClaim{}
	for _, id := range claimRequirements(before, after, i.Operations) {
		c, e := s.liveClaim(id)
		if e != nil {
			return nil, e
		}
		out = append(out, protocol.RequiredClaim{IssueID: id, Claim: c})
	}
	return out, nil
}
