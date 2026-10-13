package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

func searchLimitFixture(t *testing.T, reads int, timeout time.Duration) (*Server, protocol.Client) {
	t.Helper()
	s := openTestServer(t, t.TempDir(), Config{Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"feat"}, ReadConcurrency: reads, SearchTimeout: Duration(timeout)})
	c := protocol.Client{SchemaVersion: 1, ClientID: protocol.UUID(), StateRevision: 1, Status: "connected", Actor: protocol.Actor{Name: "Test", Kind: "human"}}
	if e := s.SaveClient(c); e != nil {
		t.Fatal(e)
	}
	return s, c
}

// searchBody is an all-projects search request for query.
func searchBody(query string) string {
	b, _ := json.Marshal(map[string]any{"query": query, "scope": map[string]any{"all_projects": true}})
	return string(b)
}

// serveError serves one request in process and returns its status, error
// code and message.
func serveError(s *Server, ctx context.Context, id, method, path, body string) (int, string, string) {
	r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	r.Header.Set("X-Lit-Client-ID", id)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var v struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	return w.Code, v.Error.Code, v.Error.Message
}

// Queries are limited to 1,000 bytes, counted in UTF-8 bytes rather than
// characters; a longer one is refused before anything is searched.
func TestSearchQueryLengthLimit(t *testing.T) {
	s, c := searchLimitFixture(t, 8, time.Minute)
	for _, v := range []struct {
		query string
		want  int
	}{
		{strings.Repeat("a", 1000), 200},
		{strings.Repeat("a", 1001), 422},
		{strings.Repeat("€", 333) + "a", 200},  // 1,000 bytes, 334 characters
		{strings.Repeat("€", 333) + "ab", 422}, // 1,001 bytes, 335 characters
		{strings.Repeat("€", 334), 422},        // 1,002 bytes
	} {
		status, code, msg := serveError(s, context.Background(), c.ClientID, "POST", "/v1/search", searchBody(v.query))
		if status != v.want || v.want == 422 && (code != "validation_failed" || !strings.Contains(msg, "1000 bytes")) {
			t.Errorf("%d-byte query: %d %s %q, want %d", len(v.query), status, code, msg, v.want)
		}
	}
}

// A deliberately slow search, parked inside its snapshot until its deadline
// has passed, fails with 504 search_timeout and a message to narrow the
// query or scope. It frees its read snapshot: with a cap of one, the next
// read and the next search are served.
func TestSlowSearchTimesOut(t *testing.T) {
	const timeout = 20 * time.Millisecond
	s, c := searchLimitFixture(t, 1, timeout)
	clock := newParkedClock(s, time.Now().UTC())
	finish := park(t, clock, func() [3]any {
		status, code, msg := serveError(s, context.Background(), c.ClientID, "POST", "/v1/search", searchBody("anything"))
		return [3]any{status, code, msg}
	})
	// This test is about the deadline itself, so it waits well past it.
	time.Sleep(10 * timeout)
	got := finish()
	if got[0] != 504 || got[1] != "search_timeout" || !strings.Contains(got[2].(string), "narrow the query or its scope") || !strings.Contains(got[2].(string), "20ms") {
		t.Fatal("slow search", got)
	}
	// The searches below are not slow; a generous deadline keeps them from
	// timing out on a loaded host.
	s.Config.SearchTimeout = Duration(time.Minute)
	within(t, "a read after the timed-out search", func() {
		if status, code, _ := serveError(s, context.Background(), c.ClientID, "GET", "/v1/claims", ""); status != 200 {
			t.Error("read after timeout", status, code)
		}
		if status, code, _ := serveError(s, context.Background(), c.ClientID, "POST", "/v1/search", searchBody("anything")); status != 200 {
			t.Error("search after timeout", status, code)
		}
	})
}

// The deadline bounds the search itself, from the moment its snapshot is
// open: waiting for a read snapshot over the cap does not count against it
// (that wait is bounded by the request, as for every read). The deadline is
// long enough for the search itself on a loaded host.
func TestSearchDeadlineStartsWithItsSnapshot(t *testing.T) {
	const timeout = time.Second
	s, c := searchLimitFixture(t, 1, timeout)
	held, e := s.DB.Read(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan int, 1)
	go func() {
		status, _, _ := serveError(s, context.Background(), c.ClientID, "POST", "/v1/search", searchBody("anything"))
		done <- status
	}()
	waitInStack(t, "storage.(*DB).Read")
	time.Sleep(timeout + timeout/2)
	held.Close()
	if status := <-done; status != 200 {
		t.Fatal("search that waited past its deadline for a snapshot", status)
	}
}

// A server configured without a deadline still has one: the default.
func TestSearchTimeoutDefaultsWhenUnset(t *testing.T) {
	s, _ := searchLimitFixture(t, 1, 0)
	if got := s.searchTimeout(); got != DefaultSearchTimeout {
		t.Fatal(got)
	}
}
