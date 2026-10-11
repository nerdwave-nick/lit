package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// TimeFormat is the fixed-width UTC layout of record and transaction stamps,
// so that their text order is their time order.
const TimeFormat = "2006-01-02T15:04:05.000000Z"

// ErrInvalidWrite matches (with errors.Is) a write that breaks an invariant
// the service should already have checked. The write changes nothing.
var ErrInvalidWrite = errors.New("invalid storage write")

func invalidWrite(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidWrite, fmt.Sprintf(format, args...))
}

// Request is one committed durable request: its hash, its canonical intent,
// its time and its results. The actor and client are the intent's.
type Request struct {
	Hash      string
	Intent    protocol.Intent
	Timestamp string
	Results   []protocol.ChangedObject
}

// WriteRequest stores one durable request: one transaction record holding the
// canonical intent and results once, and for each changed record its new
// current row and one history row. records are the changed records after the
// request (protocol.Project, Issue, Comment or Milestone), in any order; their
// computed membership lists are ignored.
//
// It checks what the schema cannot: the intent's canonical hash, a timestamp
// later than every stored one, one result per record, consecutive revisions
// stamped with the request time, a change to at least one of each record's own
// fields, links mirrored inside the request, no parent or blocking cycle, and
// that every body diff applies. A violation is ErrInvalidWrite; then, as on
// any error, the transaction is left as it was before the call.
func (t *WriteTx) WriteRequest(ctx context.Context, r Request, records []any) (err error) {
	if _, err = t.exec(ctx, "SAVEPOINT write_request"); err != nil {
		return err
	}
	defer func() {
		undo := context.WithoutCancel(ctx)
		if err != nil {
			_, _ = t.exec(undo, "ROLLBACK TO write_request")
		}
		if _, e := t.exec(undo, "RELEASE write_request"); err == nil {
			err = e
		}
	}()
	intent, e := t.checkRequest(ctx, r, records)
	if e != nil {
		return e
	}
	actor := r.Intent.Actor
	results, _ := json.Marshal(r.Results)
	if _, e = t.exec(ctx, "INSERT INTO transactions (request_hash, client_id, actor_name, actor_kind, timestamp, intent, results) VALUES (?, ?, ?, ?, ?, ?, ?)",
		r.Hash, actor.ClientID, actor.Name, actor.Kind, r.Timestamp, string(intent), string(results)); e != nil {
		return e
	}
	return t.writeRecords(ctx, r, records)
}

// checkRequest validates the request itself and returns its canonical intent.
func (t *WriteTx) checkRequest(ctx context.Context, r Request, records []any) ([]byte, error) {
	intent, hash, e := protocol.Canonical(r.Intent)
	if e != nil {
		return nil, invalidWrite("intent: %v", e)
	}
	if hash != r.Hash {
		return nil, invalidWrite("request hash %s is not the intent's hash %s", r.Hash, hash)
	}
	if e = checkStamp(r.Timestamp); e != nil {
		return nil, e
	}
	newest, e := t.NewestStamp(ctx)
	if e != nil {
		return nil, e
	}
	if r.Timestamp <= newest {
		return nil, invalidWrite("timestamp %s is not after the newest transaction %s", r.Timestamp, newest)
	}
	if len(records) == 0 || len(records) != len(r.Results) {
		return nil, invalidWrite("%d records for %d results", len(records), len(r.Results))
	}
	results := map[string]protocol.ChangedObject{}
	for _, c := range r.Results {
		results[c.Type+"/"+c.ID] = c
	}
	for _, rec := range records {
		typ, id, rev, _, _, e := identity(rec)
		if e != nil {
			return nil, e
		}
		c, ok := results[typ+"/"+id]
		if !ok || c.Revision != rev || c.BeforeRevision != rev-1 {
			return nil, invalidWrite("no result for %s %s revision %d", typ, id, rev)
		}
		delete(results, typ+"/"+id)
	}
	return intent, nil
}

// checkStamp accepts only TimeFormat text.
func checkStamp(s string) error {
	if v, e := time.Parse(TimeFormat, s); e != nil || v.Format(TimeFormat) != s {
		return invalidWrite("timestamp %q is not in the form %s", s, TimeFormat)
	}
	return nil
}

// NewestStamp is the timestamp of the newest committed transaction, or "" when
// none exists. Record stamps stay strictly increasing by following it.
func (q queries) NewestStamp(ctx context.Context) (string, error) {
	var s string
	e := q.queryRow(ctx, []any{&s}, "SELECT timestamp FROM transactions ORDER BY seq DESC LIMIT 1")
	if errors.Is(e, sql.ErrNoRows) {
		return "", nil
	}
	return s, e
}

// Transaction reads the committed request with the given hash; found is false
// when no such request committed.
func (q queries) Transaction(ctx context.Context, hash string) (r Request, found bool, err error) {
	var intent, results string
	e := q.queryRow(ctx, []any{&r.Hash, &r.Timestamp, &intent, &results}, "SELECT request_hash, timestamp, intent, results FROM transactions WHERE request_hash = ?", hash)
	if errors.Is(e, sql.ErrNoRows) {
		return Request{}, false, nil
	}
	if e != nil {
		return Request{}, false, e
	}
	if r.Intent, r.Results, e = decodeTransaction(intent, results); e != nil {
		return Request{}, false, e
	}
	return r, true, nil
}

func decodeTransaction(intent, results string) (protocol.Intent, []protocol.ChangedObject, error) {
	var i protocol.Intent
	var c []protocol.ChangedObject
	if e := protocol.Decode([]byte(intent), &i); e != nil {
		return i, nil, fmt.Errorf("stored intent: %w", e)
	}
	if e := protocol.Decode([]byte(results), &c); e != nil {
		return i, nil, fmt.Errorf("stored results: %w", e)
	}
	return i, c, nil
}
