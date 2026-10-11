package legacy

// api.go is not part of the frozen copy: it exports what lit-migrate reads and
// what its tests write, calling only the frozen code.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	store "github.com/nerdwave-nick/nerdwave-workflows/cmd/lit-migrate/internal/legacystore"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// Open opens the legacy store in dir and verifies it as the legacy lit-server
// did at every start: the frozen store's Open, then New, which validates every
// client, replays every transaction (InitRecords) and validates every claim
// (InitClaims). An empty directory becomes a new, empty store.
//
// Both write to dir: the lock file, removal of temporary files, replay of a
// committed journal and the title index. Open only a copy of a stopped store.
func Open(dir string, c Config) (*Server, error) {
	st, e := store.Open(dir)
	if e != nil {
		return nil, e
	}
	s, e := New(st, c)
	if e != nil {
		_ = st.Close()
		return nil, e
	}
	return s, nil
}

// Close releases the store's lock.
func (s *Server) Close() error { return s.Store.Close() }

// ServiceID is the store's service identity from service.json.
func (s *Server) ServiceID() string { return s.Store.Identity.ServiceID }

// Root is the store's directory.
func (s *Server) Root() string { return s.Store.Root }

// Clients reads every client record, ordered by client ID.
func (s *Server) Clients() ([]protocol.Client, error) {
	entries, e := s.Store.List("clients")
	if e != nil {
		return nil, e
	}
	out := []protocol.Client{}
	for _, f := range entries {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			return nil, fmt.Errorf("unrecognized client record %s", f.Name())
		}
		c, e := s.Client(strings.TrimSuffix(f.Name(), ".json"))
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ClientID < out[j].ClientID })
	return out, nil
}

// StoredClaim is the claim stored on the issue, live or expired; nil when the
// issue has no claim file or its claim was released.
func (s *Server) StoredClaim(issueID string) (*protocol.Claim, error) { return s.readClaim(issueID) }

// ClaimFile is the store path of an issue's claim file.
func ClaimFile(issueID string) string { return claimPath(issueID) }

// SetClaimClock makes claim operations use now instead of the wall clock.
func (s *Server) SetClaimClock(now func() time.Time) { s.claimClock = now }

// Apply commits a durable request with the legacy write path, as the legacy
// HTTP handlers did after validating it: the intent is canonicalized and
// hashed, then recordTransaction computes the records, history entries, claim
// writes and title index, and the store commits them in one journal batch.
func (s *Server) Apply(i protocol.Intent, c protocol.Client, tokens ...protocol.ClaimToken) (protocol.MutationResult, error) {
	b, hash, e := protocol.Canonical(i)
	if e != nil {
		return protocol.MutationResult{}, e
	}
	if e = protocol.Decode(b, &i); e != nil {
		return protocol.MutationResult{}, e
	}
	writes, r, e := s.recordTransaction(i, hash, c, tokens...)
	if e == nil && len(writes) > 0 {
		e = s.Store.Commit(writes)
	}
	return r, e
}

// Disconnect stores c, which should be disconnected, and clears every claim
// it owns, live or expired, as the legacy disconnect did.
func (s *Server) Disconnect(c protocol.Client) error {
	writes, e := s.disconnectWrites(c)
	if e != nil {
		return e
	}
	return s.Store.Commit(writes)
}

// WriteFiles commits raw store writes, so fixture tests can store what the
// legacy server wrote in other ways (a hand-edited file, an old format).
func (s *Server) WriteFiles(writes ...store.Write) error { return s.Store.Commit(writes) }

// Prepare completes a durable intent with the legacy preparer: it adds every
// changed record as a target and every structural guard, at their current
// revisions. The intent's own targets name the records its operations
// create (no expected revision) or change (their current revision).
func (s *Server) Prepare(i protocol.Intent, c protocol.Client) (protocol.Prepared, error) {
	return s.prepareRecordIntent(i, c)
}
