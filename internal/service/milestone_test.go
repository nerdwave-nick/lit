package service

import (
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

func TestMilestoneCreatePrepareAndMembershipProjection(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	project, issue, milestone := protocol.UUID(), protocol.UUID(), protocol.UUID()
	executeRecordTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: project, Title: textPointer("feat/milestone")}))
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, project, "issue.create", protocol.ProjectInput{ID: issue, Title: textPointer("Member")}))
	p, err := s.PrepareRecords(protocol.PrepareRequest{Operation: "milestone.create", Project: project, Items: []protocol.ProjectInput{{ID: milestone, Title: textPointer("M1"), Content: textPointer("Objective"), IssueIDs: []string{issue}}}}, c)
	if err != nil {
		t.Fatal("prepare milestone create:", err)
	}
	code, response := directRequest(t, s, c, "POST", "/v1/milestones", protocol.DurableRequest{Intent: p.Intent, RequestHash: p.RequestHash})
	if code != 201 || response["data"] == nil {
		t.Fatalf("POST milestone: %d %#v", code, response)
	}
	got, err := s.Milestone(milestone)
	if err != nil || got.ProjectID != project || got.IssueIDs == nil || len(got.IssueIDs) != 1 || got.IssueIDs[0] != issue {
		t.Fatalf("milestone record = %#v, %v", got, err)
	}
	member, err := s.Issue(issue)
	if err != nil || member.Revision != 1 {
		t.Fatalf("membership changed issue revision: %#v %v", member, err)
	}
	code, result := directRequest(t, s, c, "GET", "/v1/milestones/"+milestone, nil)
	if code != 200 {
		t.Fatalf("GET milestone: %d %#v", code, result)
	}
	data := result["data"].(map[string]any)
	progress := data["progress"].(map[string]any)
	if progress["total"] != float64(1) || progress["open"] != float64(1) || progress["closed"] != float64(0) {
		t.Fatalf("wrong projection: %#v", progress)
	}
	listCode, listed := directRequest(t, s, c, "GET", "/v1/milestones?project_id="+project+"&title=M1", nil)
	if listCode != 200 || len(listed["items"].([]any)) != 1 {
		t.Fatalf("milestone list route: %d %#v", listCode, listed)
	}
	historyCode, historyPage := directRequest(t, s, c, "GET", "/v1/milestones/"+milestone+"/history", nil)
	if historyCode != 200 || len(historyPage["items"].([]any)) != 1 {
		t.Fatalf("milestone history route: %d %#v", historyCode, historyPage)
	}
	code, result = directRequest(t, s, c, "GET", "/v1/issues?project_id="+project+"&milestone_id=M1", nil)
	if code != 200 || len(result["items"].([]any)) != 1 {
		t.Fatalf("milestone issue query: %d %#v", code, result)
	}
	queryPage, result := directRequest(t, s, c, "POST", "/v1/snapshots", ProjectSnapshot{Query: &ProjectQuery{Type: "milestones", ProjectID: project}})
	if queryPage != 200 || len(result["items"].([]any)) != 1 {
		t.Fatalf("milestone snapshot query: %d %#v", queryPage, result)
	}
	badScope, _ := directRequest(t, s, c, "POST", "/v1/snapshots", ProjectSnapshot{Query: &ProjectQuery{Type: "issues", AllProjects: true, MilestoneID: milestone}})
	if badScope < 400 {
		t.Fatal("all_projects milestone query accepted")
	}
	targetPage, result := directRequest(t, s, c, "POST", "/v1/snapshots", ProjectSnapshot{Targets: []SnapshotTarget{{Type: "milestones", Selector: "title:M1", Project: project}}})
	if targetPage != 200 || len(result["items"].([]any)) != 1 {
		t.Fatalf("milestone snapshot target: %d %#v", targetPage, result)
	}
	searchCode, search := directRequest(t, s, c, "POST", "/v1/search", protocol.SearchRequest{Scope: protocol.SearchScope{ProjectID: project}, Query: "Objective", ContextLines: 0})
	foundMilestone := false
	if values, ok := search["items"].([]any); ok {
		for _, item := range values {
			if item.(map[string]any)["type"] == "milestones" {
				foundMilestone = true
			}
		}
	}
	if searchCode != 200 || !foundMilestone {
		t.Fatalf("milestone body missing from search: %d %#v", searchCode, search)
	}
	if _, err = s.ResolveMilestone("m1", project); err != nil {
		t.Fatal("milestone title lookup must case-fold", err)
	}
	if _, err = s.ResolveMilestone("M1", ""); err == nil {
		t.Fatal("resolved title without project scope")
	}
	if _, err = s.ResolveMilestone(milestone, ""); err != nil {
		t.Fatal("UUID lookup should not need project scope", err)
	}
	if got, err := s.ResolveMilestone(milestone[:8], ""); err != nil || got.ID != milestone {
		t.Fatalf("unscoped UUID prefix lookup failed: %#v %v", got, err)
	}
	if got, err := s.ResolveMilestone("id:"+milestone[:8], ""); err != nil || got.ID != milestone {
		t.Fatalf("typed UUID prefix lookup failed: %#v %v", got, err)
	}
	unicodeID := protocol.UUID()
	executeRecordTest(t, s, c, prepareMilestoneTest(t, s, c, project, protocol.ProjectInput{ID: unicodeID, Title: textPointer("Straße")}))
	if got, err := s.ResolveMilestone("STRASSE", project); err != nil || got.ID != unicodeID {
		t.Fatalf("full Unicode title fold failed: %#v %v", got, err)
	}
	if _, err := s.PrepareRecords(protocol.PrepareRequest{Operation: "milestone.create", Project: project, Force: true, Items: []protocol.ProjectInput{{ID: protocol.UUID(), Title: textPointer("Forced")}}}, c); err == nil {
		t.Fatal("accepted milestone force")
	}
	patchPlan, err := s.PrepareRecords(protocol.PrepareRequest{Operation: "milestone.update", Project: project, Items: []protocol.ProjectInput{{Target: milestone, Set: protocol.ProjectSet{Title: textPointer("Renamed")}}}}, c)
	if err != nil {
		t.Fatal(err)
	}
	code, response = directRequest(t, s, c, "PATCH", "/v1/milestones/"+milestone, protocol.DurableRequest{Intent: patchPlan.Intent, RequestHash: patchPlan.RequestHash})
	if code != 200 || response["data"] == nil {
		t.Fatalf("PATCH milestone: %d %#v", code, response)
	}
	history, err := s.RecordHistory("milestones", milestone)
	if err != nil || len(history) != 2 {
		t.Fatalf("milestone history after update: %d %v", len(history), err)
	}
}

