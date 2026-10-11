package litclient

import (
	"encoding/json"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

func TestClaimResponseRequiresExplicitClaimState(t *testing.T) {
	for _, raw := range []string{
		`{"outcome":"applied","items":[{"issue_id":"00000000-0000-4000-8000-000000000001"}]}`,
		`{"outcome":"applied","items":[{"issue_id":"00000000-0000-4000-8000-000000000001","claim":null},{"issue_id":"00000000-0000-4000-8000-000000000001","claim":null}]}`,
	} {
		var obj map[string]json.RawMessage
		if e := json.Unmarshal([]byte(raw), &obj); e != nil {
			t.Fatal(e)
		}
		if validMutationResponse("/v1/operations", obj) {
			t.Fatal("ambiguous claim result accepted", raw)
		}
	}
}

func TestMutationResponseMustMatchRetainedOperationalEffect(t *testing.T) {
	cid, sid, id, token := protocol.UUID(), protocol.UUID(), protocol.UUID(), protocol.UUID()
	client := protocol.Client{SchemaVersion: 1, ClientID: cid, StateRevision: 2, Status: "connected", Actor: protocol.Actor{Name: "test", Kind: "human"}}
	p := Pending{ClientID: cid, ServiceID: sid, Method: "PATCH", Path: "/v1/clients/" + cid, Body: json.RawMessage(`{"runtime":null}`), Headers: map[string]string{"If-Match": "\"client:1\""}}
	raw, _ := json.Marshal(client)
	if !mutationResponseMatches(p, raw) {
		t.Fatal("valid runtime clear rejected")
	}
	client.Runtime = protocol.Runtime{Vendor: "still present"}
	raw, _ = json.Marshal(client)
	if mutationResponseMatches(p, raw) {
		t.Fatal("uncleared runtime accepted")
	}
	client.Runtime = protocol.Runtime{}
	client.StateRevision = 100
	raw, _ = json.Marshal(client)
	if mutationResponseMatches(p, raw) {
		t.Fatal("unrelated client revision accepted")
	}
	p.Path = "/v1/disconnect"
	p.Method = "POST"
	p.Body = json.RawMessage(`{}`)
	client.Status = "disconnected"
	raw, _ = json.Marshal(map[string]any{"outcome": "applied", "client": client})
	if mutationResponseMatches(p, raw) {
		t.Fatal("unrelated disconnect revision accepted")
	}
	p.Path = "/v1/connect"
	p.ClientID = ""
	p.Headers = map[string]string{}
	p.Body = json.RawMessage(`{"actor":{"name":"test","kind":"human"}}`)
	client.Status = "connected"
	client.StateRevision = 2
	raw, _ = json.Marshal(map[string]any{"client": client, "meta": protocol.Meta{ServiceID: sid, APIMajor: 1}})
	if mutationResponseMatches(p, raw) {
		t.Fatal("new registration with revision2 accepted")
	}
	p.ClientID = cid
	p.Path = "/v1/operations"
	op := protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: cid, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id, ExtendTo: "2026-09-29T15:30:00Z"}}}
	p.Body, _ = json.Marshal(op)
	claim := protocol.Claim{SchemaVersion: 1, IssueID: id, OwnerClientID: cid, Token: token, AcquiredAt: "2026-09-29T15:00:00Z", ExpiresAt: op.Items[0].ExtendTo}
	result := protocol.ClaimResult{Outcome: "applied", Items: []protocol.RequiredClaim{{IssueID: id, Claim: &claim}}}
	raw, _ = json.Marshal(result)
	if !mutationResponseMatches(p, raw) {
		t.Fatal("valid acquire rejected")
	}
	claim.ExpiresAt = "2026-09-29T15:40:00Z"
	raw, _ = json.Marshal(result)
	if mutationResponseMatches(p, raw) {
		t.Fatal("different acquisition expiry accepted")
	}
}

func TestAcquireResponseAcceptsOnlyExactCappedGrant(t *testing.T) {
	cid, id := protocol.UUID(), protocol.UUID()
	op := protocol.ClaimOperation{Operation: "claims.acquire", OwnerClientID: cid, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id, ExtendTo: "2026-09-29T18:00:00Z"}}}
	body, _ := json.Marshal(op)
	p := Pending{ClientID: cid, Path: "/v1/operations", Body: body}
	claim := protocol.Claim{SchemaVersion: 1, IssueID: id, OwnerClientID: cid, Token: protocol.UUID(), AcquiredAt: "2026-09-29T15:00:00Z", ExpiresAt: "2026-09-29T16:00:00Z"}
	v := protocol.ClaimResult{Outcome: "applied", Items: []protocol.RequiredClaim{{IssueID: id, Claim: &claim}}}
	raw, _ := json.Marshal(v)
	if !mutationResponseMatches(p, raw) {
		t.Fatal("legitimate one-hour capped grant rejected")
	}
	claim.ExpiresAt = "2026-09-29T16:01:00Z"
	raw, _ = json.Marshal(v)
	if mutationResponseMatches(p, raw) {
		t.Fatal("grant over cap accepted")
	}
}
