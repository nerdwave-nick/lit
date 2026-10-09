package litclient_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// Search has lit grep's semantics: a literal, case-insensitive substring of
// current titles, bodies and comments in the scoped project, paged through
// query-bound cursors, as a read that journals nothing.
func TestSearchFindsCurrentContentLikeGrep(t *testing.T) {
	h := newHarness(t)
	s := h.connect("grep", "person")
	project, issues := seed(t, s, "grep", "Renewal policy", "Unrelated", "Commented")
	apply(t, s, litclient.CreateComment(project, issues["Commented"].ID, "see the RENEWAL notes"))
	seed(t, s, "other", "Renewal elsewhere")
	ctx := context.Background()
	scope := protocol.SearchScope{ProjectID: issues["Unrelated"].ProjectID}

	page, err := s.Search(ctx, protocol.SearchRequest{Scope: scope, Query: "renewal"})
	if err != nil || page.NextCursor != nil {
		t.Fatalf("search: %+v %v", page, err)
	}
	found := map[string]string{}
	for _, r := range page.Items {
		found[r.Type+" "+r.IssueID] = r.Excerpts[0].Field
		if r.ProjectID != scope.ProjectID {
			t.Fatalf("result outside the project: %+v", r)
		}
	}
	want := map[string]string{"issues " + issues["Renewal policy"].ID: "title", "comments " + issues["Commented"].ID: "body"}
	if len(found) != len(want) || found["issues "+issues["Renewal policy"].ID] != "title" || found["comments "+issues["Commented"].ID] != "body" {
		t.Fatalf("results %v, want %v", found, want)
	}

	// Case-sensitive search and an edit that removes the text: current
	// content only.
	if page, err = s.Search(ctx, protocol.SearchRequest{Scope: scope, Query: "renewal", CaseSensitive: true}); err != nil || len(page.Items) != 0 {
		t.Fatalf("case-sensitive: %+v %v", page, err)
	}
	renamed := "Lease policy"
	apply(t, s, litclient.UpdateIssue(project, issues["Renewal policy"].ID, 0, litclient.IssueChange{Title: &renamed}))
	apply(t, s, litclient.UpdateIssue(project, issues["Renewal policy"].ID, 0, litclient.IssueChange{Body: str("nothing here")}))
	if page, err = s.Search(ctx, protocol.SearchRequest{Scope: scope, Query: "Renewal"}); err != nil || len(page.Items) != 1 || page.Items[0].Type != "comments" {
		t.Fatalf("after the edit: %+v %v", page, err)
	}

	// Pages continue through cursors bound to the query (here: the comment
	// and the issue in the other project).
	seen := map[string]bool{}
	q := protocol.SearchRequest{Scope: protocol.SearchScope{AllProjects: true}, Query: "renewal", Limit: 1}
	for n := 0; n < 4; n++ {
		page, err = s.Search(ctx, q)
		if err != nil || len(page.Items) != 1 || seen[page.Items[0].ID] {
			t.Fatalf("page %d: %+v %v", n, page, err)
		}
		seen[page.Items[0].ID] = true
		if page.NextCursor == nil {
			break
		}
		q.Cursor = *page.NextCursor
	}
	if len(seen) != 2 || page.NextCursor != nil {
		t.Fatalf("pages %v", seen)
	}

	// Refusals are typed; reads leave no evidence.
	if _, err := s.Search(ctx, protocol.SearchRequest{Scope: scope, Query: ""}); litclient.Code(err) != "validation_failed" {
		t.Fatalf("empty query: %v", err)
	}
	if files := h.pending(); len(files) != 0 {
		t.Fatalf("search journaled: %v", files)
	}
}

// The request is exactly lit grep's: POST /v1/search with the search object.
func TestSearchSendsTheGrepRequest(t *testing.T) {
	var got map[string]any
	answer := `{"items":[],"next_cursor":"next","server_time":"2026-10-09T00:00:00Z"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/search" || r.URL.RawQuery != "" {
			t.Errorf("request %s %s", r.Method, r.URL)
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &got); err != nil {
			t.Error(err)
		}
		w.Write([]byte(answer))
	}))
	defer server.Close()
	serviceID := protocol.UUID()
	s := &litclient.Session{StateDir: t.TempDir(), Endpoint: server.URL, HTTP: server.Client(), Context: context.Background(),
		Mapping: litclient.Mapping{SchemaVersion: 1, ClientID: protocol.UUID(), ServiceID: serviceID, Endpoint: server.URL}, Meta: protocol.Meta{ServiceID: serviceID, APIMajor: 1}}
	page, err := s.Search(context.Background(), protocol.SearchRequest{Scope: protocol.SearchScope{ProjectID: "p"}, Query: "x", Limit: 5, Cursor: "c"})
	if err != nil || page.Items == nil || page.NextCursor == nil || *page.NextCursor != "next" {
		t.Fatalf("%+v %v", page, err)
	}
	raw, _ := json.Marshal(got)
	if want := `{"case_sensitive":false,"context_lines":0,"cursor":"c","limit":5,"query":"x","scope":{"project_id":"p"}}`; string(raw) != want {
		t.Fatalf("sent %s, want %s", raw, want)
	}
	// A page without items is an invalid response, not an empty result.
	answer = `{"next_cursor":null,"server_time":"2026-10-09T00:00:00Z"}`
	if _, err := s.Search(context.Background(), protocol.SearchRequest{Query: "x"}); litclient.Code(err) != "invalid_response" {
		t.Fatalf("items omitted: %v", err)
	}
}
