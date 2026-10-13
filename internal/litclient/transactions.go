package litclient

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// RequestStatus is the reconciled status of one retained request and where its
// evidence lives.
type RequestStatus struct {
	protocol.TransactionStatus
	Path string `json:"path"`
}

// StatusOptions select which retained requests TransactionStatus inspects.
type StatusOptions struct {
	// All inspects every retained request in StateDir; otherwise only those of
	// this session's client and service.
	All bool
	// Endpoint overrides each request's recorded endpoint when non-empty.
	Endpoint string
}

// TransactionStatus inspects retained pending requests read-only. It never
// replays, moves or deletes evidence: resolved records stay for `lit
// transactions status` to clean up. Every envelope is validated before any
// request is made; corrupt evidence fails with invalid_local_state. A request
// whose original call still holds its lock is reported uncertain with proof
// request_still_active; an unreachable service yields uncertain statuses whose
// proof names the failure.
func (s *Session) TransactionStatus(ctx context.Context, o StatusOptions) ([]RequestStatus, error) {
	defer s.bind(ctx)()
	dir := filepath.Join(s.StateDir, "pending")
	entries, e := os.ReadDir(dir)
	if e != nil && !os.IsNotExist(e) {
		return nil, protocol.E(500, "invalid_local_state", e.Error())
	}
	records := []RequestStatus{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") || entry.IsDir() {
			return nil, protocol.E(500, "invalid_local_state", "unrecognized pending evidence: "+entry.Name())
		}
		path := filepath.Join(dir, entry.Name())
		p, e := ReadPending(path)
		if e != nil {
			return nil, protocol.E(500, "invalid_local_state", e.Error())
		}
		if entry.Name() != p.RequestID+".json" {
			return nil, protocol.E(500, "invalid_local_state", "pending filename differs from request identity")
		}
		if o.All || p.ServiceID == s.Mapping.ServiceID && p.ClientID == s.Mapping.ClientID {
			records = append(records, RequestStatus{TransactionStatus: protocol.TransactionStatus{RequestID: p.RequestID}, Path: path})
		}
	}
	out := []RequestStatus{}
	for _, r := range records {
		v, gone, e := s.inspect(r.Path, r.RequestID, o.Endpoint)
		if e != nil {
			return nil, e
		}
		if !gone {
			out = append(out, RequestStatus{TransactionStatus: v, Path: r.Path})
		}
	}
	return out, nil
}

// inspect reconciles one retained request under its request lock. gone reports
// that its original call resolved and removed it meanwhile.
func (s *Session) inspect(path, requestID, endpoint string) (v protocol.TransactionStatus, gone bool, err error) {
	deadline := time.Now().Add(30 * time.Second)
	if d, ok := s.Context.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	unlock, e := AcquireLock(filepath.Join(s.StateDir, "locks", "pending", requestID+".lock"), deadline)
	if e != nil {
		return protocol.TransactionStatus{RequestID: requestID, Status: "uncertain", Proof: "request_still_active"}, false, nil
	}
	defer unlock()
	// Re-read after locking: the original call may have resolved and removed it.
	p, e := ReadPending(path)
	if os.IsNotExist(e) {
		return v, true, nil
	}
	if e != nil || p.RequestID != requestID {
		return v, false, protocol.E(500, "invalid_local_state", "pending evidence changed while it was inspected")
	}
	remote := Session{StateDir: s.StateDir, HTTP: s.HTTP, Context: s.Context, Warnings: s.Warnings, Endpoint: strings.TrimRight(p.Endpoint, "/"),
		Mapping: Mapping{SchemaVersion: 1, ClientID: p.ClientID, ServiceID: p.ServiceID, Endpoint: p.Endpoint}}
	if endpoint != "" {
		q := p
		q.Endpoint = endpoint
		if e = ValidatePending(q); e != nil {
			return v, false, protocol.E(400, "invalid_endpoint", e.Error())
		}
		remote.Endpoint = strings.TrimRight(endpoint, "/")
	}
	var meta protocol.Meta
	e = remote.Call("GET", "/v1/meta", nil, nil, &meta, false)
	if e == nil && (meta.ServiceID != p.ServiceID || meta.APIMajor != 1) {
		return v, false, protocol.E(409, "wrong_service", "endpoint is bound to a different service")
	}
	v = protocol.TransactionStatus{RequestID: p.RequestID, Status: "uncertain", Proof: "service_unavailable"}
	if e == nil {
		remote.Meta = meta
		v, e = remote.Reconcile(p)
	}
	if e != nil {
		v = protocol.TransactionStatus{RequestID: p.RequestID, Status: "uncertain", Proof: "reconciliation_unavailable"}
		if pe, ok := e.(*protocol.Error); ok {
			if pe.Status == 400 || pe.Status == 422 || pe.Code == "wrong_client" || pe.Code == "wrong_service" {
				return v, false, e
			}
			v.Proof = pe.Code
		}
	}
	// Status reports evidence, not synthesized HTTP bodies.
	v.Response = nil
	s.ServerTime = remote.ServerTime
	return v, false, nil
}
