// Frozen copy of the legacy lit-server service at c5af73e (internal/service/claim_policy.go, claims.go);
// see doc.go. Do not change it.

package legacy

import (
	"encoding/json"
	"fmt"
	store "github.com/nerdwave-nick/lit/cmd/lit-migrate/internal/legacystore"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"os"
	"sort"
	"time"
)

// ClaimEnvelope keeps terminal release state versioned without creating history.
type ClaimEnvelope struct {
	SchemaVersion int             `json:"schema_version"`
	IssueID       string          `json:"issue_id"`
	Claim         *protocol.Claim `json:"claim"`
}

func claimPath(id string) string { return "issues/" + id + "/claim.json" }
func (s *Server) claimNow() time.Time {
	if s.requestTime != nil {
		return *s.requestTime
	}
	if s.claimClock != nil {
		return s.claimClock().UTC()
	}
	return time.Now().UTC()
}
func (s *Server) readClaim(id string) (*protocol.Claim, error) {
	b, e := s.Store.Read(claimPath(id))
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var fields map[string]json.RawMessage
	if e = protocol.Decode(b, &fields); e != nil {
		return nil, e
	}
	if _, ok := fields["claim"]; !ok {
		return nil, fmt.Errorf("missing claim authority field")
	}
	var v ClaimEnvelope
	if e = protocol.Decode(b, &v); e != nil {
		return nil, fmt.Errorf("invalid claim record: %w", e)
	}
	if v.SchemaVersion != 1 || v.IssueID != id {
		return nil, fmt.Errorf("invalid claim envelope")
	}
	if v.Claim == nil {
		return nil, nil
	}
	c := v.Claim
	acquired, ea := time.Parse(time.RFC3339Nano, c.AcquiredAt)
	expires, ee := time.Parse(time.RFC3339Nano, c.ExpiresAt)
	if c.SchemaVersion != 1 || c.IssueID != id || c.OwnerActor != nil || !protocol.ValidUUID(c.OwnerClientID) || !protocol.ValidUUID(c.Token) || ea != nil || ee != nil || !expires.After(acquired) || acquired.Location() != time.UTC || expires.Location() != time.UTC {
		return nil, fmt.Errorf("invalid claim record")
	}
	owner, e := s.Client(c.OwnerClientID)
	if e != nil {
		return nil, fmt.Errorf("claim owner: %w", e)
	}
	if owner.Status != "connected" {
		return nil, fmt.Errorf("claim belongs to disconnected client")
	}
	return c, nil
}

