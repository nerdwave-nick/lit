package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUnknownMutationOutcomeRemainsPending(t *testing.T) {
	for _, data := range []string{`{"outcome":"future_unknown","results":[]}`, `{}`, `{"client":{"client_id":"broken"}}`} {
		t.Run(data, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"data":` + data + `,"server_time":"2026-09-29T12:00:00Z"}`))
			}))
			defer srv.Close()
			a := &App{Session: litclient.Session{StateDir: t.TempDir(), Endpoint: srv.URL, HTTP: srv.Client(), Context: context.Background()}, Err: new(bytes.Buffer)}
			var v any
			e := a.Call("POST", "/v1/connect", map[string]any{}, nil, &v, true)
			if e == nil {
				t.Fatal("accepted malformed mutation response")
			}
			p, _ := filepath.Glob(filepath.Join(a.StateDir, "pending", "*.json"))
			if len(p) != 1 {
				t.Fatal("lost uncertainty evidence", p)
			}
		})
	}
}
func TestPendingFailureDoesNotTransmit(t *testing.T) {
	sent := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sent = true }))
	defer srv.Close()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pending"), []byte("blocked"), 0600)
	a := &App{Session: litclient.Session{StateDir: dir, Endpoint: srv.URL, HTTP: srv.Client(), Context: context.Background()}}
	e := a.Call("POST", "/v1/connect", map[string]any{}, nil, nil, true)
	if e == nil || sent {
		t.Fatal("request sent without pending evidence")
	}
}
func TestPreserveServiceTime(t *testing.T) {
	const stamp = "2026-01-01T00:00:00Z"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"ok": true}, "server_time": stamp})
	}))
	defer srv.Close()
	out := new(bytes.Buffer)
	a := &App{Session: litclient.Session{Endpoint: srv.URL, HTTP: srv.Client(), Context: context.Background()}, Out: out, Err: new(bytes.Buffer), Format: "json"}
	var v any
	if e := a.Call("GET", "/v1/meta", nil, nil, &v, false); e != nil {
		t.Fatal(e)
	}
	a.Print(Result{Items: []any{v}})
	var r Result
	json.Unmarshal(out.Bytes(), &r)
	if r.ServerTime != stamp {
		t.Fatal("fabricated server time", r.ServerTime)
	}
}
