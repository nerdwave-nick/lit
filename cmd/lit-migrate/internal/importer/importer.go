// Package importer is lit-migrate's setup import: the one-time conversion of
// a stopped legacy file store into a new lit database.
//
// The import copies the source store into a private staging directory and
// reads only that copy with the frozen legacy reader, because the reader
// writes to the store it opens. It builds the new database as a temporary
// file in one write transaction, runs validation checks 1-9 of the
// specification, and only then moves the database into the empty target
// directory. Any failure leaves no database behind, and the source is never
// written, so a failed import can simply be run again.
package importer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nerdwave-nick/nerdwave-workflows/cmd/lit-migrate/internal/legacy"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// Options configure one import.
type Options struct {
	// From is the legacy data directory: a copy of the stopped store, which
	// the import only reads. To is the new data directory; it must be missing
	// or empty.
	From, To string
	// Config is the legacy server's configuration: its title prefixes are
	// validated by legacy verification, its limits bound legacy reads.
	Config legacy.Config
	// DryRun runs every step and check in a temporary directory below TempDir
	// (default os.TempDir) and never touches To.
	DryRun  bool
	TempDir string
	// Logf reports progress, one line per step; nil discards it.
	Logf func(format string, args ...any)
}

// Report is what an import converted and checked.
type Report struct {
	ServiceID string
	// Counts are the imported rows; HistoryFiles the legacy history files.
	Counts       storage.Counts
	HistoryFiles int
	// Database and Manifest are the installed database file and the kept
	// body SHA-256 manifest; both are empty after a dry run.
	Database, Manifest string
	// Checks names every validation check that passed, in order.
	Checks []string
}

// checkNames are validation checks 1-9 of the specification.
var checkNames = []string{
	"legacy verification of the copy (Open, New, InitRecords, InitClaims)",
	"every legacy file is imported or allowlisted",
	"counts match",
	"records, clients and claims are equal",
	"body SHA-256 manifests match in both directions",
	"every converted diff round-trips and each body chain reproduces the body",
	"history read back equals the legacy history",
	"the newest transaction stamp is the newest record update",
	"lit-server verify passes",
}

// checkError names the failed validation check.
func checkError(n int, e error) error {
	return fmt.Errorf("check %d (%s) failed: %w", n, checkNames[n-1], e)
}

// Run imports the legacy store o.From into a new database in o.To.
func Run(ctx context.Context, o Options) (r Report, err error) {
	logf := o.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	from, to, err := paths(o.From, o.To)
	if err != nil {
		return r, err
	}
	created, err := prepareTarget(to, o.DryRun)
	if err != nil {
		return r, err
	}
	if created {
		defer func() {
			if err != nil {
				_ = os.Remove(to)
			}
		}()
	}
	parent := to
	if o.DryRun {
		if parent = o.TempDir; parent == "" {
			parent = os.TempDir()
		}
	} else {
		release, e := lockTarget(to)
		if e != nil {
			return r, e
		}
		defer release()
	}
	staging, err := os.MkdirTemp(parent, ".lit-migrate-")
	if err != nil {
		return r, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(staging)) }()

	copied := filepath.Join(staging, "legacy")
	if err = copyStore(from, copied); err != nil {
		return r, err
	}
	logf("lit-migrate: copied %s to %s", from, copied)
	snap, err := readLegacy(copied, o.Config)
	if err != nil {
		return r, checkError(1, err)
	}
	r.Checks = append(r.Checks, checkNames[0])
	logf("lit-migrate: check 1 passed: %s (%d records, %d history entries, %d clients, %d claims)", checkNames[0], len(snap.records), snap.entries, len(snap.clients), len(snap.claims))
	if r.HistoryFiles, err = checkFiles(copied, snap); err != nil {
		return r, checkError(2, err)
	}
	r.Checks = append(r.Checks, checkNames[1])
	logf("lit-migrate: check 2 passed: %s", checkNames[1])

	dbDir := filepath.Join(staging, "db")
	manifest, err := build(ctx, dbDir, snap, &r, logf)
	if err != nil {
		return r, err
	}
	if err = verifyDatabase(ctx, dbDir); err != nil {
		return r, checkError(9, err)
	}
	r.Checks = append(r.Checks, checkNames[8])
	logf("lit-migrate: check 9 passed: %s", checkNames[8])
	r.ServiceID = snap.serviceID
	if o.DryRun {
		logf("lit-migrate: dry run complete; %s was not touched", to)
		return r, nil
	}
	if r.Database, r.Manifest, err = install(dbDir, to, from, snap.serviceID, manifest); err != nil {
		return r, err
	}
	logf("lit-migrate: installed %s; body manifest %s", r.Database, r.Manifest)
	return r, nil
}

// build imports snap into a new database in dir and runs checks 3-8 on it.
// The database is closed when build returns.
func build(ctx context.Context, dir string, snap *snapshot, r *Report, logf func(string, ...any)) ([]manifestEntry, error) {
	db, e := storage.Create(dir, snap.serviceID)
	if e != nil {
		return nil, e
	}
	defer func() { _ = db.Close() }()
	if e = importSnapshot(ctx, db, snap); e != nil {
		return nil, fmt.Errorf("import: %w", e)
	}
	logf("lit-migrate: imported %d transactions in one write transaction", len(snap.requests))
	manifest, e := checkImported(ctx, db, snap, r, logf)
	if e != nil {
		return nil, e
	}
	return manifest, db.Close()
}

// paths makes from and to absolute and refuses overlapping directories.
func paths(from, to string) (string, string, error) {
	if from == "" || to == "" {
		return "", "", errors.New("both --from and --to are required")
	}
	a, e := filepath.Abs(from)
	if e != nil {
		return "", "", e
	}
	b, e := filepath.Abs(to)
	if e != nil {
		return "", "", e
	}
	if within(a, b) || within(b, a) {
		return "", "", fmt.Errorf("--from %s and --to %s must be separate directories", a, b)
	}
	return a, b, nil
}

// within reports whether path is dir or below it.
func within(path, dir string) bool {
	rel, e := filepath.Rel(dir, path)
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// prepareTarget refuses a target that exists and is not an empty directory.
// A real import creates a missing target and reports that it did.
func prepareTarget(to string, dryRun bool) (created bool, err error) {
	entries, e := os.ReadDir(to)
	switch {
	case errors.Is(e, os.ErrNotExist):
		if dryRun {
			return false, nil
		}
		if e = os.Mkdir(to, 0o700); e != nil {
			return false, e
		}
		return true, nil
	case e != nil:
		return false, fmt.Errorf("target %s: %w", to, e)
	case len(entries) > 0:
		return false, fmt.Errorf("target %s is not empty; the setup import writes only into a new, empty data directory", to)
	}
	return false, nil
}
