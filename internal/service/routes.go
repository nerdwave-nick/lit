package service

import (
	"strings"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// access is how a route reaches storage.
type access int

const (
	// writeAccess holds the process write lock and runs in one BEGIN IMMEDIATE
	// transaction that reads, validates and writes.
	writeAccess access = iota
	// readAccess runs in one WAL read snapshot, without the write lock.
	readAccess
	// searchAccess is a readAccess whose work is bounded by the search
	// deadline.
	searchAccess
)

// routes is the static route table. The HTTP method alone cannot classify a
// route: several reads use POST, and the retained-request lookups are GETs
// that must be serialized. The first matching entry wins. A pattern segment is
// a literal, literal alternatives ("a|b"), {uuid} (one UUID segment), {seg}
// (any one segment) or {rest} (one or more segments, since titles may contain
// slashes).
var routes = []struct {
	method, pattern string
	access          access
}{
	// Reconciliation is serialized with writes, so it never reports a request
	// that is still waiting for or holding the write lock as uncommitted.
	{"POST", "/v1/transaction-status", writeAccess},
	{"GET", "/v1/issues|comments|milestones/{uuid}/requests/{rest}", writeAccess},
	{"GET", "/v1/projects/{rest}/requests/{rest}", writeAccess},

	{"GET", "/v1/meta", readAccess},
	{"GET", "/v1/completions/{seg}", readAccess},
	{"GET", "/v1/clients/{seg}", readAccess},
	{"GET", "/v1/claims", readAccess},
	{"POST", "/v1/claim-snapshots", readAccess},
	{"POST", "/v1/search", searchAccess},
	{"POST", "/v1/ranked-search", searchAccess},
	{"POST", "/v1/issue-summaries", readAccess},
	{"POST", "/v1/snapshots", readAccess},
	{"POST", "/v1/transaction-previews", readAccess},
	// Lists, records by selector, history and an issue's claim.
	{"GET", "/v1/projects|issues|comments|milestones", readAccess},
	{"GET", "/v1/projects|issues|comments|milestones/{rest}", readAccess},

	{"POST", "/v1/connect", writeAccess},
	{"PATCH", "/v1/clients/{seg}", writeAccess},
	{"POST", "/v1/disconnect", writeAccess},
	{"POST", "/v1/operations", writeAccess},
	{"POST", "/v1/issues/{uuid}/claim", writeAccess},
	{"POST", "/v1/issues/{uuid}/claim/renew|release", writeAccess},
	{"POST", "/v1/transactions", writeAccess},
	{"POST", "/v1/projects|issues|comments|milestones", writeAccess},
	{"PATCH", "/v1/projects|issues|comments|milestones/{rest}", writeAccess},
	{"POST", "/v1/issues/{uuid}/relationship-changes", writeAccess},
}

// routeAccess classifies a request by method and path. A request no entry
// matches is a write, so a route missing from the table is serialized; it can
// never write outside the write lock.
func routeAccess(method, path string) access {
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for _, r := range routes {
		if r.method == method && matchRoute(strings.Split(strings.TrimPrefix(r.pattern, "/"), "/"), segments) {
			return r.access
		}
	}
	return writeAccess
}

func matchRoute(pattern, segments []string) bool {
	if len(pattern) == 0 {
		return len(segments) == 0
	}
	if len(segments) == 0 {
		return false
	}
	switch p := pattern[0]; p {
	case "{rest}":
		for n := 1; n <= len(segments); n++ {
			if matchRoute(pattern[1:], segments[n:]) {
				return true
			}
		}
		return false
	case "{seg}":
	case "{uuid}":
		if !protocol.ValidUUID(segments[0]) {
			return false
		}
	default:
		found := false
		for _, literal := range strings.Split(p, "|") {
			found = found || literal == segments[0]
		}
		if !found {
			return false
		}
	}
	return matchRoute(pattern[1:], segments[1:])
}
