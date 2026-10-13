package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/nerdwave-nick/lit/internal/litclient"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCallPreservesLargeResponseNumbers(t *testing.T) {
	record := `{"schema_version":1,"id":"p","revision":9007199254740993,"created_at":"now","updated_at":"now","title":"test/large","description":"# Body","repository_refs":[],"issue_ids":[]}`
	for _, payload := range []string{`{"data":{"items":[` + record + `],"next_cursor":null}}`, `{"items":[` + record + `],"next_cursor":null}`} {
		t.Run(payload[:8], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, payload) }))
			defer server.Close()
			var out bytes.Buffer
			app := App{Session: litclient.Session{Context: context.Background(), HTTP: server.Client(), Endpoint: server.URL}, Args: Args{Command: "projects", Verb: "get"}, Format: "markdown", Out: &out, Err: &out}
			var result Result
			if err := app.Call("GET", "/read", nil, nil, &result, false); err != nil {
				t.Fatal(err)
			}
			revision := result.Items[0].(map[string]any)["revision"]
			if revision != json.Number("9007199254740993") {
				t.Fatalf("rounded revision: %T %v", revision, revision)
			}
			if app.Print(result) != 0 || !strings.Contains(out.String(), "revision: 9007199254740993\n") {
				t.Fatal(out.String())
			}
			out.Reset()
			app.Format = "json"
			if app.Print(result) != 0 || !strings.Contains(out.String(), `"revision":9007199254740993`) {
				t.Fatal(out.String())
			}
		})
	}
}
