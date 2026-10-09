package litclient

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

func pendingFiles(t *testing.T, s *Session) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(s.StateDir, "pending", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func errorCode(e error) string {
	if pe, ok := e.(*protocol.Error); ok {
		return pe.Code
	}
	return ""
}

// breakCleanup replaces the single pending record with a non-empty directory,
// so its later removal fails without depending on permissions or the UID.
func breakCleanup(t *testing.T, s *Session) {
	t.Helper()
	files := pendingFiles(t, s)
	if len(files) != 1 {
		t.Fatalf("pending records %v", files)
	}
	if err := os.Remove(files[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(files[0], 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files[0], "keep"), nil, 0600); err != nil {
		t.Fatal(err)
	}
}

func testSession(t *testing.T, endpoint string) *Session {
	serviceID := protocol.UUID()
	return &Session{StateDir: t.TempDir(), Endpoint: endpoint, HTTP: http.DefaultClient, Context: context.Background(),
		Mapping: Mapping{SchemaVersion: 1, ClientID: protocol.UUID(), ServiceID: serviceID, Endpoint: endpoint}, Meta: protocol.Meta{ServiceID: serviceID, APIMajor: 1}}
}

func clientRecord(id string, revision int64, status string) protocol.Client {
	return protocol.Client{SchemaVersion: 1, ClientID: id, StateRevision: revision, Status: status, Actor: protocol.Actor{Name: "test", Kind: "human"}}
}

func TestCallJournalsMutationBeforeSending(t *testing.T) {
	var s *Session
	var journaled Pending
	var sent []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent, _ = readBody(r)
		files := pendingFiles(t, s)
		if len(files) != 1 {
			t.Errorf("no journal before sending: %v", files)
		} else if p, err := ReadPending(files[0]); err != nil {
			t.Errorf("invalid journal: %v", err)
		} else {
			journaled = p
		}
		json.NewEncoder(w).Encode(protocol.Response{Data: map[string]any{"client": clientRecord(protocol.UUID(), 1, "connected"), "meta": protocol.Meta{ServiceID: s.Meta.ServiceID, APIMajor: 1}}})
	}))
	defer server.Close()
	s = testSession(t, server.URL)
	s.Mapping = Mapping{}
	body := map[string]any{"actor": map[string]any{"name": "test", "kind": "human"}}
	var v map[string]any
	if err := s.Call("POST", "/v1/connect", body, nil, &v, true); err != nil {
		t.Fatal(err)
	}
	if journaled.Method != "POST" || journaled.Path != "/v1/connect" || journaled.ServiceID != s.Meta.ServiceID || journaled.Endpoint != server.URL || !bytes.Equal(journaled.Body, sent) {
		t.Fatalf("journal differs from the sent request: %+v body %s", journaled, sent)
	}
	if files := pendingFiles(t, s); len(files) != 0 {
		t.Fatalf("confirmed journal retained: %v", files)
	}
}

func TestCallTransportFailureIsUncertainAndRetainsEvidence(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	endpoint := server.URL
	server.Close()
	s := testSession(t, endpoint)
	err := s.Call("POST", "/v1/disconnect", map[string]any{}, map[string]string{"If-Match": "\"client:1\""}, nil, true)
	if errorCode(err) != "outcome_uncertain" {
		t.Fatalf("transport failure: %v", err)
	}
	id, _ := err.(*protocol.Error).Details.(map[string]any)["request_id"].(string)
	p, readErr := ReadPending(filepath.Join(s.StateDir, "pending", id+".json"))
	if readErr != nil || p.RequestID != id || p.ClientID != s.Mapping.ClientID || p.Path != "/v1/disconnect" {
		t.Fatalf("evidence for %q: %+v %v", id, p, readErr)
	}
	if err = s.Call("GET", "/v1/meta", nil, nil, nil, false); errorCode(err) != "service_unavailable" {
		t.Fatalf("read failure: %v", err)
	}
	if files := pendingFiles(t, s); len(files) != 1 {
		t.Fatalf("a read journaled or the mutation lost evidence: %v", files)
	}
}

// reconcilingServer fails the mutation with a server error, so its outcome is
// uncertain, then answers read-only reconciliation with status.
func reconcilingServer(t *testing.T, s **Session, status func(protocol.ReconcileRequest) protocol.TransactionStatus, onStatus func()) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/disconnect":
			w.WriteHeader(503)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "unavailable", "message": "lost", "details": map[string]any{}}})
		case "/v1/transaction-status":
			var request protocol.ReconcileRequest
			json.NewDecoder(r.Body).Decode(&request)
			if onStatus != nil {
				onStatus()
			}
			v := status(request)
			v.RequestDigest, v.ServiceID, v.ClientID = protocol.ReconcileDigest(request), (*s).Meta.ServiceID, (*s).Mapping.ClientID
			json.NewEncoder(w).Encode(protocol.Response{Data: v})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
}

