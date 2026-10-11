package service

import (
	"encoding/json"
	"fmt"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"net/url"
	"testing"
)

func TestDiscoveryFiltersAndCursors(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	p := protocol.UUID()
	executeRecordTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: p, Title: textPointer("feat/filter"), RepositoryRefs: []string{"r"}}))
	a, b := protocol.UUID(), protocol.UUID()
	none := "none"
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, p, "issue.create", protocol.ProjectInput{ID: a, Title: textPointer("Alpha"), Content: textPointer("Straße"), Labels: []string{"a", "b"}, Assignee: &none}, protocol.ProjectInput{ID: b, Title: textPointer("Beta")}))
	ai, _ := s.Issue(a)
	for _, test := range []struct {
		query string
		want  int
	}{{`{"type":"issues","all_projects":true,"q":"STRASSE"}`, 1}, {`{"type":"issues","all_projects":true,"assignee":"none"}`, 1}, {`{"type":"issues","all_projects":true,"assignee":null}`, 1}, {`{"type":"issues","all_projects":true,"labels_all":["a","b"],"labels_any":["c","b"],"labels_none":["z"]}`, 1}, {`{"type":"issues","all_projects":true,"created_after":"` + ai.CreatedAt + `"}`, 2}, {`{"type":"issues","all_projects":true,"created_before":"` + ai.CreatedAt + `"}`, 0}} {
		var q ProjectQuery
		if e := protocol.Decode([]byte(test.query), &q); e != nil {
			t.Fatal(e)
		}
		page, e := recordPageOf(s, q)
		if e != nil || len(page.Items) != test.want {
			t.Fatal(test.query, page, e)
		}
	}
	q := ProjectQuery{Type: "issues", AllProjects: true, Sort: "title", Limit: 1}
	first, e := recordPageOf(s, q)
	if e != nil || first.NextCursor == nil {
		t.Fatal(first, e)
	}
	q.Cursor = *first.NextCursor
	second, e := recordPageOf(s, q)
	if e != nil || len(second.Items) != 1 || second.Items[0].(protocol.Issue).ID == first.Items[0].(protocol.Issue).ID {
		t.Fatal(second, e)
	}
	q.Q = "Alpha"
	if _, e = recordPageOf(s, q); e == nil {
		t.Fatal("cursor accepted changed filters")
	}
	for _, raw := range []string{`{"type":"comments","issue_id":"` + a + `","title":""}`, `{"type":"projects","parent_id":null}`, `{"type":"issues","all_projects":true,"blocked":null}`, `{"type":"issues","all_projects":true,"created_after":"not-time"}`} {
		var q ProjectQuery
		e := protocol.Decode([]byte(raw), &q)
		if e == nil {
			_, e = recordPageOf(s, q)
		}
		if e == nil {
			t.Fatal("invalid filter accepted", raw)
		}
	}
	q2, e := recordURLQuery("issues", url.Values{"all_projects": {"true"}, "labels_all": {"a", "b"}})
	if e != nil {
		t.Fatal(e)
	}
	page, e := recordPageOf(s, q2)
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
}

