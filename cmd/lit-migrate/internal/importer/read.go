package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/cmd/lit-migrate/internal/legacy"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// recordTypes are the legacy record directories, in import order.
var recordTypes = []string{storage.Projects, storage.Issues, storage.Comments, storage.Milestones}

// allowlist are the legacy files that hold nothing to import: the owner lock
// and the derived title index (rewritten by legacy verification).
var allowlist = map[string]bool{legacyLock: true, "indexes/titles.json": true}

// owner is one legacy record and its history.
type owner struct {
	typ, id string
	record  any
	history []legacy.ProjectHistory
}

// snapshot is everything read from the verified legacy copy.
type snapshot struct {
	serviceID string
	records   []owner // by type, then ID
	entries   int     // history entries of all records
	requests  []storage.Request
	clients   []protocol.Client
	claims    []protocol.Claim // non-null claims, live or expired, by issue
	consumed  map[string]bool  // store paths read into the snapshot
}

// readLegacy opens the legacy copy in dir with the frozen reader, which
// verifies it (check 1), and reads every record with its history, every
// client and every non-null claim.
func readLegacy(dir string, c legacy.Config) (*snapshot, error) {
	s, e := legacy.Open(dir, c)
	if e != nil {
		return nil, e
	}
	defer s.Close()
	snap := &snapshot{serviceID: s.ServiceID(), consumed: map[string]bool{storage.LegacyMarker: true}}
	state, e := s.ReadRecordState()
	if e != nil {
		return nil, e
	}
	for _, typ := range recordTypes {
		var ids []string
		for key := range state {
			if t, id, _ := strings.Cut(key, "/"); t == typ {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			hs, e := s.RecordHistory(typ, id)
			if e != nil {
				return nil, e
			}
			snap.records = append(snap.records, owner{typ, id, state[typ+"/"+id], hs})
			snap.entries += len(hs)
			dir := typ + "/" + id + "/"
			snap.consumed[dir+"content.md"], snap.consumed[dir+"relationships.json"] = true, true
			for _, h := range hs {
				snap.consumed[dir+"history/"+h.Timestamp+"-"+h.RequestHash+".json"] = true
			}
			if typ == storage.Issues {
				if e = snap.readClaim(s, id); e != nil {
					return nil, e
				}
			}
		}
	}
	if snap.clients, e = s.Clients(); e != nil {
		return nil, e
	}
	for _, c := range snap.clients {
		snap.consumed["clients/"+c.ClientID+".json"] = true
	}
	if snap.requests, e = requests(snap.records); e != nil {
		return nil, e
	}
	return snap, nil
}

// readClaim reads the issue's claim file, if any: a claim, live or expired,
// or a released claim, which imports as no claim.
func (snap *snapshot) readClaim(s *legacy.Server, issue string) error {
	path := legacy.ClaimFile(issue)
	if _, e := os.Lstat(filepath.Join(s.Root(), filepath.FromSlash(path))); e != nil {
		if os.IsNotExist(e) {
			return nil
		}
		return e
	}
	snap.consumed[path] = true
	c, e := s.StoredClaim(issue)
	if e != nil || c == nil {
		return e
	}
	snap.claims = append(snap.claims, *c)
	return nil
}

// requests deduplicates the owners' history entries by request hash into one
// transaction record each, oldest first. Every copy of a request must carry
// the same timestamp, intent and results; legacy verification guarantees it.
func requests(records []owner) ([]storage.Request, error) {
	byHash := map[string]storage.Request{}
	for _, o := range records {
		for _, h := range o.history {
			r := storage.Request{Hash: h.RequestHash, Intent: h.Intent, Timestamp: h.Timestamp, Results: h.Results}
			old, seen := byHash[h.RequestHash]
			if !seen {
				byHash[h.RequestHash] = r
				continue
			}
			if old.Timestamp != r.Timestamp || !sameJSON(old.Intent, r.Intent) || !sameJSON(old.Results, r.Results) {
				return nil, fmt.Errorf("the copies of request %s differ between owners", h.RequestHash)
			}
		}
	}
	out := make([]storage.Request, 0, len(byHash))
	for _, r := range byHash {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp < out[j].Timestamp })
	return out, nil
}

func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// checkFiles is check 2: every regular file of the legacy copy was read into
// the snapshot or is allowlisted. It returns the number of history files.
func checkFiles(dir string, snap *snapshot) (historyFiles int, err error) {
	var unlisted []string
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, e error) error {
		if e != nil || d.IsDir() {
			return e
		}
		rel, e := filepath.Rel(dir, p)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		if parts := strings.Split(rel, "/"); len(parts) == 4 && parts[2] == "history" {
			historyFiles++
		}
		if !snap.consumed[rel] && !allowlist[rel] {
			unlisted = append(unlisted, rel)
		}
		return nil
	})
	if err == nil && len(unlisted) > 0 {
		shown := unlisted[:min(5, len(unlisted))]
		err = fmt.Errorf("%d legacy files were neither imported nor allowlisted: %s", len(unlisted), strings.Join(shown, ", "))
	}
	return historyFiles, err
}
