package importer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// legacyLock is the legacy store's owner lock file.
const legacyLock = ".lock"

// copyStore copies the stopped legacy store from into dst, which must not
// exist. It holds the store's owner lock while copying, so a legacy
// lit-server cannot start meanwhile, and refuses a store that a running
// lit-server still owns. It never writes to from.
func copyStore(from, dst string) error {
	if _, e := os.Lstat(filepath.Join(from, storage.LegacyMarker)); e != nil {
		return fmt.Errorf("%s is not a legacy lit file store (no %s): %w", from, storage.LegacyMarker, e)
	}
	release, e := holdLegacyLock(from)
	if e != nil {
		return e
	}
	defer release()
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(from, p)
		if e != nil {
			return e
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("symlink %s in the legacy store", rel)
		case d.IsDir():
			return os.Mkdir(target, 0o700)
		case !d.Type().IsRegular():
			return fmt.Errorf("%s in the legacy store is not a regular file", rel)
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		return os.WriteFile(target, b, 0o600)
	})
}

// holdLegacyLock takes the legacy owner lock of dir without creating its lock
// file; a store without one has no owner. It returns the release function.
func holdLegacyLock(dir string) (func(), error) {
	f, e := os.OpenFile(filepath.Join(dir, legacyLock), os.O_RDWR, 0)
	if errors.Is(e, os.ErrNotExist) {
		return func() {}, nil
	}
	if e != nil {
		return nil, e
	}
	if e = lock(f); e != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%s is still owned by a running legacy lit-server (%v); stop and disable lit.socket and lit.service, and confirm the process exited", dir, e)
	}
	return func() { _ = unlock(f); _ = f.Close() }, nil
}

// lockTarget takes the target's owner lock, the lock file lit-server takes,
// so no lit-server can start on the target during the import. The release
// function unlocks and removes the lock file.
func lockTarget(to string) (func(), error) {
	path := filepath.Join(to, storage.LockFile)
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if e != nil {
		return nil, e
	}
	if e = lock(f); e != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("target %s is owned by another process: %w", to, e)
	}
	return func() { _ = os.Remove(path); _ = unlock(f); _ = f.Close() }, nil
}
