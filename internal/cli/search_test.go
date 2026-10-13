package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

func TestGrepArguments(t *testing.T) {
	for _, argv := range [][]string{{"grep", "needle", "-n", "-C", "2", "--all-projects"}, {"--format", "json", "grep", "--", "--help"}, {"grep", "--", "-literal"}} {
		a, e := Parse(argv)
		if e != nil || a.Command != "grep" || len(a.Positionals) != 1 {
			t.Fatalf("%v: %+v %v", argv, a, e)
		}
	}
	for _, argv := range [][]string{{"grep", ""}, {"grep", "a", "b"}, {"grep", "a", "-C", "-1"}, {"grep", "a", "--project", "feat/a", "--all-projects"}, {"grep", "a", "--limit", "1001"}, {"grep", "a", "--cursor", ""}, {"grep", "a", "-n", "-n"}} {
		if _, e := Parse(argv); e == nil {
			t.Fatalf("accepted %v", argv)
		}
	}
}

func TestGrepCompactContext(t *testing.T) {
	for _, flag := range []string{"-C2", "-C=2"} {
		a, err := Parse([]string{"grep", "needle", flag, "--format", "json"})
		if err != nil || a.One("context") != "2" || a.One("format") != "json" {
			t.Fatalf("%s: %#v %v", flag, a, err)
		}
	}
	for _, args := range [][]string{{"grep", "needle", "-C2", "--context", "3"}, {"grep", "needle", "-C-1"}, {"grep", "needle", "-Cbad"}} {
		if _, err := Parse(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	a, err := Parse([]string{"grep", "--", "-C2"})
	if err != nil || a.Has("context") || a.Positionals[0] != "-C2" {
		t.Fatalf("literal: %#v %v", a, err)
	}
}

func TestReservedFlagContentUsesEquals(t *testing.T) {
	for _, value := range []string{"--session", "--endpoint", "--format", "--runtime-vendor", "--runtime-session-id"} {
		a, err := Parse([]string{"issues", "create", "--issue", "Test", "--content=" + value})
		if err != nil || len(a.Groups) != 1 || a.Groups[0]["content"][0] != value {
			t.Fatalf("equals %s: %#v %v", value, a, err)
		}
		if _, err := Parse([]string{"issues", "create", "--issue", "Test", "--content", value}); err == nil {
			t.Fatalf("split option-like value unexpectedly accepted: %s", value)
		}
	}
}

// -E and --regex send the pattern as a regex; plain grep sends no regex field.
func TestGrepRegexFlag(t *testing.T) {
	for _, tc := range []struct {
		argv  []string
		regex any
	}{
		{[]string{"grep", "-E", "econn(refused|reset)", "--all-projects"}, true},
		{[]string{"grep", "--all-projects", "--regex", "--", "-econn"}, true},
		{[]string{"grep", "econn(refused|reset)", "--all-projects"}, nil},
	} {
		var sent map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/search" {
				t.Errorf("path %s", r.URL.Path)
			}
			_ = json.NewDecoder(r.Body).Decode(&sent)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": []any{}}, "server_time": "2026-01-01T00:00:00Z"})
		}))
		args, e := Parse(tc.argv)
		if e != nil {
			t.Fatal(tc.argv, e)
		}
		app := &App{Session: litclient.Session{Mapping: Mapping{ClientID: protocol.UUID()}, StateDir: t.TempDir(), Endpoint: server.URL, HTTP: server.Client(), Context: context.Background()}, Args: args, Err: new(bytes.Buffer)}
		if _, e := app.grep(); e != nil {
			t.Fatal(tc.argv, e)
		}
		server.Close()
		if sent["regex"] != tc.regex || sent["query"] != args.Positionals[0] {
			t.Errorf("%v sent %v", tc.argv, sent)
		}
	}
	for _, argv := range [][]string{{"grep", "-E", "-E", "a"}, {"grep", "--regex=true", "a"}, {"grep", "-Ea", "b"}} {
		if _, e := Parse(argv); e == nil {
			t.Errorf("accepted %v", argv)
		}
	}
}
