package service

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// requestState is the request being served: its write transaction and its times.
type requestState struct {
	ctx context.Context
	tx  *storage.WriteTx
	// now is the request time for claims and responses; stamp is the process
	// clock's time for record stamps.
	now, stamp time.Time
}

// serialized runs f as one request: it waits for the process write lock,
// refuses work once the service is stopping, and opens the write transaction
// f reads and writes through. Whatever f does not commit is rolled back.
func (s *Server) serialized(ctx context.Context, f func() error) error {
	w, e := s.DB.LockWrites(ctx)
	if e != nil {
		return e
	}
	defer w.Unlock()
	if s.stopping.Load() {
		return errShuttingDown
	}
	tx, e := w.Begin(ctx)
	if e != nil {
		return e
	}
	now := w.Now()
	if s.claimClock != nil {
		now = s.claimClock().UTC()
	}
	s.cur = &requestState{ctx: ctx, tx: tx, now: now, stamp: w.Now()}
	defer func() { s.cur = nil }()
	return f()
}

// q reads through the current request's transaction.
func (s *Server) q() *storage.WriteTx  { return s.cur.tx }
func (s *Server) ctx() context.Context { return s.cur.ctx }

// changes are a request's writes, applied together just before it commits.
type changes struct {
	request *storage.Request // the durable request, with records
	records []any
	release []string         // claims to delete, by issue
	claims  []protocol.Claim // claims to store
	client  *protocol.Client
	// disconnect writes client as a disconnect, deleting all its claims.
	disconnect bool
}

func (c changes) empty() bool {
	return c.request == nil && len(c.release) == 0 && len(c.claims) == 0 && c.client == nil
}

func (s *Server) apply(c changes) error {
	tx, ctx := s.q(), s.ctx()
	for _, id := range c.release {
		if _, e := tx.DeleteClaim(ctx, id); e != nil {
			return e
		}
	}
	for _, claim := range c.claims {
		if e := tx.PutClaim(ctx, claim); e != nil {
			return e
		}
	}
	if c.request != nil {
		if e := tx.WriteRequest(ctx, *c.request, c.records); e != nil {
			return e
		}
	}
	if c.client != nil && c.disconnect {
		return tx.DisconnectClient(ctx, *c.client)
	}
	if c.client != nil {
		return tx.PutClient(ctx, *c.client)
	}
	return nil
}

// commitResponse writes c, then materializes and bounds the exact success
// envelope, and commits only when it fits: a failure at any point, including
// an oversized response after the writes, leaves nothing committed.
func (s *Server) commitResponse(w http.ResponseWriter, status int, data any, c changes) error {
	if !c.empty() {
		if e := s.apply(c); e != nil {
			return e
		}
	}
	b, e := json.Marshal(protocol.Response{Data: data, ServerTime: s.responseTime()})
	if e != nil {
		return e
	}
	b = append(b, '\n')
	if int64(len(b)) > s.Config.Limits.SnapshotBytes {
		return protocol.E(413, "limit_exceeded", "response exceeds snapshot limit")
	}
	if !c.empty() {
		if e = s.q().Commit(); e != nil {
			return e
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, e = w.Write(b)
	return e
}

func (s *Server) claimNow() time.Time { return s.cur.now }

func (s *Server) responseTime() string { return s.claimNow().Format(time.RFC3339Nano) }

func (s *Server) write(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(protocol.Response{Data: data, ServerTime: s.responseTime()})
}