// LiveClaim and Claims must be called under Server.Mu (or during startup).
func (s *Server) LiveClaim(id string) (*protocol.Claim, error) {
	c, e := s.readClaim(id)
	if e != nil || c == nil {
		return c, e
	}
	expires, _ := time.Parse(time.RFC3339Nano, c.ExpiresAt)
	if !expires.After(s.claimNow()) {
		return nil, nil
	}
	return c, nil
}
func (s *Server) Claims(owner string) ([]protocol.Claim, error) {
	out := []protocol.Claim{}
	issues, e := s.Issues()
	if e != nil {
		return nil, e
	}
	now := s.claimNow()
	for _, p := range issues {
		c, e := s.readClaim(p.ID)
		if e != nil {
			return nil, e
		}
		if c == nil {
			continue
		}
		expires, _ := time.Parse(time.RFC3339Nano, c.ExpiresAt)
		if expires.After(now) && (owner == "" || c.OwnerClientID == owner) {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IssueID < out[j].IssueID })
	return out, nil
}
func (s *Server) InitClaims() error { _, e := s.Claims(""); return e }
func claimWrite(id string, c *protocol.Claim) store.Write {
	return store.JSONWrite(claimPath(id), ClaimEnvelope{1, id, c})
}
func (s *Server) claimTransaction(op protocol.ClaimOperation, client protocol.Client) ([]store.Write, protocol.ClaimResult, error) {
	result := protocol.ClaimResult{Outcome: "already_satisfied", Items: []protocol.RequiredClaim{}}
	fail := func(e error) ([]store.Write, protocol.ClaimResult, error) { return nil, result, e }
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
		claims, e := s.Claims(client.ClientID)
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
	writes := []store.Write{}
	for _, item := range op.Items {
		if _, e := s.Issue(item.IssueID); e != nil {
			return fail(e)
		}
		current, e := s.readClaim(item.IssueID)
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
		writes = append(writes, claimWrite(item.IssueID, next))
		result.Items = append(result.Items, protocol.RequiredClaim{IssueID: item.IssueID, Claim: next})
	}
	if len(writes) > 0 {
		result.Outcome = "applied"
	} else if op.Operation == "claims.release" && len(op.Items) > 0 {
		result.Outcome = "no_active_claim"
	}
	return writes, result, nil
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

// ApplyClaims is the in-process test/integration seam. HTTP prebounds its response
// before committing the same transaction, so no validation error follows a write.
func (s *Server) ApplyClaims(op protocol.ClaimOperation, c protocol.Client) (protocol.ClaimResult, error) {
	writes, result, e := s.claimTransaction(op, c)
	if e == nil && len(writes) > 0 {
		e = s.Store.Commit(writes)
	}
	return result, e
}

// Work requirements concern changed existing issues and explicit existing issue
// targets; pure structural guards grant no write permission. Appending comments
// is the sole membership-change exemption, even when another client owns work.
func claimRequirements(before, after RecordState, ops []protocol.Operation) []string {
	required := map[string]bool{}
	for _, op := range ops {
		if op.Type == "issues" && before[recordKey("issues", op.ID)] != nil {
			required[op.ID] = true
		}
		if op.Type == "comments" && op.Kind != "create" {
			if v := before[recordKey("comments", op.ID)]; v != nil {
				required[v.(protocol.Comment).IssueID] = true
			}
		}
	}
	for _, key := range changedKeys(before, after) {
		old, ok := before[key].(protocol.Issue)
		if !ok {
			continue
		}
		next := after[key].(protocol.Issue)
		// Ignore only the appended-comment membership itself; every other changed
		// field, including hierarchy and both link mirrors, needs ownership policy.
		old.CommentIDs = next.CommentIDs
		if !same(old, next) {
			required[old.ID] = true
		}
	}
	return sortedKeys(required)
}
func (s *Server) claimPolicy(before, after RecordState, i protocol.Intent, c protocol.Client, tokens []protocol.ClaimToken) ([]store.Write, error) {
	ids := claimRequirements(before, after, i.Operations)
	required := map[string]bool{}
	for _, id := range ids {
		required[id] = true
	}
	seen := map[string]bool{}
	for _, token := range tokens {
		if !protocol.ValidUUID(token.IssueID) || !protocol.ValidUUID(token.Token) || seen[token.IssueID] || !required[token.IssueID] {
			return nil, invalid("invalid or inapplicable claim token")
		}
		seen[token.IssueID] = true
		current, e := s.LiveClaim(token.IssueID)
		if e != nil {
			return nil, e
		}
		if current == nil || current.Token != token.Token || current.OwnerClientID != c.ClientID {
			return nil, protocol.E(409, "claim_conflict", "provided claim token is stale")
		}
	}
	writes := []store.Write{}
	for _, id := range ids {
		current, e := s.LiveClaim(id)
		if e != nil {
			return nil, e
		}
		if current != nil && current.OwnerClientID != c.ClientID && !i.Force {
			return nil, protocol.E(409, "claim_conflict", "another client owns affected issue")
		}
		closing := false
		for _, op := range i.Operations {
			if op.Type == "issues" && op.ID == id && op.Set.State != nil && *op.Set.State == "closed" {
				closing = true
			}
		}
		if current != nil && (i.Force || closing) {
			writes = append(writes, claimWrite(id, nil))
		}
	}
	return writes, nil
}
func (s *Server) preparedClaims(before, after RecordState, i protocol.Intent) ([]protocol.RequiredClaim, error) {
	out := []protocol.RequiredClaim{}
	for _, id := range claimRequirements(before, after, i.Operations) {
		c, e := s.LiveClaim(id)
		if e != nil {
			return nil, e
		}
		out = append(out, protocol.RequiredClaim{IssueID: id, Claim: c})
	}
	return out, nil
}

// Clear all owned records, including expired evidence, in the disconnect journal.
func (s *Server) disconnectWrites(c protocol.Client) ([]store.Write, error) {
	issues, e := s.Issues()
	if e != nil {
		return nil, e
	}
	writes := []store.Write{}
	for _, issue := range issues {
		claim, e := s.readClaim(issue.ID)
		if e != nil {
			return nil, e
		}
		if claim != nil && claim.OwnerClientID == c.ClientID {
			writes = append(writes, claimWrite(issue.ID, nil))
		}
	}
	writes = append(writes, store.JSONWrite("clients/"+c.ClientID+".json", c))
	return writes, nil
}
