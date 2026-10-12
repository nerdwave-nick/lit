package service

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// historyText needs every kind of JSON escape the encoder makes: quotes,
// backslashes, HTML characters, the JavaScript line separators, short and
// \u00XX control escapes, CRLF and non-ASCII text.
const historyText = "<b>Fish & \"chips\"</b> \\ / \u2028 \u2029 \b\f\x01\x1f\t é ✓ 🐟\r\nsecond line\n"

// historyFixture writes a project, two issues and a comment whose histories
// hold bodies with historyText, title changes, an assignee and a parent set
// and cleared (null in the stored set), labels, a state change, a link and a
// request that changes two issues at once.
func historyFixture(t *testing.T) (*Server, protocol.Client, [][2]string) {
	t.Helper()
	s, c := projectTestServer(t, t.TempDir())
	p, a, b, comment := protocol.UUID(), protocol.UUID(), protocol.UUID(), protocol.UUID()
	mustExecute(t, s, c, prepareProjectTest(t, s, c, "create", protocol.ProjectInput{ID: p, Title: textPointer("feat/history"), Content: textPointer(historyText)}))
	who, other := "who <them>", "other"
	update := func(items ...protocol.ProjectInput) {
		t.Helper()
		mustExecute(t, s, c, prepareRecordTest(t, s, c, p, "issue.update", items...))
	}
	mustExecute(t, s, c, prepareRecordTest(t, s, c, p, "issue.create",
		protocol.ProjectInput{ID: a, Title: textPointer("A & <a>"), Content: textPointer(historyText), Labels: []string{"b", "a"}, Assignee: &who},
		protocol.ProjectInput{ID: b, Title: textPointer("B"), Content: textPointer("b\n")}))
	update(protocol.ProjectInput{Target: a, Set: protocol.ProjectSet{Title: textPointer("A renamed <x> & é"), Body: textPointer(historyText + "more\n")}},
		protocol.ProjectInput{Target: b, Set: protocol.ProjectSet{Body: textPointer(historyText)}})
	update(protocol.ProjectInput{Target: a, Clear: []string{"assignee"}, Set: protocol.ProjectSet{ParentID: nullable(&b)}})
	update(protocol.ProjectInput{Target: a, Set: protocol.ProjectSet{ParentID: nullable(nil), Assignee: nullable(&other), State: textPointer("closed")}, Add: protocol.ProjectMembers{Labels: []string{"c"}}})
	update(protocol.ProjectInput{Target: a, Set: protocol.ProjectSet{Body: textPointer("")}})
	mustExecute(t, s, c, prepareRecordTest(t, s, c, p, "issue.link", protocol.ProjectInput{From: b, To: []string{a}, Relation: "blocks"}))
	mustExecute(t, s, c, prepareRecordTest(t, s, c, p, "comment.create", protocol.ProjectInput{ID: comment, Issue: a, Content: textPointer(historyText)}))
	return s, c, [][2]string{{"projects", p}, {"issues", a}, {"issues", b}, {"comments", comment}}
}

// The history API answers with the standard JSON encoding of the record's
// decoded history entries, in the page envelope, for whole snapshots and for
// every page of a paged read.
func TestHistoryResponseIsTheEncodedHistory(t *testing.T) {
	s, c, owners := historyFixture(t)
	for n, owner := range owners {
		typ, id := owner[0], owner[1]
		all, e := s.RecordHistory(typ, id)
		if e != nil {
			t.Fatal(e)
		}
		if n == 1 && len(all) != 6 {
			t.Fatalf("issue A has %d revisions", len(all))
		}
		check := func(query string, items []ProjectHistory) *string {
			t.Helper()
			r := httptest.NewRequest("GET", "/v1/"+typ+"/"+id+"/history"+query, nil)
			r.Header.Set("X-Lit-Client-ID", c.ClientID)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			var got struct {
				NextCursor *string `json:"next_cursor"`
				ServerTime string  `json:"server_time"`
			}
			if e := json.Unmarshal(w.Body.Bytes(), &got); e != nil || w.Code != 200 {
				t.Fatal(typ, query, w.Code, e, w.Body.String())
			}
			want := ProjectPage{Items: []any{}, NextCursor: got.NextCursor}
			for _, h := range items {
				want.Items = append(want.Items, h)
			}
			var buf bytes.Buffer
			if e := json.NewEncoder(&buf).Encode(struct {
				Items      []any   `json:"items"`
				NextCursor *string `json:"next_cursor"`
				ServerTime string  `json:"server_time"`
			}{want.Items, want.NextCursor, got.ServerTime}); e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(w.Body.Bytes(), buf.Bytes()) {
				t.Fatalf("%s %s%s answers\n%s\nnot\n%s", typ, id, query, w.Body.Bytes(), buf.Bytes())
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("content type %q", ct)
			}
			return got.NextCursor
		}
		check("?all=true", all)
		limit := min(len(all), 100)
		check("", all[:limit])
		cursor := check("?limit=2", all[:min(2, len(all))])
		for n := 2; cursor != nil; n += 2 {
			cursor = check("?limit=2&cursor="+url.QueryEscape(*cursor), all[n:min(n+2, len(all))])
		}
	}
}
