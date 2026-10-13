package service

import (
	"sort"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// liveClaim is the claim on an issue that is live at the request time.
func (s *Server) liveClaim(id string) (*protocol.Claim, error) {
	return s.q().LiveClaim(s.ctx(), id, s.claimNow())
}

// liveClaims lists the live claims of owner (of everyone when empty) in issue order.
func (s *Server) liveClaims(owner string) ([]protocol.Claim, error) {
	out, e := s.q().LiveClaims(s.ctx(), owner, s.claimNow())
	sort.Slice(out, func(i, j int) bool { return out[i].IssueID < out[j].IssueID })
	return out, e
}

// claimTransaction validates a claim operation against the current claims and
// returns its writes without applying them.
func (s *Server) claimTransaction(op protocol.ClaimOperation, client protocol.Client) (changes, protocol.ClaimResult, error) {
	result := protocol.ClaimResult{Outcome: "already_satisfied", Items: []protocol.RequiredClaim{}}
	fail := func(e error) (changes, protocol.ClaimResult, error) { return changes{}, result, e }
	if op.SchemaVersion != 0 && op.SchemaVersion != 1 {
		return fail(invalid("unsupported schema_version"))
	}
	if op.OwnerClientID != client.ClientID {
		return fail(protocol.E(409, "wrong_client", "claim owner differs from request client"))
	}
	if op.Operation != "claims.acquire" && op.Operation != "claims.renew" && op.Operation != "claims.release" {
		return fail(invalid("unknown claim operation"))
	}
	if op.Selection != "explicit" && op.Selection != "all_owned" {
		return fail(invalid("invalid claim selection"))
	}
	if op.Selection == "all_owned" && op.Operation == "claims.acquire" {
		return fail(invalid("acquire needs explicit issues"))
	}
	if len(op.Items) == 0 && op.Selection != "all_owned" {
		return fail(invalid("empty claim batch"))
	}
	if len(op.Items) > s.Config.Limits.ExplicitItems {
		return fail(protocol.E(413, "limit_exceeded", "claim batch too large"))
	}
	seen := map[string]string{}
	for _, item := range op.Items {
		if !protocol.ValidUUID(item.IssueID) {
			return fail(invalid("invalid issue ID"))
		}
		if _, ok := seen[item.IssueID]; ok {
			return fail(invalid("duplicate claim issue"))
		}
		seen[item.IssueID] = item.Token
		if op.Operation == "claims.acquire" {
			if item.Token != "" {
				return fail(invalid("acquire does not accept token"))
			}
		} else if (!protocol.ValidUUID(item.Token) && !(op.Operation == "claims.release" && item.Token == "")) || item.Force {
			return fail(invalid("renew/release needs token and does not accept force"))
		}
		if op.Operation == "claims.release" && item.ExtendTo != "" {
			return fail(invalid("release does not accept extend_to"))
		}
	}
	if op.Selection == "all_owned" {
		claims, e := s.liveClaims(client.ClientID)
		if e != nil {
			return fail(e)
		}
		if len(claims) != len(seen) {
			return fail(protocol.E(409, "affected_set_changed", "owned live claim set changed"))
		}
		for _, c := range claims {
			if seen[c.IssueID] != c.Token {
				return fail(protocol.E(409, "affected_set_changed", "owned live claim set changed"))
			}
		}
	}
	now := s.claimNow()
	var cs changes
	for _, item := range op.Items {
		if _, e := s.issue(item.IssueID); e != nil {
			return fail(e)
		}
		current, e := s.q().Claim(s.ctx(), item.IssueID)
		if e != nil {
			return fail(e)
		}
		live := current != nil
		if live {
			expiry, _ := time.Parse(time.RFC3339Nano, current.ExpiresAt)
			live = expiry.After(now)
		}
		var next *protocol.Claim
		switch op.Operation {
		case "claims.acquire":
			if live && !item.Force {
				return fail(protocol.E(409, "claim_conflict", "issue already claimed"))
			}
			expiry, e := claimExpiry(item.ExtendTo, now, true)
			if e != nil {
				return fail(e)
			}
			next = &protocol.Claim{SchemaVersion: 1, IssueID: item.IssueID, OwnerClientID: client.ClientID, Token: protocol.UUID(), AcquiredAt: now.Format(time.RFC3339Nano), ExpiresAt: expiry.Format(time.RFC3339Nano)}
		case "claims.renew", "claims.release":
			if live && (current.OwnerClientID != client.ClientID || current.Token != item.Token) {
				return fail(protocol.E(409, "claim_conflict", "claim owner or token differs"))
			}
			if !live {
				if op.Operation == "claims.release" {
					result.Items = append(result.Items, protocol.RequiredClaim{IssueID: item.IssueID})
					continue
				}
				if current != nil && current.Token == item.Token && current.OwnerClientID == client.ClientID {
					return fail(protocol.E(410, "claim_expired", "claim expired"))
				}
				return fail(protocol.E(409, "claim_conflict", "no matching live claim"))
			}
			if op.Operation == "claims.renew" {
				expiry, parseErr := time.Parse(time.RFC3339Nano, item.ExtendTo)
				if parseErr != nil || expiry.Location() != time.UTC {
					return fail(invalid("extend_to must be a UTC timestamp"))
				}
				oldExpiry, _ := time.Parse(time.RFC3339Nano, current.ExpiresAt)
				if !expiry.After(oldExpiry) {
					result.Items = append(result.Items, protocol.RequiredClaim{IssueID: item.IssueID, Claim: current})
					continue
				}
				expiry, e := claimExpiry(item.ExtendTo, now, false)
				if e != nil {
					return fail(e)
				}
				old, _ := time.Parse(time.RFC3339Nano, current.ExpiresAt)
				copy := *current
				next = &copy
				if expiry.After(old) {
					next.ExpiresAt = expiry.Format(time.RFC3339Nano)
				} else {
					result.Items = append(result.Items, protocol.RequiredClaim{IssueID: item.IssueID, Claim: current})
					continue
				}
			}
		}
		if next == nil {
			cs.release = append(cs.release, item.IssueID)
		} else {
			cs.claims = append(cs.claims, *next)
		}
		result.Items = append(result.Items, protocol.RequiredClaim{IssueID: item.IssueID, Claim: next})
	}
	if !cs.empty() {
		result.Outcome = "applied"
	} else if op.Operation == "claims.release" && len(op.Items) > 0 {
		result.Outcome = "no_active_claim"
	}
	return cs, result, nil
}
func claimExpiry(value string, now time.Time, acquire bool) (time.Time, error) {
	if value == "" && acquire {
		return now.Add(30 * time.Minute), nil
	}
	expiry, e := time.Parse(time.RFC3339Nano, value)
	if e != nil || expiry.Location() != time.UTC {
		return time.Time{}, invalid("extend_to must be a UTC timestamp")
	}
	if !expiry.After(now) {
		return time.Time{}, invalid("extend_to must be in the future")
	}
	if expiry.After(now.Add(time.Hour)) {
		if acquire {
			return now.Add(time.Hour), nil
		}
		return time.Time{}, invalid("extend_to exceeds the one-hour maximum")
	}
	return expiry, nil
}
