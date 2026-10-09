package litclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

func TestLockHoldsMappingUntilClose(t *testing.T) {
	dir := t.TempDir()
	m := Mapping{SchemaVersion: 1, ClientID: protocol.UUID(), ServiceID: protocol.UUID(), Endpoint: "http://127.0.0.1:7411"}
	if err := WriteJSON(MappingPath(dir, "held"), m); err != nil {
		t.Fatal(err)
	}
	first := &Session{StateDir: dir}
	if err := first.Lock("held", time.Now().Add(time.Second)); err != nil || first.Mapping != m {
		t.Fatalf("lock: %+v %v", first.Mapping, err)
	}
	second := &Session{StateDir: dir}
	if err := second.Lock("held", time.Now().Add(20*time.Millisecond)); errorCode(err) != "session_locked" {
		t.Fatalf("concurrent lock: %v", err)
	}
	first.Close()
	first.Close()
	if err := second.Lock("held", time.Now().Add(time.Second)); err != nil {
		t.Fatalf("after close: %v", err)
	}
	second.Close()

	unmapped := &Session{StateDir: dir}
	if err := unmapped.Lock("fresh", time.Now().Add(time.Second)); err != nil || unmapped.Mapping != (Mapping{}) {
		t.Fatalf("unmapped: %+v %v", unmapped.Mapping, err)
	}
	unmapped.Close()
	if err := os.WriteFile(MappingPath(dir, "corrupt"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	corrupt := &Session{StateDir: dir}
	if err := corrupt.Lock("corrupt", time.Now().Add(time.Second)); errorCode(err) != "invalid_local_state" {
		t.Fatalf("corrupt: %v", err)
	}
	// A failed Lock releases the lock it took.
	if err := corrupt.Lock("corrupt", time.Now().Add(20*time.Millisecond)); errorCode(err) != "invalid_local_state" {
		t.Fatalf("corrupt retry: %v", err)
	}
}

func TestOpenChecksServiceAndClient(t *testing.T) {
	serviceID, clientID := protocol.UUID(), protocol.UUID()
	status := "connected"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/meta":
			json.NewEncoder(w).Encode(protocol.Response{Data: protocol.Meta{ServiceID: serviceID, APIMajor: 1}})
		case "/v1/clients/" + clientID:
			json.NewEncoder(w).Encode(protocol.Response{Data: clientRecord(clientID, 3, status)})
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer server.Close()
	open := func(m Mapping, o OpenOptions) (*Session, error) {
		s := &Session{HTTP: server.Client(), Context: context.Background(), Mapping: m}
		o.Endpoint, o.EndpointSet = server.URL, true
		return s, s.Open(o)
	}
	mapped := Mapping{SchemaVersion: 1, ClientID: clientID, ServiceID: serviceID, Endpoint: server.URL}

	if _, err := open(Mapping{}, OpenOptions{}); errorCode(err) != "not_connected" {
		t.Fatalf("unmapped: %v", err)
	}
	if s, err := open(Mapping{}, OpenOptions{AllowUnmapped: true}); err != nil || s.Meta.ServiceID != serviceID || s.Endpoint != server.URL || s.Client.ClientID != "" {
		t.Fatalf("unmapped allowed: %+v %v", s, err)
	}
	other := mapped
	other.ServiceID = protocol.UUID()
	if _, err := open(other, OpenOptions{AllowUnmapped: true}); errorCode(err) != "wrong_service" {
		t.Fatalf("wrong service: %v", err)
	}
	if s, err := open(mapped, OpenOptions{}); err != nil || s.Client.ClientID != clientID {
		t.Fatalf("connected: %+v %v", s, err)
	}
	status = "disconnected"
	if s, err := open(mapped, OpenOptions{}); errorCode(err) != "client_disconnected" || s.Client.Status != "disconnected" {
		t.Fatalf("disconnected: %+v %v", s, err)
	}
	if s, err := open(mapped, OpenOptions{AllowDisconnected: true}); err != nil || s.Client.Status != "disconnected" {
		t.Fatalf("disconnected allowed: %+v %v", s, err)
	}
	s := &Session{HTTP: server.Client(), Context: context.Background()}
	if err := s.Open(OpenOptions{Endpoint: "localhost:7411", EndpointSet: true, EnvEndpoint: server.URL}); errorCode(err) != "invalid_endpoint" {
		t.Fatalf("invalid endpoint: %v", err)
	}
}
