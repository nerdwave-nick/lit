package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// A durable request's body is checked in a fixed order: the client first,
// then the route's own checks, then the body (JSON, schema version, hash,
// route match), then the transaction. Each failure keeps its code.
func TestDurableRequestErrorsKeepTheirOrder(t *testing.T) {
	_, h := fixture(t)
	status, v := request(t, h, "POST", "/v1/connect", `{"actor":{"name":"A","kind":"agent"}}`, "", "")
	if status != 201 {
		t.Fatal(status, v)
	}
	id := v["data"].(map[string]any)["client"].(map[string]any)["client_id"].(string)
	project := protocol.UUID()
	preview := func(t *testing.T, body string) protocol.Prepared {
		t.Helper()
		status, v := request(t, h, "POST", "/v1/transaction-previews", body, id, "")
		if status != 200 {
			t.Fatal(status, v)
		}
		var p protocol.Prepared
		if e := json.Unmarshal(mustJSON(v["data"]), &p); e != nil {
			t.Fatal(e)
		}
		return p
	}
	create := preview(t, `{"schema_version":1,"operation":"project.create","items":[{"id":"`+project+`","title":"feat/order"}]}`)
	durable := func(p protocol.Prepared, hash string, version int) string {
		return string(mustJSON(protocol.DurableRequest{SchemaVersion: version, Intent: p.Intent, RequestHash: hash}))
	}
	valid := durable(create, create.RequestHash, 1)
	for _, tc := range []struct {
		name, method, path, body, client, code string
		status                                 int
	}{
		{"no client before a bad body", "POST", "/v1/transactions", `{"intent":`, "", "client_required", 400},
		{"unknown client before a bad body", "POST", "/v1/transactions", `{"intent":`, protocol.UUID(), "unknown_client", 404},
		{"truncated JSON", "POST", "/v1/transactions", `{"intent":`, id, "invalid_json", 400},
		{"unknown field", "POST", "/v1/transactions", strings.Replace(valid, `{`, `{"extra":1,`, 1), id, "unknown_field", 400},
		{"schema version before the hash", "POST", "/v1/transactions", durable(create, "sha256:0", 2), id, "validation_failed", 422},
		{"hash", "POST", "/v1/transactions", durable(create, "sha256:0", 1), id, "request_hash_mismatch", 422},
		{"route resource", "POST", "/v1/issues", valid, id, "validation_failed", 422},
		{"unknown target before the body", "PATCH", "/v1/issues/" + protocol.UUID(), `{"intent":`, id, "not_found", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, v := request(t, h, tc.method, tc.path, tc.body, tc.client, "")
			if status != tc.status || v["error"].(map[string]any)["code"] != tc.code {
				t.Fatal(status, v)
			}
		})
	}
	if status, v = request(t, h, "POST", "/v1/transactions", valid, id, ""); status != 200 {
		t.Fatal(status, v)
	}
	// A disconnected client is refused before its body is read.
	if status, v = request(t, h, "POST", "/v1/disconnect", `{}`, id, `"client:1"`); status != 200 {
		t.Fatal(status, v)
	}
	if status, v = request(t, h, "POST", "/v1/transactions", `{"intent":`, id, ""); status != 409 || v["error"].(map[string]any)["code"] != "client_disconnected" {
		t.Fatal(status, v)
	}
}
