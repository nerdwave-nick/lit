package storage

// The setup import (lit-migrate) converts a legacy file store into a new
// database. Its write path keeps the legacy history as it was accepted:
// transaction records with their stored results, and history rows with their
// field differences verbatim, including old entries that changed only the
// now computed membership lists. Current rows go through the same row
// writer as WriteRequest, so whatever that writer maintains with a row is
// maintained for imported rows too.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// Create creates a new database in the data directory dir, which must not
// hold one yet, with the given service identity instead of a new one, and
// opens it like Open. The setup import carries the legacy service identity
// over this way.
func Create(dir, serviceID string) (db *DB, err error) {
	if !protocol.ValidUUID(serviceID) {
		return nil, fmt.Errorf("invalid service identity %q", serviceID)
	}
	switch _, e := os.Lstat(filepath.Join(dir, DatabaseFile)); {
	case e == nil:
		return nil, fmt.Errorf("%s already holds a lit database", dir)
	case !errors.Is(e, os.ErrNotExist):
		return nil, e
	}
	if db, err = Open(dir); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = db.Close()
			db = nil
		}
	}()
	ctx := context.Background()
	w, err := db.LockWrites(ctx)
	if err != nil {
		return nil, err
	}
	defer w.Unlock()
	tx, err := w.Begin(ctx)
	if err != nil {
		return nil, err
	}
	n, err := tx.Counts(ctx)
	if err != nil {
		return nil, err
	}
	if n != (Counts{}) {
		return nil, fmt.Errorf("%s holds a database that is not empty", dir)
	}
	if _, err = tx.exec(ctx, "UPDATE service SET service_id = ?", serviceID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	db.serviceID = serviceID
	return db, nil
}

// ImportTransaction stores the transaction record of a request committed by
// the legacy store, with its intent, time and results as stored there. The
// intent's canonical hash must be the request hash, and the timestamp, in
// TimeFormat, must be later than every stored one: import requests oldest
// first.
func (t *WriteTx) ImportTransaction(ctx context.Context, r Request) error {
	intent, e := t.checkIntent(ctx, r)
	if e != nil {
		return e
	}
	return t.insertTransaction(ctx, r, intent)
}

// ImportRevision is one legacy history entry of a record, for the setup
// import. Differences are stored verbatim; the body never appears among them.
// Body is the legacy whole-body hunk, nil when the body did not change.
type ImportRevision struct {
	OwnerType   string
	OwnerID     string
	Revision    int64
	RequestHash string
	Differences map[string]FieldDifference
	Body        *BodyHunk
}

// ImportRevision stores one history row of an imported transaction: the
// differences as given, and the body hunk converted to a forward diff that
// must reproduce the hunk's after text from its before text.
func (t *WriteTx) ImportRevision(ctx context.Context, h ImportRevision) error {
	if _, ok := typeOrder[h.OwnerType]; !ok || !protocol.ValidUUID(h.OwnerID) || h.Revision < 1 {
		return invalidWrite("history of %s %s revision %d", h.OwnerType, h.OwnerID, h.Revision)
	}
	if _, ok := h.Differences[bodyField(h.OwnerType)]; ok {
		return invalidWrite("history of %s %s revision %d changes %s outside its body hunk", h.OwnerType, h.OwnerID, h.Revision, bodyField(h.OwnerType))
	}
	var bodyDiff any // NULL when the body is unchanged
	if h.Body != nil {
		if h.Body.Before == h.Body.After {
			return invalidWrite("history of %s %s revision %d has a body hunk without a change", h.OwnerType, h.OwnerID, h.Revision)
		}
		d, e := diffBody(h.OwnerType, h.OwnerID, h.Body.Before, h.Body.After)
		if e != nil {
			return e
		}
		bodyDiff = d
	}
	differences, e := json.Marshal(h.Differences)
	if e != nil {
		return invalidWrite("history of %s %s revision %d: %v", h.OwnerType, h.OwnerID, h.Revision, e)
	}
	_, e = t.exec(ctx, "INSERT INTO history (owner_type, owner_id, revision, request_hash, differences, body_diff) VALUES (?, ?, ?, ?, ?, ?)",
		h.OwnerType, h.OwnerID, h.Revision, h.RequestHash, string(differences), bodyDiff)
	return e
}

