package litclient_test

import (
	"context"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
)

func expiry(t *testing.T, v string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestClaimsFollowCLISemantics(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	s := h.connect("claims", "Human")
	other := h.connect("claims-other", "Agent")
	project, issues := seed(t, s, "claims", "A", "B", "C")
	a, b, c := issues["A"].ID, issues["B"].ID, issues["C"].ID
	on := func(ids ...string) litclient.ClaimSelection {
		return litclient.ClaimSelection{Project: project, Issues: ids}
	}
	lease := func(d time.Duration, ids ...string) litclient.ClaimRequest {
		return litclient.ClaimRequest{ClaimSelection: on(ids...), For: d}
	}

	acquired, err := s.AcquireClaims(ctx, lease(10*time.Minute, a))
	if err != nil || acquired.Outcome != "applied" || len(acquired.Items) != 1 || acquired.Items[0].IssueID != a || acquired.Items[0].Claim.OwnerClientID != s.Client.ClientID {
		t.Fatalf("acquire: %+v %v", acquired, err)
	}
	first := acquired.Items[0].Claim
	renewed, err := s.RenewClaims(ctx, lease(20*time.Minute, a))
	if err != nil || renewed.Items[0].Claim.Token != first.Token || !expiry(t, renewed.Items[0].Claim.ExpiresAt).After(expiry(t, first.ExpiresAt)) {
		t.Fatalf("renew: %+v %v", renewed, err)
	}
	snap, err := s.SnapshotClaims(ctx, on(a, b))
	if err != nil || len(snap.Items) != 2 || snap.Items[0].Claim == nil || snap.Items[1].Claim != nil || snap.ServerTime == "" {
		t.Fatalf("snapshot: %+v %v", snap, err)
	}
	if released, err := s.ReleaseClaims(ctx, on(a)); err != nil || released.Items[0].Claim != nil {
		t.Fatalf("release: %+v %v", released, err)
	}

	operations := h.count("POST /v1/operations")
	if _, err = s.RenewClaims(ctx, lease(0, a)); litclient.Code(err) != "claim_expired" {
		t.Fatalf("renew absent claim: %v", err)
	}
	if h.count("POST /v1/operations") != operations {
		t.Fatal("absent claim renewal was sent")
	}

	// An expired claim is never reacquired by renewal.
	if _, err = s.AcquireClaims(ctx, lease(time.Second, a)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap, err = s.SnapshotClaims(ctx, on(a))
		if err != nil {
			t.Fatal(err)
		}
		if snap.Items[0].Claim == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("one-second claim did not expire")
		}
		time.Sleep(10 * time.Millisecond)
	}
	operations = h.count("POST /v1/operations")
	if _, err = s.RenewClaims(ctx, lease(10*time.Minute, a)); litclient.Code(err) != "claim_expired" {
		t.Fatalf("renew expired claim: %v", err)
	}
	if snap, err = s.SnapshotClaims(ctx, on(a)); err != nil || snap.Items[0].Claim != nil || h.count("POST /v1/operations") != operations {
		t.Fatalf("expired claim was reacquired: %+v %v", snap, err)
	}

	// Another client's claim is a conflict for every operation.
	if _, err = other.AcquireClaims(ctx, lease(0, b)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcquireClaims(ctx, lease(0, b)); litclient.Code(err) != "claim_conflict" {
		t.Fatalf("acquire foreign claim: %v", err)
	}
	if _, err = s.RenewClaims(ctx, lease(0, b)); litclient.Code(err) != "claim_conflict" {
		t.Fatalf("renew foreign claim: %v", err)
	}
	if _, err = s.ReleaseClaims(ctx, on(b)); litclient.Code(err) != "claim_conflict" {
		t.Fatalf("release foreign claim: %v", err)
	}

	// Lease bounds are checked against service time before sending.
	operations = h.count("POST /v1/operations")
	until := time.Now().Add(2 * time.Hour)
	if _, err = s.AcquireClaims(ctx, litclient.ClaimRequest{ClaimSelection: on(c), Until: &until}); litclient.Code(err) != "invalid_arguments" {
		t.Fatalf("lease beyond one hour: %v", err)
	}
	soon := time.Now().Add(time.Minute)
	if _, err = s.AcquireClaims(ctx, litclient.ClaimRequest{ClaimSelection: on(c), For: time.Minute, Until: &soon}); litclient.Code(err) != "invalid_arguments" {
		t.Fatalf("for and until: %v", err)
	}
	if h.count("POST /v1/operations") != operations {
		t.Fatal("invalid leases were sent")
	}
	if v, err := s.AcquireClaims(ctx, litclient.ClaimRequest{ClaimSelection: on(c), Until: &soon}); err != nil || v.Items[0].Claim.ExpiresAt != soon.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("acquire until: %+v %v", v, err)
	}

	// All selects exactly the claims this client owns.
	if _, err = s.AcquireClaims(ctx, lease(0, a)); err != nil {
		t.Fatal(err)
	}
	owned, err := s.SnapshotClaims(ctx, litclient.ClaimSelection{All: true})
	if err != nil || len(owned.Items) != 2 {
		t.Fatalf("owned claims: %+v %v", owned, err)
	}
	if released, err := s.ReleaseClaims(ctx, litclient.ClaimSelection{All: true}); err != nil || len(released.Items) != 2 {
		t.Fatalf("release all: %+v %v", released, err)
	}
	if snap, err = other.SnapshotClaims(ctx, on(a, b, c)); err != nil || snap.Items[0].Claim != nil || snap.Items[1].Claim == nil || snap.Items[2].Claim != nil {
		t.Fatalf("after release all: %+v %v", snap, err)
	}
}
