package litclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

func pendingRequest(p Pending) protocol.ReconcileRequest {
	return protocol.ReconcileRequest{ServiceID: p.ServiceID, ClientID: p.ClientID, Method: p.Method, Path: p.Path, Body: p.Body, Headers: p.Headers}
}

// ValidatePending checks that p is a well-formed retained request this client
// could have sent.
func ValidatePending(p Pending) error {
	if p.SchemaVersion != 1 || !protocol.ValidUUID(p.RequestID) || !protocol.ValidUUID(p.ServiceID) || p.ClientID != "" && !protocol.ValidUUID(p.ClientID) {
		return fmt.Errorf("invalid pending identity or format version")
	}
	if _, e := time.Parse(time.RFC3339Nano, p.CreatedAt); e != nil {
		return fmt.Errorf("invalid pending timestamp")
	}
	u, e := url.Parse(p.Endpoint)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("invalid pending endpoint")
	}
	if p.ClientID == "" && p.Path != "/v1/connect" {
		return fmt.Errorf("missing pending client identity")
	}
	if !(p.Method == "POST" && (p.Path == "/v1/connect" || p.Path == "/v1/disconnect" || p.Path == "/v1/transactions" || p.Path == "/v1/operations") || p.Method == "PATCH" && p.Path == "/v1/clients/"+p.ClientID) {
		return fmt.Errorf("unsupported pending method/path")
	}
	var body map[string]json.RawMessage
	if e := protocol.Decode(p.Body, &body); e != nil || body == nil {
		return fmt.Errorf("invalid pending body")
	}
	seen := map[string]bool{}
	for k, v := range p.Headers {
		lower := strings.ToLower(k)
		if seen[lower] {
			return fmt.Errorf("duplicate pending header")
		}
		seen[lower] = true
		switch lower {
		case "content-type":
			if v != "application/json" {
				return fmt.Errorf("invalid pending content type")
			}
		case "if-match":
		case "x-lit-client-id":
			if v != p.ClientID {
				return fmt.Errorf("pending client header differs")
			}
		default:
			return fmt.Errorf("unsupported pending header")
		}
	}
	return nil
}

// ReadPending reads and validates one retained request envelope.
func ReadPending(path string) (Pending, error) {
	var p Pending
	st, e := os.Lstat(path)
	if e != nil {
		return p, e
	}
	if !st.Mode().IsRegular() || st.Size() > 32<<20 {
		return p, fmt.Errorf("pending record must be a bounded regular file")
	}
	f, e := os.Open(path)
	if e != nil {
		return p, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 32<<20+1))
	if e != nil {
		return p, e
	}
	if len(b) > 32<<20 {
		return p, fmt.Errorf("pending record exceeds limit")
	}
	if e = protocol.Decode(b, &p); e != nil {
		return p, e
	}
	return p, ValidatePending(p)
}

// Reconcile asks the service, read-only, what became of the retained request.
// It never replays the request.
func (s *Session) Reconcile(p Pending) (protocol.TransactionStatus, error) {
	v := protocol.TransactionStatus{RequestID: p.RequestID, Status: "uncertain", Proof: "registration_identity_not_received"}
	if p.ClientID == "" {
		return v, nil
	}
	e := s.Call("POST", "/v1/transaction-status", pendingRequest(p), nil, &v, false)
	if e != nil {
		return v, e
	}
	if !validStatus(p, v) {
		return v, protocol.E(500, "invalid_response", "reconciliation proof does not match retained request")
	}
	v.RequestID = p.RequestID
	return v, nil
}