func TestMilestoneMembershipGuardsAndProjectValidation(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	p1, p2 := protocol.UUID(), protocol.UUID()
	executeRecordTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: p1, Title: textPointer("feat/one")}, protocol.ProjectInput{ID: p2, Title: textPointer("feat/two")}))
	i1, i2 := protocol.UUID(), protocol.UUID()
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, p1, "issue.create", protocol.ProjectInput{ID: i1, Title: textPointer("One")}))
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, p2, "issue.create", protocol.ProjectInput{ID: i2, Title: textPointer("Two")}))
	if _, err := s.PrepareRecords(protocol.PrepareRequest{Operation: "milestone.create", Project: p1, Items: []protocol.ProjectInput{{ID: protocol.UUID(), Title: textPointer("Bad"), IssueIDs: []string{i2}}}}, c); err == nil {
		t.Fatal("accepted foreign-project member")
	}
	i3 := protocol.UUID()
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, p1, "issue.create", protocol.ProjectInput{ID: i3, Title: textPointer("Three")}))
	m := protocol.UUID()
	other := c
	other.ClientID = protocol.UUID()
	if err := s.SaveClient(other); err != nil {
		t.Fatal(err)
	}
	held, err := s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: other.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: i1}}}, other)
	if err != nil {
		t.Fatal(err)
	}
	created := prepareMilestoneTest(t, s, c, p1, protocol.ProjectInput{ID: m, Title: textPointer("One"), IssueIDs: []string{i1}})
	executeRecordTest(t, s, c, created)
	stillHeld, err := s.LiveClaim(i1)
	if err != nil || stillHeld == nil || stillHeld.OwnerClientID != other.ClientID {
		t.Fatalf("milestone creation transferred member claim: %#v %v", stillHeld, err)
	}
	if _, err = s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.release", OwnerClientID: other.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: i1, Token: held.Items[0].Claim.Token}}}, other); err != nil {
		t.Fatal("release test claim", err)
	}
	update, err := s.PrepareRecords(protocol.PrepareRequest{Operation: "milestone.update", Project: p1, Items: []protocol.ProjectInput{{Target: m, Add: protocol.ProjectMembers{IssueIDs: []string{i3}}}}}, c)
	if err != nil {
		t.Fatal(err)
	}
	// The exact existing and incoming member revisions fence the prepared change.
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, p1, "issue.update", protocol.ProjectInput{Target: i1, Set: protocol.ProjectSet{Body: textPointer("changed")}}))
	code, _ := executeProjectTest(t, s, c, update)
	if code != 409 {
		t.Fatalf("stale member guard accepted: status %d", code)
	}
	got, _ := s.Milestone(m)
	if len(got.IssueIDs) != 1 || got.IssueIDs[0] != i1 {
		t.Fatalf("stale transaction partially applied: %#v", got)
	}
	if _, err = s.PrepareRecords(protocol.PrepareRequest{Operation: "milestone.create", Project: p1, Items: []protocol.ProjectInput{{ID: protocol.UUID(), Title: textPointer("oNE")}}}, c); err == nil {
		t.Fatal("accepted case-folded duplicate milestone title")
	}
	updated, err := s.PrepareRecords(protocol.PrepareRequest{Operation: "milestone.update", Project: p1, Items: []protocol.ProjectInput{{Target: m, Add: protocol.ProjectMembers{IssueIDs: []string{i3}}}}}, c)
	if err != nil {
		t.Fatal("prepare member add", err)
	}
	executeRecordTest(t, s, c, updated)
	updated, err = s.PrepareRecords(protocol.PrepareRequest{Operation: "milestone.update", Project: p1, Items: []protocol.ProjectInput{{Target: m, Set: protocol.ProjectSet{IssueIDs: &[]string{i1}}}}}, c)
	if err != nil {
		t.Fatal("prepare member replacement", err)
	}
	executeRecordTest(t, s, c, updated)
	updated, err = s.PrepareRecords(protocol.PrepareRequest{Operation: "milestone.update", Project: p1, Items: []protocol.ProjectInput{{Target: m, Clear: []string{"issues"}}}}, c)
	if err != nil {
		t.Fatal("prepare member clear", err)
	}
	executeRecordTest(t, s, c, updated)
	got, _ = s.Milestone(m)
	if got.IssueIDs == nil || len(got.IssueIDs) != 0 {
		t.Fatalf("clear issues failed: %#v", got.IssueIDs)
	}
}

