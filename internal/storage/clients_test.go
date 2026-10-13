package storage

import (
	"context"
	"encoding/json"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"strings"
	"testing"
	"time"
)

func strptr(s string) *string { return &s }

// readClient reads id through a fresh read snapshot.
func readClient(t *testing.T, db *DB, id string) (protocol.Client, bool) {
	t.Helper()
	r, e := db.Read(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	c, ok, e := r.Client(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	return c, ok
}

func sameJSON(t *testing.T, got, want any) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Fatalf("got  %s\nwant %s", g, w)
	}
}

func TestClientRecordsRoundTrip(t *testing.T) {
	db := openTemp(t)
	full := protocol.Client{SchemaVersion: 1, ClientID: protocol.UUID(), StateRevision: 7, Status: "connected",
		Actor: protocol.Actor{Name: "Ada", Kind: "human"}, ProjectID: strptr(protocol.UUID()),
		Runtime: protocol.Runtime{Vendor: "claude", SessionID: "s-1"}, OutputFormat: strptr("markdown")}
	bare := protocol.Client{SchemaVersion: 1, ClientID: protocol.UUID(), StateRevision: 1, Status: "disconnected",
		Actor: protocol.Actor{Name: "bot", Kind: "agent"}}
	if e := write(t, db, func(ctx context.Context, tx *WriteTx) error {
		if e := tx.PutClient(ctx, full); e != nil {
			return e
		}
		return tx.PutClient(ctx, bare)
	}); e != nil {
		t.Fatal(e)
	}
	for _, want := range []protocol.Client{full, bare} {
		got, ok := readClient(t, db, want.ClientID)
		if !ok {
			t.Fatalf("client %s not found", want.ClientID)
		}
		sameJSON(t, got, want)
	}
	if _, ok := readClient(t, db, protocol.UUID()); ok {
		t.Fatal("an unknown client was found")
	}

	// Writing a client again replaces every field, including clearing optional ones.
	next := full
	next.StateRevision, next.Status, next.ProjectID, next.OutputFormat = 8, "disconnected", nil, nil
	next.Actor, next.Runtime = protocol.Actor{Name: "Ada L.", Kind: "agent"}, protocol.Runtime{}
	if e := write(t, db, func(ctx context.Context, tx *WriteTx) error {
		if e := tx.PutClient(ctx, next); e != nil {
			return e
		}
		// The write transaction reads its own write.
		got, ok, e := tx.Client(ctx, next.ClientID)
		if e != nil || !ok || got.StateRevision != 8 {
			t.Errorf("inside the write: %+v %v %v", got, ok, e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	got, _ := readClient(t, db, next.ClientID)
	sameJSON(t, got, next)
}

func TestInvalidClientRecordsAreRefused(t *testing.T) {
	db := openTemp(t)
	valid := protocol.Client{SchemaVersion: 1, ClientID: protocol.UUID(), StateRevision: 1, Status: "connected", Actor: protocol.Actor{Name: "me", Kind: "agent"}}
	cases := []struct {
		name string
		edit func(*protocol.Client)
		want string
	}{
		{"schema version", func(c *protocol.Client) { c.SchemaVersion = 2 }, "schema_version"},
		{"status", func(c *protocol.Client) { c.Status = "gone" }, "CHECK"},
		{"actor kind", func(c *protocol.Client) { c.Actor.Kind = "robot" }, "CHECK"},
		{"actor name", func(c *protocol.Client) { c.Actor.Name = "" }, "CHECK"},
		{"state revision", func(c *protocol.Client) { c.StateRevision = 0 }, "CHECK"},
		{"client ID", func(c *protocol.Client) { c.ClientID = "short" }, "CHECK"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bad := valid
			c.edit(&bad)
			e := write(t, db, func(ctx context.Context, tx *WriteTx) error { return tx.PutClient(ctx, bad) })
			if e == nil || !strings.Contains(e.Error(), c.want) {
				t.Fatalf("got %v, want an error mentioning %s", e, c.want)
			}
			if _, ok := readClient(t, db, bad.ClientID); ok {
				t.Fatal("the invalid client was stored")
			}
		})
	}
}

// disconnectFixture gives client a a live and an expired claim and client b a
// live claim, at now.
func disconnectFixture(t *testing.T, db *DB) (w claimWorld, now time.Time) {
	t.Helper()
	w = claimFixture(t, db, 3)
	now = t0.Add(time.Hour)
	putClaims(t, db,
		claimOf(w.issues[0], w.a.ClientID, t0, now.Add(time.Minute)),
		claimOf(w.issues[1], w.a.ClientID, t0, now.Add(-time.Minute)),
		claimOf(w.issues[2], w.b.ClientID, t0, now.Add(time.Minute)),
	)
	return w, now
}

func disconnected(c protocol.Client) protocol.Client {
	c.Status = "disconnected"
	c.StateRevision++
	return c
}

func claimsOf(t *testing.T, db *DB, owner string) (ids []string) {
	t.Helper()
	read(t, db, func(ctx context.Context, r *ReadTx) {
		got, e := r.OwnedClaims(ctx, owner)
		if e != nil {
			t.Fatal(e)
		}
		ids = issueIDs(got)
	})
	return ids
}

func TestDisconnectClearsLiveAndExpiredClaimsAndWritesTheClient(t *testing.T) {
	db := openTemp(t)
	w, _ := disconnectFixture(t, db)
	gone := disconnected(w.a)
	if e := write(t, db, func(ctx context.Context, tx *WriteTx) error { return tx.DisconnectClient(ctx, gone) }); e != nil {
		t.Fatal(e)
	}
	if ids := claimsOf(t, db, w.a.ClientID); len(ids) != 0 {
		t.Fatalf("disconnect kept claims %v", ids)
	}
	if ids := claimsOf(t, db, w.b.ClientID); len(ids) != 1 || ids[0] != w.issues[2] {
		t.Fatalf("another client's claims changed: %v", ids)
	}
	got, _ := readClient(t, db, w.a.ClientID)
	sameJSON(t, got, gone)
}

func TestDisconnectNeedsADisconnectedClient(t *testing.T) {
	db := openTemp(t)
	w, _ := disconnectFixture(t, db)
	e := write(t, db, func(ctx context.Context, tx *WriteTx) error { return tx.DisconnectClient(ctx, w.a) })
	if e == nil || !strings.Contains(e.Error(), "disconnected") {
		t.Fatalf("got %v", e)
	}
	if ids := claimsOf(t, db, w.a.ClientID); len(ids) != 2 {
		t.Fatalf("claims after a refused disconnect: %v", ids)
	}
}

// A failure late in the request leaves the claims and the client unchanged.
func TestDisconnectIsAllOrNothing(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, ctx context.Context, tx *WriteTx, w claimWorld) error
	}{
		{"the client write fails after the claims were cleared", func(t *testing.T, ctx context.Context, tx *WriteTx, w claimWorld) error {
			bad := disconnected(w.a)
			bad.Actor.Kind = "robot"
			e := tx.DisconnectClient(ctx, bad)
			if e == nil || !strings.Contains(e.Error(), "CHECK") {
				t.Fatalf("got %v, want the client's CHECK violation", e)
			}
			// Even a caller that commits anyway keeps the claims.
			return nil
		}},
		{"a later write in the request fails", func(t *testing.T, ctx context.Context, tx *WriteTx, w claimWorld) error {
			if e := tx.DisconnectClient(ctx, disconnected(w.a)); e != nil {
				t.Fatal(e)
			}
			e := tx.PutClaim(ctx, claimOf(protocol.UUID(), w.b.ClientID, t0, t0.Add(time.Hour)))
			if e == nil || !strings.Contains(e.Error(), "FOREIGN KEY") {
				t.Fatalf("got %v, want a FOREIGN KEY violation", e)
			}
			return e
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := openTemp(t)
			w, _ := disconnectFixture(t, db)
			_ = write(t, db, func(ctx context.Context, tx *WriteTx) error { return c.run(t, ctx, tx, w) })
			if ids := claimsOf(t, db, w.a.ClientID); len(ids) != 2 {
				t.Fatalf("claims of the client after the failure: %v", ids)
			}
			got, _ := readClient(t, db, w.a.ClientID)
			sameJSON(t, got, w.a)
		})
	}
}
