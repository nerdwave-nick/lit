package service

import (
	"github.com/nerdwave-nick/lit/internal/protocol"
	"testing"
	"time"
)

func claimFixture(t *testing.T) (*Server, protocol.Client, string) {
	t.Helper()
	s, c := projectTestServer(t, t.TempDir())
	t.Cleanup(func() { s.DB.Close() })
	pid, id := protocol.UUID(), protocol.UUID()
	executeProjectTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: pid, Title: textPointer("feat/claims")}))
	p, e := s.PrepareRecords(protocol.PrepareRequest{Operation: "issue.create", Project: pid, Items: []protocol.ProjectInput{{ID: id, Title: textPointer("Claim me")}}}, c)
	if e != nil {
		t.Fatal(e)
	}
	executeProjectTest(t, s, c, p)
	return s, c, id
}
func TestClaimLifecycle(t *testing.T) {
	s, c, id := claimFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	s.claimClock = func() time.Time { return now }
	op := protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id}}}
	result, e := s.ApplyClaims(op, c)
	if e != nil {
		t.Fatal(e)
	}
	first := result.Items[0].Claim
	if first == nil || first.ExpiresAt != now.Add(30*time.Minute).Format(time.RFC3339Nano) {
		t.Fatal(first)
	}
	if _, e = s.ApplyClaims(op, c); e == nil {
		t.Fatal("duplicate acquire accepted")
	}
	op.Operation = "claims.renew"
	op.Items[0].Token = first.Token
	op.Items[0].ExtendTo = now.Add(50 * time.Minute).Format(time.RFC3339Nano)
	result, e = s.ApplyClaims(op, c)
	if e != nil {
		t.Fatal(e)
	}
	if result.Items[0].Claim.Token != first.Token {
		t.Fatal("renew changed token")
	}
	op.Items[0].ExtendTo = now.Add(10 * time.Minute).Format(time.RFC3339Nano)
	result, e = s.ApplyClaims(op, c)
	if e != nil || result.Outcome != "already_satisfied" {
		t.Fatal(result, e)
	}
	now = now.Add(time.Hour)
	if v, e := s.LiveClaim(id); e != nil || v != nil {
		t.Fatal(v, e)
	}
	if _, e = s.ApplyClaims(op, c); e == nil {
		t.Fatal("expired renewal revived")
	}
	op.Operation = "claims.acquire"
	op.Items[0] = protocol.ClaimItem{IssueID: id}
	result, e = s.ApplyClaims(op, c)
	if e != nil {
		t.Fatal(e)
	}
	if result.Items[0].Claim.Token == first.Token {
		t.Fatal("token reused")
	}
	op.Operation = "claims.release"
	op.Items[0] = protocol.ClaimItem{IssueID: id, Token: first.Token}
	if _, e = s.ApplyClaims(op, c); e == nil {
		t.Fatal("old token released replacement")
	}
}

