package service

import (
	"errors"
	"testing"
)

// Every route the service serves is classified; reads that use POST and the
// GET lookups that reconcile retained requests are the interesting cases.
func TestRouteTableClassifiesEveryRoute(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	const hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	reads := [][2]string{
		{"GET", "/v1/meta"},
		{"GET", "/v1/completions/issues"},
		{"GET", "/v1/clients/" + id},
		{"GET", "/v1/claims"},
		{"POST", "/v1/claim-snapshots"},
		{"POST", "/v1/issue-summaries"},
		{"POST", "/v1/snapshots"},
		{"POST", "/v1/transaction-previews"},
		{"GET", "/v1/projects"},
		{"GET", "/v1/projects/feat/a"},
		{"GET", "/v1/projects/" + id},
		{"GET", "/v1/projects/" + id + "/history"},
		{"GET", "/v1/projects/" + id + "/history/" + hash},
		{"GET", "/v1/issues/" + id + "/claim"},
	}
	writes := [][2]string{
		{"POST", "/v1/connect"},
		{"PATCH", "/v1/clients/" + id},
		{"POST", "/v1/disconnect"},
		{"POST", "/v1/operations"},
		{"POST", "/v1/issues/" + id + "/claim"},
		{"POST", "/v1/issues/" + id + "/claim/renew"},
		{"POST", "/v1/issues/" + id + "/claim/release"},
		{"POST", "/v1/transactions"},
		{"POST", "/v1/transaction-status"},
		{"POST", "/v1/projects"},
		{"PATCH", "/v1/projects/feat/a"},
		{"GET", "/v1/projects/" + id + "/requests/" + hash},
		{"GET", "/v1/projects/feat/a/requests/" + hash},
		{"POST", "/v1/issues/" + id + "/relationship-changes"},
		// Unknown routes are serialized, so a route missing from the table can
		// never write outside the write lock.
		{"GET", "/"},
		{"GET", "/v2/meta"},
		{"DELETE", "/v1/issues/" + id},
		{"PUT", "/v1/projects"},
	}
	for _, typ := range []string{"issues", "comments", "milestones"} {
		reads = append(reads,
			[2]string{"GET", "/v1/" + typ},
			[2]string{"GET", "/v1/" + typ + "/" + id},
			[2]string{"GET", "/v1/" + typ + "/feat/a: title"},
			[2]string{"GET", "/v1/" + typ + "/feat/x:requests/" + hash},
			[2]string{"GET", "/v1/" + typ + "/" + id + "/history"},
			[2]string{"GET", "/v1/" + typ + "/" + id + "/history/" + hash},
		)
		writes = append(writes,
			[2]string{"POST", "/v1/" + typ},
			[2]string{"PATCH", "/v1/" + typ + "/" + id},
			[2]string{"PATCH", "/v1/" + typ + "/feat/a: title"},
			[2]string{"GET", "/v1/" + typ + "/" + id + "/requests/" + hash},
		)
	}
	// Searches are reads bounded by the search deadline.
	searches := [][2]string{{"POST", "/v1/search"}, {"POST", "/v1/ranked-search"}}
	for want, cases := range map[access][][2]string{readAccess: reads, searchAccess: searches, writeAccess: writes} {
		for _, c := range cases {
			if got := routeAccess(c[0], c[1]); got != want {
				t.Errorf("%s %s: access %d, want %d", c[0], c[1], got, want)
			}
		}
	}
}

// A write attempted inside a read snapshot fails loudly instead of running
// outside the write lock, so a route misclassified as a read cannot write.
func TestReadRequestsCannotWrite(t *testing.T) {
	s, c := projectTestServer(t, t.TempDir())
	changed := c
	changed.Actor.Name = "Changed"
	if _, e := inSnapshot(s, func(rs *Server) (struct{}, error) { return struct{}{}, rs.commit(changes{client: &changed}) }); !errors.Is(e, errReadOnly) {
		t.Fatal("read request wrote", e)
	}
	if got, e := s.Client(c.ClientID); e != nil || got.Actor.Name != "Test" {
		t.Fatal(got, e)
	}
}