func TestMilestoneSelectorKeepsUnscopedHexAsIDOnly(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	project := protocol.UUID()
	executeRecordTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: project, Title: textPointer("feat/selectors")}))
	const prefixID = "abcdef12-0000-4000-8000-000000000001"
	const hexTitleID = "ffffffff-0000-4000-8000-000000000002"
	executeRecordTest(t, s, c, prepareMilestoneTest(t, s, c, project, protocol.ProjectInput{ID: prefixID, Title: textPointer("Prefix record")}))
	executeRecordTest(t, s, c, prepareMilestoneTest(t, s, c, project, protocol.ProjectInput{ID: hexTitleID, Title: textPointer("abcdef12")}))
	if _, err := s.ResolveMilestone("deadbeef", ""); err == nil {
		t.Fatal("unscoped hex-looking selector resolved as a title")
	}
	got, err := s.ResolveMilestone("abcdef12", "")
	if err != nil || got.ID != prefixID {
		t.Fatalf("hex selector should resolve only as UUID prefix: %#v %v", got, err)
	}
	got, err = s.ResolveMilestone("title:abcdef12", project)
	if err != nil || got.ID != hexTitleID {
		t.Fatalf("typed title with project failed: %#v %v", got, err)
	}
}

func TestMilestoneProjectionBlockersClaimsHistoryAndRestart(t *testing.T) {
	root := t.TempDir()
	s, c := projectTestServer(t, root)
	p1, p2 := protocol.UUID(), protocol.UUID()
	executeRecordTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: p1, Title: textPointer("feat/members")}, protocol.ProjectInput{ID: p2, Title: textPointer("feat/external")}))
	openID, closedID, blockerID := protocol.UUID(), protocol.UUID(), protocol.UUID()
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, p1, "issue.create", protocol.ProjectInput{ID: openID, Title: textPointer("Open")}, protocol.ProjectInput{ID: closedID, Title: textPointer("Closed")}))
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, p2, "issue.create", protocol.ProjectInput{ID: blockerID, Title: textPointer("External blocker")}))
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, p2, "issue.link", protocol.ProjectInput{From: blockerID, To: []string{openID}, Relation: "blocks"}))
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, p1, "issue.close", protocol.ProjectInput{Target: closedID}))
	mID := protocol.UUID()
	created := prepareMilestoneTest(t, s, c, p1, protocol.ProjectInput{ID: mID, Title: textPointer("Workset"), IssueIDs: []string{openID, closedID}})
	executeRecordTest(t, s, c, created)
	held, err := s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: openID}}}, c)
	if err != nil || held.Items[0].Claim == nil {
		t.Fatal("claim member", err)
	}
	code, response := directRequest(t, s, c, "GET", "/v1/milestones/"+mID, nil)
	if code != 200 {
		t.Fatalf("GET milestone: %d %#v", code, response)
	}
	progress := response["data"].(map[string]any)["progress"].(map[string]any)
	if progress["total"] != float64(2) || progress["open"] != float64(1) || progress["closed"] != float64(1) || progress["blocked_open"] != float64(1) || progress["claimed_open"] != float64(1) || !same(progress["external_blockers"], []any{blockerID}) {
		t.Fatalf("incoherent milestone progress: %#v", progress)
	}
	milestoneBefore, _ := s.Milestone(mID)
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, p1, "issue.close", protocol.ProjectInput{Target: openID}))
	milestoneAfter, _ := s.Milestone(mID)
	if milestoneAfter.Revision != milestoneBefore.Revision {
		t.Fatalf("member close changed milestone revision: %d -> %d", milestoneBefore.Revision, milestoneAfter.Revision)
	}
	history, err := s.RecordHistory("milestones", mID)
	if err != nil || len(history) != 1 {
		t.Fatalf("milestone history changed with projection: %d %v", len(history), err)
	}
	code, response = directRequest(t, s, c, "GET", "/v1/milestones/"+mID+"/requests/"+created.RequestHash, nil)
	if code != 200 || response["data"].(map[string]any)["outcome"] != "recorded" {
		t.Fatalf("request reconciliation: %d %#v", code, response)
	}
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestServer(t, root, s.Config)
	reloaded, err := s.Milestone(mID)
	if err != nil || !same(reloaded, milestoneAfter) {
		t.Fatalf("restart changed milestone: %#v %v", reloaded, err)
	}
}

