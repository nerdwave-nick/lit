// Package uncertain is lit-tui's read-only view of retained requests whose
// outcome is not established: interrupted mutations of this instance's
// client and of orphaned instances found at launch. It shows each request's
// ID, operation, outcome, evidence path and service and client identity, and
// summarizes the recovery guidance of docs/recovery.md.
//
// Everything here only reads. Status comes from litclient's
// TransactionStatus, which never replays, moves or deletes evidence, or from
// the checks recovery and shutdown already made; evidence files are only
// opened for reading. Nothing is ever replayed, retired or deleted.
package uncertain

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/tui/recovery"
)

// NotChecked is the status of a retained request whose outcome was not
// asked for.
const NotChecked = "not checked"

// Item is one retained request: its status and what its evidence says.
type Item struct {
	ID string
	// Status is the reconciled status (uncertain, committed, uncommitted,
	// already_satisfied), or NotChecked; Proof and Outcome qualify it.
	Status, Proof, Outcome string
	// Evidence is the path of the retained request envelope.
	Evidence string
	// From the evidence: the request line, the operation it carries, the
	// service and client identity, the endpoint and when it was recorded.
	Method, Path, Operation       string
	ServiceID, ClientID, Endpoint string
	CreatedAt                     string
	// Problem says why the evidence could not be read, or "".
	Problem string
}

// Uncertain reports whether the request's outcome is still uncertain.
func (it Item) Uncertain() bool { return it.Status == "uncertain" }

// Group is the retained requests of one lit-tui session.
type Group struct {
	// Title names the session; Checked says when and how its requests were
	// checked, or why they were not.
	Title, Checked string
	Items          []Item
}

// Own lists the retained requests of s's client at its service, and a first
// registration of instance whose identity never arrived, with their status
// from s.TransactionStatus. When that check fails, the requests found locally
// are still returned, NotChecked, with the error. s must be open.
func Own(ctx context.Context, s *litclient.Session, instance string) ([]Item, error) {
	found, err := recovery.Find(s.StateDir, s.Mapping.ServiceID, s.Mapping.ClientID, instance)
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(found))
	index := map[string]int{}
	client := false
	for _, ev := range found {
		it := fromPending(ev.Pending, ev.Path)
		it.Status = NotChecked
		if ev.ClientID == "" {
			it.Status, it.Proof = "uncertain", "registration_identity_not_received"
		} else {
			client = true
		}
		index[it.ID] = len(items)
		items = append(items, it)
	}
	if !client {
		return items, nil
	}
	status, err := s.TransactionStatus(ctx, litclient.StatusOptions{})
	if err != nil {
		return items, err
	}
	checked := map[string]bool{}
	for _, v := range status {
		i, ok := index[v.RequestID]
		if !ok { // recorded after the scan above
			items = append(items, Describe(v.Path, recovery.Request{ID: v.RequestID}))
			i = len(items) - 1
		}
		items[i].Status, items[i].Proof, items[i].Outcome = v.Status, v.Proof, v.Outcome
		checked[v.RequestID] = true
	}
	// A client request the check did not return was resolved and removed by
	// its original call meanwhile.
	out := items[:0]
	for _, it := range items {
		if it.ClientID == "" || checked[it.ID] {
			out = append(out, it)
		}
	}
	return out, nil
}

// FromRequests describes requests whose status recovery or shutdown already
// established, reading each one's evidence.
func FromRequests(rs []recovery.Request) []Item {
	out := make([]Item, 0, len(rs))
	for _, r := range rs {
		out = append(out, Describe(r.Path, r))
	}
	return out
}

// Describe reads the evidence at path into an item with the given status.
func Describe(path string, status recovery.Request) Item {
	p, err := litclient.ReadPending(path)
	it := Item{ID: status.ID, Evidence: path}
	switch {
	case os.IsNotExist(err):
		it.Problem = "the evidence file is gone: its original call or lit transactions status may have resolved it"
	case err != nil:
		it.Problem = "the evidence cannot be read: " + err.Error()
	default:
		it = fromPending(p, path)
		if status.ID != "" {
			it.ID = status.ID
		}
	}
	it.Status, it.Proof = status.Status, status.Proof
	return it
}

func fromPending(p litclient.Pending, path string) Item {
	return Item{ID: p.RequestID, Evidence: path, Method: p.Method, Path: p.Path, Operation: Operation(p),
		ServiceID: p.ServiceID, ClientID: p.ClientID, Endpoint: p.Endpoint, CreatedAt: p.CreatedAt}
}

// Operation names what a retained request does: the intent operation of a
// transaction (issue.update), the claim operation (claims.acquire), or
// connect, disconnect and client update.
func Operation(p litclient.Pending) string {
	switch {
	case p.Path == "/v1/transactions":
		var body struct {
			Intent struct {
				Operation string `json:"operation"`
			} `json:"intent"`
		}
		_ = json.Unmarshal(p.Body, &body)
		return body.Intent.Operation
	case p.Path == "/v1/operations":
		var body struct {
			Operation string `json:"operation"`
		}
		_ = json.Unmarshal(p.Body, &body)
		return body.Operation
	case p.Path == "/v1/connect":
		return "connect"
	case p.Path == "/v1/disconnect":
		return "disconnect"
	case strings.HasPrefix(p.Path, "/v1/clients/"):
		return "client update"
	}
	return ""
}
