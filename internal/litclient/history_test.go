package litclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// History reads a record's immutable entries oldest first, with who changed
// it, when, which fields and the body before and after; pages continue
// through cursors bound to the page size, and a read journals nothing.
func TestHistoryReadsImmutableEntriesThroughCursors(t *testing.T) {
	h := newHarness(t)
	s := h.connect("history", "person")
	project, issues := seed(t, s, "history", "Tracked")
	id := issues["Tracked"].ID
	apply(t, s, litclient.UpdateIssue(project, id, 0, litclient.IssueChange{Title: str("Renamed")}))
	apply(t, s, litclient.UpdateIssue(project, id, 0, litclient.IssueChange{Body: str("Tracked body\nsecond line")}))
	apply(t, s, litclient.CloseIssue(project, id, 0))
	comment := litclient.CreateComment(project, id, "first words")
	apply(t, s, comment)
	commentID := comment.Items[0].ID
	apply(t, s, litclient.UpdateComment(commentID, 0, "better words"))
	ctx := context.Background()

	page, err := s.History(ctx, "issues", id, litclient.HistoryQuery{})
	// Four entries: creation, title, body and close. Adding the comment did
	// not change the issue: its comment_ids are computed, not stored.
	if err != nil || page.NextCursor != nil || len(page.Items) != 4 {
		t.Fatalf("issue history: %+v %v", page, err)
	}
	for i, e := range page.Items {
		if e.OwnerID != id || e.Revision != int64(i+1) || e.BeforeRevision != int64(i) || e.Timestamp == "" || e.RequestHash == "" ||
			e.Intent.Actor.Name != "person" || e.Intent.Actor.Kind != "human" || e.Intent.Actor.ClientID != s.Mapping.ClientID {
			t.Fatalf("entry %d: %+v", i, e)
		}
	}
	if d := page.Items[1].Differences["title"]; string(d.Before) != `"Tracked"` || string(d.After) != `"Renamed"` || len(page.Items[1].BodyHunks) != 0 {
		t.Fatalf("title change: %+v", page.Items[1])
	}
	if hs := page.Items[2].BodyHunks; len(hs) != 1 || hs[0].Before != "Tracked body" || hs[0].After != "Tracked body\nsecond line" {
		t.Fatalf("body change: %+v", page.Items[2])
	}
	if d := page.Items[3].Differences["state"]; string(d.Before) != `"open"` || string(d.After) != `"closed"` {
		t.Fatalf("close: %+v", page.Items[3])
	}
	for _, e := range page.Items {
		if _, ok := e.Differences["comment_ids"]; ok {
			t.Fatalf("history records the computed comment_ids: %+v", e)
		}
	}

	// One entry per page, following every cursor, yields the same entries.
	var paged []litclient.HistoryEntry
	q := litclient.HistoryQuery{Limit: 1}
	for n := 0; n < 10; n++ {
		p, err := s.History(ctx, "issues", id, q)
		if err != nil || len(p.Items) != 1 {
			t.Fatalf("page %d: %+v %v", n, p, err)
		}
		paged = append(paged, p.Items...)
		if p.NextCursor == nil {
			break
		}
		q.Cursor = *p.NextCursor
	}
	if len(paged) != 4 {
		t.Fatalf("paged %d entries", len(paged))
	}
	for i := range paged {
		if paged[i].RequestHash != page.Items[i].RequestHash {
			t.Fatalf("page %d differs: %s vs %s", i, paged[i].RequestHash, page.Items[i].RequestHash)
		}
	}
	// A cursor is bound to its page size.
	if _, err := s.History(ctx, "issues", id, litclient.HistoryQuery{Limit: 2, Cursor: q.Cursor}); litclient.Code(err) != "invalid_cursor" {
		t.Fatalf("rebound cursor: %v", err)
	}

	// A comment's history: creation, then the edit with its body change.
	cp, err := s.History(ctx, "comments", commentID, litclient.HistoryQuery{})
	if err != nil || len(cp.Items) != 2 || cp.Items[1].OwnerID != commentID {
		t.Fatalf("comment history: %+v %v", cp, err)
	}
	if hs := cp.Items[1].BodyHunks; len(hs) != 1 || hs[0].Before != "first words" || hs[0].After != "better words" {
		t.Fatalf("comment edit: %+v", cp.Items[1])
	}

	// Refusals are typed; a bad owner is refused before anything is sent.
	before := h.count("GET /v1/issues/" + id + "/history")
	for _, tc := range []struct{ typ, id string }{{"labels", id}, {"issues", "title:Renamed"}, {"issues", ""}} {
		if _, err := s.History(ctx, tc.typ, tc.id, litclient.HistoryQuery{}); litclient.Code(err) != "invalid_arguments" {
			t.Fatalf("%s %q: %v", tc.typ, tc.id, err)
		}
	}
	if _, err := s.History(ctx, "issues", id, litclient.HistoryQuery{All: true, Limit: 1}); litclient.Code(err) != "invalid_arguments" {
		t.Fatalf("all with limit: %v", err)
	}
	if after := h.count("GET /v1/issues/" + id + "/history"); after != before {
		t.Fatalf("refused reads were sent: %d -> %d", before, after)
	}
	if _, err := s.History(ctx, "issues", protocol.UUID(), litclient.HistoryQuery{}); litclient.Code(err) != "not_found" {
		t.Fatalf("unknown issue: %v", err)
	}
	if files := h.pending(); len(files) != 0 {
		t.Fatalf("history journaled: %v", files)
	}
}

