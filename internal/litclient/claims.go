package litclient

import (
	"context"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// ClaimSelection names the issues a claim operation reads or changes.
type ClaimSelection struct {
	// Project scopes Issues references; "" uses the client's selected project.
	Project string
	// Issues are issue references (titles, UUIDs or UUID prefixes).
	Issues []string
	// All selects every live claim this client owns instead of Issues.
	All bool
}

// ClaimRequest is a claim operation. A lease is bounded by service time: For
// (default 30 minutes) or an absolute Until, at most one hour ahead.
type ClaimRequest struct {
	ClaimSelection
	For   time.Duration
	Until *time.Time
	// Force lets acquire take over a live claim another client holds. Agents
	// and lit-tui never force.
	Force bool
}

// ClaimSnapshot is the live claim state of the selected issues at ServerTime.
// An issue without a live claim has a nil Claim.
type ClaimSnapshot struct {
	Items      []protocol.RequiredClaim `json:"items"`
	ServerTime string                   `json:"server_time"`
}

// SnapshotClaims reads the live claims of the selected issues.
func (s *Session) SnapshotClaims(ctx context.Context, sel ClaimSelection) (ClaimSnapshot, error) {
	defer s.bind(ctx)()
	return s.snapshotClaims(sel)
}

func (s *Session) snapshotClaims(sel ClaimSelection) (ClaimSnapshot, error) {
	query := map[string]any{}
	if sel.All {
		query["owner_client_id"] = s.Mapping.ClientID
	} else {
		query["targets"] = sel.Issues
		if sel.Project != "" {
			query["project"] = sel.Project
		} else if s.Client.ProjectID != nil {
			query["project"] = *s.Client.ProjectID
		}
	}
	var snapshot ClaimSnapshot
	if e := s.Call("POST", "/v1/claim-snapshots", query, nil, &snapshot, false); e != nil {
		return ClaimSnapshot{}, e
	}
	return snapshot, nil
}

// AcquireClaims claims the selected issues for this client. A live claim held
// by another client is refused with claim_conflict unless forced.
func (s *Session) AcquireClaims(ctx context.Context, r ClaimRequest) (protocol.ClaimResult, error) {
	defer s.bind(ctx)()
	return s.claimOperation("acquire", r)
}

// RenewClaims extends this client's live claims on the selected issues.
// Renewing an absent or expired claim fails with claim_expired before anything
// is sent; nothing is ever reacquired automatically. Another client's claim is
// refused with claim_conflict.
func (s *Session) RenewClaims(ctx context.Context, r ClaimRequest) (protocol.ClaimResult, error) {
	defer s.bind(ctx)()
	return s.claimOperation("renew", r)
}

// ReleaseClaims ends this client's claims on the selected issues. An issue
// without a live claim is reported with a nil claim.
func (s *Session) ReleaseClaims(ctx context.Context, sel ClaimSelection) (protocol.ClaimResult, error) {
	defer s.bind(ctx)()
	return s.claimOperation("release", ClaimRequest{ClaimSelection: sel})
}

// claimOperation reads the selected claims, then sends one claim operation for
// exactly that state: tokens come from the snapshot and lease targets from its
// service time.
func (s *Session) claimOperation(verb string, r ClaimRequest) (protocol.ClaimResult, error) {
	if r.For != 0 && r.Until != nil {
		return protocol.ClaimResult{}, protocol.E(400, "invalid_arguments", "a lease takes either a duration or an expiry, not both")
	}
	snapshot, e := s.snapshotClaims(r.ClaimSelection)
	if e != nil {
		return protocol.ClaimResult{}, e
	}
	target := ""
	if verb == "acquire" || verb == "renew" {
		clock := snapshot.ServerTime
		if clock == "" {
			clock = s.ServerTime
		}
		if target, e = leaseTarget(clock, r.For, r.Until); e != nil {
			return protocol.ClaimResult{}, e
		}
	}
	items := []map[string]any{}
	for _, v := range snapshot.Items {
		if verb == "renew" && v.Claim == nil {
			return protocol.ClaimResult{}, protocol.E(409, "claim_expired", "cannot renew an absent or expired claim")
		}
		item := map[string]any{"issue_id": v.IssueID}
		if verb != "acquire" && v.Claim != nil {
			item["token"] = v.Claim.Token
		}
		if target != "" {
			item["extend_to"] = target
		}
		if r.Force {
			item["force"] = true
		}
		items = append(items, item)
	}
	selection := "explicit"
	if r.All {
		selection = "all_owned"
	}
	payload := map[string]any{"schema_version": 1, "operation": "claims." + verb, "owner_client_id": s.Mapping.ClientID, "selection": selection, "items": items}
	var result protocol.ClaimResult
	if e = s.Call("POST", "/v1/operations", payload, nil, &result, true); e != nil {
		return protocol.ClaimResult{}, e
	}
	return result, nil
}

// leaseTarget is the requested expiry relative to service time: until, or now
// plus d (30 minutes when zero). It must lie in the next hour.
func leaseTarget(serverTime string, d time.Duration, until *time.Time) (string, error) {
	now, e := time.Parse(time.RFC3339Nano, serverTime)
	if e != nil {
		return "", protocol.E(500, "invalid_response", "claim snapshot omitted valid server time")
	}
	if d == 0 {
		d = 30 * time.Minute
	}
	target := now.Add(d)
	if until != nil {
		target = *until
	}
	if !target.After(now) || target.After(now.Add(time.Hour)) {
		return "", protocol.E(400, "invalid_arguments", "claim expiry must be in the future and at most one hour from service time")
	}
	return target.UTC().Format(time.RFC3339Nano), nil
}
