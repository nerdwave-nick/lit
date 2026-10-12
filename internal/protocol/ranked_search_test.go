package protocol

import "testing"

// Ranked search requests are strict like grep's: no unknown, duplicate or
// null fields, one scope, a known state, a positive limit, a nonempty cursor.
func TestRankedSearchStrictRequests(t *testing.T) {
	for _, s := range []string{`null`, `[]`, `{"query":"x","unknown":1}`, `{"query":"x","Query":"y"}`, `{"query":"x","query":"y"}`, `{"query":null}`,
		`{"query":"x","scope":null}`, `{"query":"x","scope":{"project_id":""}}`, `{"query":"x","scope":{"project_id":"p","all_projects":true}}`,
		`{"query":"x","scope":{"unknown":true}}`, `{"query":"x","scope":{"all_projects":null}}`, `{"query":"x","state":"done"}`, `{"query":"x","state":""}`,
		`{"query":"x","state":null}`, `{"query":"x","limit":0}`, `{"query":"x","limit":1.5}`, `{"query":"x","cursor":""}`, `{"query":"x","context_lines":1}`,
		`{"query":"x","case_sensitive":true}`} {
		var q RankedSearchRequest
		if Decode([]byte(s), &q) == nil {
			t.Errorf("accepted %s", s)
		}
	}
	var q RankedSearchRequest
	if e := Decode([]byte(`{"query":"x","scope":{"project_id":"p"},"state":"closed","limit":5,"cursor":"c"}`), &q); e != nil {
		t.Fatal(e)
	}
	if q != (RankedSearchRequest{Scope: SearchScope{ProjectID: "p"}, Query: "x", State: "closed", Limit: 5, Cursor: "c"}) {
		t.Fatalf("%+v", q)
	}
}
