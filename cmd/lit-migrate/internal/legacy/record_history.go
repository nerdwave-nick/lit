// Frozen copy of the legacy lit-server service at c5af73e (internal/service/record_history.go);
// see doc.go. Do not change it.

package legacy

import (
	"bytes"
	"encoding/json"
	"fmt"
	store "github.com/nerdwave-nick/nerdwave-workflows/cmd/lit-migrate/internal/legacystore"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"os"
	"sort"
	"strings"
	"time"
)

func mustJSON(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
func recordDiff(before, after any) map[string]FieldDifference {
	typ, _, _, _, _ := recordIdentity(after)
	a := map[string]json.RawMessage{}
	if before != nil {
		a = recordFields(before)
	}
	b := recordFields(after)
	delete(b, bodyField(typ))
	out := map[string]FieldDifference{}
	for k, v := range b {
		old := a[k]
		if old == nil {
			old = json.RawMessage("null")
		}
		if !bytes.Equal(old, v) {
			out[k] = FieldDifference{Before: old, After: v}
		}
	}
	return out
}
func applyRecordHistory(typ string, prior any, h ProjectHistory) (any, error) {
	fields := map[string]json.RawMessage{}
	body := ""
	if prior != nil {
		fields = recordFields(prior)
		body = recordBody(prior)
	}
	delete(fields, bodyField(typ))
	for k, d := range h.Differences {
		if k == bodyField(typ) {
			return nil, fmt.Errorf("body must use hunks")
		}
		old := fields[k]
		if old == nil {
			old = json.RawMessage("null")
		}
		if !bytes.Equal(old, d.Before) || bytes.Equal(d.Before, d.After) {
			return nil, fmt.Errorf("invalid history difference")
		}
		fields[k] = d.After
	}
	if len(h.BodyHunks) > 1 {
		return nil, fmt.Errorf("invalid hunks")
	}
	if len(h.BodyHunks) == 1 {
		hunk := h.BodyHunks[0]
		if hunk.Before != body || hunk.Before == hunk.After {
			return nil, fmt.Errorf("invalid body history")
		}
		body = hunk.After
	}
	fields[bodyField(typ)] = mustJSON(body)
	return decodeRecord(typ, mustJSON(fields))
}
func (s *Server) RecordHistory(typ, id string) ([]ProjectHistory, error) {
	if !protocol.ResourceType(typ) || !protocol.ValidUUID(id) {
		return nil, invalid("invalid history owner")
	}
	path := recordPath(typ, id) + "/history"
	entries, e := s.Store.List(path)
	if e != nil {
		return nil, e
	}
	out := []ProjectHistory{}
	for _, entry := range entries {
		b, e := s.Store.Read(path + "/" + entry.Name())
		if e != nil {
			return nil, e
		}
		var h ProjectHistory
		if e = protocol.Decode(b, &h); e != nil {
			return nil, e
		}
		if h.SchemaVersion != 1 || h.OwnerID != id || entry.Name() != h.Timestamp+"-"+h.RequestHash+".json" {
			return nil, fmt.Errorf("invalid immutable history")
		}
		_, hash, e := protocol.Canonical(h.Intent)
		if e != nil || hash != h.RequestHash {
			return nil, fmt.Errorf("invalid history intent")
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp < out[j].Timestamp })
	return out, nil
}

// InitRecords verifies immutable cross-owner histories by replaying each semantic
// transaction in chronological order, then compares the reconstructed authority.
// This accepts the unchanged project-only v1 format and never rewrites its history.
func (s *Server) InitRecords() error {
	current, e := s.ReadRecordState()
	if e != nil {
		return e
	}
	histories := map[string]map[string]ProjectHistory{}
	stamps := map[string]string{}
	for key, r := range current {
		typ, id, _, _, _ := recordIdentity(r)
		hs, e := s.RecordHistory(typ, id)
		if e != nil {
			return e
		}
		if len(hs) == 0 {
			return fmt.Errorf("record has no history")
		}
		for _, h := range hs {
			if h.Intent.ServiceID != s.Store.Identity.ServiceID {
				return fmt.Errorf("history service differs")
			}
			stamp, e := time.Parse(historyTimeFormat, h.Timestamp)
			if e != nil || stamp.Format(historyTimeFormat) != h.Timestamp {
				return fmt.Errorf("invalid history timestamp")
			}
			if histories[h.RequestHash] == nil {
				histories[h.RequestHash] = map[string]ProjectHistory{}
				stamps[h.RequestHash] = h.Timestamp
			}
			if _, ok := histories[h.RequestHash][key]; ok {
				return fmt.Errorf("duplicate owner history")
			}
			histories[h.RequestHash][key] = h
		}
	}
	hashes := sortedKeys(histories)
	sort.Slice(hashes, func(i, j int) bool { return stamps[hashes[i]] < stamps[hashes[j]] })
	state := RecordState{}
	last := ""
	for _, hash := range hashes {
		owners := histories[hash]
		keys := sortedKeys(owners)
		h := owners[keys[0]]
		if h.Timestamp <= last {
			return fmt.Errorf("nonmonotonic transaction history")
		}
		last = h.Timestamp
		// Retired historical project prefixes remain valid; current records still use
		// the configured allowlist below.
		validator := &Server{Config: s.Config}
		validator.Config.TitlePrefixes = append([]string{}, s.Config.TitlePrefixes...)
		for _, op := range h.Intent.Operations {
			if op.Type == "projects" && op.Set.Title != nil {
				validator.Config.TitlePrefixes = append(validator.Config.TitlePrefixes, strings.Split(*op.Set.Title, "/")[0])
			}
		}
		for _, r := range state {
			if p, ok := r.(protocol.Project); ok {
				validator.Config.TitlePrefixes = append(validator.Config.TitlePrefixes, strings.Split(p.Title, "/")[0])
			}
		}
		after, e := validator.finalState(state, h.Intent.Operations)
		if e != nil {
			return fmt.Errorf("history operation: %w", e)
		}
		if e = validateReferences(state, after, h.Intent); e != nil {
			return fmt.Errorf("history precondition: %w", e)
		}
		changed := changedKeys(state, after)
		if len(changed) != len(owners) {
			return fmt.Errorf("incomplete multi-owner history")
		}
		results := []protocol.ChangedObject{}
		for _, k := range changed {
			after[k] = stampRecord(after[k], h.Timestamp)
			typ, id, rev, _, _ := recordIdentity(after[k])
			results = append(results, protocol.ChangedObject{Type: typ, ID: id, BeforeRevision: rev - 1, Revision: rev})
		}
		// Old project history preserved descriptor ordering; operations were sorted so
		// its result order is already identical to the general resource-key order.
		for _, k := range changed {
			entry, ok := owners[k]
			if !ok || entry.Timestamp != h.Timestamp || !same(entry.Intent, h.Intent) || !same(entry.Results, results) {
				return fmt.Errorf("inconsistent multi-owner history")
			}
			typ, _, rev, _, _ := recordIdentity(after[k])
			if entry.BeforeRevision != rev-1 || entry.Revision != rev {
				return fmt.Errorf("invalid history revisions")
			}
			replayed, e := applyRecordHistory(typ, state[k], entry)
			if e != nil {
				return e
			}
			if !same(replayed, after[k]) {
				return fmt.Errorf("history differences do not match canonical operation")
			}
		}
		state = after
	}
	if !same(state, current) {
		return fmt.Errorf("records differ from accepted history")
	}
	if e = s.validateRecordState(current); e != nil {
		return e
	}
	idx, e := stateTitleIndex(current)
	if e != nil {
		return e
	}
	b := mustJSON(idx)
	old, e := s.Store.Read("indexes/titles.json")
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if !bytes.Equal(old, b) {
		return s.Store.Commit([]store.Write{{Path: "indexes/titles.json", Data: b}})
	}
	return nil
}
