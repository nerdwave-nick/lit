package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/textdiff"
)

// manifestEntry is one record's body checksum: the body SHA-256 manifest.
type manifestEntry struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	SHA256   string `json:"sha256"`
}

func entryOf(typ string, r any) manifestEntry {
	id, rev, _, body := identity(r)
	h := sha256.Sum256([]byte(body))
	return manifestEntry{typ, id, rev, hex.EncodeToString(h[:])}
}

// checkImported runs checks 3-8 on the imported database in one read
// snapshot, comparing it with snap, and returns the body manifest.
func checkImported(ctx context.Context, db *storage.DB, snap *snapshot, r *Report, logf func(string, ...any)) ([]manifestEntry, error) {
	q, e := db.Read(ctx)
	if e != nil {
		return nil, e
	}
	defer q.Close()
	if r.Counts, e = checkCounts(ctx, q, snap, r.HistoryFiles); e != nil {
		return nil, checkError(3, e)
	}
	passed(r, logf, 3, fmt.Sprintf("%+v", r.Counts))
	if e = checkRecords(ctx, q, snap); e != nil {
		return nil, checkError(4, e)
	}
	passed(r, logf, 4, "")
	manifest, e := checkManifest(ctx, q, snap)
	if e != nil {
		return nil, checkError(5, e)
	}
	passed(r, logf, 5, fmt.Sprintf("%d bodies", len(manifest)))
	histories := map[string][]storage.HistoryEntry{}
	for _, o := range snap.records {
		if histories[o.typ+"/"+o.id], e = q.History(ctx, o.typ, o.id, 0, 0); e != nil {
			return nil, checkError(6, e)
		}
	}
	diffs, e := checkBodies(snap, histories)
	if e != nil {
		return nil, checkError(6, e)
	}
	passed(r, logf, 6, fmt.Sprintf("%d diffs", diffs))
	for _, o := range snap.records {
		if got := histories[o.typ+"/"+o.id]; !sameJSON(got, o.history) {
			return nil, checkError(7, fmt.Errorf("%s %s: the history reads back as %s, the legacy history is %s", o.typ, o.id, show(got), show(o.history)))
		}
	}
	passed(r, logf, 7, "")
	newest, e := q.NewestStamp(ctx)
	if e != nil {
		return nil, checkError(8, e)
	}
	updated := ""
	for _, o := range snap.records {
		_, _, stamp, _ := identity(o.record)
		updated = max(updated, stamp)
	}
	if newest != updated {
		return nil, checkError(8, fmt.Errorf("the newest transaction is stamped %q, the newest record update %q", newest, updated))
	}
	passed(r, logf, 8, newest)
	return manifest, nil
}

func passed(r *Report, logf func(string, ...any), n int, detail string) {
	r.Checks = append(r.Checks, checkNames[n-1])
	if detail != "" {
		detail = " (" + detail + ")"
	}
	logf("lit-migrate: check %d passed: %s%s", n, checkNames[n-1], detail)
}

// checkCounts is check 3: records per type, history files and rows, distinct
// request hashes and transaction rows, clients, and non-null claims.
func checkCounts(ctx context.Context, q *storage.ReadTx, snap *snapshot, historyFiles int) (storage.Counts, error) {
	got, e := q.Counts(ctx)
	if e != nil {
		return got, e
	}
	want := storage.Counts{Transactions: len(snap.requests), History: snap.entries, Clients: len(snap.clients), Claims: len(snap.claims)}
	for _, o := range snap.records {
		switch o.typ {
		case storage.Projects:
			want.Projects++
		case storage.Issues:
			want.Issues++
		case storage.Comments:
			want.Comments++
		case storage.Milestones:
			want.Milestones++
		}
	}
	if got != want || historyFiles != snap.entries {
		return got, fmt.Errorf("the database holds %+v, the legacy store %+v and %d history files", got, want, historyFiles)
	}
	return got, nil
}

