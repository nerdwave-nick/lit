package service

import (
	"encoding/json"
	"fmt"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"testing"
)

func TestReconcileClientCurrentEffectNotHistoricalAcceptance(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	defer s.DB.Close()
	q := protocol.ReconcileRequest{ServiceID: s.DB.ServiceID(), ClientID: c.ClientID, Method: "POST", Path: "/v1/disconnect", Body: json.RawMessage(`{}`), Headers: map[string]string{"If-Match": fmt.Sprintf("\"client:%d\"", c.StateRevision)}}
	v, e := s.Reconcile(q, c)
	if e != nil || v.Status != "uncommitted" {
		t.Fatal(v, e)
	}
	c.Status = "disconnected"
	c.StateRevision++
	s.SaveClient(c)
	v, e = s.Reconcile(q, c)
	if e != nil || v.Status != "already_satisfied" || v.Proof != "current_state" {
		t.Fatal(v, e)
	}
	q.Path = "/v1/clients/" + c.ClientID
	q.Method = "PATCH"
	q.Body = json.RawMessage(`{"actor":{"name":"Test"}}`)
	q.Headers["If-Match"] = fmt.Sprintf("\"client:%d\"", c.StateRevision)
	v, e = s.Reconcile(q, c)
	if e != nil || v.Status != "uncertain" {
		t.Fatal("disconnected patch", v, e)
	}
	q.Path = "/v1/disconnect"
	q.Method = "POST"
	q.Body = json.RawMessage(`{}`)
	v, e = s.Reconcile(q, c)
	if e != nil || v.Status != "already_satisfied" || v.Proof != "current_validated_state" {
		t.Fatal(v, e)
	}
	c.StateRevision += 2
	v, e = s.Reconcile(q, c)
	if e != nil || v.Status != "uncertain" {
		t.Fatal("later client revisions", v, e)
	}
}
func TestReconcileClaimEmptyReleaseAndDisconnectedNoop(t *testing.T) {
	s, c, id := claimFixture(t)
	op := protocol.ClaimOperation{Operation: "claims.release", OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{{IssueID: id}}}
	b, _ := json.Marshal(op)
	q := protocol.ReconcileRequest{ServiceID: s.DB.ServiceID(), ClientID: c.ClientID, Method: "POST", Path: "/v1/operations", Body: b}
	v, e := s.Reconcile(q, c)
	if e != nil || v.Status != "already_satisfied" || v.Outcome != "no_active_claim" {
		t.Fatal(v, e)
	}
	op.Selection = "all_owned"
	op.Items = []protocol.ClaimItem{}
	q.Body, _ = json.Marshal(op)
	c.Status = "disconnected"
	v, e = s.Reconcile(q, c)
	if e != nil || v.Status != "uncertain" {
		t.Fatal(v, e)
	}
}
