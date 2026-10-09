package litclient

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/service"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/store"
)

// summaryService runs a real in-process lit-server with one project holding a
// claimed parent issue (with a body) and its child, and returns a session for
// a second, human client.
func summaryService(t *testing.T) (*service.Server, *Session, string, string) {
	t.Helper()
	st, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	srv, e := service.New(st, service.Config{Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"feat"}})
	if e != nil {
		st.Close()
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	agent := protocol.Client{SchemaVersion: 1, ClientID: protocol.UUID(), StateRevision: 1, Status: "connected", Actor: protocol.Actor{Name: "Claude", Kind: "agent"}}
	human := clientRecord(protocol.UUID(), 1, "connected")
	for _, c := range []protocol.Client{agent, human} {
		if e := srv.SaveClient(c); e != nil {
			t.Fatal(e)
		}
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	commit := func(p protocol.Prepared, e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
		b, _ := json.Marshal(protocol.DurableRequest{SchemaVersion: 1, Intent: p.Intent, RequestHash: p.RequestHash})
		r := httptest.NewRequest("POST", "/v1/transactions", bytes.NewReader(b))
		r.Header.Set("X-Lit-Client-ID", agent.ClientID)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	project, parent := protocol.UUID(), protocol.UUID()
	title, body, child := "feat/summaries", "SECRET-BODY", "Child"
	commit(srv.PrepareProjects(protocol.PrepareRequest{Operation: "project.create", Items: []protocol.ProjectInput{{ID: project, Title: &title}}}, agent))
	parentTitle := "Parent"
	commit(srv.PrepareRecords(protocol.PrepareRequest{Operation: "issue.create", Project: project, Items: []protocol.ProjectInput{{ID: parent, Title: &parentTitle, Content: &body}}}, agent))
	commit(srv.PrepareRecords(protocol.PrepareRequest{Operation: "issue.create", Project: project, Items: []protocol.ProjectInput{{ID: protocol.UUID(), Title: &child, Parent: &parent}}}, agent))
	if _, e := srv.ApplyClaims(protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: agent.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: parent}}}, agent); e != nil {
		t.Fatal(e)
	}
	s := &Session{StateDir: t.TempDir(), Endpoint: ts.URL, HTTP: ts.Client(), Context: context.Background(),
		Mapping: Mapping{SchemaVersion: 1, ClientID: human.ClientID, ServiceID: st.Identity.ServiceID, Endpoint: ts.URL}, Meta: srv.Meta(), Client: human}
	return srv, s, project, parent
}

func TestIssueSummariesReadsBodilessPagesWithClaimOwners(t *testing.T) {
	srv, s, project, parent := summaryService(t)
	page, e := s.IssueSummaries(context.Background(), map[string]any{"project_id": project, "all": true})
	if e != nil || len(page.Items) != 2 || page.NextCursor != nil {
		t.Fatalf("all: %+v %v", page, e)
	}
	byID := map[string]protocol.IssueSummary{}
	for _, item := range page.Items {
		byID[item.ID] = item
	}
	p := byID[parent]
	if p.Title != "Parent" || len(p.ChildIDs) != 1 || !p.Claimed || p.Claim == nil || p.Claim.OwnerActor == nil || *p.Claim.OwnerActor != (protocol.Actor{Name: "Claude", Kind: "agent"}) {
		t.Fatalf("parent summary: %+v", p)
	}
	if s.ServerTime == "" {
		t.Fatal("server time not recorded")
	}
	// Pages follow the service's query-bound cursors.
	seen := map[string]bool{}
	query := map[string]any{"project_id": project, "limit": 1}
	for n := 0; n < 3; n++ {
		page, e = s.IssueSummaries(context.Background(), query)
		if e != nil || len(page.Items) != 1 || seen[page.Items[0].ID] {
			t.Fatalf("page %d: %+v %v", n, page, e)
		}
		seen[page.Items[0].ID] = true
		if page.NextCursor == nil {
			break
		}
		query["cursor"] = *page.NextCursor
	}
	if len(seen) != 2 || page.NextCursor != nil {
		t.Fatalf("pages: %v %+v", seen, page)
	}
	// A limit is a typed refusal, never a truncated page; reads leave no evidence.
	srv.Config.Limits.SnapshotBytes = 200
	page, e = s.IssueSummaries(context.Background(), map[string]any{"project_id": project, "all": true})
	if errorCode(e) != "limit_exceeded" || page.Items != nil {
		t.Fatalf("limit: %+v %v", page, e)
	}
	if files := pendingFiles(t, s); len(files) != 0 {
		t.Fatalf("read journaled: %v", files)
	}
}

func TestIssueSummariesSendsTheQueryUnchanged(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/issue-summaries" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		b, _ := readBody(r)
		if err := json.Unmarshal(b, &got); err != nil {
			t.Error(err)
		}
		w.Write([]byte(`{"items":[],"next_cursor":"next","server_time":"2026-10-09T00:00:00Z"}`))
	}))
	defer server.Close()
	s := testSession(t, server.URL)
	page, e := s.IssueSummaries(context.Background(), map[string]any{"project_id": "p", "limit": 5, "cursor": "c"})
	if e != nil || page.Items == nil || page.NextCursor == nil || *page.NextCursor != "next" {
		t.Fatalf("%+v %v", page, e)
	}
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"schema_version":1`) || !strings.Contains(string(raw), `"query":{"cursor":"c","limit":5,"project_id":"p"}`) {
		t.Fatalf("sent %s", raw)
	}
}
