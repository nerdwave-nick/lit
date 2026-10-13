package importer

import (
	"context"
	"fmt"

	"github.com/nerdwave-nick/nerdwave-workflows/cmd/lit-migrate/internal/legacy"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// importSnapshot writes snap into the new, empty database in one write
// transaction: the transaction records oldest first, the current records,
// one history row per legacy history entry, the clients and the claims.
func importSnapshot(ctx context.Context, db *storage.DB, snap *snapshot) error {
	w, e := db.LockWrites(ctx)
	if e != nil {
		return e
	}
	defer w.Unlock()
	tx, e := w.Begin(ctx)
	if e != nil {
		return e
	}
	for _, r := range snap.requests {
		if e = tx.ImportTransaction(ctx, r); e != nil {
			return fmt.Errorf("transaction %s at %s: %w", r.Hash, r.Timestamp, e)
		}
	}
	records := make([]any, 0, len(snap.records))
	for _, o := range snap.records {
		records = append(records, o.record)
	}
	if e = tx.ImportRecords(ctx, records); e != nil {
		return fmt.Errorf("records: %w", e)
	}
	for _, o := range snap.records {
		for _, h := range o.history {
			rev, e := revision(o.typ, h)
			if e == nil {
				e = tx.ImportRevision(ctx, rev)
			}
			if e != nil {
				return fmt.Errorf("history of %s %s revision %d: %w", o.typ, o.id, h.Revision, e)
			}
		}
	}
	for _, c := range snap.clients {
		if e = tx.PutClient(ctx, c); e != nil {
			return fmt.Errorf("client %s: %w", c.ClientID, e)
		}
	}
	for _, c := range snap.claims {
		// Claims keep their original times, byte for byte. The legacy server
		// wrote them in canonical RFC3339Nano form; a time in another form
		// was edited outside lit and cannot be copied exactly, so the import
		// stops instead of rewriting it.
		if e = tx.PutClaim(ctx, c); e != nil {
			return fmt.Errorf("%w; release that claim on the legacy server, or repair its claim file on the copy, and run the import again", e)
		}
	}
	return tx.Commit()
}

// revision converts one legacy history entry: its differences verbatim,
// including those of the now computed membership lists, and its whole-body
// hunk, if any, which the storage module stores as a verified forward diff.
func revision(typ string, h legacy.ProjectHistory) (storage.ImportRevision, error) {
	r := storage.ImportRevision{OwnerType: typ, OwnerID: h.OwnerID, Revision: h.Revision, RequestHash: h.RequestHash, Differences: map[string]storage.FieldDifference{}}
	if h.BeforeRevision != h.Revision-1 {
		return r, fmt.Errorf("revision %d follows revision %d", h.Revision, h.BeforeRevision)
	}
	if h.Differences == nil {
		r.Differences = nil
	}
	for k, d := range h.Differences {
		r.Differences[k] = storage.FieldDifference{Before: d.Before, After: d.After}
	}
	switch len(h.BodyHunks) {
	case 0:
	case 1:
		r.Body = &storage.BodyHunk{Before: h.BodyHunks[0].Before, After: h.BodyHunks[0].After}
	default:
		return r, fmt.Errorf("%d body hunks", len(h.BodyHunks))
	}
	return r, nil
}
