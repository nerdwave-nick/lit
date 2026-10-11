package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"time"
)

// Claims are stored one row per issue, live or expired; release deletes the
// row. Their times are Unix nanoseconds, so liveness and order compare
// numerically; they convert to and from the protocol's RFC3339Nano UTC text
// here. A claim is live while its expiry is after the request time. Claims
// carry schema version 1, which is not stored; their owner_actor is a
// projection of the owning client, never stored.

const claimColumns = "SELECT issue_id, owner_client_id, token, acquired_at, expires_at FROM claims "

// Claim reads the stored claim on issueID, live or expired; nil when none.
func (q queries) Claim(ctx context.Context, issueID string) (*protocol.Claim, error) {
	return q.oneClaim(ctx, claimColumns+"WHERE issue_id = ?", issueID)
}

// LiveClaim reads the claim on issueID when it is live at now; nil otherwise.
func (q queries) LiveClaim(ctx context.Context, issueID string, now time.Time) (*protocol.Claim, error) {
	return q.oneClaim(ctx, claimColumns+"WHERE issue_id = ? AND expires_at > ?", issueID, now.UnixNano())
}

// LiveClaims lists the claims live at now that owner holds, or that anyone
// holds when owner is empty, ordered by acquisition time and then issue ID.
func (q queries) LiveClaims(ctx context.Context, owner string, now time.Time) ([]protocol.Claim, error) {
	if owner == "" {
		return q.claims(ctx, claimColumns+"WHERE expires_at > ? ORDER BY acquired_at, issue_id", now.UnixNano())
	}
	return q.claims(ctx, claimColumns+"WHERE owner_client_id = ? AND expires_at > ? ORDER BY acquired_at, issue_id", owner, now.UnixNano())
}

// OwnedClaims lists every claim owner holds, live or expired, ordered by
// acquisition time and then issue ID.
func (q queries) OwnedClaims(ctx context.Context, owner string) ([]protocol.Claim, error) {
	return q.claims(ctx, claimColumns+"WHERE owner_client_id = ? ORDER BY acquired_at, issue_id", owner)
}

// ExpiredClaims lists the claims no longer live at now, ordered by expiry
// time and then issue ID.
func (q queries) ExpiredClaims(ctx context.Context, now time.Time) ([]protocol.Claim, error) {
	return q.claims(ctx, claimColumns+"WHERE expires_at <= ? ORDER BY expires_at, issue_id", now.UnixNano())
}

// PutClaim inserts the claim on c.IssueID, or replaces the one stored there
// (acquiring over an expired claim, renewing, forcing). The service applies
// claim policy first; the schema's checks are the backstop.
func (t *WriteTx) PutClaim(ctx context.Context, c protocol.Claim) error {
	if c.SchemaVersion != 1 {
		return fmt.Errorf("claim on %s: unsupported schema_version %d", c.IssueID, c.SchemaVersion)
	}
	if c.OwnerActor != nil {
		return fmt.Errorf("claim on %s: owner_actor is derived from the client and is not stored", c.IssueID)
	}
	acquired, e := claimNanos("acquired_at", c.AcquiredAt)
	if e != nil {
		return fmt.Errorf("claim on %s: %w", c.IssueID, e)
	}
	expires, e := claimNanos("expires_at", c.ExpiresAt)
	if e != nil {
		return fmt.Errorf("claim on %s: %w", c.IssueID, e)
	}
	_, e = t.exec(ctx, `INSERT INTO claims (issue_id, owner_client_id, token, acquired_at, expires_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (issue_id) DO UPDATE SET owner_client_id = excluded.owner_client_id, token = excluded.token,
	acquired_at = excluded.acquired_at, expires_at = excluded.expires_at`,
		c.IssueID, c.OwnerClientID, c.Token, acquired, expires)
	return e
}

// DeleteClaim releases the claim on issueID by deleting its row; deleted
// reports whether one existed. Nothing is kept in its place.
func (t *WriteTx) DeleteClaim(ctx context.Context, issueID string) (deleted bool, err error) {
	r, e := t.exec(ctx, "DELETE FROM claims WHERE issue_id = ?", issueID)
	if e != nil {
		return false, e
	}
	n, e := r.RowsAffected()
	return n > 0, t.classify(e)
}

// claimNanos converts a claim time to Unix nanoseconds. Only UTC RFC3339Nano
// text that formats back to itself is accepted, so a stored claim reads back
// byte-identical.
func claimNanos(field, s string) (int64, error) {
	v, e := time.Parse(time.RFC3339Nano, s)
	if e != nil || v.Location() != time.UTC || claimText(v.UnixNano()) != s {
		return 0, fmt.Errorf("%s %q is not a canonical UTC RFC3339Nano time", field, s)
	}
	return v.UnixNano(), nil
}

func claimText(nanos int64) string { return time.Unix(0, nanos).UTC().Format(time.RFC3339Nano) }

type claimRow struct {
	c                 protocol.Claim
	acquired, expires int64
}

func (r *claimRow) dest() []any {
	return []any{&r.c.IssueID, &r.c.OwnerClientID, &r.c.Token, &r.acquired, &r.expires}
}

func (r *claimRow) claim() protocol.Claim {
	r.c.SchemaVersion, r.c.AcquiredAt, r.c.ExpiresAt = 1, claimText(r.acquired), claimText(r.expires)
	return r.c
}

func (q queries) oneClaim(ctx context.Context, query string, args ...any) (*protocol.Claim, error) {
	var r claimRow
	e := q.queryRow(ctx, r.dest(), query, args...)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	c := r.claim()
	return &c, nil
}

// claims runs a claim query; the result is empty, never nil, without rows.
func (q queries) claims(ctx context.Context, query string, args ...any) ([]protocol.Claim, error) {
	rows, e := q.tx.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, q.classify(e)
	}
	defer rows.Close()
	out := []protocol.Claim{}
	for rows.Next() {
		var r claimRow
		if e = rows.Scan(r.dest()...); e != nil {
			return nil, q.classify(e)
		}
		out = append(out, r.claim())
	}
	return out, q.classify(rows.Err())
}
