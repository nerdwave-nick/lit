package protocol

import (
	"encoding/json"
	"fmt"
)

// Claim is operational authority, independent of durable assignment/revision.
// OwnerActor is a read projection of the owner's actor name and kind on claim
// status reads; it is never part of the durable claim envelope.
type Claim struct {
	SchemaVersion int    `json:"schema_version"`
	IssueID       string `json:"issue_id"`
	OwnerClientID string `json:"owner_client_id"`
	Token         string `json:"token"`
	AcquiredAt    string `json:"acquired_at"`
	ExpiresAt     string `json:"expires_at"`
	OwnerActor    *Actor `json:"owner_actor,omitempty"`
}
type ClaimToken struct {
	IssueID string `json:"issue_id"`
	Token   string `json:"token"`
}
type RequiredClaim struct {
	IssueID string `json:"issue_id"`
	Claim   *Claim `json:"claim"`
}
type ClaimItem struct {
	IssueID  string `json:"issue_id"`
	Token    string `json:"token,omitempty"`
	ExtendTo string `json:"extend_to,omitempty"`
	Force    bool   `json:"force,omitempty"`
}
type ClaimOperation struct {
	SchemaVersion int         `json:"schema_version,omitempty"`
	Operation     string      `json:"operation"`
	OwnerClientID string      `json:"owner_client_id"`
	Selection     string      `json:"selection"`
	Items         []ClaimItem `json:"items"`
}
type ClaimResult struct {
	Outcome string          `json:"outcome"`
	Items   []RequiredClaim `json:"items"`
}

// Preserve field presence while rejecting null and inapplicable operational input.
func (v *ClaimOperation) UnmarshalJSON(b []byte) error {
	type plain ClaimOperation
	var p plain
	if e := Decode(b, &p); e != nil {
		return e
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(b, &fields); e != nil {
		return e
	}
	for k, raw := range fields {
		if string(raw) == "null" {
			return fmt.Errorf("null %s", k)
		}
	}
	var items []map[string]json.RawMessage
	if e := json.Unmarshal(fields["items"], &items); e != nil {
		return e
	}
	for _, item := range items {
		for k, raw := range item {
			if string(raw) == "null" {
				return fmt.Errorf("null %s", k)
			}
			if p.Operation == "claims.acquire" && k == "token" || p.Operation != "claims.acquire" && k == "force" || p.Operation == "claims.release" && k == "extend_to" {
				return fmt.Errorf("inapplicable claim field %s", k)
			}
		}
	}
	*v = ClaimOperation(p)
	return nil
}

func (v *DurableRequest) UnmarshalJSON(b []byte) error {
	type plain DurableRequest
	var p plain
	if e := Decode(b, &p); e != nil {
		return e
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(b, &fields); e != nil {
		return e
	}
	if raw, ok := fields["claims"]; ok && string(raw) == "null" {
		return fmt.Errorf("null claims")
	}
	*v = DurableRequest(p)
	return nil
}
func (v *PrepareRequest) UnmarshalJSON(b []byte) error {
	type plain PrepareRequest
	var p plain
	if e := Decode(b, &p); e != nil {
		return e
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(b, &fields); e != nil {
		return e
	}
	if raw, ok := fields["force"]; ok && string(raw) == "null" {
		return fmt.Errorf("null force")
	}
	*v = PrepareRequest(p)
	return nil
}
func (v *Intent) UnmarshalJSON(b []byte) error {
	type plain Intent
	var p plain
	if e := Decode(b, &p); e != nil {
		return e
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(b, &fields); e != nil {
		return e
	}
	if raw, ok := fields["force"]; ok && string(raw) == "null" {
		return fmt.Errorf("null force")
	}
	*v = Intent(p)
	return nil
}