func TestMilestoneHistoryHonorsExplicitProjectAndPagination(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	project, selected := protocol.UUID(), protocol.UUID()
	executeRecordTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: project, Title: textPointer("feat/history")}, protocol.ProjectInput{ID: selected, Title: textPointer("feat/selected")}))
	mID := protocol.UUID()
	created := prepareMilestoneTest(t, s, c, project, protocol.ProjectInput{ID: mID, Title: textPointer("History scope")})
	executeRecordTest(t, s, c, created)
	updated, err := s.PrepareRecords(protocol.PrepareRequest{Operation: "milestone.update", Project: project, Items: []protocol.ProjectInput{{Target: mID, Set: protocol.ProjectSet{Body: textPointer("second revision")}}}}, c)
	if err != nil {
		t.Fatal(err)
	}
	executeRecordTest(t, s, c, updated)
	c.ProjectID = &selected
	if err = s.SaveClient(c); err != nil {
		t.Fatal(err)
	}
	path := "/v1/milestones/" + mID + "/history?project_id=" + project + "&limit=1"
	code, first := directRequest(t, s, c, "GET", path, nil)
	if code != 200 || len(first["items"].([]any)) != 1 || first["next_cursor"] == nil {
		t.Fatalf("scoped first history page: %d %#v", code, first)
	}
	cursor := first["next_cursor"].(string)
	code, secondPage := directRequest(t, s, c, "GET", path+"&cursor="+cursor, nil)
	if code != 200 || len(secondPage["items"].([]any)) != 1 {
		t.Fatalf("scoped next history page: %d %#v", code, secondPage)
	}
	code, all := directRequest(t, s, c, "GET", "/v1/milestones/"+mID+"/history?project_id="+project+"&all=true", nil)
	if code != 200 || len(all["items"].([]any)) != 2 {
		t.Fatalf("scoped full history: %d %#v", code, all)
	}
	code, detail := directRequest(t, s, c, "GET", "/v1/milestones/"+mID+"/history/"+created.RequestHash+"?project_id="+project, nil)
	if code != 200 || detail["data"].(map[string]any)["request_hash"] != created.RequestHash {
		t.Fatalf("scoped history detail: %d %#v", code, detail)
	}
}

func prepareMilestoneTest(t *testing.T, s *Server, c protocol.Client, project string, item protocol.ProjectInput) protocol.Prepared {
	t.Helper()
	p, e := s.PrepareRecords(protocol.PrepareRequest{Operation: "milestone.create", Project: project, Items: []protocol.ProjectInput{item}}, c)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