// The request is exactly lit issues history's: GET of the owner's history
// route with limit and cursor; entries for another owner are invalid.
func TestHistorySendsTheHistoryRequest(t *testing.T) {
	owner := protocol.UUID()
	entry := map[string]any{"schema_version": 1, "owner_id": owner, "before_revision": 0, "revision": 1, "timestamp": "2026-10-09T00:00:00.000000Z",
		"request_hash": "h", "intent": map[string]any{"schema_version": 1, "operation": "transaction", "actor": map[string]any{"client_id": "c", "name": "n", "kind": "agent"}},
		"results": []any{}, "differences": map[string]any{}, "body_hunks": []any{}}
	var answer []byte
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Method + " " + r.URL.String()
		w.Write(answer)
	}))
	defer server.Close()
	serviceID := protocol.UUID()
	s := &litclient.Session{StateDir: t.TempDir(), Endpoint: server.URL, HTTP: server.Client(), Context: context.Background(),
		Mapping: litclient.Mapping{SchemaVersion: 1, ClientID: protocol.UUID(), ServiceID: serviceID, Endpoint: server.URL}, Meta: protocol.Meta{ServiceID: serviceID, APIMajor: 1}}
	answer, _ = json.Marshal(map[string]any{"items": []any{entry}, "next_cursor": "next", "server_time": "2026-10-09T00:00:00Z"})
	page, err := s.History(context.Background(), "comments", owner, litclient.HistoryQuery{Limit: 5, Cursor: "c"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Intent.Actor.Kind != "agent" || page.NextCursor == nil || *page.NextCursor != "next" {
		t.Fatalf("%+v %v", page, err)
	}
	if want := "GET /v1/comments/" + owner + "/history?cursor=c&limit=5"; got != want {
		t.Fatalf("sent %s, want %s", got, want)
	}
	if _, err := s.History(context.Background(), "issues", owner, litclient.HistoryQuery{All: true}); err != nil || got != "GET /v1/issues/"+owner+"/history?all=true" {
		t.Fatalf("all: %s %v", got, err)
	}
	if _, err := s.History(context.Background(), "issues", owner, litclient.HistoryQuery{}); err != nil || got != "GET /v1/issues/"+owner+"/history" {
		t.Fatalf("default: %s %v", got, err)
	}

	entry["owner_id"] = protocol.UUID()
	answer, _ = json.Marshal(map[string]any{"items": []any{entry}, "next_cursor": nil})
	if _, err := s.History(context.Background(), "issues", owner, litclient.HistoryQuery{}); litclient.Code(err) != "invalid_response" {
		t.Fatalf("foreign entry: %v", err)
	}
	answer = []byte(`{"next_cursor":null}`)
	if _, err := s.History(context.Background(), "issues", owner, litclient.HistoryQuery{}); litclient.Code(err) != "invalid_response" {
		t.Fatalf("items omitted: %v", err)
	}
}
