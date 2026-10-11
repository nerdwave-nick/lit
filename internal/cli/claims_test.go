package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestClaimArguments(t *testing.T) {
	for _, argv := range [][]string{
		{"claims", "renew", "--all", "A"}, {"claims", "acquire", "A", "--for", "2h"},
		{"claims", "renew", "A", "--for", "0s"}, {"claims", "acquire", "A", "--for", "1m", "--until", "2026-01-01T00:00:00Z"},
		{"claims", "release"}, {"claims", "get", "A", "--force"}, {"claims", "list", "A"},
	} {
		a, err := Parse(argv)
		if err == nil {
			err = validateArgs(a)
		}
		if err == nil {
			t.Fatalf("accepted %v", argv)
		}
	}
	for _, argv := range [][]string{{"claims", "renew", "--all"}, {"claims", "get", "A", "B"}, {"claims", "acquire", "A", "--force", "--for", "1h"}, {"claims", "list"}} {
		a, e := Parse(argv)
		if e != nil {
			t.Fatal(e)
		}
		if e = validateArgs(a); e != nil {
			t.Fatal(e)
		}
	}
}
func TestClaimMalformedSuccessRetainsPending(t *testing.T) {
	for _, data := range []string{`{"outcome":"applied"}`, `{"outcome":"applied","items":null}`, `{"outcome":"applied","items":[{"issue_id":"invalid","claim":null}]}`} {
		t.Run(data, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"data":` + data + `}`)) }))
			defer server.Close()
			app := &App{Session: litclient.Session{StateDir: t.TempDir(), Endpoint: server.URL, HTTP: server.Client(), Context: context.Background()}, Err: new(bytes.Buffer)}
			if e := app.Call("POST", "/v1/operations", map[string]any{}, nil, nil, true); e == nil {
				t.Fatal("accepted malformed success")
			}
			pending, _ := filepath.Glob(filepath.Join(app.StateDir, "pending", "*.json"))
			if len(pending) != 1 {
				t.Fatal("lost pending", pending)
			}
		})
	}
}

func TestClaimRenewAllFencesExactSnapshot(t *testing.T) {
	id, owner, token := protocol.UUID(), protocol.UUID(), protocol.UUID()
	const clock = "2026-01-01T00:00:00Z"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			if r.URL.Path != "/v1/claim-snapshots" {
				t.Errorf("path %s", r.URL.Path)
			}
			var query map[string]any
			json.NewDecoder(r.Body).Decode(&query)
			if len(query) != 1 || query["owner_client_id"] != owner {
				t.Errorf("query %v", query)
			}
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": []any{map[string]any{"issue_id": id, "claim": map[string]any{"token": token}}}, "server_time": clock}, "server_time": clock})
			return
		}
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		if r.URL.Path != "/v1/operations" || payload["selection"] != "all_owned" || payload["owner_client_id"] != owner || payload["operation"] != "claims.renew" {
			t.Errorf("payload %v", payload)
		}
		items := payload["items"].([]any)
		if len(items) != 1 {
			t.Error(items)
		}
		item := items[0].(map[string]any)
		if item["issue_id"] != id || item["token"] != token || item["extend_to"] != "2026-01-01T00:30:00Z" {
			t.Error(item)
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"outcome": "applied", "items": []any{map[string]any{"issue_id": id, "claim": protocol.Claim{SchemaVersion: 1, IssueID: id, OwnerClientID: owner, Token: token, AcquiredAt: clock, ExpiresAt: "2026-01-01T00:30:00Z"}}}}, "server_time": clock})
	}))
	defer server.Close()
	args, _ := Parse([]string{"claims", "renew", "--all"})
	app := &App{Session: litclient.Session{Mapping: Mapping{ClientID: owner}, StateDir: t.TempDir(), Endpoint: server.URL, HTTP: server.Client(), Context: context.Background()}, Args: args, Err: new(bytes.Buffer)}
	result, e := app.claims()
	if e != nil || result.Outcome != "applied" || calls != 2 {
		t.Fatal(result, e, calls)
	}
	pending, _ := filepath.Glob(filepath.Join(app.StateDir, "pending", "*.json"))
	if len(pending) != 0 {
		t.Fatal(pending)
	}
}
