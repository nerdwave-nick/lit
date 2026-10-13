package service

import (
	"github.com/nerdwave-nick/lit/internal/protocol"
	"testing"
	"time"
)

func TestClaimFiltersProjectionAndBothLinkEndpoints(t *testing.T) {
	s, c, id := claimFixture(t)
	issue, _ := s.Issue(id)
	otherID := protocol.UUID()
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, issue.ProjectID, "issue.create", protocol.ProjectInput{ID: otherID, Title: textPointer("Other")}))
	rival := c
	rival.ClientID = protocol.UUID()
	if e := s.SaveClient(rival); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	s.claimClock = func() time.Time { return now }
	out, e := s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id}}}, c)
	if e != nil {
		t.Fatal(e)
	}
	yes, no := true, false
	for _, q := range []ProjectQuery{{Type: "issues", AllProjects: true, Claimed: &yes}, {Type: "issues", AllProjects: true, OwnerClientID: c.ClientID}} {
		page, e := recordPageOf(s, q)
		if e != nil || len(page.Items) != 1 {
			t.Fatal(page, e)
		}
	}
	page, e := recordPageOf(s, ProjectQuery{Type: "issues", AllProjects: true, Claimed: &no})
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	code, v := directRequest(t, s, c, "GET", "/v1/issues/"+id, nil)
	if code != 200 || v["data"].(map[string]any)["claim"] == nil {
		t.Fatal(code, v)
	}
	req := protocol.PrepareRequest{Operation: "issue.link", Items: []protocol.ProjectInput{{From: otherID, To: []string{id}, Relation: "blocks"}}}
	if _, e = s.PrepareRecords(req, rival); e == nil {
		t.Fatal("claimed remote endpoint not protected")
	}
	plan, e := s.PrepareRecords(req, c)
	if e != nil {
		t.Fatal(e)
	}
	if len(plan.RequiredClaims) != 2 {
		t.Fatal(plan.RequiredClaims)
	}
	executeRecordTest(t, s, c, plan)
	// Even an already-satisfied link must reject stale supplied ownership.
	plan, e = s.PrepareRecords(req, c)
	if e != nil {
		t.Fatal(e)
	}
	if e = dryRun(s, plan.Intent, plan.RequestHash, c, protocol.ClaimToken{IssueID: id, Token: protocol.UUID()}); e == nil {
		t.Fatal("stale no-op link token accepted")
	}
	// Blocked issues remain maintenance-claimable, regardless discovery frontier.
	_, e = s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: rival.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id, Force: true}}}, rival)
	if e != nil {
		t.Fatal(e)
	}
	req.Operation = "issue.unlink"
	if _, e = s.PrepareRecords(req, c); e == nil {
		t.Fatal("competing unlink accepted")
	}
	req.Force = true
	plan, e = s.PrepareRecords(req, c)
	if e != nil {
		t.Fatal(e)
	}
	executeRecordTest(t, s, c, plan)
	if claim, _ := s.LiveClaim(id); claim != nil {
		t.Fatal("forced unlink retained endpoint claim")
	}
	_, e = s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id}}}, c)
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(time.Hour)
	page, e = recordPageOf(s, ProjectQuery{Type: "issues", AllProjects: true, Claimed: &yes})
	if e != nil || len(page.Items) != 0 {
		t.Fatal(page, e)
	}
	code, v = directRequest(t, s, c, "GET", "/v1/issues/"+id, nil)
	if code != 200 || v["data"].(map[string]any)["claim"] != nil {
		t.Fatal(code, v)
	}
	_ = out
}
func TestClaimListingFiltersPagesAndCursorBinding(t *testing.T) {
	s, c, id := claimFixture(t)
	issue, _ := s.Issue(id)
	second := protocol.UUID()
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, issue.ProjectID, "issue.create", protocol.ProjectInput{ID: second, Title: textPointer("Second")}))
	_, e := s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id}, {IssueID: second}}}, c)
	if e != nil {
		t.Fatal(e)
	}
	base := "/v1/claims?owner_client_id=" + c.ClientID + "&limit=1"
	code, v := directRequest(t, s, c, "GET", base, nil)
	if code != 200 || len(v["items"].([]any)) != 1 || v["next_cursor"] == nil {
		t.Fatal(code, v)
	}
	first := v["items"].([]any)[0].(map[string]any)["issue_id"]
	cursor := v["next_cursor"].(string)
	code, v = directRequest(t, s, c, "GET", base+"&cursor="+cursor, nil)
	if code != 200 || len(v["items"].([]any)) != 1 || v["next_cursor"] != nil || v["items"].([]any)[0].(map[string]any)["issue_id"] == first {
		t.Fatal(code, v)
	}
	code, _ = directRequest(t, s, c, "GET", base+"&issue_id="+id+"&cursor="+cursor, nil)
	if code != 400 {
		t.Fatal("cursor rebound", code)
	}
	code, v = directRequest(t, s, c, "GET", "/v1/claims?issue_id="+id, nil)
	if code != 200 || len(v["items"].([]any)) != 1 {
		t.Fatal(code, v)
	}
	code, v = directRequest(t, s, c, "GET", "/v1/claims?all=true", nil)
	if code != 200 || len(v["items"].([]any)) != 2 {
		t.Fatal(code, v)
	}
	for _, query := range []string{"limit=0", "limit=1001", "owner_client_id=bad", "issue_id=bad", "sort=title", "limit=1&limit=2", "all=true&limit=1", "archived=true"} {
		code, _ = directRequest(t, s, c, "GET", "/v1/claims?"+query, nil)
		if code < 400 || code >= 500 {
			t.Fatal(query, code)
		}
	}
}
func TestClaimExpiryIsOneInstantPerSnapshot(t *testing.T) {
	for _, kind := range []string{"filter", "targets", "owned"} {
		t.Run(kind, func(t *testing.T) {
			s, c, id := claimFixture(t)
			now := time.Now().UTC()
			s.claimClock = func() time.Time { return now }
			_, e := s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id, ExtendTo: now.Add(time.Second).Format(time.RFC3339Nano)}}}, c)
			if e != nil {
				t.Fatal(e)
			}
			reads := 0
			before := now.Add(time.Second - time.Nanosecond)
			s.claimClock = func() time.Time {
				reads++
				if reads == 1 {
					return before
				}
				return now.Add(time.Second + time.Nanosecond)
			}
			method, path := "GET", "/v1/issues?all_projects=true&claimed=true"
			var body any
			if kind == "targets" {
				method, path = "POST", "/v1/snapshots"
				body = map[string]any{"targets": []any{map[string]any{"type": "issues", "selector": id}}}
			}
			if kind == "owned" {
				method, path = "POST", "/v1/claim-snapshots"
				body = map[string]any{"owner_client_id": c.ClientID}
			}
			code, v := directRequest(t, s, c, method, path, body)
			if code != 200 {
				t.Fatal(code, v)
			}
			var items []any
			if kind == "owned" {
				items = v["data"].(map[string]any)["items"].([]any)
			} else {
				items = v["items"].([]any)
			}
			if len(items) != 1 || items[0].(map[string]any)["claim"] == nil {
				t.Fatal("snapshot mixed expiry instants", v)
			}
			if reads != 1 || v["server_time"] != before.Format(time.RFC3339Nano) {
				t.Fatal("snapshot clock differs", reads, v)
			}
			code, v = directRequest(t, s, c, "GET", "/v1/issues?all_projects=true&claimed=true", nil)
			if code != 200 || len(v["items"].([]any)) != 0 {
				t.Fatal("later request failed to observe expiry", code, v)
			}
		})
	}
}
