// Frozen copy of the legacy lit-server service at c5af73e (internal/service/service.go, transactions.go);
// see doc.go. Do not change it.

package legacy

import (
	"fmt"
	store "github.com/nerdwave-nick/nerdwave-workflows/cmd/lit-migrate/internal/legacystore"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Server struct {
	Mu          sync.Mutex
	Store       *store.Store
	Config      Config
	stopping    atomic.Bool
	claimClock  func() time.Time
	requestTime *time.Time
}

func New(s *store.Store, c Config) (*Server, error) {
	v := &Server{Store: s, Config: c}
	entries, e := s.List("clients")
	if e != nil {
		return nil, e
	}
	for _, f := range entries {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			return nil, fmt.Errorf("unrecognized client record")
		}
		id := strings.TrimSuffix(f.Name(), ".json")
		if _, e := v.Client(id); e != nil {
			return nil, e
		}
	}
	if e := v.InitProjects(); e != nil {
		return nil, e
	}
	if e := v.InitClaims(); e != nil {
		return nil, e
	}
	return v, nil
}
func (s *Server) Client(id string) (protocol.Client, error) {
	var c protocol.Client
	if !protocol.ValidUUID(id) {
		return c, protocol.E(404, "unknown_client", "unknown client identity")
	}
	b, e := s.Store.Read("clients/" + id + ".json")
	if os.IsNotExist(e) {
		return c, protocol.E(404, "unknown_client", "unknown client identity")
	}
	if e != nil {
		return c, e
	}
	if e = protocol.Decode(b, &c); e != nil {
		return c, fmt.Errorf("invalid client record: %w", e)
	}
	if c.SchemaVersion != 1 || c.ClientID != id || c.StateRevision < 1 || (c.Status != "connected" && c.Status != "disconnected") || validateClient(c) != nil {
		return c, fmt.Errorf("unsupported or invalid client record")
	}
	return c, nil
}
func validateClient(c protocol.Client) error {
	if strings.TrimSpace(c.Actor.Name) == "" || (c.Actor.Kind != "human" && c.Actor.Kind != "agent") {
		return fmt.Errorf("actor requires a name and human or agent kind")
	}
	if c.OutputFormat != nil && *c.OutputFormat != "json" && *c.OutputFormat != "cli" && *c.OutputFormat != "markdown" && *c.OutputFormat != "human" {
		return fmt.Errorf("output_format must be cli, markdown or json (human is a legacy alias)")
	}
	if c.ProjectID != nil && !protocol.ValidUUID(*c.ProjectID) {
		return fmt.Errorf("invalid project ID")
	}
	return nil
}
func (s *Server) SaveClient(c protocol.Client) error {
	return s.Store.Commit([]store.Write{store.JSONWrite("clients/"+c.ClientID+".json", c)})
}
func invalid(msg string) error { return protocol.E(422, "validation_failed", msg) }
func union(a, b []string) []string {
	out := append([]string{}, a...)
	for _, v := range b {
		found := false
		for _, x := range out {
			found = found || v == x
		}
		if !found {
			out = append(out, v)
		}
	}
	return out
}
