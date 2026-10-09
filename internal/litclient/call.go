package litclient

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// Call sends one request and decodes its data into dest. A mutation is first
// journaled to StateDir/pending/<request_id>.json under its request lock and is
// never sent if that fails. An outcome that cannot be established returns
// outcome_uncertain and retains the journal; unless the context has ended, Call
// then reconciles it read-only and, on proof, returns the confirmed result or
// mutation_uncommitted and removes the journal. It never replays a request.
// Responses are limited to 32 MiB and must match the request they answer.
func (s *Session) Call(method, path string, body any, headers map[string]string, dest any, mutation bool) (callErr error) {
	var b []byte
	var e error
	if body != nil {
		b, e = json.Marshal(body)
		if e != nil {
			return e
		}
	}
	req, e := http.NewRequestWithContext(s.Context, method, s.Endpoint+path, bytes.NewReader(b))
	if e != nil {
		return protocol.E(400, "invalid_endpoint", e.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	if s.Mapping.ClientID != "" {
		req.Header.Set("X-Lit-Client-ID", s.Mapping.ClientID)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	pending := ""
	var retained Pending
	var unlockPending func()
	defer func() {
		if unlockPending != nil {
			unlockPending()
		}
	}()
	defer func() {
		if pending == "" || callErr == nil {
			return
		}
		pe, ok := callErr.(*protocol.Error)
		if !ok || pe.Code != "outcome_uncertain" {
			return
		}
		pe.Details = map[string]any{"request_id": retained.RequestID}
		if s.Context.Err() != nil {
			return
		}
		status, e := s.Reconcile(retained)
		if e != nil || status.Status == "uncertain" {
			return
		}
		if status.Status == "uncommitted" {
			callErr = protocol.E(503, "mutation_uncommitted", "read-only reconciliation found the request uncommitted at the observed snapshot; it was not retried")
			if e = os.Remove(pending); e != nil {
				s.warn("warning: pending record cleanup failed:", e)
			}
			return
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(status.Response, &obj) != nil || !mutationResponseMatches(retained, status.Response) {
			return
		}
		if dest != nil && DecodeResponseData(status.Response, dest) != nil {
			return
		}
		if e = os.Remove(pending); e != nil {
			s.warn("warning: confirmed operation; pending record cleanup failed:", e)
		}
		callErr = nil
	}()
	if mutation {
		id := protocol.UUID()
		h := map[string]string{}
		for k, v := range req.Header {
			h[k] = v[0]
		}
		p := Pending{1, id, s.Meta.ServiceID, s.Mapping.ClientID, s.Endpoint, method, path, b, h, protocol.Now()}
		retained = p
		pending = filepath.Join(s.StateDir, "pending", id+".json")
		deadline := time.Now().Add(30 * time.Second)
		if d, ok := s.Context.Deadline(); ok {
			deadline = d
		}
		unlock, lockErr := AcquireLock(filepath.Join(s.StateDir, "locks", "pending", id+".lock"), deadline)
		if lockErr != nil {
			pending = ""
			return protocol.E(500, "pending_write_failed", "mutation was not sent: "+lockErr.Error())
		}
		unlockPending = unlock
		if e = WriteJSON(pending, p); e != nil {
			return protocol.E(500, "pending_write_failed", "mutation was not sent: "+e.Error())
		}
	}
	resp, e := s.HTTP.Do(req)
	if e != nil {
		if mutation {
			return protocol.E(0, "outcome_uncertain", "mutation outcome is uncertain; pending request retained")
		}
		return protocol.E(503, "service_unavailable", "cannot reach lit: "+e.Error())
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
	if e != nil || len(raw) > 32<<20 {
		if mutation {
			return protocol.E(0, "outcome_uncertain", "response incomplete; pending request retained")
		}
		return protocol.E(500, "invalid_response", "invalid service response")
	}
	var wire struct {
		Data       json.RawMessage `json:"data"`
		Items      json.RawMessage `json:"items"`
		NextCursor *string         `json:"next_cursor"`
		Error      *protocol.Error `json:"error"`
		ServerTime string          `json:"server_time"`
	}
	var checked map[string]json.RawMessage
	if e = protocol.Decode(raw, &checked); e == nil {
		e = DecodeResponseData(raw, &wire)
	}
	if e != nil {
		if mutation {
			return protocol.E(0, "outcome_uncertain", "invalid mutation response; pending request retained")
		}
		return protocol.E(500, "invalid_response", "invalid JSON response")
	}
	if wire.Error != nil {
		if mutation && (!validErrorResponse(resp.StatusCode, wire.Error) || len(wire.Data) > 0 || wire.Items != nil) {
			return pendingError("invalid mutation error response")
		}
		wire.Error.Status = resp.StatusCode
		if mutation && resp.StatusCode >= 500 {
			return protocol.E(0, "outcome_uncertain", "service could not establish mutation outcome; pending request retained")
		}
		if pending != "" {
			_ = os.Remove(pending)
		}
		return wire.Error
	}
	if len(wire.Data) == 0 && wire.Items != nil {
		wire.Data, _ = json.Marshal(map[string]any{"items": wire.Items, "next_cursor": wire.NextCursor})
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || len(wire.Data) == 0 {
		if mutation {
			return protocol.E(0, "outcome_uncertain", "unexpected mutation response; pending request retained")
		}
		return protocol.E(500, "invalid_response", "unexpected service response")
	}
	if dest != nil {
		if e = DecodeResponseData(wire.Data, dest); e != nil {
			if mutation {
				return protocol.E(0, "outcome_uncertain", "cannot decode mutation response; pending request retained")
			}
			return protocol.E(500, "invalid_response", e.Error())
		}
	}

	if mutation {
		var obj map[string]json.RawMessage
		if json.Unmarshal(wire.Data, &obj) != nil || !mutationResponseMatches(retained, wire.Data) {
			return protocol.E(0, "outcome_uncertain", "unrecognized mutation result; pending request retained")
		}
	}
	s.ServerTime = wire.ServerTime
	if pending != "" {
		if e = os.Remove(pending); e != nil {
			s.warn("warning: confirmed operation; pending record cleanup failed:", e)
		}
	}
	return nil
}

func validMutationResponse(path string, obj map[string]json.RawMessage) bool {
	if raw, ok := obj["outcome"]; ok {
		var outcome string
		if json.Unmarshal(raw, &outcome) != nil {
			return false
		}
		switch outcome {
		case "applied", "already_satisfied", "already_disconnected", "no_active_claim":
		default:
			return false
		}
	}
	if path == "/v1/connect" || path == "/v1/disconnect" || strings.HasPrefix(path, "/v1/clients/") {
		raw := obj["client"]
		if strings.HasPrefix(path, "/v1/clients/") {
			raw, _ = json.Marshal(obj)
		}
		var c protocol.Client
		if json.Unmarshal(raw, &c) != nil || c.SchemaVersion != 1 || !protocol.ValidUUID(c.ClientID) || c.StateRevision < 1 || (c.Status != "connected" && c.Status != "disconnected") {
			return false
		}
		return true
	}
	if path == "/v1/operations" {
		var outcome string
		var items []protocol.RequiredClaim
		if json.Unmarshal(obj["outcome"], &outcome) != nil || (outcome != "applied" && outcome != "already_satisfied" && outcome != "no_active_claim") || json.Unmarshal(obj["items"], &items) != nil || items == nil {
			return false
		}
		var rawItems []map[string]json.RawMessage
		if json.Unmarshal(obj["items"], &rawItems) != nil {
			return false
		}
		for _, raw := range rawItems {
			if _, ok := raw["claim"]; !ok {
				return false
			}
		}
		seen := map[string]bool{}
		for _, item := range items {
			if !protocol.ValidUUID(item.IssueID) || seen[item.IssueID] {
				return false
			}
			seen[item.IssueID] = true
			if c := item.Claim; c != nil {
				if c.SchemaVersion != 1 || c.IssueID != item.IssueID || !protocol.ValidUUID(c.OwnerClientID) || !protocol.ValidUUID(c.Token) {
					return false
				}
				start, e := time.Parse(time.RFC3339Nano, c.AcquiredAt)
				if e != nil {
					return false
				}
				end, e := time.Parse(time.RFC3339Nano, c.ExpiresAt)
				if e != nil || !end.After(start) {
					return false
				}
			}
		}
		return true
	}
	if path == "/v1/transactions" || strings.HasPrefix(path, "/v1/projects") {
		var outcome, hash string
		var results []protocol.ChangedObject
		if json.Unmarshal(obj["outcome"], &outcome) != nil || (outcome != "applied" && outcome != "already_satisfied") || json.Unmarshal(obj["request_hash"], &hash) != nil || len(hash) != 64 || json.Unmarshal(obj["results"], &results) != nil || results == nil {
			return false
		}
		return true
	}
	return len(obj) > 0
}

// DecodeResponseData decodes a response keeping exact JSON numbers in maps;
// typed protocol fields still decode into their declared integer types.
// Reconciliation shares it so recovered responses do not lose precision
// compared with direct responses.
func DecodeResponseData(raw []byte, dest any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(dest)
}
