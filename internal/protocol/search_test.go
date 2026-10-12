package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSearchStrictRequests(t *testing.T) {
	for _, s := range []string{`{"query":"x","Cursor":""}`, `{"query":"x","Scope":{"project_id":null}}`, `{"query":"x","Scope":null}`, `{"query":"x","Limit":0}`, `{"query":"x","scope":{"All_Projects":true}}`, `null`, `{"query":"x","query":"y"}`, `{"query":"x","unknown":1}`, `{"query":null}`, `{"scope":null}`, `{"scope":{"project_id":null}}`, `{"scope":{"project_id":""}}`, `{"scope":{"project_id":"p","all_projects":false}}`, `{"scope":{"unknown":true}}`, `{"context_lines":null}`, `{"context_lines":1.5}`, `{"case_sensitive":null}`, `{"limit":0}`, `{"cursor":""}`} {
		var q SearchRequest
		if Decode([]byte(s), &q) == nil {
			t.Fatalf("accepted %s", s)
		}
	}
}

// regex is an optional boolean; literal requests encode without it, so their
// cursors (bound to the encoded request) are unchanged.
func TestSearchRegexField(t *testing.T) {
	for s, want := range map[string]bool{`{"query":"x","regex":true}`: true, `{"query":"x","regex":false}`: false, `{"query":"x"}`: false} {
		var q SearchRequest
		if e := Decode([]byte(s), &q); e != nil || q.Regex != want {
			t.Fatalf("%s: %+v %v", s, q, e)
		}
	}
	for _, s := range []string{`{"query":"x","regex":null}`, `{"query":"x","regex":"true"}`, `{"query":"x","regex":1}`, `{"query":"x","Regex":true}`} {
		var q SearchRequest
		if Decode([]byte(s), &q) == nil {
			t.Fatalf("accepted %s", s)
		}
	}
	if b, _ := json.Marshal(SearchRequest{Query: "x"}); strings.Contains(string(b), "regex") {
		t.Fatalf("literal request encodes regex: %s", b)
	}
}
