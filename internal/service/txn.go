package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// reads are the storage reads of a request, shared by read snapshots
// (*storage.ReadTx) and write transactions (*storage.WriteTx).
type reads interface {
	Record(ctx context.Context, typ, id string) (any, error)
	IDByTitle(ctx context.Context, typ, projectID, title string) (string, bool, error)
	IDsByPrefix(ctx context.Context, typ, prefix, projectID string, limit int) ([]string, error)
	List(ctx context.Context, l storage.ListQuery) ([]any, error)
	StructuralSet(ctx context.Context, issueIDs []string) ([]storage.RecordRef, error)
	NewestStamp(ctx context.Context) (string, error)
	Client(ctx context.Context, id string) (protocol.Client, bool, error)
	Claim(ctx context.Context, issueID string) (*protocol.Claim, error)
	LiveClaim(ctx context.Context, issueID string, now time.Time) (*protocol.Claim, error)
	LiveClaims(ctx context.Context, owner string, now time.Time) ([]protocol.Claim, error)
	MilestoneProgress(ctx context.Context, milestoneID string, now time.Time) ([]storage.MemberProgress, error)
	History(ctx context.Context, typ, id string, afterRevision int64, limit int) ([]storage.HistoryEntry, error)
	HistoryRevision(ctx context.Context, typ, id, hash string) (int64, string, bool, error)
	HistoryByRequest(ctx context.Context, typ, id, hash string) (storage.HistoryEntry, bool, error)
	Transaction(ctx context.Context, hash string) (storage.Request, bool, error)
	SearchScan(ctx context.Context, projectID string, fn func(storage.SearchRecord) error) error
	CompletionCandidates(ctx context.Context, typ, projectID string) ([]protocol.Completion, error)
}

// requestState is the request being served: its storage transaction and its times.
type requestState struct {
	ctx context.Context
	q   reads
	// tx is the write transaction of a write; reads have none.
	tx *storage.WriteTx
	// now is the request time for claims and responses; stamp is the process
	// clock's time for record stamps.
	now, stamp time.Time
}

// errReadOnly reports a write attempted by a request classified as a read:
// the route table must classify it as a write.
var errReadOnly = errors.New("a read request cannot write; classify its route as a write")

// serialized runs f as one write: it waits for the process write lock,
// refuses work once the service is stopping, and opens the write transaction
// f reads and writes through. The request time is sampled after the lock is
// taken. Whatever f does not commit is rolled back.
func (s *Server) serialized(ctx context.Context, f func(*Server) error) error {
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
	return f(s.serving(&requestState{ctx: ctx, q: tx, tx: tx, now: w.Now(), stamp: w.Now()}))
}

// snapshot runs f as one read in one WAL read snapshot, without the write
// lock. The request time is sampled before the snapshot begins. A read waits
// while the maximum number of snapshots is open, until ctx ends.
func (s *Server) snapshot(ctx context.Context, f func(*Server) error) error {
	r, e := s.DB.Read(ctx)
	if e != nil {
		return e
	}
	defer r.Close()
	return f(s.serving(&requestState{ctx: ctx, q: r, now: r.Now(), stamp: r.Now()}))
}

// serving returns the request-scoped Server for st. Tests may replace the
// request time with claimClock, which is consulted once the request's
// transaction is open.
func (s *Server) serving(st *requestState) *Server {
	if s.claimClock != nil {
		st.now = s.claimClock().UTC()
	}
	return &Server{shared: s.shared, cur: st}
}

// q reads through the current request's transaction.
func (s *Server) q() reads             { return s.cur.q }
func (s *Server) ctx() context.Context { return s.cur.ctx }

// writeTx is the current request's write transaction.
func (s *Server) writeTx() (*storage.WriteTx, error) {
	if s.cur.tx == nil {
		return nil, errReadOnly
	}
	return s.cur.tx, nil
}

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
	tx, e := s.writeTx()
	if e != nil {
		return e
	}
	ctx := s.ctx()
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
		if e = s.cur.tx.Commit(); e != nil {
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