// ImportRecords creates the current rows of records (protocol.Project, Issue,
// Comment or Milestone), in any order, with their revisions and stamps as
// given and no history; their computed membership lists are ignored. Every
// link must be mirrored within records. Like WriteRequest, it reads every
// record back, checks parents and blocking edges for cycles, and on any error
// leaves the transaction as it was before the call.
func (t *WriteTx) ImportRecords(ctx context.Context, records []any) (err error) {
	if _, err = t.exec(ctx, "SAVEPOINT import_records"); err != nil {
		return err
	}
	defer func() {
		undo := context.WithoutCancel(ctx)
		if err != nil {
			_, _ = t.exec(undo, "ROLLBACK TO import_records")
		}
		if _, e := t.exec(undo, "RELEASE import_records"); err == nil {
			err = e
		}
	}()
	changes := make([]change, 0, len(records))
	seen := map[string]bool{}
	for _, rec := range records {
		typ, id, rev, _, _, e := identity(rec)
		if e != nil {
			return e
		}
		if seen[typ+"/"+id] || rev < 1 {
			return invalidWrite("%s %s imported twice or without a revision", typ, id)
		}
		seen[typ+"/"+id] = true
		changes = append(changes, change{typ: typ, id: id, after: rec})
	}
	sort.SliceStable(changes, func(i, j int) bool { return typeOrder[changes[i].typ] < typeOrder[changes[j].typ] })
	for _, c := range changes {
		if e := t.writeRow(ctx, c); e != nil {
			return e
		}
	}
	var parents, links []string
	for _, c := range changes {
		a, ok := c.after.(protocol.Issue)
		if !ok {
			continue
		}
		if a.ParentID != nil {
			if _, e := t.exec(ctx, "UPDATE issues SET parent_id = ? WHERE id = ?", a.ParentID, a.ID); e != nil {
				return e
			}
			parents = append(parents, a.ID)
		}
		changed, e := t.writeLinks(ctx, a, seen)
		if e != nil {
			return e
		}
		if changed {
			links = append(links, a.ID)
		}
	}
	return t.checkWritten(ctx, changes, parents, links)
}

// Counts are the number of rows of each kind.
type Counts struct {
	Projects, Issues, Comments, Milestones int
	Transactions, History                  int
	Clients, Claims                        int
}

// Counts counts the rows of each kind, live and expired claims alike.
func (q queries) Counts(ctx context.Context) (Counts, error) {
	var n Counts
	e := q.queryRow(ctx, []any{&n.Projects, &n.Issues, &n.Comments, &n.Milestones, &n.Transactions, &n.History, &n.Clients, &n.Claims},
		`SELECT (SELECT count(*) FROM projects), (SELECT count(*) FROM issues), (SELECT count(*) FROM comments),
		(SELECT count(*) FROM milestones), (SELECT count(*) FROM transactions), (SELECT count(*) FROM history),
		(SELECT count(*) FROM clients), (SELECT count(*) FROM claims)`)
	return n, e
}

// RecordIDs lists the IDs of every record of type typ, in ID order.
func (q queries) RecordIDs(ctx context.Context, typ string) ([]string, error) {
	if _, ok := typeOrder[typ]; !ok {
		return nil, fmt.Errorf("unknown record type %q", typ)
	}
	out := []string{}
	e := q.each(ctx, "SELECT id FROM "+typ+" ORDER BY id", nil, func(s rowScanner) error {
		var id string
		e := s.Scan(&id)
		out = append(out, id)
		return e
	})
	return out, e
}
