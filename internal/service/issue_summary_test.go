package service

import (
	"encoding/json"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

// summaryFixture holds a parent with a body, a comment and a foreign claim, a
// child, and a blocker, so summaries exercise every relationship member.
type summaryFixture struct {
	s                 *Server
	c, rival          protocol.Client
	project           string
	parent, child, bl string
}

func newSummaryFixture(t *testing.T) summaryFixture {
	t.Helper()
	s, c := projectTestServer(t, t.TempDir())
	t.Cleanup(func() { s.DB.Close() })
	f := summaryFixture{s: s, c: c, project: protocol.UUID(), parent: protocol.UUID(), child: protocol.UUID(), bl: protocol.UUID()}
	executeRecordTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: f.project, Title: textPointer("feat/summaries")}))
	who := "nick"
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, f.project, "issue.create",
		protocol.ProjectInput{ID: f.parent, Title: textPointer("Parent"), Content: textPointer("SECRET-PARENT-BODY"), Labels: []string{"b", "a"}, Assignee: &who},
		protocol.ProjectInput{ID: f.bl, Title: textPointer("Blocker"), Content: textPointer("SECRET-BLOCKER-BODY")}))
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, f.project, "issue.create", protocol.ProjectInput{ID: f.child, Title: textPointer("Child"), Parent: &f.parent, Content: textPointer("SECRET-CHILD-BODY")}))
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, f.project, "comment.create", protocol.ProjectInput{ID: protocol.UUID(), Issue: f.parent, Content: textPointer("SECRET-COMMENT-BODY")}))
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, f.project, "issue.link", protocol.ProjectInput{From: f.bl, To: []string{f.parent}, Relation: "blocks"}))
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, f.project, "issue.link", protocol.ProjectInput{From: f.child, To: []string{f.bl}, Relation: "related"}))
	f.rival = protocol.Client{SchemaVersion: 1, ClientID: protocol.UUID(), StateRevision: 1, Status: "connected", Actor: protocol.Actor{Name: "Claude", Kind: "agent"}, ProjectID: &f.project, Runtime: protocol.Runtime{Vendor: "claude", SessionID: "PRIVATE-RUNTIME-SESSION"}}
	if e := s.SaveClient(f.rival); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: f.rival.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: f.parent}}}, f.rival); e != nil {
		t.Fatal(e)
	}
	return f
}

func summaryRequest(t *testing.T, s *Server, c protocol.Client, query map[string]any) (int, map[string]any) {
	t.Helper()
	return directRequest(t, s, c, "POST", "/v1/issue-summaries", map[string]any{"schema_version": 1, "query": query})
}

func summaryItems(t *testing.T, v map[string]any) map[string]map[string]any {
	t.Helper()
	items, ok := v["items"].([]any)
	if !ok {
		t.Fatal("summary page has no items", v)
	}
	out := map[string]map[string]any{}
	for _, item := range items {
		m := item.(map[string]any)
		out[m["id"].(string)] = m
	}
	return out
}

func assertOwnerActor(t *testing.T, where string, claim any, owner string) {
	t.Helper()
	m, ok := claim.(map[string]any)
	if !ok {
		t.Fatalf("%s: claim missing: %v", where, claim)
	}
	if m["owner_client_id"] != owner {
		t.Fatalf("%s: owner_client_id %v", where, m["owner_client_id"])
	}
	actor, ok := m["owner_actor"].(map[string]any)
	if !ok || len(actor) != 2 || actor["name"] != "Claude" || actor["kind"] != "agent" {
		t.Fatalf("%s: owner_actor %v", where, m["owner_actor"])
	}
}

