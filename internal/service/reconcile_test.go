package service

import (
	"encoding/json"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"testing"
	"time"
)

func reconcileDurableTest(t *testing.T, s *Server, c protocol.Client, p protocol.Prepared) protocol.TransactionStatus {
	t.Helper()
	b, _ := json.Marshal(protocol.DurableRequest{SchemaVersion: 1, Intent: p.Intent, RequestHash: p.RequestHash})
	v, e := s.Reconcile(protocol.ReconcileRequest{ServiceID: s.DB.ServiceID(), ClientID: c.ClientID, Method: "POST", Path: "/v1/transactions", Body: b}, c)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestReconcileDurableHistoryAndNoop(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	p := prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: protocol.UUID(), Title: textPointer("feat/a")}, protocol.ProjectInput{ID: protocol.UUID(), Title: textPointer("feat/b")})
	if v := reconcileDurableTest(t, s, c, p); v.Status != "uncommitted" {
		t.Fatal(v)
	}
	executeProjectTest(t, s, c, p)
	if v := reconcileDurableTest(t, s, c, p); v.Status != "committed" || len(v.Results) != 2 {
		t.Fatal(v)
	}
	// The first explicit owner is unchanged. Its negative history lookup must not
	// hide a commitment recorded by another changed owner.
	p = prepareProjectTest(t, s, c, "update", protocol.ProjectInput{Target: "feat/a", Set: protocol.ProjectSet{Title: textPointer("feat/a")}}, protocol.ProjectInput{Target: "feat/b", Set: protocol.ProjectSet{Description: textPointer("changed")}})
	executeProjectTest(t, s, c, p)
	if v := reconcileDurableTest(t, s, c, p); v.Status != "committed" || len(v.Results) != 1 {
		t.Fatal(v)
	}
	// Later edits do not erase the history proof.
	executeProjectTest(t, s, c, prepareProjectTest(t, s, c, "update", protocol.ProjectInput{Target: "feat/b", Set: protocol.ProjectSet{Description: textPointer("later")}}))
	if v := reconcileDurableTest(t, s, c, p); v.Status != "committed" {
		t.Fatal(v)
	}
	p = prepareProjectTest(t, s, c, "update", protocol.ProjectInput{Target: "feat/a", Set: protocol.ProjectSet{Title: textPointer("feat/a")}})
	if v := reconcileDurableTest(t, s, c, p); v.Status != "already_satisfied" || v.Outcome != "already_satisfied" {
		t.Fatal(v)
	}
}
func TestReconcileClaimEvidence(t *testing.T) {
	s, c, id := claimFixture(t)
	now := time.Now().UTC()
	s.claimClock = func() time.Time { return now }
	op := protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id}}}
	check := func(want string) {
		t.Helper()
		b, _ := json.Marshal(op)
		v, e := s.Reconcile(protocol.ReconcileRequest{ServiceID: s.DB.ServiceID(), ClientID: c.ClientID, Method: "POST", Path: "/v1/operations", Body: b}, c)
		if e != nil || v.Status != want {
			t.Fatalf("%+v %v", v, e)
		}
	}
	check("uncertain") // No acquisition receipt/token: even absence is not history.
	out, e := s.ApplyClaims(op, c)
	if e != nil {
		t.Fatal(e)
	}
	check("uncertain")
	op.Operation = "claims.renew"
	op.Items[0].Token = out.Items[0].Claim.Token
	op.Items[0].ExtendTo = now.Add(50 * time.Minute).Format(time.RFC3339Nano)
	check("uncommitted")
	s.ApplyClaims(op, c)
	check("already_satisfied")
	now = now.Add(time.Hour)
	check("uncertain")
	op.Operation = "claims.release"
	op.Items[0].ExtendTo = ""
	check("uncertain")
	fresh, e := s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id}}}, c)
	if e != nil {
		t.Fatal(e)
	}
	check("uncertain") // Same owner, replacement token is never proof.
	op.Items[0].Token = fresh.Items[0].Claim.Token
	check("uncommitted")
	s.ApplyClaims(op, c)
	check("uncertain") // absence can mean release, expiry, or force.
}

func TestReconcileNoopDoesNotBypassOriginalOwnershipOrRevisions(t *testing.T) {
	s, c, id := claimFixture(t)
	plan := prepareRecordTest(t, s, c, "feat/claims", "issue.update", protocol.ProjectInput{Target: id, Set: protocol.ProjectSet{Title: textPointer("Claim me")}})
	other := c
	other.ClientID = protocol.UUID()
	s.SaveClient(other)
	if _, e := s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: other.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id}}}, other); e != nil {
		t.Fatal(e)
	}
	if v := reconcileDurableTest(t, s, c, plan); v.Status != "uncertain" {
		t.Fatal("no-op bypassed ownership", v)
	}
	claim, _ := s.LiveClaim(id)
	s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.release", OwnerClientID: other.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id, Token: claim.Token}}}, other)
	// Mutating the description leaves the no-op title satisfied but changes its
	// required project revision; matching fields must not override the old guard.
	project, _ := s.ResolveProject("feat/claims")
	executeProjectTest(t, s, c, prepareProjectTest(t, s, c, "update", protocol.ProjectInput{Target: project.ID, Set: protocol.ProjectSet{Description: textPointer("later")}}))
	if v := reconcileDurableTest(t, s, c, plan); v.Status != "uncertain" {
		t.Fatal("no-op bypassed stale guard", v)
	}
}
func TestReconcileRejectsFutureRetainedVersionEvenWithHistory(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	plan := prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: protocol.UUID(), Title: textPointer("feat/version")})
	executeProjectTest(t, s, c, plan)
	b, _ := json.Marshal(protocol.DurableRequest{SchemaVersion: 2, Intent: plan.Intent, RequestHash: plan.RequestHash})
	if _, e := s.Reconcile(protocol.ReconcileRequest{ServiceID: s.DB.ServiceID(), ClientID: c.ClientID, Method: "POST", Path: "/v1/transactions", Body: b}, c); e == nil {
		t.Fatal("future retained version accepted")
	}
}
