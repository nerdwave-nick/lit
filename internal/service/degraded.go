package service

import (
	"net/http"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// Degraded serves lit-server's degraded mode, entered when the database fails
// its startup checks: every request, /v1/meta and every read included, gets
// 503 recovery_required whose message and details carry reason. No route
// stays readable, so nothing reads the damaged database (a read meeting
// damage would make lit-server exit and restart into the same state) and
// every write is refused. Only an operator leaves degraded mode, by restoring
// a backup or repairing the database and restarting lit-server.
//
// The response is the protocol's error envelope:
//
//	{"error": {"code": "recovery_required",
//	           "message": "lit-server is in degraded mode: REASON. ...",
//	           "details": {"mode": "degraded", "reason": "REASON"}},
//	 "server_time": "..."}
func Degraded(reason string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		e := protocol.E(http.StatusServiceUnavailable, "recovery_required",
			"lit-server is in degraded mode: "+reason+". An operator must restore a backup or repair the database, then restart lit-server; see docs/recovery.md")
		e.Details = map[string]any{"mode": "degraded", "reason": reason}
		protocol.WriteError(w, e)
	})
}
