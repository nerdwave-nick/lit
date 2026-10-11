package litclient_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// TransactionStatus reconciles retained evidence read-only: it never resends
// the request and never removes the evidence, even once its outcome is known.
func TestTransactionStatusInspectsWithoutReplayOrDeletion(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	s := h.connect("status", "Human")
	other := h.connect("status-other", "Human")
	project, issues := seed(t, s, "status", "A")
	a := issues["A"]

	// The service commits the update, but its response is lost and inline
	// reconciliation is unavailable, so the outcome stays uncertain.
	h.setIntercept(func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/v1/transactions":
			h.backend.ServeHTTP(httptest.NewRecorder(), r)
			w.WriteHeader(http.StatusBadGateway)
			return true
		case "/v1/transaction-status":
			protocol.WriteError(w, protocol.E(503, "service_unavailable", "reconciliation offline"))
			return true
		}
		return false
	})
	plan, err := s.Prepare(ctx, litclient.UpdateIssue(project, a.ID, a.Revision, litclient.IssueChange{Title: str("A2")}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Commit(ctx, plan)
	var pe *protocol.Error
	if litclient.Code(err) != "outcome_uncertain" || !errors.As(err, &pe) {
		t.Fatalf("lost response: %v", err)
	}
	requestID, _ := pe.Details.(map[string]any)["request_id"].(string)
	evidence := filepath.Join(h.stateDir, "pending", requestID+".json")
	if files := h.pending(); len(files) != 1 || files[0] != evidence {
		t.Fatalf("retained evidence %v, want %s", files, evidence)
	}
	h.setIntercept(nil)
	commits := h.count("POST /v1/transactions")

	for range 2 {
		status, err := s.TransactionStatus(ctx, litclient.StatusOptions{})
		if err != nil || len(status) != 1 || status[0].RequestID != requestID || status[0].Path != evidence || status[0].Status == "uncertain" || status[0].Response != nil {
			t.Fatalf("status: %+v %v", status, err)
		}
	}
	if h.count("POST /v1/transactions") != commits {
		t.Fatal("status inspection replayed the request")
	}
	if _, err = os.Stat(evidence); err != nil {
		t.Fatalf("status inspection removed evidence: %v", err)
	}
	if got := issue(t, s, project, a.ID); got.Title != "A2" || got.Revision != a.Revision+1 {
		t.Fatalf("committed once: %+v", got)
	}

	// Scope: a session sees only its own requests unless it asks for all.
	if status, err := other.TransactionStatus(ctx, litclient.StatusOptions{}); err != nil || len(status) != 0 {
		t.Fatalf("other session's scope: %+v %v", status, err)
	}
	if status, err := other.TransactionStatus(ctx, litclient.StatusOptions{All: true}); err != nil || len(status) != 1 {
		t.Fatalf("all requests: %+v %v", status, err)
	}

	// A request whose original call still holds its lock stays uncertain.
	unlock, err := litclient.AcquireLock(filepath.Join(h.stateDir, "locks", "pending", requestID+".lock"), time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	busy, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	status, err := s.TransactionStatus(busy, litclient.StatusOptions{})
	cancel()
	unlock()
	if err != nil || len(status) != 1 || status[0].Status != "uncertain" || status[0].Proof != "request_still_active" {
		t.Fatalf("active request: %+v %v", status, err)
	}

	// Corrupt evidence fails closed before any request is made.
	if err = os.WriteFile(filepath.Join(h.stateDir, "pending", "corrupt.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	reconciles := h.count("POST /v1/transaction-status")
	if _, err = s.TransactionStatus(ctx, litclient.StatusOptions{}); litclient.Code(err) != "invalid_local_state" || h.count("POST /v1/transaction-status") != reconciles {
		t.Fatalf("corrupt evidence: %v", err)
	}
}
