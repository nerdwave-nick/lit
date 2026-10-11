package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

func readCapFixture(t *testing.T, reads int) (*Server, protocol.Client) {
	t.Helper()
	s := openTestServer(t, t.TempDir(), Config{Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"feat"}, ReadConcurrency: reads})
	c := protocol.Client{SchemaVersion: 1, ClientID: protocol.UUID(), StateRevision: 1, Status: "connected", Actor: protocol.Actor{Name: "Test", Kind: "human"}}
	if e := s.SaveClient(c); e != nil {
		t.Fatal(e)
	}
	return s, c
}

// serveCode serves one request in process under ctx and returns its status
// and error code.
func serveCode(s *Server, ctx context.Context, id, method, path, body string) (int, string) {
	r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	r.Header.Set("X-Lit-Client-ID", id)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var v struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	return w.Code, v.Error.Code
}

// Reads over the configured cap wait for a snapshot to close, while writes
// need none and go ahead.
func TestReadsOverTheCapWait(t *testing.T) {
	s, c := readCapFixture(t, 1)
	// Open two snapshots at once where the cap allows it, so that a read past
	// a wrongly applied cap would find an open connection and finish at once.
	first, e := s.DB.Read(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if second, e := s.DB.Read(short); e == nil {
		second.Close()
	}
	cancel()
	first.Close()
	held, e := s.DB.Read(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan int, 1)
	go func() {
		code, _ := serveCode(s, context.Background(), c.ClientID, "GET", "/v1/clients/"+c.ClientID, "")
		done <- code
	}()
	waitInStack(t, "storage.(*DB).Read")
	within(t, "a write", func() {
		renamed := c
		renamed.Actor.Name = "Renamed"
		if e := s.SaveClient(renamed); e != nil {
			t.Error(e)
		}
	})
	select {
	case code := <-done:
		t.Fatal("read over the cap did not wait", code)
	case <-time.After(100 * time.Millisecond):
	}
	held.Close()
	if code := <-done; code != 200 {
		t.Fatal("waiting read", code)
	}
}

// A read that ends while waiting over the cap is a retryable storage_busy,
// never a storage_error; so is a search whose request is cancelled.
func TestCancelledReadsAreBusy(t *testing.T) {
	s, c := readCapFixture(t, 1)
	held, e := s.DB.Read(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if status, code := serveCode(s, ctx, c.ClientID, "GET", "/v1/claims", ""); status != 503 || code != "storage_busy" {
		t.Fatal("read past the cap wait", status, code)
	}
	held.Close()
	// The search is parked inside its snapshot, its request is cancelled,
	// and then it continues.
	clock := newParkedClock(s, time.Now().UTC())
	ctx, cancel = context.WithCancel(context.Background())
	finish := park(t, clock, func() [2]any {
		status, code := serveCode(s, ctx, c.ClientID, "POST", "/v1/search", `{"query":"anything","scope":{"all_projects":true}}`)
		return [2]any{status, code}
	})
	cancel()
	if got := finish(); got != [2]any{503, "storage_busy"} {
		t.Fatal("cancelled search", got)
	}
	if status, code := serveCode(s, context.Background(), c.ClientID, "POST", "/v1/search", `{"query":"anything","scope":{"all_projects":true}}`); status != 200 {
		t.Fatal("search after cancellation", status, code)
	}
}