func TestCallReconcilesUncertainMutationInline(t *testing.T) {
	ifMatch := map[string]string{"If-Match": "\"client:1\""}
	t.Run("already satisfied", func(t *testing.T) {
		var s *Session
		server := reconcilingServer(t, &s, func(protocol.ReconcileRequest) protocol.TransactionStatus {
			response, _ := json.Marshal(map[string]any{"outcome": "already_disconnected", "client": clientRecord(s.Mapping.ClientID, 2, "disconnected")})
			return protocol.TransactionStatus{Status: "already_satisfied", Proof: "current_validated_state", Outcome: "already_disconnected", Response: response}
		}, nil)
		defer server.Close()
		s = testSession(t, server.URL)
		var v struct {
			Outcome string          `json:"outcome"`
			Client  protocol.Client `json:"client"`
		}
		if err := s.Call("POST", "/v1/disconnect", map[string]any{}, ifMatch, &v, true); err != nil {
			t.Fatal(err)
		}
		if v.Outcome != "already_disconnected" || v.Client.Status != "disconnected" {
			t.Fatalf("recovered result %+v", v)
		}
		if files := pendingFiles(t, s); len(files) != 0 {
			t.Fatalf("confirmed evidence retained: %v", files)
		}
	})
	t.Run("uncommitted", func(t *testing.T) {
		var s *Session
		server := reconcilingServer(t, &s, func(protocol.ReconcileRequest) protocol.TransactionStatus {
			return protocol.TransactionStatus{Status: "uncommitted", Proof: "original_client_revision_unchanged"}
		}, nil)
		defer server.Close()
		s = testSession(t, server.URL)
		if err := s.Call("POST", "/v1/disconnect", map[string]any{}, ifMatch, nil, true); errorCode(err) != "mutation_uncommitted" {
			t.Fatalf("uncommitted: %v", err)
		}
		if files := pendingFiles(t, s); len(files) != 0 {
			t.Fatalf("uncommitted evidence retained: %v", files)
		}
	})
	t.Run("still uncertain", func(t *testing.T) {
		var s *Session
		server := reconcilingServer(t, &s, func(protocol.ReconcileRequest) protocol.TransactionStatus {
			return protocol.TransactionStatus{Status: "uncertain", Proof: "client_state_changed_without_receipt"}
		}, nil)
		defer server.Close()
		s = testSession(t, server.URL)
		if err := s.Call("POST", "/v1/disconnect", map[string]any{}, ifMatch, nil, true); errorCode(err) != "outcome_uncertain" {
			t.Fatalf("uncertain: %v", err)
		}
		if files := pendingFiles(t, s); len(files) != 1 {
			t.Fatalf("uncertain evidence lost: %v", files)
		}
	})
}

func TestCallCleanupWarningsGoToWarningWriter(t *testing.T) {
	t.Run("confirmed", func(t *testing.T) {
		var s *Session
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			breakCleanup(t, s)
			json.NewEncoder(w).Encode(protocol.Response{Data: map[string]any{"outcome": "applied", "client": clientRecord(s.Mapping.ClientID, 2, "disconnected")}})
		}))
		defer server.Close()
		for _, warnings := range []*bytes.Buffer{new(bytes.Buffer), nil} {
			s = testSession(t, server.URL)
			if warnings != nil {
				s.Warnings = warnings
			}
			if err := s.Call("POST", "/v1/disconnect", map[string]any{}, map[string]string{"If-Match": "\"client:1\""}, nil, true); err != nil {
				t.Fatal(err)
			}
			if warnings != nil && (!strings.HasPrefix(warnings.String(), "warning: confirmed operation; pending record cleanup failed:") || strings.Count(warnings.String(), "\n") != 1) {
				t.Fatalf("warnings %q", warnings)
			}
		}
	})
	t.Run("uncommitted", func(t *testing.T) {
		var s *Session
		server := reconcilingServer(t, &s, func(protocol.ReconcileRequest) protocol.TransactionStatus {
			return protocol.TransactionStatus{Status: "uncommitted", Proof: "original_client_revision_unchanged"}
		}, func() { breakCleanup(t, s) })
		defer server.Close()
		s = testSession(t, server.URL)
		var warnings bytes.Buffer
		s.Warnings = &warnings
		if err := s.Call("POST", "/v1/disconnect", map[string]any{}, map[string]string{"If-Match": "\"client:1\""}, nil, true); errorCode(err) != "mutation_uncommitted" {
			t.Fatalf("uncommitted: %v", err)
		}
		if !strings.HasPrefix(warnings.String(), "warning: pending record cleanup failed:") {
			t.Fatalf("warnings %q", &warnings)
		}
	})
}

func readBody(r *http.Request) ([]byte, error) {
	var b bytes.Buffer
	_, err := b.ReadFrom(r.Body)
	return b.Bytes(), err
}