func TestBoundedConsistentSnapshotsAndHistory(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	p, a, b := protocol.UUID(), protocol.UUID(), protocol.UUID()
	executeRecordTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: p, Title: textPointer("feat/snapshots")}))
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, p, "issue.create", protocol.ProjectInput{ID: a, Title: textPointer("A")}, protocol.ProjectInput{ID: b, Title: textPointer("B")}))
	target := func(id string) map[string]string { return map[string]string{"type": "issues", "selector": id} }
	query := map[string]any{"targets": []any{target(b), target(a), target(b)}}
	code, v := directRequest(t, s, c, "POST", "/v1/snapshots", query)
	if code != 200 || len(v["items"].([]any)) != 2 || v["items"].([]any)[0].(map[string]any)["id"] != b {
		t.Fatal(code, v)
	}
	code, v = directRequest(t, s, c, "POST", "/v1/snapshots", map[string]any{"targets": []any{target(a), target(protocol.UUID())}})
	if code != 404 || v["items"] != nil {
		t.Fatal("partial snapshot", code, v)
	}
	done := make(chan error, 1)
	go func() {
		for n := 0; n < 12; n++ {
			state := "closed"
			if n%2 == 1 {
				state = "open"
			}
			plan, e := s.PrepareRecords(protocol.PrepareRequest{Operation: "issue.update", Items: []protocol.ProjectInput{{Target: a, Set: protocol.ProjectSet{State: &state}}, {Target: b, Set: protocol.ProjectSet{State: &state}}}}, c)
			if e != nil {
				done <- e
				return
			}
			code, _ := executeProjectTest(t, s, c, plan)
			if code != 200 {
				done <- fmt.Errorf("writer status %d", code)
				return
			}
		}
		done <- nil
	}()
	for n := 0; n < 20; n++ {
		code, v := directRequest(t, s, c, "POST", "/v1/snapshots", query)
		if code != 200 {
			t.Fatal(code, v)
		}
		items := v["items"].([]any)
		if items[0].(map[string]any)["state"] != items[1].(map[string]any)["state"] {
			t.Fatal("mixed transaction snapshot", v)
		}
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	code, v = directRequest(t, s, c, "GET", "/v1/issues/"+a+"/history?limit=2", nil)
	if code != 200 || len(v["items"].([]any)) != 2 || v["next_cursor"] == nil {
		t.Fatal(code, v)
	}
	cursor := v["next_cursor"].(string)
	code, v = directRequest(t, s, c, "GET", "/v1/issues/"+b+"/history?limit=2&cursor="+url.QueryEscape(cursor), nil)
	if code != 400 {
		t.Fatal("history cursor crossed owner", code, v)
	}
	s.Config.Limits.ExpandedRecords = 1
	code, v = directRequest(t, s, c, "POST", "/v1/snapshots", map[string]any{"query": map[string]any{"type": "issues", "all_projects": true, "all": true}})
	if code != 413 || v["items"] != nil {
		t.Fatal("truncated all snapshot", code, v)
	}
	s.Config.Limits.ExpandedRecords = 100
	s.Config.Limits.SnapshotBytes = 200
	code, v = directRequest(t, s, c, "POST", "/v1/snapshots", query)
	if code != 413 || v["items"] != nil {
		t.Fatal("partial response bytes", code, v)
	}
}

func TestCursorSetOrderingIsSemantic(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	p := protocol.UUID()
	executeRecordTest(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: p, Title: textPointer("feat/cursor")}))
	executeRecordTest(t, s, c, prepareRecordTest(t, s, c, p, "issue.create", protocol.ProjectInput{ID: protocol.UUID(), Title: textPointer("A"), Labels: []string{"a", "b"}}, protocol.ProjectInput{ID: protocol.UUID(), Title: textPointer("B"), Labels: []string{"a", "b"}}))
	q := ProjectQuery{Type: "issues", ProjectID: p, LabelsAll: []string{"a", "b"}, Limit: 1}
	page, e := recordPageOf(s, q)
	if e != nil || page.NextCursor == nil {
		t.Fatal(page, e)
	}
	q.Cursor = *page.NextCursor
	q.LabelsAll = []string{"b", "a", "a"}
	page, e = recordPageOf(s, q)
	if e != nil || len(page.Items) != 1 {
		t.Fatal("equivalent set rejected", page, e)
	}
}

func TestSnapshotStrictPresenceAndLimits(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	for _, raw := range []string{`{"query":null}`, `{"targets":null}`, `{"query":{"type":"projects"},"targets":[]}`, `{"query":{"type":"projects","all":true,"limit":0}}`, `{"query":{"type":"projects","limit":0}}`} {
		var body any
		if e := json.Unmarshal([]byte(raw), &body); e != nil {
			t.Fatal(e)
		}
		code, v := directRequest(t, s, c, "POST", "/v1/snapshots", body)
		if code < 400 || v["items"] != nil {
			t.Fatal(raw, code, v)
		}
	}
	s.Config.Limits.ExplicitItems = 1
	code, v := directRequest(t, s, c, "POST", "/v1/snapshots", map[string]any{"targets": []any{map[string]string{"type": "projects", "selector": protocol.UUID()}, map[string]string{"type": "projects", "selector": protocol.UUID()}}})
	if code != 413 {
		t.Fatal(code, v)
	}
}
