package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func uncertain(proof string) protocol.TransactionStatus {
	return protocol.TransactionStatus{Status: "uncertain", Proof: proof}
}
func established(status, proof, outcome string, response any) protocol.TransactionStatus {
	v := protocol.TransactionStatus{Status: status, Proof: proof, Outcome: outcome}
	if response != nil {
		v.Response = mustJSON(response)
	}
	return v
}

// reconcile runs in a serialized transaction that it never commits. It cannot
// execute, refresh, or reserve a request. Negative conclusions describe this
// serialized snapshot, not a promise that a delayed original request will never arrive.
func (s *Server) reconcile(q protocol.ReconcileRequest, c protocol.Client) (result protocol.TransactionStatus, err error) {
	defer func() {
		result.ServiceID = s.DB.ServiceID()
		result.ClientID = c.ClientID
		result.RequestDigest = protocol.ReconcileDigest(q)
	}()
	if q.ServiceID != s.DB.ServiceID() {
		return protocol.TransactionStatus{}, protocol.E(409, "wrong_service", "pending request belongs to a different service")
	}
	if q.ClientID != c.ClientID {
		return protocol.TransactionStatus{}, protocol.E(409, "wrong_client", "pending request belongs to a different client")
	}
	if q.Method == "POST" && q.Path == "/v1/transactions" {
		return s.reconcileDurable(q, c)
	}
	if q.Method == "POST" && q.Path == "/v1/operations" {
		return s.reconcileClaims(q, c)
	}
	if q.Method == "POST" && (q.Path == "/v1/connect" || q.Path == "/v1/disconnect") || q.Method == "PATCH" && q.Path == "/v1/clients/"+c.ClientID {
		return s.reconcileClient(q, c)
	}
	return protocol.TransactionStatus{}, invalid("unsupported retained request")
}
func (s *Server) reconcileDurable(q protocol.ReconcileRequest, c protocol.Client) (protocol.TransactionStatus, error) {
	var d protocol.DurableRequest
	if e := protocol.Decode(q.Body, &d); e != nil {
		return protocol.TransactionStatus{}, invalid(e.Error())
	}
	if d.SchemaVersion != 0 && d.SchemaVersion != 1 {
		return protocol.TransactionStatus{}, invalid("unsupported retained schema_version")
	}
	canonical, hash, e := protocol.Canonical(d.Intent)
	if e != nil || hash != d.RequestHash || d.Intent.ServiceID != q.ServiceID || d.Intent.Actor.ClientID != q.ClientID {
		return protocol.TransactionStatus{}, invalid("invalid retained durable identity")
	}
	if e = protocol.Decode(canonical, &d.Intent); e != nil {
		return protocol.TransactionStatus{}, invalid("invalid canonical intent")
	}
	if len(d.Intent.Targets)+len(d.Intent.Guards) > s.Config.Limits.ExpandedRecords {
		return protocol.TransactionStatus{}, protocol.E(413, "limit_exceeded", "too many reconciliation owners")
	}
	// A committed request has one transaction record, keyed by its hash, that
	// keeps its results exactly as they were returned.
	committed, found, e := s.q().Transaction(s.ctx(), hash)
	if e != nil {
		return protocol.TransactionStatus{}, e
	}
	if found {
		v := established("committed", "immutable_history", "applied", protocol.MutationResult{Outcome: "applied", RequestHash: hash, Items: committed.Results})
		v.RequestHash = hash
		v.Results = committed.Results
		return v, nil
	}
	if c.Status != "connected" {
		return uncertain("client_disconnected_without_history"), nil
	}
	cs, res, e := s.recordTransaction(d.Intent, hash, c, d.Claims...)
	if e != nil {
		if _, ok := e.(*protocol.Error); ok {
			return uncertain("original_preconditions_no_longer_valid"), nil
		}
		return protocol.TransactionStatus{}, e
	}
	if len(res.Items) > 0 {
		v := established("uncommitted", "all_target_histories_absent_and_original_revisions_valid", "", nil)
		v.RequestHash = hash
		return v, nil
	}
	// Force/close may release operational claims even without durable changes.
	// Their absent history alone says nothing about the lost operational effect.
	if !cs.empty() {
		return uncertain("operational_effect_without_durable_history"), nil
	}
	v := established("already_satisfied", "current_validated_state", "already_satisfied", res)
	v.RequestHash = hash
	return v, nil
}
func (s *Server) reconcileClaims(q protocol.ReconcileRequest, c protocol.Client) (protocol.TransactionStatus, error) {
	if c.Status != "connected" {
		return uncertain("client_disconnected"), nil
	}
	var op protocol.ClaimOperation
	if e := protocol.Decode(q.Body, &op); e != nil {
		return protocol.TransactionStatus{}, invalid(e.Error())
	}
	if op.OwnerClientID != c.ClientID || (op.Operation != "claims.acquire" && op.Operation != "claims.renew" && op.Operation != "claims.release") || (op.Selection != "explicit" && op.Selection != "all_owned") || op.SchemaVersion != 0 && op.SchemaVersion != 1 {
		return protocol.TransactionStatus{}, invalid("invalid retained claim operation")
	}
	if len(op.Items) > s.Config.Limits.ExplicitItems {
		return protocol.TransactionStatus{}, protocol.E(413, "limit_exceeded", "too many claim items")
	}
	seen := map[string]bool{}
	desired := true
	definitelyMissingEffect := false
	result := protocol.ClaimResult{Outcome: "already_satisfied", Items: []protocol.RequiredClaim{}}
	for _, item := range op.Items {
		if !protocol.ValidUUID(item.IssueID) || seen[item.IssueID] {
			return protocol.TransactionStatus{}, invalid("invalid or duplicate claim issue")
		}
		seen[item.IssueID] = true
		if op.Operation != "claims.acquire" && !protocol.ValidUUID(item.Token) && !(op.Operation == "claims.release" && item.Token == "") {
			return protocol.TransactionStatus{}, invalid("invalid retained claim token")
		}
		var target time.Time
		if op.Operation == "claims.renew" {
			var e error
			target, e = time.Parse(time.RFC3339Nano, item.ExtendTo)
			if e != nil {
				return protocol.TransactionStatus{}, invalid("invalid absolute renewal target")
			}
		}
		live, e := s.liveClaim(item.IssueID)
		if e != nil {
			return protocol.TransactionStatus{}, e
		}
		result.Items = append(result.Items, protocol.RequiredClaim{IssueID: item.IssueID, Claim: live})
		if op.Operation == "claims.acquire" {
			desired = false
			continue
		}
		if op.Operation == "claims.release" && item.Token == "" && live == nil {
			continue
		}
		if live == nil || live.OwnerClientID != c.ClientID || live.Token != item.Token {
			desired = false
			continue
		}
		if op.Operation == "claims.release" {
			desired = false
			definitelyMissingEffect = true
			continue
		}
		expiry, _ := time.Parse(time.RFC3339Nano, live.ExpiresAt)
		if expiry.Before(target) {
			desired = false
			definitelyMissingEffect = true
		}
	}
	if op.Operation == "claims.acquire" {
		return uncertain("acquisition_token_not_received"), nil
	}
	if definitelyMissingEffect {
		return established("uncommitted", "matching_live_token_has_not_reached_requested_effect", "", nil), nil
	}
	if !desired {
		return uncertain("ownership_expired_replaced_or_released"), nil
	}
	// Validate complete selection and original token/expiry rules. This engine
	// returns proposed writes only; reconciliation never commits them.
	cs, validated, e := s.claimTransaction(op, c)
	if e != nil {
		if _, ok := e.(*protocol.Error); ok {
			return uncertain("original_claim_preconditions_no_longer_valid"), nil
		}
		return protocol.TransactionStatus{}, e
	}
	if !cs.empty() {
		return uncertain("claim_effect_not_established"), nil
	}
	return established("already_satisfied", "current_validated_state", validated.Outcome, validated), nil
}
func retainedRevision(headers map[string]string) (int64, error) {
	value := ""
	for k, v := range headers {
		if strings.EqualFold(k, "If-Match") {
			value = v
		}
	}
	if !strings.HasPrefix(value, "\"client:") || !strings.HasSuffix(value, "\"") {
		return 0, fmt.Errorf("missing retained client revision")
	}
	n, e := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(value, "\"client:"), "\""), 10, 64)
	if e != nil || n < 1 {
		return 0, fmt.Errorf("invalid retained client revision")
	}
	return n, nil
}
func (s *Server) reconcileClient(q protocol.ReconcileRequest, c protocol.Client) (protocol.TransactionStatus, error) {
	if c.Status != "connected" && q.Path != "/v1/disconnect" && q.Path != "/v1/connect" {
		return uncertain("client_disconnected"), nil
	}
	var p map[string]json.RawMessage
	if e := protocol.Decode(q.Body, &p); e != nil || p == nil {
		return protocol.TransactionStatus{}, invalid("invalid retained client payload")
	}
	if q.Path == "/v1/connect" {
		var id string
		if json.Unmarshal(p["client_id"], &id) != nil || id != c.ClientID {
			return uncertain("registration_identity_not_received"), nil
		}
		delete(p, "client_id")
	}
	revision, e := retainedRevision(q.Headers)
	if e != nil {
		return uncertain("original_client_revision_unavailable"), nil
	}
	desired := c
	if q.Path == "/v1/disconnect" {
		if len(p) != 0 {
			return protocol.TransactionStatus{}, invalid("invalid disconnect payload")
		}
		desired.Status = "disconnected"
	} else {
		if e = s.patchClient(&desired, p); e != nil {
			return protocol.TransactionStatus{}, e
		}
		if q.Path == "/v1/connect" {
			desired.Status = "connected"
		}
	}
	same := bytes.Equal(mustJSON(c), mustJSON(desired))
	if c.StateRevision == revision && !same {
		return established("uncommitted", "original_client_revision_unchanged", "", nil), nil
	}
	if !same || c.StateRevision < revision || c.StateRevision > revision+1 {
		return uncertain("client_state_changed_without_receipt"), nil
	}
	// At the original revision this is a validated no-op, not proof of historical acceptance.
	var response any = c
	outcome := "already_satisfied"
	if q.Path == "/v1/disconnect" {
		claims, e := s.liveClaims(c.ClientID)
		if e != nil {
			return protocol.TransactionStatus{}, e
		}
		if len(claims) > 0 {
			return uncertain("disconnect_claims_still_live"), nil
		}
		outcome = "already_disconnected"
		response = map[string]any{"outcome": outcome, "client": c}
	}
	if q.Path == "/v1/connect" {
		response = map[string]any{"client": c, "meta": s.Meta()}
	}
	proof := "current_validated_state"
	if c.StateRevision != revision {
		proof = "current_state"
	}
	return established("already_satisfied", proof, outcome, response), nil
}
func (s *Server) reconcileHTTP(w http.ResponseWriter, r *http.Request, c protocol.Client) error {
	var q protocol.ReconcileRequest
	if e := s.Body(r, &q); e != nil {
		return e
	}
	v, e := s.reconcile(q, c)
	if e != nil {
		return e
	}
	s.write(w, 200, v)
	return nil
}