func TestClaimAbsoluteRenewalAndSetFences(t *testing.T) {
	s, c, id := claimFixture(t)
	now := time.Now().UTC()
	s.claimClock = func() time.Time { return now }
	acquired, e := s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id}}}, c)
	if e != nil {
		t.Fatal(e)
	}
	claim := acquired.Items[0].Claim
	renew := protocol.ClaimOperation{Operation: "claims.renew", OwnerClientID: c.ClientID, Selection: "all_owned", Items: []protocol.ClaimItem{{IssueID: id, Token: claim.Token, ExtendTo: now.Add(-time.Minute).Format(time.RFC3339Nano)}}}
	out, e := s.ApplyClaims(renew, c)
	if e != nil || out.Outcome != "already_satisfied" {
		t.Fatal(out, e)
	}
	renew.Items[0].ExtendTo = now.Add(3 * time.Hour).Format(time.RFC3339Nano)
	if _, e = s.ApplyClaims(renew, c); e == nil {
		t.Fatal("overlong target accepted")
	}
	renew.Items[0].ExtendTo = now.Add(50 * time.Minute).Format(time.RFC3339Nano)
	out, e = s.ApplyClaims(renew, c)
	if e != nil {
		t.Fatal(e)
	}
	expiry := out.Items[0].Claim.ExpiresAt
	now = now.Add(10 * time.Minute)
	out, e = s.ApplyClaims(renew, c)
	if e != nil || out.Items[0].Claim.ExpiresAt != expiry {
		t.Fatal(out, e)
	}
	renew.Items = nil
	if _, e = s.ApplyClaims(renew, c); e == nil {
		t.Fatal("changed owner set accepted")
	}
	renew.Items = []protocol.ClaimItem{{IssueID: id, Token: protocol.UUID(), ExtendTo: now.Add(30 * time.Minute).Format(time.RFC3339Nano)}}
	if _, e = s.ApplyClaims(renew, c); e == nil {
		t.Fatal("replacement token accepted")
	}
	current, _ := s.LiveClaim(id)
	if current.ExpiresAt != expiry {
		t.Fatal("rejected batch changed expiry")
	}
}
func TestClaimsProtectMutationsAndExemptAppends(t *testing.T) {
	s, c, id := claimFixture(t)
	other := c
	other.ClientID = protocol.UUID()
	if e := s.SaveClient(other); e != nil {
		t.Fatal(e)
	}
	oldIssue, _ := s.Issue(id)
	beforeProject, _ := s.Project(oldIssue.ProjectID)
	out, e := s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id}}}, c)
	if e != nil {
		t.Fatal(e)
	}
	claim := out.Items[0].Claim
	// Competing write fails even during preparation, while comment append succeeds.
	req := protocol.PrepareRequest{Operation: "issue.update", Items: []protocol.ProjectInput{{Target: id, Set: protocol.ProjectSet{Body: textPointer("change")}}}}
	if _, e = s.PrepareRecords(req, other); e == nil {
		t.Fatal("competitor prepared write")
	}
	note := protocol.UUID()
	plan := prepareRecordTest(t, s, other, oldIssue.ProjectID, "comment.create", protocol.ProjectInput{ID: note, Issue: id, Content: textPointer("Question")})
	executeRecordTest(t, s, other, plan)
	if _, e = s.PrepareRecords(protocol.PrepareRequest{Operation: "comment.update", Items: []protocol.ProjectInput{{Target: note, Set: protocol.ProjectSet{Body: textPointer("replace")}}}}, other); e == nil {
		t.Fatal("competitor edited comment")
	}
	// Existing parent is an indirect changed owner on child creation.
	if _, e = s.PrepareRecords(protocol.PrepareRequest{Operation: "issue.create", Project: oldIssue.ProjectID, Items: []protocol.ProjectInput{{ID: protocol.UUID(), Title: textPointer("Child"), Parent: &id}}}, other); e == nil {
		t.Fatal("claimed parent mutated")
	}
	plan, e = s.PrepareRecords(req, c)
	if e != nil {
		t.Fatal(e)
	}
	if len(plan.RequiredClaims) != 1 || plan.RequiredClaims[0].Claim.Token != claim.Token {
		t.Fatal(plan.RequiredClaims)
	}
	executeRecordTest(t, s, c, plan)
	current, _ := s.LiveClaim(id)
	if !same(current, claim) {
		t.Fatal("ordinary write renewed claim")
	}
	projectAfter, _ := s.Project(oldIssue.ProjectID)
	if projectAfter.Revision != beforeProject.Revision {
		t.Fatal("claim changed project revision")
	}
	// Tokens are outside canonical hash but stale supplied tokens always fence.
	plan, e = s.PrepareRecords(req, c)
	if e != nil {
		t.Fatal(e)
	}
	e = dryRun(s, plan.Intent, plan.RequestHash, c, protocol.ClaimToken{IssueID: id, Token: protocol.UUID()})
	if e == nil {
		t.Fatal("stale token accepted on no-op")
	}
	req.Force = true
	plan, e = s.PrepareRecords(req, other)
	if e != nil {
		t.Fatal(e)
	}
	executeRecordTest(t, s, other, plan)
	if live, _ := s.LiveClaim(id); live != nil {
		t.Fatal("force failed to revoke")
	}
	// No explicit claim is left behind for an ordinary unclaimed transaction.
	req.Force = false
	req.Items[0].Set.Body = textPointer("another")
	plan, e = s.PrepareRecords(req, other)
	if e != nil {
		t.Fatal(e)
	}
	executeRecordTest(t, s, other, plan)
	if live, _ := s.LiveClaim(id); live != nil {
		t.Fatal("temporary reservation leaked")
	}
}

func TestClaimClosedMaintenanceCloseAndAssignment(t *testing.T) {
	s, c, id := claimFixture(t)
	issue, _ := s.Issue(id)
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, issue.ProjectID, "issue.update", protocol.ProjectInput{Target: id, Set: protocol.ProjectSet{State: textPointer("closed"), Assignee: nullable(textPointer("Responsible"))}}))
	before, _ := s.Issue(id)
	_, e := s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id}}}, c)
	if e != nil {
		t.Fatal(e)
	}
	after, _ := s.Issue(id)
	if !same(before, after) {
		t.Fatal("claim changed durable assignment/revision")
	}
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, issue.ProjectID, "issue.close", protocol.ProjectInput{Target: id}))
	if claim, e := s.LiveClaim(id); e != nil || claim != nil {
		t.Fatal("closing maintenance issue retained ownership", claim, e)
	}
	after, _ = s.Issue(id)
	if !same(before, after) {
		t.Fatal("no-op close changed durable revision")
	}
}
func TestClaimBatchFailureNeverPartiallyChangesOwnership(t *testing.T) {
	s, c, id := claimFixture(t)
	issue, _ := s.Issue(id)
	second := protocol.UUID()
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, issue.ProjectID, "issue.create", protocol.ProjectInput{ID: second, Title: textPointer("Second")}))
	rival := c
	rival.ClientID = protocol.UUID()
	if e := s.SaveClient(rival); e != nil {
		t.Fatal(e)
	}
	_, e := s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: rival.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: second}}}, rival)
	if e != nil {
		t.Fatal(e)
	}
	acquire := protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id}, {IssueID: second}}}
	if _, e = s.ApplyClaims(acquire, c); e == nil {
		t.Fatal("competing acquire accepted")
	}
	if claim, _ := s.LiveClaim(id); claim != nil {
		t.Fatal("partial acquisition", claim)
	}
	acquire.Items = acquire.Items[:1]
	out, e := s.ApplyClaims(acquire, c)
	if e != nil {
		t.Fatal(e)
	}
	first := out.Items[0].Claim
	for _, verb := range []string{"renew", "release"} {
		op := protocol.ClaimOperation{Operation: "claims." + verb, OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id, Token: first.Token}, {IssueID: second, Token: protocol.UUID()}}}
		if verb == "renew" {
			for i := range op.Items {
				op.Items[i].ExtendTo = time.Now().UTC().Add(50 * time.Minute).Format(time.RFC3339Nano)
			}
		}
		if _, e = s.ApplyClaims(op, c); e == nil {
			t.Fatal("competing batch accepted")
		}
		got, _ := s.LiveClaim(id)
		if !same(got, first) {
			t.Fatal("partial", verb, got)
		}
	}
}
