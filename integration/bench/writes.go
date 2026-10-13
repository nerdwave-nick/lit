package bench

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// Apply previews and commits t as lit does (POST /v1/transaction-previews,
// then POST /v1/transactions with the claim tokens this client owns) and
// returns the number of history rows it wrote: one per changed record.
// name is the recorded operation.
func (c *Client) Apply(name string, t Txn) (int, error) {
	rows := 0
	err := c.op(name, func() error {
		env, err := c.request(name+"/preview", "POST", "/v1/transaction-previews", protocol.PrepareRequest{SchemaVersion: 1, Operation: t.Operation, Project: t.Project, Items: t.Items})
		if err != nil {
			return err
		}
		var plan protocol.Prepared
		if err = json.Unmarshal(env.Data, &plan); err != nil {
			return err
		}
		tokens := []protocol.ClaimToken{}
		for _, r := range plan.RequiredClaims {
			if r.Claim != nil && r.Claim.OwnerClientID == c.ClientID {
				tokens = append(tokens, protocol.ClaimToken{IssueID: r.IssueID, Token: r.Claim.Token})
			}
		}
		env, err = c.request(name+"/commit", "POST", "/v1/transactions", protocol.DurableRequest{SchemaVersion: 1, Intent: plan.Intent, RequestHash: plan.RequestHash, Claims: tokens})
		if err != nil {
			return err
		}
		var result protocol.MutationResult
		if err = json.Unmarshal(env.Data, &result); err != nil {
			return err
		}
		if result.Outcome != "applied" {
			return fmt.Errorf("%s: outcome %s", t.Operation, result.Outcome)
		}
		rows = len(result.Items)
		return nil
	})
	return rows, err
}

// Claim runs a claim operation (acquire, renew or release) on one issue as
// lit does: a claim snapshot, then POST /v1/operations with the observed
// token and a lease of lease from the snapshot's service time.
func (c *Client) Claim(verb, issue string, lease time.Duration) error {
	name := "claim." + verb
	return c.op(name, func() error {
		env, err := c.request(name+"/snapshot", "POST", "/v1/claim-snapshots", map[string]any{"targets": []string{issue}})
		if err != nil {
			return err
		}
		var snap struct {
			Items      []protocol.RequiredClaim `json:"items"`
			ServerTime string                   `json:"server_time"`
		}
		if err = json.Unmarshal(env.Data, &snap); err != nil {
			return err
		}
		now, err := time.Parse(time.RFC3339Nano, snap.ServerTime)
		if err != nil || len(snap.Items) != 1 {
			return fmt.Errorf("claim snapshot: %v %d items", err, len(snap.Items))
		}
		item := protocol.ClaimItem{IssueID: snap.Items[0].IssueID}
		if verb != "acquire" && snap.Items[0].Claim != nil {
			item.Token = snap.Items[0].Claim.Token
		}
		if verb != "release" {
			item.ExtendTo = now.Add(lease).UTC().Format(time.RFC3339Nano)
		}
		body := protocol.ClaimOperation{SchemaVersion: 1, Operation: "claims." + verb, OwnerClientID: c.ClientID, Selection: "explicit", Items: []protocol.ClaimItem{item}}
		env, err = c.request(name+"/operation", "POST", "/v1/operations", body)
		if err != nil {
			return err
		}
		var result protocol.ClaimResult
		if err = json.Unmarshal(env.Data, &result); err != nil {
			return err
		}
		if result.Outcome != "applied" {
			return fmt.Errorf("%s: outcome %s", name, result.Outcome)
		}
		return nil
	})
}
