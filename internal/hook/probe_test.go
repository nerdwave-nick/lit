package hook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

func TestProbeRecognizesALitService(t *testing.T) {
	serve := func(status int, body string, delay time.Duration) string {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/meta" {
				t.Errorf("probed %s", r.URL.Path)
			}
			time.Sleep(delay)
			w.WriteHeader(status)
			w.Write([]byte(body))
		}))
		t.Cleanup(s.Close)
		return s.URL
	}
	meta := `{"data":{"service_id":"` + protocol.UUID() + `","api_major":1}}`
	for _, tc := range []struct {
		name string
		url  string
		ok   bool
	}{
		{"lit", serve(200, meta, 0), true},
		{"server error", serve(500, meta, 0), false},
		{"not lit", serve(200, "<html>", 0), false},
		{"incompatible", serve(200, `{"data":{"service_id":"`+protocol.UUID()+`","api_major":2}}`, 0), false},
		{"no identity", serve(200, `{"data":{"api_major":1}}`, 0), false},
		{"slow", serve(200, meta, 2*time.Second), false},
		{"closed port", "http://127.0.0.1:1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			e := probe(context.Background(), tc.url, 500*time.Millisecond)
			if (e == nil) != tc.ok {
				t.Fatalf("probe: %v", e)
			}
			if time.Since(start) > time.Second {
				t.Fatalf("probe took %s", time.Since(start))
			}
		})
	}
}

func TestProbeStopsWithTheContext(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if e := probe(ctx, s.URL, 5*time.Second); e == nil || time.Since(start) > time.Second {
		t.Fatalf("%v after %s", e, time.Since(start))
	}
}
