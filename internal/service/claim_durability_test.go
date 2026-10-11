package service

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

func disconnectClaimTest(t *testing.T, s *Server, c protocol.Client) int {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/disconnect", bytes.NewBufferString("{}"))
	r.Header.Set("X-Lit-Client-ID", c.ClientID)
	r.Header.Set("If-Match", fmt.Sprintf("\"client:%d\"", c.StateRevision))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w.Code
}

// storedClaim reads the stored claim on an issue, live or expired.
func storedClaim(t *testing.T, s *Server, id string) *protocol.Claim {
	t.Helper()
	c, e := inSnapshot(s, func(rs *Server) (*protocol.Claim, error) { return rs.q().Claim(rs.ctx(), id) })
	if e != nil {
		t.Fatal(e)
	}
	return c
}

func TestClaimRestartKeepsExpiry(t *testing.T) {
	s, c, id := claimFixture(t)
	out, e := s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id}}}, c)
	if e != nil {
		t.Fatal(e)
	}
	saved := out.Items[0].Claim
	root, config := s.DB.Dir(), s.Config
	s.DB.Close()
	reopened := openTestServer(t, root, config)
	got, e := reopened.LiveClaim(id)
	if e != nil || !same(got, saved) {
		t.Fatal(got, e)
	}
}

// Disconnect clears the client's live and expired claims together with its
// status: a failure after those writes changes nothing, also after a restart.
func TestClaimDisconnectAtomicAcrossLiveAndExpired(t *testing.T) {
	s, c, id := claimFixture(t)
	issue, _ := s.Issue(id)
	second, third := protocol.UUID(), protocol.UUID()
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, issue.ProjectID, "issue.create", protocol.ProjectInput{ID: second, Title: textPointer("Second")}, protocol.ProjectInput{ID: third, Title: textPointer("Other owner")}))
	other := c
	other.ClientID = protocol.UUID()
	if e := s.SaveClient(other); e != nil {
		t.Fatal(e)
	}
	held, e := s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: other.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: third}}}, other)
	if e != nil {
		t.Fatal(e)
	}
	otherClaim := held.Items[0].Claim
	// One owner has a live and an expired claim; another owns separate state.
	now := time.Now().UTC()
	s.claimClock = func() time.Time { return now }
	if _, e = s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id, ExtendTo: now.Add(time.Minute).Format(time.RFC3339Nano)}, {IssueID: second}}}, c); e != nil {
		t.Fatal(e)
	}
	now = now.Add(2 * time.Minute)
	expired, live := storedClaim(t, s, id), storedClaim(t, s, second)
	if expired == nil || live == nil {
		t.Fatal("claims missing before disconnect")
	}
	s.Config.Limits.SnapshotBytes = 64
	if status := disconnectClaimTest(t, s, c); status != 413 {
		t.Fatal(status)
	}
	root, config := s.DB.Dir(), s.Config
	config.Limits = protocol.DefaultLimits()
	s.DB.Close()
	s = openTestServer(t, root, config)
	s.claimClock = func() time.Time { return now }
	check := func(committed bool) {
		t.Helper()
		got, e := s.Client(c.ClientID)
		if e != nil || (got.Status == "disconnected") != committed {
			t.Fatal(got, e)
		}
		if committed && (storedClaim(t, s, id) != nil || storedClaim(t, s, second) != nil) {
			t.Fatal("disconnect kept claims")
		}
		if !committed && (!same(storedClaim(t, s, id), expired) || !same(storedClaim(t, s, second), live)) {
			t.Fatal("failed disconnect released claims")
		}
		if retained, e := s.LiveClaim(third); e != nil || !same(retained, otherClaim) {
			t.Fatal("other ownership changed", retained, e)
		}
		if untouched, e := s.Client(other.ClientID); e != nil || untouched.Status != "connected" || untouched.StateRevision != other.StateRevision {
			t.Fatal(untouched, e)
		}
	}
	check(false)
	if status := disconnectClaimTest(t, s, c); status != 200 {
		t.Fatal(status)
	}
	check(true)
}

// Closing an issue releases its claim in the same transaction.
func TestClaimCloseReleaseAtomic(t *testing.T) {
	s, c, id := claimFixture(t)
	if _, e := s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id}}}, c); e != nil {
		t.Fatal(e)
	}
	issue, _ := s.Issue(id)
	plan := prepareRecordTest(t, s, c, issue.ProjectID, "issue.close", protocol.ProjectInput{Target: id})
	s.Config.Limits.SnapshotBytes = 64
	if status, _ := executeProjectTest(t, s, c, plan); status != 413 {
		t.Fatal(status)
	}
	root, config := s.DB.Dir(), s.Config
	config.Limits = protocol.DefaultLimits()
	s.DB.Close()
	s = openTestServer(t, root, config)
	check := func(committed bool) {
		t.Helper()
		got, e := s.Issue(id)
		if e != nil {
			t.Fatal(e)
		}
		claim, e := s.LiveClaim(id)
		if e != nil {
			t.Fatal(e)
		}
		if (got.State == "closed") != committed || (claim == nil) != committed {
			t.Fatal(got, claim)
		}
	}
	check(false)
	executeRecordTest(t, s, c, plan)
	check(true)
}
