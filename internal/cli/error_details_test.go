package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

func TestReadableErrorsIncludeRecoveryDetails(t *testing.T) {
	for _, format := range []string{"cli", "markdown"} {
		t.Run(format, func(t *testing.T) {
			var out, errOut bytes.Buffer
			app := App{Format: format, Out: &out, Err: &errOut}
			err := protocol.E(0, "outcome_uncertain", "Request outcome is uncertain")
			err.Details = map[string]any{"request_id": "request-uuid", "conflict": map[string]any{"revision": json.Number("9007199254740993"), "title": "bad\x1b[2J|title"}}
			if code := app.Error(err); code != 4 {
				t.Fatal(code)
			}
			if out.Len() != 0 {
				t.Fatal("failure stdout", out.String())
			}
			for _, want := range []string{"request-uuid", "9007199254740993", "Request"} {
				if !strings.Contains(errOut.String(), want) {
					t.Errorf("missing %s: %s", want, errOut.String())
				}
			}
			if strings.ContainsRune(errOut.String(), '\x1b') {
				t.Fatal("unsafe controls", errOut.String())
			}
		})
	}
}
func TestWireErrorNumbersRemainExact(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		fmt.Fprint(w, `{"error":{"code":"revision_conflict","message":"changed","details":{"revision":9007199254740993}}}`)
	}))
	defer server.Close()
	var out bytes.Buffer
	app := App{Session: litclient.Session{Context: context.Background(), Endpoint: server.URL, HTTP: server.Client()}, Format: "json", Err: &out}
	err := app.Call("GET", "/record", nil, nil, nil, false)
	if err == nil {
		t.Fatal("missing error")
	}
	if app.Error(err) != 3 || !strings.Contains(out.String(), `"revision":9007199254740993`) {
		t.Fatal(out.String())
	}
}

func TestReadableErrorsPreserveTypedRegistrationRecovery(t *testing.T) {
	for _, format := range []string{"cli", "markdown"} {
		t.Run(format, func(t *testing.T) {
			var errOut bytes.Buffer
			app := App{Format: format, Err: &errOut}
			failure := protocol.E(500, "mapping_write_failed", "Connected, but could not save the logical session mapping")
			failure.Details = map[string]string{"logical_session": "poc-agent", "client_id": "existing-client-id", "service_id": "existing-service-id", "endpoint": "http://127.0.0.1:7411"}
			if app.Error(failure) != 1 {
				t.Fatal("changed exit status")
			}
			for _, want := range []string{"poc-agent", "existing-client-id", "existing-service-id", "http://127.0.0.1:7411"} {
				if !strings.Contains(errOut.String(), want) {
					t.Errorf("missing recovery identity %s: %s", want, errOut.String())
				}
			}
		})
	}
}
func TestEmptyErrorDetailsDoNotAddNoise(t *testing.T) {
	for _, format := range []string{"cli", "markdown"} {
		var errOut bytes.Buffer
		app := App{Format: format, Err: &errOut}
		app.Error(protocol.E(400, "invalid_arguments", "provide a session"))
		if strings.Contains(errOut.String(), "Details") || strings.Contains(errOut.String(), "| Field |") {
			t.Fatal(errOut.String())
		}
	}
}
