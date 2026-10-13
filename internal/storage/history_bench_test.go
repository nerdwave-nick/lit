package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// bodyUpdate is the canonical request that updates issue i to body, as the
// benchmark's batched issue.update does: its intent sets the title and the
// whole body, beside others more items' bodies of other issues.
func bodyUpdate(tb testing.TB, stamp string, i protocol.Issue, others int) Request {
	tb.Helper()
	r := request(tb, stamp, i)
	in := r.Intent
	in.Operations[0].Set = protocol.ProjectSet{Title: &i.Title, Body: &i.Body}
	for n := range others {
		other, body := id(1000+n), i.Body
		in.Operations = append(in.Operations, protocol.Operation{Type: Issues, ID: other, Kind: "update", Set: protocol.ProjectSet{Body: &body}})
	}
	b, hash, e := protocol.Canonical(in)
	if e != nil {
		tb.Fatal(e)
	}
	if e = protocol.Decode(b, &in); e != nil {
		tb.Fatal(e)
	}
	return Request{Hash: hash, Intent: in, Timestamp: stamp, Results: r.Results}
}

// longLived builds an issue with revisions revisions, as the benchmark's
// long-lived issue: a body of about 2.5 KB in lines, each update editing a
// line and changing the title in a request that also sets two other bodies.
func longLived(tb testing.TB, revisions int) (*DB, string) {
	tb.Helper()
	db := openTemp(tb)
	p := newProject(id(1), "feat/one", "", at(1))
	mustApply(tb, db, at(1), p)
	rng := rand.New(rand.NewSource(1))
	lines := make([]string, 40)
	for n := range lines {
		lines[n] = fmt.Sprintf("line %d of the body, with some words to make it %d long", n, rng.Int())
	}
	i := newIssue(id(2), p.ID, "Title", strings.Join(lines, "\n")+"\n", at(2))
	mustApply(tb, db, at(2), i)
	for n := 1; n < revisions; n++ {
		i = next(i, at(2+n))
		lines[rng.Intn(len(lines))] = fmt.Sprintf("edited at revision %d: %d", n, rng.Int())
		i.Body, i.Title = strings.Join(lines, "\n")+"\n", fmt.Sprintf("Title %d", n)
		r := bodyUpdate(tb, at(2+n), i, 2)
		if e := write(tb, db, func(ctx context.Context, tx *WriteTx) error { return tx.WriteRequest(ctx, r, []any{i}) }); e != nil {
			tb.Fatal(e)
		}
	}
	return db, i.ID
}

// BenchmarkHistory205 reads and encodes the whole history of an issue with
// 205 revisions, as GET /v1/issues/<id>/history?all=true does.
func BenchmarkHistory205(b *testing.B) {
	db, issue := longLived(b, 205)
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		r, e := db.Read(ctx)
		if e != nil {
			b.Fatal(e)
		}
		hs, e := r.History(ctx, Issues, issue, 0, 1001)
		r.Close()
		if e != nil || len(hs) != 205 {
			b.Fatal(len(hs), e)
		}
		if _, e = json.Marshal(map[string]any{"items": hs}); e != nil {
			b.Fatal(e)
		}
	}
}

// BenchmarkHistoryJSON205 reads the whole history of an issue with 205
// revisions as stored rows and encodes them as the history API does.
func BenchmarkHistoryJSON205(b *testing.B) {
	db, issue := longLived(b, 205)
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		r, e := db.Read(ctx)
		if e != nil {
			b.Fatal(e)
		}
		rows, e := r.HistoryRows(ctx, Issues, issue, 0, 1001)
		r.Close()
		if e != nil || len(rows) != 205 {
			b.Fatal(len(rows), e)
		}
		_ = AppendHistory(nil, issue, rows)
	}
}