func TestIssueSummariesOmitBodiesAndProjectRelationshipsAndClaims(t *testing.T) {
	f := newSummaryFixture(t)
	code, v := summaryRequest(t, f.s, f.c, map[string]any{"project_id": f.project, "all": true})
	if code != 200 || v["next_cursor"] != nil || v["server_time"] == nil {
		t.Fatal(code, v)
	}
	raw := string(mustJSON(v))
	if strings.Contains(raw, "SECRET") || strings.Contains(raw, "PRIVATE-RUNTIME") {
		t.Fatal("summary leaked bodies or client state", raw)
	}
	items := summaryItems(t, v)
	if len(items) != 3 {
		t.Fatal(items)
	}
	want := []string{"assignee", "blocked_by", "blocks", "child_ids", "claim", "claimed", "created_at", "id", "labels", "parent_id", "project_id", "related", "revision", "state", "title", "updated_at"}
	for id, item := range items {
		keys := []string{}
		for k := range item {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if strings.Join(keys, ",") != strings.Join(want, ",") {
			t.Fatal("summary members", id, keys)
		}
	}
	parent, child, blocker := items[f.parent], items[f.child], items[f.bl]
	full, _ := f.s.Issue(f.parent)
	if parent["title"] != "Parent" || parent["state"] != "open" || parent["assignee"] != "nick" || parent["revision"] != float64(full.Revision) || parent["project_id"] != f.project || parent["parent_id"] != nil {
		t.Fatal(parent)
	}
	if strings.Join(stringsOf(parent["labels"]), ",") != "a,b" || strings.Join(stringsOf(parent["child_ids"]), ",") != f.child || strings.Join(stringsOf(parent["blocked_by"]), ",") != f.bl {
		t.Fatal(parent)
	}
	if parent["claimed"] != true {
		t.Fatal(parent)
	}
	assertOwnerActor(t, "summary", parent["claim"], f.rival.ClientID)
	if child["parent_id"] != f.parent || strings.Join(stringsOf(child["related"]), ",") != f.bl || child["claimed"] != false || child["claim"] != nil {
		t.Fatal(child)
	}
	if strings.Join(stringsOf(blocker["blocks"]), ",") != f.parent || strings.Join(stringsOf(blocker["related"]), ",") != f.child || len(stringsOf(blocker["labels"])) != 0 || blocker["assignee"] != nil {
		t.Fatal(blocker)
	}
	// Issue filters and the session's selected project apply as for snapshot queries.
	code, v = summaryRequest(t, f.s, f.rival, map[string]any{"parent_id": f.parent, "all": true})
	if code != 200 || len(summaryItems(t, v)) != 1 || summaryItems(t, v)[f.child] == nil {
		t.Fatal(code, v)
	}
	code, v = summaryRequest(t, f.s, f.c, map[string]any{"type": "issues", "all_projects": true, "claimed": true, "all": true})
	if code != 200 || len(summaryItems(t, v)) != 1 || summaryItems(t, v)[f.parent] == nil {
		t.Fatal(code, v)
	}
}

func stringsOf(v any) []string {
	out := []string{}
	list, _ := v.([]any)
	for _, x := range list {
		out = append(out, x.(string))
	}
	return out
}

func TestIssueSummaryPagesCursorsAndLimits(t *testing.T) {
	f := newSummaryFixture(t)
	seen := map[string]bool{}
	query := map[string]any{"project_id": f.project, "sort": "title", "limit": 1}
	for page := 0; ; page++ {
		code, v := summaryRequest(t, f.s, f.c, query)
		if code != 200 || len(v["items"].([]any)) != 1 {
			t.Fatal(page, code, v)
		}
		for id := range summaryItems(t, v) {
			if seen[id] {
				t.Fatal("page repeated", id)
			}
			seen[id] = true
		}
		next, ok := v["next_cursor"].(string)
		if !ok {
			break
		}
		if page > 3 {
			t.Fatal("cursor did not terminate")
		}
		query["cursor"] = next
	}
	if len(seen) != 3 {
		t.Fatal(seen)
	}
	// A cursor is bound to its normalized query.
	code, v := summaryRequest(t, f.s, f.c, map[string]any{"project_id": f.project, "limit": 1})
	if code != 200 || v["next_cursor"] == nil {
		t.Fatal(code, v)
	}
	code, v = summaryRequest(t, f.s, f.c, map[string]any{"project_id": f.project, "limit": 1, "state": "open", "cursor": v["next_cursor"]})
	if code != 400 || errorCode(v) != "invalid_cursor" {
		t.Fatal("cursor rebound", code, v)
	}
	f.s.Config.Limits.MaxPage = 2
	code, v = summaryRequest(t, f.s, f.c, map[string]any{"project_id": f.project, "limit": 3})
	if code != 422 || errorCode(v) != "validation_failed" || v["items"] != nil {
		t.Fatal("limit above max_page", code, v)
	}
	f.s.Config.Limits.DefaultPage = 2
	code, v = summaryRequest(t, f.s, f.c, map[string]any{"project_id": f.project})
	if code != 200 || len(v["items"].([]any)) != 2 || v["next_cursor"] == nil {
		t.Fatal("default page", code, v)
	}
	f.s.Config.Limits.ExpandedRecords = 2
	code, v = summaryRequest(t, f.s, f.c, map[string]any{"project_id": f.project, "all": true})
	if code != 413 || errorCode(v) != "limit_exceeded" || v["items"] != nil {
		t.Fatal("truncated all summaries", code, v)
	}
	f.s.Config.Limits.ExpandedRecords = 100
	f.s.Config.Limits.SnapshotBytes = 300
	code, v = summaryRequest(t, f.s, f.c, map[string]any{"project_id": f.project, "all": true})
	if code != 413 || errorCode(v) != "limit_exceeded" || v["items"] != nil {
		t.Fatal("partial summary bytes", code, v)
	}
}

func errorCode(v map[string]any) string {
	e, _ := v["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func TestIssueSummaryRequestValidation(t *testing.T) {
	f := newSummaryFixture(t)
	for _, raw := range []string{
		`{}`,
		`{"schema_version":1}`,
		`{"schema_version":2,"query":{"all":true}}`,
		`{"schema_version":1,"query":null}`,
		`{"schema_version":1,"query":{"type":"comments","issue_id":"` + f.parent + `"}}`,
		`{"schema_version":1,"query":{"type":"projects"}}`,
		`{"schema_version":1,"query":{"project_id":"` + f.project + `","all":true,"limit":1}}`,
		`{"schema_version":1,"query":{"project_id":"` + f.project + `","milestone_id":""}}`,
		`{"schema_version":1,"query":{"author":"x"}}`,
		`{"schema_version":1,"query":{"all":true},"targets":[]}`,
	} {
		var body any
		if e := json.Unmarshal([]byte(raw), &body); e != nil {
			t.Fatal(e)
		}
		code, v := directRequest(t, f.s, f.c, "POST", "/v1/issue-summaries", body)
		if code != 400 && code != 422 || v["items"] != nil {
			t.Fatal(raw, code, v)
		}
	}
	code, v := directRequest(t, f.s, f.c, "GET", "/v1/issue-summaries", nil)
	if code != 404 {
		t.Fatal("summary GET", code, v)
	}
}

func TestIssueSummaryRefusesDisconnectedClientLikeSnapshots(t *testing.T) {
	f := newSummaryFixture(t)
	gone := f.c
	gone.Status = "disconnected"
	gone.StateRevision++
	if e := f.s.SaveClient(gone); e != nil {
		t.Fatal(e)
	}
	snapCode, snap := directRequest(t, f.s, gone, "POST", "/v1/snapshots", map[string]any{"query": map[string]any{"type": "issues", "project_id": f.project, "all": true}})
	code, v := summaryRequest(t, f.s, gone, map[string]any{"project_id": f.project, "all": true})
	if snapCode != 409 || errorCode(snap) != "client_disconnected" || code != snapCode || errorCode(v) != errorCode(snap) || v["items"] != nil {
		t.Fatal(snapCode, snap, code, v)
	}
}

// Claim owners are readable wherever live claim status is exposed, without
// exposing the owning client's record or persisting the projection.
func TestClaimStatusNamesOwnerActorWithoutClientRecords(t *testing.T) {
	f := newSummaryFixture(t)
	s, c, id := f.s, f.c, f.parent
	code, v := directRequest(t, s, c, "POST", "/v1/claim-snapshots", map[string]any{"targets": []string{id}, "project": f.project})
	if code != 200 {
		t.Fatal(code, v)
	}
	assertOwnerActor(t, "claim snapshot targets", v["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["claim"], f.rival.ClientID)
	code, v = directRequest(t, s, f.rival, "POST", "/v1/claim-snapshots", map[string]any{"owner_client_id": f.rival.ClientID})
	if code != 200 {
		t.Fatal(code, v)
	}
	assertOwnerActor(t, "claim snapshot owner", v["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["claim"], f.rival.ClientID)
	code, v = directRequest(t, s, c, "GET", "/v1/claims?issue_id="+id, nil)
	if code != 200 || len(v["items"].([]any)) != 1 {
		t.Fatal(code, v)
	}
	assertOwnerActor(t, "claim list", v["items"].([]any)[0], f.rival.ClientID)
	code, v = directRequest(t, s, c, "GET", "/v1/issues/"+id+"/claim", nil)
	if code != 200 {
		t.Fatal(code, v)
	}
	assertOwnerActor(t, "issue claim", v["data"], f.rival.ClientID)
	code, v = directRequest(t, s, c, "GET", "/v1/issues/"+id, nil)
	if code != 200 {
		t.Fatal(code, v)
	}
	assertOwnerActor(t, "issue read", v["data"].(map[string]any)["claim"], f.rival.ClientID)
	code, v = directRequest(t, s, c, "POST", "/v1/snapshots", map[string]any{"targets": []any{map[string]string{"type": "issues", "selector": id}}})
	if code != 200 {
		t.Fatal(code, v)
	}
	assertOwnerActor(t, "issue snapshot", v["items"].([]any)[0].(map[string]any)["claim"], f.rival.ClientID)
	code, v = directRequest(t, s, c, "GET", "/v1/issues?"+url.Values{"project_id": {f.project}, "claimed": {"true"}}.Encode(), nil)
	if code != 200 || len(v["items"].([]any)) != 1 {
		t.Fatal(code, v)
	}
	assertOwnerActor(t, "issue list", v["items"].([]any)[0].(map[string]any)["claim"], f.rival.ClientID)
	// Other clients' records stay private; only the actor's name and kind appear.
	code, v = directRequest(t, s, c, "GET", "/v1/clients/"+f.rival.ClientID, nil)
	if code != 403 || errorCode(v) != "wrong_client" {
		t.Fatal(code, v)
	}
	// The projection never enters the stored claim.
	if stored := storedClaim(t, s, id); stored == nil || stored.OwnerActor != nil {
		t.Fatal(stored)
	}
	target := time.Now().UTC().Add(45 * time.Minute).Format(time.RFC3339Nano)
	renewed, e := s.ApplyClaims(protocol.ClaimOperation{Operation: "claims.renew", OwnerClientID: f.rival.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id, Token: mustLiveClaim(t, s, id).Token, ExtendTo: target}}}, f.rival)
	if e != nil || renewed.Outcome != "applied" || len(renewed.Items) != 1 {
		t.Fatal(renewed, e)
	}
	if stored := storedClaim(t, s, id); stored == nil || stored.OwnerActor != nil {
		t.Fatal("renewal persisted projected owner", stored)
	}
}

func mustLiveClaim(t *testing.T, s *Server, id string) *protocol.Claim {
	t.Helper()
	c, e := s.LiveClaim(id)
	if e != nil || c == nil {
		t.Fatal(c, e)
	}
	return c
}

// A stored claim never carries the read-only owner projection.
func TestDurableClaimRejectsProjectedOwnerActor(t *testing.T) {
	f := newSummaryFixture(t)
	claim := *mustLiveClaim(t, f.s, f.parent)
	claim.OwnerActor = &protocol.Actor{Name: "Claude", Kind: "agent"}
	_, e := inRequest(f.s, func() (struct{}, error) { return struct{}{}, f.s.commit(changes{claims: []protocol.Claim{claim}}) })
	if e == nil {
		t.Fatal("stored a claim with a projected owner actor")
	}
	if stored := storedClaim(t, f.s, f.parent); stored == nil || stored.OwnerActor != nil {
		t.Fatal(stored)
	}
}
