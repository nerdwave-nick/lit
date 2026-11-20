package hook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nerdwave-nick/lit/internal/workflowsession"
)

// The status line runs every couple of seconds and must answer within about
// 300 ms, so it answers from a cache and asks the server only when the entry
// is old. One entry per host session lives in
// <workflow state>/hooks/statusline/<hash of host and session>.json, next to
// the hook log and apart from the project: reachability, the claim count and
// the stage are trusted for a few seconds, the project's title for minutes. A
// failed refresh is cached as well, so a server that is down or slow costs one
// bounded attempt per statusTTL and not one per refresh.
var (
	statusTTL = 5 * time.Second
	titleTTL  = 5 * time.Minute
	// cacheKeep is how long the entry of a session nothing refreshes any more
	// stays on disk.
	cacheKeep = 24 * time.Hour
)

const (
	cacheVersion = 1
	cacheDir     = "statusline"
)

// statusEntry is one cached observation.
type statusEntry struct {
	Version   int       `json:"version"`
	CheckedAt time.Time `json:"checked_at"` // when reachability, claims and stage were read
	Endpoint  string    `json:"endpoint"`
	ClientID  string    `json:"client_id"` // empty for a runtime without a session
	Reachable bool      `json:"reachable"`
	Claims    int       `json:"claims"`
	Stage     string    `json:"stage,omitempty"`
	ProjectID string    `json:"project_id,omitempty"`
	Title     string    `json:"title,omitempty"`
	TitleAt   time.Time `json:"title_at,omitempty"` // when Title was read
}

// statusCachePath is where the host session's entry lives; nothing is created.
func statusCachePath(host, runtime string) (string, error) {
	state, err := workflowsession.StateDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(host + "\x00" + runtime))
	return filepath.Join(state, "hooks", cacheDir, hex.EncodeToString(sum[:16])+".json"), nil
}

// readStatusEntry returns the entry at path; a missing, unreadable or foreign
// one is none.
func readStatusEntry(path string) (statusEntry, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return statusEntry{}, false
	}
	var e statusEntry
	if json.Unmarshal(raw, &e) != nil || e.Version != cacheVersion {
		return statusEntry{}, false
	}
	return e, true
}

// usableFor reports whether e describes this endpoint and client.
func (e statusEntry) usableFor(endpoint, client string) bool {
	return e.Endpoint == endpoint && e.ClientID == client
}

// fresh reports whether the reachability, claims and stage of e can be trusted
// at now. An entry from the future (a clock that moved back) is not.
func (e statusEntry) fresh(now time.Time) bool {
	age := now.Sub(e.CheckedAt)
	return age >= 0 && age < statusTTL
}

// writeStatusEntry replaces the entry at path atomically. A new entry also
// removes the entries of sessions that ended long ago.
func writeStatusEntry(path string, e statusEntry) error {
	e.Version = cacheVersion
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_, statErr := os.Stat(path)
	isNew := statErr != nil
	tmp, err := os.CreateTemp(dir, ".entry-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(raw)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err == nil && isNew {
		pruneStatusEntries(dir, time.Now())
	}
	return err
}

func pruneStatusEntries(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, f := range entries {
		info, err := f.Info()
		if err == nil && strings.HasSuffix(f.Name(), ".json") && now.Sub(info.ModTime()) > cacheKeep {
			os.Remove(filepath.Join(dir, f.Name()))
		}
	}
}
