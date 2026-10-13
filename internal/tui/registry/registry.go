// Package registry is lit-tui's per-instance session registry. Every running
// lit-tui instance records one entry in StateDir/tui/instances:
//
//   - <id>.json is the instance's logical session mapping, saved by
//     litclient's Connect once its fresh client is registered;
//   - <id>.json.lock is that mapping's lock, which the instance holds for its
//     whole life, so it doubles as the liveness lock;
//   - <id>.instance.json describes the instance (pid, host, start time).
//
// The registry is outside the CLI's sessions/ namespace, so lit never adopts a
// lit-tui session. An entry whose lock is held belongs to a live instance; an
// entry whose lock is free is an orphan the next launch may recover. Pending
// request evidence stays in StateDir/pending, where lit transactions status
// sees it; the registry never touches it.
package registry

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

// SchemaVersion is the version of instance descriptions.
const SchemaVersion = 1

const infoSuffix = ".instance.json"

// DefaultPage is how many directory entries Scan reads at a time.
const DefaultPage = 128

// Info describes one lit-tui instance.
type Info struct {
	SchemaVersion int    `json:"schema_version"`
	InstanceID    string `json:"instance_id"`
	PID           int    `json:"pid"`
	Host          string `json:"host"`
	StartedAt     string `json:"started_at"`
	Endpoint      string `json:"endpoint"`
}

// Entry is one instance's files in the registry.
type Entry struct {
	ID   string
	Root string
}

// Root returns the registry directory inside the client state directory.
func Root(stateDir string) string { return filepath.Join(stateDir, "tui", "instances") }

// New returns the entry for instance id in the registry of stateDir.
func New(stateDir, id string) Entry { return Entry{ID: id, Root: Root(stateDir)} }

// SessionPath is the instance's session mapping; litclient locks it at
// SessionPath()+".lock".
func (e Entry) SessionPath() string { return filepath.Join(e.Root, e.ID+".json") }

// LockPath is the instance's liveness lock.
func (e Entry) LockPath() string { return e.SessionPath() + ".lock" }

// InfoPath is the instance's description. Its presence publishes the entry.
func (e Entry) InfoPath() string { return filepath.Join(e.Root, e.ID+infoSuffix) }

// Publish writes info, publishing the entry. The caller must already hold the
// entry's liveness lock, so a published entry is always either locked by its
// live owner or abandoned.
func Publish(e Entry, info Info) error {
	if e2 := validate(e, info); e2 != nil {
		return e2
	}
	return litclient.WriteJSON(e.InfoPath(), info)
}

// ReadInfo reads and validates the entry's description. A missing description
// satisfies os.IsNotExist.
func ReadInfo(e Entry) (Info, error) {
	var info Info
	b, err := os.ReadFile(e.InfoPath())
	if err != nil {
		return info, err
	}
	if err = protocol.Decode(b, &info); err != nil {
		return info, fmt.Errorf("invalid lit-tui instance description %s: %w", e.InfoPath(), err)
	}
	return info, validate(e, info)
}

func validate(e Entry, info Info) error {
	if info.SchemaVersion != SchemaVersion || !protocol.ValidUUID(e.ID) || info.InstanceID != e.ID || info.StartedAt == "" {
		return fmt.Errorf("unsupported or invalid lit-tui instance description %s", e.InfoPath())
	}
	return nil
}

// Scan lists every published entry, reading the registry directory page
// entries at a time until its end. It touches nothing. A missing registry is
// empty.
func Scan(stateDir string, page int) ([]Entry, error) {
	if page <= 0 {
		page = DefaultPage
	}
	root := Root(stateDir)
	dir, err := os.Open(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	var out []Entry
	for {
		list, err := dir.ReadDir(page)
		for _, d := range list {
			id, ok := strings.CutSuffix(d.Name(), infoSuffix)
			if ok && !d.IsDir() && protocol.ValidUUID(id) {
				out = append(out, Entry{ID: id, Root: root})
			}
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
	}
}

// Remove deletes the entry's session mapping and description, then calls
// unlock to release its liveness lock, then deletes the lock file. The caller
// must hold the lock; Remove always releases it. Nothing else in the state
// directory is touched. If the mapping or description cannot be deleted, the
// entry stays published and the lock file is kept.
//
// The mapping goes first: an interruption leaves at most a description
// without a client, which a later recovery removes. The lock file goes last,
// after unlocking, because some platforms cannot delete a locked file.
func Remove(e Entry, unlock func()) error {
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	for _, path := range []string{e.SessionPath(), e.InfoPath()} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	unlock()
	unlock = nil
	if err := os.Remove(e.LockPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
