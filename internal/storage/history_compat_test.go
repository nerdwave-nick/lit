package storage

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// compatRecording is testdata/history_compat.json, written by the legacy file
// store for a fixed sequence of edits (see testdata/historycompat).
type compatRecording struct {
	Requests []struct {
		Hash      string                   `json:"hash"`
		Intent    protocol.Intent          `json:"intent"`
		Timestamp string                   `json:"timestamp"`
		Results   []protocol.ChangedObject `json:"results"`
		Records   []json.RawMessage        `json:"records"`
	} `json:"requests"`
	Histories map[string][]HistoryEntry `json:"histories"`
}

func decodeAs(t *testing.T, typ string, b []byte) any {
	t.Helper()
	var e error
	var r any
	switch typ {
	case Projects:
		var p protocol.Project
		e, r = json.Unmarshal(b, &p), &p
	case Issues:
		var p protocol.Issue
		e, r = json.Unmarshal(b, &p), &p
	case Comments:
		var p protocol.Comment
		e, r = json.Unmarshal(b, &p), &p
	case Milestones:
		var p protocol.Milestone
		e, r = json.Unmarshal(b, &p), &p
	}
	if e != nil || r == nil {
		t.Fatal(typ, e)
	}
	switch p := r.(type) {
	case *protocol.Project:
		return *p
	case *protocol.Issue:
		return *p
	case *protocol.Comment:
		return *p
	}
	return *r.(*protocol.Milestone)
}

// The storage module's history output equals the legacy history API's for the
// same edits, apart from what computed membership lists change on purpose:
// a change to a membership list alone is no longer a revision (so owners that
// had one are not compared, and such records leave the results), and those
// lists are no longer field differences.
func TestHistoryMatchesTheLegacyHistoryAPI(t *testing.T) {
	b, e := os.ReadFile("testdata/history_compat.json")
	if e != nil {
		t.Fatal(e)
	}
	var rec compatRecording
	if e = json.Unmarshal(b, &rec); e != nil {
		t.Fatal(e)
	}
	db := openTemp(t)
	skipped := map[string]bool{}                     // owners with a membership-only revision
	results := map[string][]protocol.ChangedObject{} // per request hash, without those
	for _, req := range rec.Requests {
		var records []any
		kept := []protocol.ChangedObject{}
		for _, raw := range req.Records {
			var key struct{ ID string }
			_ = json.Unmarshal(raw, &key)
			var result protocol.ChangedObject
			for _, c := range req.Results {
				if c.ID == key.ID {
					result = c
				}
			}
			after := decodeAs(t, result.Type, raw)
			var before any
			read(t, db, func(ctx context.Context, r *ReadTx) {
				if before, e = r.Record(ctx, result.Type, result.ID); e != nil {
					before = nil
				}
			})
			if before != nil && equalJSON(withoutStamp(before), withoutStamp(after)) {
				skipped[result.Type+"/"+result.ID] = true
				continue
			}
			records = append(records, after)
			kept = append(kept, result)
		}
		results[req.Hash] = kept
		r := Request{Hash: req.Hash, Intent: req.Intent, Timestamp: req.Timestamp, Results: kept}
		if e := write(t, db, func(ctx context.Context, tx *WriteTx) error { return tx.WriteRequest(ctx, r, records) }); e != nil {
			t.Fatalf("request %s: %v", req.Hash, e)
		}
	}
	compared := 0
	for key, legacy := range rec.Histories {
		if skipped[key] {
			continue
		}
		for n := range legacy {
			for _, k := range []string{"issue_ids", "child_ids", "comment_ids"} {
				if k != "issue_ids" || strings.HasPrefix(key, Projects+"/") {
					delete(legacy[n].Differences, k)
				}
			}
			legacy[n].Results = results[legacy[n].RequestHash]
		}
		typ, id, _ := strings.Cut(key, "/")
		sameJSON(t, history(t, db, typ, id, 0, 0), legacy)
		compared++
	}
	if compared != 5 || len(skipped) != 2 {
		t.Fatalf("compared %d owners and skipped %d; the recording changed", compared, len(skipped))
	}
}

// withoutStamp is a record's own fields without its revision and update time.
func withoutStamp(r any) map[string]json.RawMessage {
	m := ownFields(r)
	delete(m, "revision")
	delete(m, "updated_at")
	return m
}
