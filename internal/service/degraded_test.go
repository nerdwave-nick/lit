package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// In degraded mode every route, reads and the meta route included, answers
// 503 recovery_required with the reason, and nothing reaches storage.
func TestDegradedModeAnswersEveryRequestWithRecoveryRequired(t *testing.T) {
	const reason = "database integrity check failed: integrity_check: wrong # of entries in index comments_issue"
	h := Degraded(reason)
	for _, r := range []struct{ method, path, body string }{
		{"GET", "/v1/meta", ""},
		{"POST", "/v1/connect", `{"actor":{"name":"me","kind":"agent"}}`},
		{"GET", "/v1/projects", ""},
		{"POST", "/v1/search", `{"q":"x"}`},
		{"POST", "/v1/transactions", `{}`},
		{"POST", "/v1/claims", `{}`},
		{"GET", "/elsewhere", ""},
	} {
		req := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
		req.Header.Set("X-Lit-Client-ID", "00000000-0000-4000-8000-000000000001")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var body struct {
			Error struct {
				Code    string         `json:"code"`
				Message string         `json:"message"`
				Details map[string]any `json:"details"`
			} `json:"error"`
			ServerTime string `json:"server_time"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
			t.Fatalf("%s %s: %v: %s", r.method, r.path, e, w.Body)
		}
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Content-Type") != "application/json" || body.Error.Code != "recovery_required" || body.ServerTime == "" {
			t.Fatalf("%s %s: %d %s", r.method, r.path, w.Code, w.Body)
		}
		if !strings.Contains(body.Error.Message, reason) || !strings.Contains(body.Error.Message, "degraded mode") || body.Error.Details["reason"] != reason || body.Error.Details["mode"] != "degraded" {
			t.Fatalf("%s %s: %s", r.method, r.path, w.Body)
		}
	}
}