// checkRecords is check 4: each legacy record is JSON-equal to the record read
// from the new rows, membership lists compared as computed values; clients
// and claims are compared the same way.
func checkRecords(ctx context.Context, q *storage.ReadTx, snap *snapshot) error {
	for _, o := range snap.records {
		got, e := q.Record(ctx, o.typ, o.id)
		if e != nil {
			return e
		}
		if !sameJSON(got, o.record) {
			return fmt.Errorf("%s %s reads back as %s, the legacy record is %s", o.typ, o.id, show(got), show(o.record))
		}
	}
	for _, c := range snap.clients {
		got, ok, e := q.Client(ctx, c.ClientID)
		if e != nil || !ok || !sameJSON(got, c) {
			return fmt.Errorf("client %s reads back as %s (%v, %v)", c.ClientID, show(got), ok, e)
		}
	}
	for _, c := range snap.claims {
		got, e := q.Claim(ctx, c.IssueID)
		if e != nil || got == nil || !sameJSON(*got, c) {
			return fmt.Errorf("the claim on %s reads back as %s, the legacy claim is %s (%v)", c.IssueID, show(got), show(c), e)
		}
	}
	return nil
}

// checkManifest is check 5: the body manifest of the legacy records equals
// the one of every record in the new database, in both directions.
func checkManifest(ctx context.Context, q *storage.ReadTx, snap *snapshot) ([]manifestEntry, error) {
	old := map[manifestEntry]bool{}
	manifest := make([]manifestEntry, 0, len(snap.records))
	for _, o := range snap.records {
		m := entryOf(o.typ, o.record)
		old[m] = true
		manifest = append(manifest, m)
	}
	found := 0
	for _, typ := range recordTypes {
		ids, e := q.RecordIDs(ctx, typ)
		if e != nil {
			return nil, e
		}
		for _, id := range ids {
			r, e := q.Record(ctx, typ, id)
			if e != nil {
				return nil, e
			}
			if m := entryOf(typ, r); !old[m] {
				return nil, fmt.Errorf("the new database holds %+v, which the legacy store does not", m)
			}
			found++
		}
	}
	if found != len(old) {
		return nil, fmt.Errorf("the new database holds %d of the %d legacy bodies", found, len(old))
	}
	return manifest, nil
}

// checkBodies is check 6: every legacy body hunk converts to a diff that
// reproduces it, and each owner's history, replayed from the empty body,
// reproduces its current body. It returns the number of diffs.
func checkBodies(snap *snapshot, histories map[string][]storage.HistoryEntry) (int, error) {
	diffs := 0
	for _, o := range snap.records {
		for _, h := range o.history {
			for _, hunk := range h.BodyHunks {
				if got, e := textdiff.Apply(textdiff.Diff(hunk.Before, hunk.After), hunk.Before); e != nil || got != hunk.After {
					return diffs, fmt.Errorf("%s %s revision %d: the converted diff does not reproduce the body (%v)", o.typ, o.id, h.Revision, e)
				}
				diffs++
			}
		}
		body := ""
		for _, h := range histories[o.typ+"/"+o.id] {
			for _, hunk := range h.BodyHunks {
				body = hunk.After
			}
		}
		if _, _, _, want := identity(o.record); body != want {
			return diffs, fmt.Errorf("%s %s: its body chain from the empty body gives %q, not its body", o.typ, o.id, shorten(body))
		}
	}
	return diffs, nil
}

// verifyDatabase is check 9: lit-server verify, the startup checks and a full
// history replay, on the closed new database. A search index the startup
// checks had to rebuild is a failure too: the import must maintain it.
func verifyDatabase(ctx context.Context, dir string) error {
	db, e := storage.OpenExisting(dir)
	if e != nil {
		return e
	}
	defer db.Close()
	var repaired []string
	r, e := db.Verify(ctx, func(format string, args ...any) { repaired = append(repaired, fmt.Sprintf(format, args...)) })
	switch {
	case e != nil:
		return e
	case len(r.Problems) > 0:
		return fmt.Errorf("%d problems: %s", len(r.Problems), strings.Join(r.Problems[:min(5, len(r.Problems))], "; "))
	case len(repaired) > 0:
		return fmt.Errorf("the startup checks repaired the new database: %s", strings.Join(repaired, "; "))
	}
	return db.Close()
}

func show(v any) string { b, _ := json.Marshal(v); return shorten(string(b)) }

func shorten(s string) string {
	const limit = 400
	if len(s) <= limit {
		return s
	}
	return fmt.Sprintf("%s... (%d bytes)", s[:limit], len(s))
}
