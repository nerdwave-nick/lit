package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/service"
	"github.com/nerdwave-nick/lit/internal/storage"
)

type endpointTransport func(*http.Request) (*http.Response, error)

func (f endpointTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEndpointSelectionAndServiceBinding(t *testing.T) {
	for _, tc := range []struct{ name, explicit, environment, stored, want string }{
		{name: "default", want: "http://127.0.0.1:7411"},
		{name: "remembered", stored: "https://remembered.example", want: "https://remembered.example"},
		{name: "environment", environment: "https://env.example", stored: "https://remembered.example", want: "https://env.example"},
		{name: "flag", explicit: "https://flag.example/", environment: "https://env.example", stored: "https://remembered.example", want: "https://flag.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			state := t.TempDir()
			t.Setenv("LIT_STATE_DIR", state)
			t.Setenv("LIT_SESSION", "")
			t.Setenv("LIT_ENDPOINT", "")
			data, err := storage.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer data.Close()
			handler, err := service.New(data, service.Config{Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"test"}})
			if err != nil {
				t.Fatal(err)
			}
			var endpoints []string
			original := http.DefaultTransport
			http.DefaultTransport = endpointTransport(func(r *http.Request) (*http.Response, error) {
				endpoints = append(endpoints, r.URL.Scheme+"://"+r.URL.Host)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w.Result(), nil
			})
			t.Cleanup(func() { http.DefaultTransport = original })
			var out, stderr bytes.Buffer
			if tc.stored != "" {
				if code := Run([]string{"connect", "--session", "endpoint-test", "--endpoint", tc.stored, "--actor-name", "Tester", "--actor-kind", "human"}, &out, &stderr); code != 0 {
					t.Fatalf("seed: %d %s", code, &stderr)
				}
			}
			endpoints = nil
			out.Reset()
			stderr.Reset()
			t.Setenv("LIT_ENDPOINT", tc.environment)
			args := []string{"connect", "--session", "endpoint-test", "--actor-name", "Tester", "--actor-kind", "human"}
			if tc.explicit != "" {
				args = append(args, "--endpoint", tc.explicit)
			}
			if code := Run(args, &out, &stderr); code != 0 {
				t.Fatalf("connect: %d %s", code, &stderr)
			}
			if len(endpoints) < 2 {
				t.Fatalf("did not connect: %v", endpoints)
			}
			for _, endpoint := range endpoints {
				if endpoint != tc.want {
					t.Fatalf("request to %q want %q", endpoint, tc.want)
				}
			}
			saved, err := litclient.ReadMapping(litclient.MappingPath(state, "endpoint-test"))
			if err != nil || saved.Endpoint != tc.want {
				t.Fatalf("mapping: %+v %v", saved, err)
			}
			saved.ServiceID = protocol.UUID()
			if err := litclient.WriteJSON(litclient.MappingPath(state, "endpoint-test"), saved); err != nil {
				t.Fatal(err)
			}
			out.Reset()
			stderr.Reset()
			endpoints = nil
			if code := Run(args, &out, &stderr); code == 0 || !strings.Contains(stderr.String(), "wrong_service") {
				t.Fatalf("binding lost: %d %s", code, &stderr)
			}
			if len(endpoints) != 1 {
				t.Fatalf("wrong service performed further requests: %v", endpoints)
			}
		})
	}
}

func TestInvalidExplicitEndpointDoesNotFallback(t *testing.T) {
	for _, value := range []string{"", "localhost:7411", "ftp://example.com", "http://example.com/path", "http://user:secret@example.com", "http://example.com?query=1", "http://example.com#fragment", "http://[::1"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("LIT_STATE_DIR", filepath.Join(t.TempDir(), "state"))
			t.Setenv("LIT_ENDPOINT", "https://valid.example")
			original := http.DefaultTransport
			http.DefaultTransport = endpointTransport(func(r *http.Request) (*http.Response, error) {
				t.Fatalf("invalid endpoint transmitted: %s", r.URL)
				return nil, nil
			})
			t.Cleanup(func() { http.DefaultTransport = original })
			var out, stderr bytes.Buffer
			code := Run([]string{"connect", "--endpoint=" + value}, &out, &stderr)
			if code == 0 || (!strings.Contains(stderr.String(), "invalid_endpoint") && !strings.Contains(stderr.String(), "invalid_arguments")) {
				t.Fatalf("code=%d err=%s", code, &stderr)
			}
		})
	}
}
