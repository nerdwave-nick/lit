package service

import (
	"bytes"
	"encoding/json"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"net/http/httptest"
	"strings"
	"testing"
)

func directRequest(t *testing.T, s *Server, c protocol.Client, method, path string, body any) (int, map[string]any) {
	t.Helper()
	b := []byte{}
	if body != nil {
		b = mustJSON(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("X-Lit-Client-ID", c.ClientID)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	v := map[string]any{}
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e, w.Body.String())
	}
	return w.Code, v
}
func TestRecordHTTPResourceAndScope(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	project := protocol.UUID()
	plan := prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: project, Title: textPointer("feat/http")})
	req := protocol.DurableRequest{SchemaVersion: 1, Intent: plan.Intent, RequestHash: plan.RequestHash}
	for _, path := range []string{"/v1/issues", "/v1/comments"} {
		code, _ := directRequest(t, s, c, "POST", path, req)
		if code != 422 {
			t.Fatal("route accepts wrong resource", path, code)
		}
	}
	executeRecordTest(t, s, c, plan)
	c.ProjectID = &project
	if e := s.SaveClient(c); e != nil {
		t.Fatal(e)
	}
	id := protocol.UUID()
	plan = prepareRecordTest(t, s, c, project, "issue.create", protocol.ProjectInput{ID: id, Title: textPointer("HTTP")})
	req = protocol.DurableRequest{SchemaVersion: 1, Intent: plan.Intent, RequestHash: plan.RequestHash}
	code, _ := directRequest(t, s, c, "POST", "/v1/comments", req)
	if code != 422 {
		t.Fatal(code)
	}
	code, v := directRequest(t, s, c, "POST", "/v1/issues", req)
	if code != 201 || v["data"] == nil || v["items"] != nil {
		t.Fatal(code, v)
	}
	for _, path := range []string{"/v1/issues?all_projects=true", "/v1/issues?project_id=" + project} {
		code, v = directRequest(t, s, c, "GET", path, nil)
		if code != 200 || v["items"] == nil || v["data"] != nil {
			t.Fatal(code, v)
		}
	}
	code, v = directRequest(t, s, c, "GET", "/v1/issues/"+id, nil)
	if code != 200 || v["data"] == nil {
		t.Fatal(code, v)
	}
	code, v = directRequest(t, s, c, "GET", "/v1/issues/"+id+"/history", nil)
	if code != 200 || v["items"] == nil {
		t.Fatal(code, v)
	}
	code, v = directRequest(t, s, c, "GET", "/v1/issues/"+id+"/requests/"+plan.RequestHash, nil)
	if code != 200 || v["data"].(map[string]any)["outcome"] != "recorded" {
		t.Fatal(code, v)
	}
}
func TestRecordResponseLimitBeforeCommit(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	project := protocol.UUID()
	executeRecordTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: project, Title: textPointer("feat/limit")}))
	plan := prepareRecordTest(t, s, c, project, "issue.create", protocol.ProjectInput{ID: protocol.UUID(), Title: textPointer("Limit")})
	before, _ := allRecords(s)
	s.Config.Limits.SnapshotBytes = 250
	code, body := executeProjectTest(t, s, c, plan)
	if code != 413 || !strings.Contains(string(body), "limit_exceeded") {
		t.Fatal(code, string(body))
	}
	after, _ := allRecords(s)
	if !same(before, after) {
		t.Fatal("limit rejection committed")
	}
	s.Config.Limits.SnapshotBytes = 1 << 20
	s.Config.Limits.ExpandedRecords = 1
	code, _ = executeProjectTest(t, s, c, plan)
	if code != 413 {
		t.Fatal("execute skipped expansion limit", code)
	}
	after, _ = allRecords(s)
	if !same(before, after) {
		t.Fatal("expansion limit committed")
	}
}
func TestIssueSelectorsAndFinalState(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	p1, p2 := protocol.UUID(), protocol.UUID()
	executeRecordTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: p1, Title: textPointer("feat/one")}, protocol.ProjectInput{ID: p2, Title: textPointer("feat/two")}))
	id1, id2 := "abcdef01-0000-4000-8000-000000000001", "abcdef02-0000-4000-8000-000000000002"
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, p1, "issue.create", protocol.ProjectInput{ID: id1, Title: textPointer("Discussion: parent/history")}, protocol.ProjectInput{ID: id2, Title: textPointer("abcdef01")}))
	other := protocol.UUID()
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, p2, "issue.create", protocol.ProjectInput{ID: other, Title: textPointer("Discussion: parent/history")}))
	if _, e := s.ResolveIssue("abcdef01", p1); e == nil {
		t.Fatal("title/id ambiguity accepted")
	}
	if _, e := s.ResolveIssue("abcdef", p1); e == nil {
		t.Fatal("prefix ambiguity accepted")
	}
	for selector, want := range map[string]string{"id:ABCDEF01": id1, "feat/one:title:abcdef01": id2, "feat/one:title:Discussion: parent/history": id1, "title:Discussion: parent/history": other, "id:ABCDEF01-0000": id1} {
		got, e := s.ResolveIssue(selector, p2)
		if e != nil || got.ID != want {
			t.Fatal(selector, got, e)
		}
	}
	if _, e := s.PrepareRecords(protocol.PrepareRequest{Operation: "issue.update", Project: p1, Items: []protocol.ProjectInput{{Target: id1, Parent: &other}}}, c); e == nil {
		t.Fatal("cross-project parent accepted")
	}
	// Rename swaps validate the complete final state, not an intermediate title collision.
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, p1, "issue.update", protocol.ProjectInput{Target: id1, Set: protocol.ProjectSet{Title: textPointer("abcdef01")}}, protocol.ProjectInput{Target: id2, Set: protocol.ProjectSet{Title: textPointer("Discussion: parent/history")}}))
}

func TestIssueStrictQueryNullAndPresence(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	project := protocol.UUID()
	executeRecordTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: project, Title: textPointer("feat/query")}))
	c.ProjectID = &project
	s.SaveClient(c)
	for _, raw := range []string{`{"schema_version":1,"query":{"type":"issues","project_id":null}}`, `{"schema_version":1,"query":{"type":"issues","issue_id":""}}`, `{"schema_version":1,"query":{"type":"comments","all_projects":false}}`} {
		var body any
		json.Unmarshal([]byte(raw), &body)
		code, v := directRequest(t, s, c, "POST", "/v1/snapshots", body)
		if code < 400 {
			t.Fatal("invalid query accepted", raw, code, v)
		}
	}
}
