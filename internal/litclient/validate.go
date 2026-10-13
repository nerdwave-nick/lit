package litclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"strings"
	"time"
)

func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
func containsJSON(have, want any) bool {
	w, ok := want.(map[string]any)
	if !ok {
		return sameJSON(have, want)
	}
	h, ok := have.(map[string]any)
	if !ok {
		return false
	}
	for k, v := range w {
		if !containsJSON(h[k], v) {
			return false
		}
	}
	return true
}

// Bind even successful responses to the exact outgoing semantic request. The
// permissive additive wire parser must never turn another request's proof into
// permission to discard this request's only recovery evidence.
func mutationResponseMatches(p Pending, raw json.RawMessage) bool {
	var obj map[string]json.RawMessage
	if protocol.Decode(raw, &obj) != nil || !validMutationResponse(p.Path, obj) {
		return false
	}
	switch p.Path {
	case "/v1/transactions":
		var q protocol.DurableRequest
		var v protocol.MutationResult
		if protocol.Decode(p.Body, &q) != nil || json.Unmarshal(raw, &v) != nil || v.RequestHash != q.RequestHash {
			return false
		}
		if v.Outcome == "already_satisfied" {
			return len(v.Items) == 0
		}
		if v.Outcome != "applied" || len(v.Items) == 0 {
			return false
		}
		targets := map[string]protocol.ObjectRef{}
		for _, r := range q.Intent.Targets {
			targets[r.Type+"/"+r.ID] = r
		}
		seen := map[string]bool{}
		for _, r := range v.Items {
			key := r.Type + "/" + r.ID
			ref, ok := targets[key]
			if !ok || seen[key] || r.Revision != r.BeforeRevision+1 {
				return false
			}
			seen[key] = true
			before := int64(0)
			if ref.ExpectedRevision != nil {
				before = *ref.ExpectedRevision
			}
			if r.BeforeRevision != before {
				return false
			}
		}
		return true
	case "/v1/operations":
		var q protocol.ClaimOperation
		var v protocol.ClaimResult
		if protocol.Decode(p.Body, &q) != nil || json.Unmarshal(raw, &v) != nil || len(q.Items) != len(v.Items) {
			return false
		}
		if q.Operation == "claims.acquire" && v.Outcome != "applied" {
			return false
		}
		if q.Operation == "claims.renew" && v.Outcome != "applied" && v.Outcome != "already_satisfied" {
			return false
		}
		wanted := map[string]protocol.ClaimItem{}
		for _, x := range q.Items {
			wanted[x.IssueID] = x
		}
		for _, x := range v.Items {
			input, ok := wanted[x.IssueID]
			if !ok {
				return false
			}
			if q.Operation == "claims.release" {
				if x.Claim != nil {
					return false
				}
				continue
			}
			if x.Claim == nil || x.Claim.OwnerClientID != p.ClientID {
				return false
			}
			if q.Operation == "claims.acquire" && input.ExtendTo != "" {
				target, e := time.Parse(time.RFC3339Nano, input.ExtendTo)
				expiry, f := time.Parse(time.RFC3339Nano, x.Claim.ExpiresAt)
				start, g := time.Parse(time.RFC3339Nano, x.Claim.AcquiredAt)
				if target.After(start.Add(time.Hour)) {
					target = start.Add(time.Hour)
				}
				if e != nil || f != nil || g != nil || !expiry.Equal(target) {
					return false
				}
			}
			if q.Operation == "claims.renew" {
				if x.Claim.Token != input.Token {
					return false
				}
				target, e := time.Parse(time.RFC3339Nano, input.ExtendTo)
				expiry, f := time.Parse(time.RFC3339Nano, x.Claim.ExpiresAt)
				if e != nil || f != nil || expiry.Before(target) {
					return false
				}
			}
		}
		return true
	default:
		var c protocol.Client
		clientRaw := raw
		if p.Path == "/v1/connect" || p.Path == "/v1/disconnect" {
			clientRaw = obj["client"]
		}
		if json.Unmarshal(clientRaw, &c) != nil || p.ClientID != "" && c.ClientID != p.ClientID || c.Actor.Name == "" || (c.Actor.Kind != "human" && c.Actor.Kind != "agent") {
			return false
		}
		if p.ClientID == "" {
			if c.StateRevision != 1 {
				return false
			}
		} else {
			condition := ""
			for k, v := range p.Headers {
				if strings.EqualFold(k, "If-Match") {
					condition = v
				}
			}
			if condition != fmt.Sprintf("\"client:%d\"", c.StateRevision) && condition != fmt.Sprintf("\"client:%d\"", c.StateRevision-1) {
				return false
			}
		}
		if p.Path == "/v1/connect" {
			var meta protocol.Meta
			if json.Unmarshal(obj["meta"], &meta) != nil || meta.ServiceID != p.ServiceID || meta.APIMajor != 1 || c.Status != "connected" {
				return false
			}
		}
		if p.Path == "/v1/disconnect" {
			return c.Status == "disconnected"
		}
		var have, want map[string]any
		if json.Unmarshal(clientRaw, &have) != nil || json.Unmarshal(p.Body, &want) != nil {
			return false
		}
		delete(want, "client_id")
		if selector, ok := want["project_id"].(string); ok && !protocol.ValidUUID(selector) {
			if c.ProjectID == nil || !protocol.ValidUUID(*c.ProjectID) {
				return false
			}
			delete(want, "project_id")
		}
		// runtime:null clears the composite to its empty representation.
		if x, ok := want["runtime"]; ok && x == nil {
			want["runtime"] = map[string]any{"vendor": "", "session_id": ""}
		}
		if r, ok := want["runtime"].(map[string]any); ok {
			for k, v := range r {
				if v == nil {
					r[k] = ""
				}
			}
			if h, ok := have["runtime"].(map[string]any); ok {
				for k, v := range r {
					if v == "" {
						if _, exists := h[k]; !exists {
							h[k] = ""
						}
					}
				}
			}
		}
		return c.Status == "connected" && containsJSON(have, want)
	}
}
func validStatus(p Pending, v protocol.TransactionStatus) bool {
	if v.RequestDigest != protocol.ReconcileDigest(pendingRequest(p)) {
		return false
	}
	if v.ServiceID != p.ServiceID || v.ClientID != p.ClientID || v.RequestID != "" && v.RequestID != p.RequestID {
		return false
	}
	switch v.Status {
	case "uncertain":
		return v.Proof != "" && v.Outcome == "" && len(v.Results) == 0 && len(v.Response) == 0
	case "committed":
		if p.Path != "/v1/transactions" || v.Proof != "immutable_history" || v.Outcome != "applied" || !mutationResponseMatches(p, v.Response) {
			return false
		}
		var result protocol.MutationResult
		json.Unmarshal(v.Response, &result)
		return v.RequestHash == result.RequestHash && sameJSON(v.Results, result.Items)
	case "already_satisfied":
		if (v.Proof != "current_validated_state" && v.Proof != "current_state") || !mutationResponseMatches(p, v.Response) {
			return false
		}
		if p.Path == "/v1/transactions" {
			var result protocol.MutationResult
			json.Unmarshal(v.Response, &result)
			return v.Proof == "current_validated_state" && v.Outcome == "already_satisfied" && result.Outcome == v.Outcome && v.RequestHash == result.RequestHash && len(v.Results) == 0
		}
		if v.RequestHash != "" || len(v.Results) != 0 {
			return false
		}
		if p.Path == "/v1/operations" {
			var q protocol.ClaimOperation
			var result protocol.ClaimResult
			json.Unmarshal(p.Body, &q)
			json.Unmarshal(v.Response, &result)
			if v.Proof != "current_validated_state" || q.Operation == "claims.acquire" || result.Outcome != v.Outcome || result.Outcome == "applied" {
				return false
			}
			if q.Operation == "claims.release" {
				for _, x := range q.Items {
					if x.Token != "" {
						return false
					}
				}
			}
			return true
		}
		if p.Path == "/v1/disconnect" {
			return v.Outcome == "already_disconnected"
		}
		return v.Outcome == "already_satisfied"
	case "uncommitted":
		if v.Outcome != "" || len(v.Results) > 0 || len(v.Response) > 0 {
			return false
		}
		if p.Path == "/v1/transactions" {
			var q protocol.DurableRequest
			if json.Unmarshal(p.Body, &q) != nil {
				return false
			}
			return v.RequestHash == q.RequestHash && v.Proof == "all_target_histories_absent_and_original_revisions_valid"
		}
		if v.RequestHash != "" {
			return false
		}
		if p.Path == "/v1/operations" {
			return v.Proof == "matching_live_token_has_not_reached_requested_effect"
		}
		return v.Proof == "original_client_revision_unchanged"
	}
	return false
}
func validErrorResponse(status int, e *protocol.Error) bool {
	if status < 400 || status >= 600 || e == nil || strings.TrimSpace(e.Message) == "" || e.Code == "" {
		return false
	}
	if _, ok := e.Details.(map[string]any); !ok {
		return false
	}
	for _, r := range e.Code {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}
func pendingError(message string) error {
	return protocol.E(0, "outcome_uncertain", fmt.Sprintf("%s; pending request retained", message))
}
