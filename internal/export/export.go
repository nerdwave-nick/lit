// Package export writes the tracker as a one-way Markdown tree for people:
// project directories with project.md, flat issues/ and milestones/
// directories, sibling .history.md files, a README.md and a manifest.json.
// lit writes the tree and never reads it back; it is not a backup and omits
// claims, clients and transaction evidence.
//
// An export reads everything from one storage read snapshot, so it shows one
// exact point in history, and its bytes depend only on that state, apart from
// the export time in README.md and manifest.json. It is written to a staging
// directory next to the target and switched into place atomically.
package export

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// Snapshot is one consistent read of the tracker; *storage.ReadTx is one.
type Snapshot interface {
	List(ctx context.Context, q storage.ListQuery) ([]any, error)
	History(ctx context.Context, typ, id string, afterRevision int64, limit int) ([]storage.HistoryEntry, error)
	NewestTransaction(ctx context.Context) (storage.TransactionStamp, bool, error)
}

// Options are the inputs that do not come from the snapshot.
type Options struct {
	ServiceID string    // the tracker's identity, shown in README.md and the manifest
	Time      time.Time // the export time, normally the snapshot's request time
}

// Counts are the exported records per type.
type Counts struct {
	Projects   int `json:"projects"`
	Issues     int `json:"issues"`
	Comments   int `json:"comments"`
	Milestones int `json:"milestones"`
}

// Generator marks a manifest.json written by this package; a target
// directory is replaced only when it holds one (or nothing).
const Generator = "lit-server export"

// Manifest is manifest.json. LastTransaction names the newest committed
// request the export includes (nil for an empty tracker), so a later run can
// tell whether anything has changed since.
type Manifest struct {
	SchemaVersion   int                       `json:"schema_version"`
	Generator       string                    `json:"generator"`
	ServiceID       string                    `json:"service_id"`
	ExportedAt      string                    `json:"exported_at"`
	LastTransaction *storage.TransactionStamp `json:"last_transaction"`
	Counts          Counts                    `json:"counts"`
}

// ReadManifest reads the manifest of the export in dir. It fails when dir
// holds no export written by this package.
func ReadManifest(dir string) (Manifest, error) {
	var m Manifest
	b, e := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if e != nil {
		return Manifest{}, e
	}
	if e = json.Unmarshal(b, &m); e != nil {
		return Manifest{}, fmt.Errorf("%s: %w", filepath.Join(dir, "manifest.json"), e)
	}
	if m.Generator != Generator {
		return Manifest{}, fmt.Errorf("%s holds no export by %s", dir, Generator)
	}
	return m, nil
}

// Export writes the tracker as seen by snap into dir, replacing a previous
// export there. dir must be absent, an empty directory or a previous export;
// anything else is refused. Its parent must be writable: the staging directory
// and a lock file (.<name>.export-lock) live next to it. A concurrent export
// into the same directory fails with ErrBusy. On any error the previous
// export stays as it was.
func Export(ctx context.Context, snap Snapshot, dir string, opt Options) (m Manifest, err error) {
	target, err := prepare(dir)
	if err != nil {
		return Manifest{}, err
	}
	defer target.release()
	t, err := load(ctx, snap)
	if err != nil {
		return Manifest{}, err
	}
	m = Manifest{SchemaVersion: 1, Generator: Generator, ServiceID: opt.ServiceID, ExportedAt: opt.Time.UTC().Format(storage.TimeFormat),
		Counts: Counts{len(t.projects), len(t.issues), t.commentCount, len(t.milestones)}}
	if last, found, e := snap.NewestTransaction(ctx); e != nil {
		return Manifest{}, e
	} else if found {
		m.LastTransaction = &last
	}
	r := newRenderer(t)
	if err = r.writeAll(ctx, snap, target.write, m); err != nil {
		return Manifest{}, err
	}
	manifest, _ := json.MarshalIndent(m, "", "  ")
	if err = target.write("manifest.json", append(manifest, '\n')); err != nil {
		return Manifest{}, err
	}
	if err = ctx.Err(); err != nil {
		return Manifest{}, err
	}
	if err = target.commit(); err != nil {
		return Manifest{}, fmt.Errorf("switch the export into %s: %w", dir, err)
	}
	return m, nil
}
