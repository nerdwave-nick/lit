package recovery

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// Request is one retained request whose outcome is not established, and
// where its evidence lives.
type Request struct {
	ID     string `json:"request_id"`
	Status string `json:"status"`
	Proof  string `json:"proof"`
	Path   string `json:"path"`
}

// Evidence is one retained request envelope and its path.
type Evidence struct {
	litclient.Pending
	Path string
}

// Find lists the retained requests attributable to one lit-tui instance:
// those of its client at its service, and a first registration naming the
// instance in its runtime whose client identity never arrived. Unreadable
// envelopes cannot be attributed and are skipped. Find only reads.
func Find(stateDir, serviceID, clientID, instanceID string) ([]Evidence, error) {
	dir := filepath.Join(stateDir, "pending")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Evidence
	for _, d := range entries {
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, d.Name())
		p, err := litclient.ReadPending(path)
		if err != nil {
			continue
		}
		if clientID != "" && p.ClientID == clientID && p.ServiceID == serviceID || p.ClientID == "" && registers(p, instanceID) {
			out = append(out, Evidence{Pending: p, Path: path})
		}
	}
	return out, nil
}

// registers reports whether p is a first connect for instance id.
func registers(p litclient.Pending, id string) bool {
	var body struct {
		Runtime protocol.Runtime `json:"runtime"`
	}
	return id != "" && p.Path == "/v1/connect" && json.Unmarshal(p.Body, &body) == nil && body.Runtime.Vendor == Vendor && body.Runtime.SessionID == id
}

// Vendor is the runtime vendor lit-tui registers its clients with; the
// runtime session ID is the instance ID.
const Vendor = "lit-tui"

// Inspect classifies an instance's retained requests read-only and returns
// those that are still uncertain. A first registration without a client
// identity is uncertain without asking the service; the client's requests are
// reconciled through s.TransactionStatus, which never replays, moves or
// deletes evidence. s must be locked and, when mapped, opened. An error means
// the requests could not be verified.
func Inspect(ctx context.Context, s *litclient.Session, instanceID string) ([]Request, error) {
	found, err := Find(s.StateDir, s.Mapping.ServiceID, s.Mapping.ClientID, instanceID)
	if err != nil {
		return nil, err
	}
	var out []Request
	own := false
	for _, ev := range found {
		if ev.ClientID == "" {
			out = append(out, Request{ID: ev.RequestID, Status: "uncertain", Proof: "registration_identity_not_received", Path: ev.Path})
		} else {
			own = true
		}
	}
	if !own {
		return out, nil
	}
	status, err := s.TransactionStatus(ctx, litclient.StatusOptions{})
	if err != nil {
		return nil, err
	}
	for _, v := range status {
		if v.Status == "uncertain" {
			out = append(out, Request{ID: v.RequestID, Status: v.Status, Proof: v.Proof, Path: v.Path})
		}
	}
	return out, nil
}
