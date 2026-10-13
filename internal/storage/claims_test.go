package storage

import (
	"context"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"sort"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// claimWorld holds committed issues (sorted by ID) and two connected clients.
type claimWorld struct {
	issues []string
	a, b   protocol.Client
}

// claimFixture inserts a project with n issues using plain SQL, plus two
// connected clients.
func claimFixture(t *testing.T, db *DB, n int) claimWorld {
	t.Helper()
	w := claimWorld{}
	for _, c := range []*protocol.Client{&w.a, &w.b} {
		*c = protocol.Client{SchemaVersion: 1, ClientID: protocol.UUID(), StateRevision: 1, Status: "connected", Actor: protocol.Actor{Name: "agent", Kind: "agent"}}
	}
	e := write(t, db, func(ctx context.Context, tx *WriteTx) error {
		p, e := addProject(ctx, tx.queries, "feat/claims")
		if e != nil {
			return e
		}
		for i := 0; i < n; i++ {
			id := protocol.UUID()
			if _, e = tx.exec(ctx, issueSQL, id, p, "Issue "+id, "issue "+id, "open", nil); e != nil {
				return e
			}
			w.issues = append(w.issues, id)
		}
		if e = tx.PutClient(ctx, w.a); e != nil {
			return e
		}
		return tx.PutClient(ctx, w.b)
	})
	if e != nil {
		t.Fatal(e)
	}
	sort.Strings(w.issues)
	return w
}

func claimOf(issue, owner string, acquired, expires time.Time) protocol.Claim {
	return protocol.Claim{SchemaVersion: 1, IssueID: issue, OwnerClientID: owner, Token: protocol.UUID(), AcquiredAt: acquired.Format(time.RFC3339Nano), ExpiresAt: expires.Format(time.RFC3339Nano)}
}

func putClaims(t *testing.T, db *DB, claims ...protocol.Claim) {
	t.Helper()
	if e := write(t, db, func(ctx context.Context, tx *WriteTx) error {
		for _, c := range claims {
			if e := tx.PutClaim(ctx, c); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

// read runs f in a fresh read snapshot.
func read(t testing.TB, db *DB, f func(ctx context.Context, r *ReadTx)) {
	t.Helper()
	ctx := context.Background()
	r, e := db.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	f(ctx, r)
}

func storedClaim(t *testing.T, db *DB, issue string) (c *protocol.Claim) {
	t.Helper()
	read(t, db, func(ctx context.Context, r *ReadTx) {
		var e error
		if c, e = r.Claim(ctx, issue); e != nil {
			t.Fatal(e)
		}
	})
	return c
}

func issueIDs(claims []protocol.Claim) []string {
	out := []string{}
	for _, c := range claims {
		out = append(out, c.IssueID)
	}
	return out
}

func TestClaimsRoundTripExactly(t *testing.T) {
	db := openTemp(t)
	w := claimFixture(t, db, 3)
	// RFC3339Nano drops trailing zeros, so stored times vary in width.
	claims := []protocol.Claim{
		claimOf(w.issues[0], w.a.ClientID, t0, t0.Add(30*time.Minute)),
		claimOf(w.issues[1], w.a.ClientID, t0.Add(500*time.Millisecond), t0.Add(time.Hour)),
		claimOf(w.issues[2], w.b.ClientID, t0.Add(123456789), t0.Add(time.Hour+1)),
	}
	putClaims(t, db, claims...)
	for _, want := range claims {
		sameJSON(t, storedClaim(t, db, want.IssueID), want)
	}

	// Renewal rewrites the claim in place.
	renewed := claims[0]
	renewed.ExpiresAt = t0.Add(45 * time.Minute).Format(time.RFC3339Nano)
	putClaims(t, db, renewed)
	sameJSON(t, storedClaim(t, db, renewed.IssueID), renewed)
}

func TestInvalidClaimsAreRefused(t *testing.T) {
	db := openTemp(t)
	w := claimFixture(t, db, 1)
	issue := w.issues[0]
	cases := []struct {
		name string
		edit func(*protocol.Claim)
		want string
	}{
		{"schema version", func(c *protocol.Claim) { c.SchemaVersion = 2 }, "schema_version"},
		{"owner actor is a projection", func(c *protocol.Claim) { c.OwnerActor = &protocol.Actor{Name: "x", Kind: "agent"} }, "owner_actor"},
		{"time with an offset", func(c *protocol.Claim) { c.AcquiredAt = "2026-10-11T14:00:00+02:00" }, "acquired_at"},
		{"time that would not round-trip", func(c *protocol.Claim) { c.ExpiresAt = "2026-10-11T13:00:00.500Z" }, "expires_at"},
		{"time that does not parse", func(c *protocol.Claim) { c.ExpiresAt = "soon" }, "expires_at"},
		{"expiry not after acquisition", func(c *protocol.Claim) { c.ExpiresAt = c.AcquiredAt }, "CHECK"},
		{"token", func(c *protocol.Claim) { c.Token = "t" }, "CHECK"},
		{"unknown issue", func(c *protocol.Claim) { c.IssueID = protocol.UUID() }, "FOREIGN KEY"},
		{"unknown owner", func(c *protocol.Claim) { c.OwnerClientID = protocol.UUID() }, "FOREIGN KEY"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bad := claimOf(issue, w.a.ClientID, t0, t0.Add(time.Hour))
			c.edit(&bad)
			e := write(t, db, func(ctx context.Context, tx *WriteTx) error { return tx.PutClaim(ctx, bad) })
			if e == nil || !strings.Contains(e.Error(), c.want) {
				t.Fatalf("got %v, want an error mentioning %s", e, c.want)
			}
			if got := storedClaim(t, db, issue); got != nil {
				t.Fatalf("the invalid claim was stored: %+v", got)
			}
		})
	}
}

func TestLiveClaimsEndAtExpiry(t *testing.T) {
	db := openTemp(t)
	w := claimFixture(t, db, 2)
	now := t0.Add(time.Hour)
	ending := claimOf(w.issues[0], w.a.ClientID, t0, now)
	running := claimOf(w.issues[1], w.a.ClientID, t0, now.Add(time.Nanosecond))
	putClaims(t, db, ending, running)
	read(t, db, func(ctx context.Context, r *ReadTx) {
		for _, c := range []struct {
			claim protocol.Claim
			live  bool
		}{{ending, false}, {running, true}} {
			got, e := r.LiveClaim(ctx, c.claim.IssueID, now)
			if e != nil {
				t.Fatal(e)
			}
			if (got != nil) != c.live {
				t.Fatalf("claim expiring at %s live at %s: got %v, want %v", c.claim.ExpiresAt, now.Format(time.RFC3339Nano), got != nil, c.live)
			}
			if c.live {
				sameJSON(t, got, c.claim)
			}
			// The stored row stays readable after expiry, as renewal evidence.
			if stored, e := r.Claim(ctx, c.claim.IssueID); e != nil || stored == nil {
				t.Fatalf("stored claim: %v %v", stored, e)
			}
		}
	})
}

func TestClaimsByOwnerAreOrderedByAcquisition(t *testing.T) {
	db := openTemp(t)
	w := claimFixture(t, db, 6)
	now := t0.Add(time.Hour)
	is := w.issues
	// As text, "…12:00:00.1Z" sorts before "…12:00:00Z"; acquisition order must not.
	putClaims(t, db,
		claimOf(is[0], w.a.ClientID, t0.Add(100*time.Millisecond), now.Add(time.Minute)),
		claimOf(is[1], w.a.ClientID, t0, now.Add(time.Minute)),
		claimOf(is[2], w.a.ClientID, t0.Add(time.Second), now.Add(time.Minute)), // ties with is[3]
		claimOf(is[3], w.a.ClientID, t0.Add(time.Second), now.Add(time.Minute)),
		claimOf(is[4], w.a.ClientID, t0.Add(-time.Hour), now), // expired
		claimOf(is[5], w.b.ClientID, t0.Add(time.Millisecond), now.Add(time.Minute)),
	)
	idle := protocol.UUID()
	read(t, db, func(ctx context.Context, r *ReadTx) {
		check := func(name string, got []protocol.Claim, e error, want ...string) {
			t.Helper()
			if e != nil {
				t.Fatal(e)
			}
			if got == nil || strings.Join(issueIDs(got), ",") != strings.Join(want, ",") {
				t.Fatalf("%s: got %v, want %v", name, issueIDs(got), want)
			}
		}
		got, e := r.LiveClaims(ctx, w.a.ClientID, now)
		check("live claims of a", got, e, is[1], is[0], is[2], is[3])
		got, e = r.LiveClaims(ctx, "", now)
		check("live claims of every owner", got, e, is[1], is[5], is[0], is[2], is[3])
		got, e = r.OwnedClaims(ctx, w.a.ClientID)
		check("all claims of a", got, e, is[4], is[1], is[0], is[2], is[3])
		got, e = r.LiveClaims(ctx, idle, now)
		check("live claims of an idle client", got, e)
		got, e = r.OwnedClaims(ctx, idle)
		check("all claims of an idle client", got, e)
	})
}

func TestExpiredClaimsAreListedByExpiry(t *testing.T) {
	db := openTemp(t)
	w := claimFixture(t, db, 4)
	now := t0.Add(time.Hour)
	is := w.issues
	putClaims(t, db,
		claimOf(is[0], w.a.ClientID, t0, now),
		claimOf(is[1], w.b.ClientID, t0, now.Add(-time.Minute)),
		claimOf(is[2], w.a.ClientID, t0, now.Add(time.Nanosecond)), // live
		claimOf(is[3], w.b.ClientID, t0, now),
	)
	read(t, db, func(ctx context.Context, r *ReadTx) {
		got, e := r.ExpiredClaims(ctx, now)
		if e != nil {
			t.Fatal(e)
		}
		if want := []string{is[1], is[0], is[3]}; strings.Join(issueIDs(got), ",") != strings.Join(want, ",") {
			t.Fatalf("got %v, want %v", issueIDs(got), want)
		}
		if got, e = r.ExpiredClaims(ctx, t0); e != nil || got == nil || len(got) != 0 {
			t.Fatalf("nothing has expired yet: %v %v", got, e)
		}
	})
}

func TestReleaseDeletesTheClaimRow(t *testing.T) {
	db := openTemp(t)
	w := claimFixture(t, db, 1)
	issue := w.issues[0]
	putClaims(t, db, claimOf(issue, w.a.ClientID, t0, t0.Add(time.Hour)))
	release := func() (deleted bool) {
		if e := write(t, db, func(ctx context.Context, tx *WriteTx) (e error) {
			deleted, e = tx.DeleteClaim(ctx, issue)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		return deleted
	}
	if !release() {
		t.Fatal("release found no claim")
	}
	read(t, db, func(ctx context.Context, r *ReadTx) {
		var rows int
		if e := r.queryRow(ctx, []any{&rows}, "SELECT count(*) FROM claims"); e != nil || rows != 0 {
			t.Fatalf("release left %d claim rows (%v)", rows, e)
		}
	})
	if release() {
		t.Fatal("a second release found a claim")
	}
	// The issue can be claimed again.
	again := claimOf(issue, w.b.ClientID, t0.Add(time.Minute), t0.Add(time.Hour))
	putClaims(t, db, again)
	sameJSON(t, storedClaim(t, db, issue), again)
}
