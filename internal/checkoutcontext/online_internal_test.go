package checkoutcontext

import "testing"

// The default cannot be exercised against a service (it is the user's own
// lit-server's address), so the whole precedence is pinned here.
func TestEndpointPrecedenceEndsAtTheDefault(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ep    Endpoint
		saved string
		want  string
	}{
		{"explicit", Endpoint{Explicit: "http://a.example:1", Supplied: true, Environment: "http://b.example:2"}, "http://c.example:3", "http://a.example:1"},
		{"environment", Endpoint{Environment: "http://b.example:2/"}, "http://c.example:3", "http://b.example:2"},
		{"saved", Endpoint{}, "http://c.example:3", "http://c.example:3"},
		{"default when nothing else is set", Endpoint{}, "", "http://127.0.0.1:7411"},
	} {
		got, err := tc.ep.choose(tc.saved)
		if err != nil || got != tc.want {
			t.Errorf("%s: %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
	if _, err := (Endpoint{Explicit: "", Supplied: true}).choose("http://c.example:3"); err == nil {
		t.Error("an empty explicit endpoint must be an error, not the saved endpoint")
	}
}
