package export

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrBusy matches (with errors.Is) an export refused because another export
// into the same directory is running.
var ErrBusy = errors.New("another export into this directory is running")

// target is an export in progress: the directory it replaces, the staging
// directory beside it and the lock that keeps other exports out.
type target struct {
	dir, staging string
	lock         *os.File
}

// prepare locks the export directory dir, checks that it may be replaced and
// creates an empty staging directory next to it.
func prepare(dir string) (_ *target, err error) {
	if dir, err = filepath.Abs(dir); err != nil {
		return nil, err
	}
	parent, base := filepath.Split(dir)
	if err = os.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(parent, "."+base+".export-lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err = lock(f); err != nil {
		f.Close()
		return nil, err
	}
	t := &target{dir: dir, lock: f}
	defer func() {
		if err != nil {
			t.release()
		}
	}()
	if err = replaceable(dir); err != nil {
		return nil, err
	}
	// Under the lock, staging directories left by a crashed export are stale.
	stale, _ := filepath.Glob(filepath.Join(parent, "."+base+".staging-*"))
	for _, s := range stale {
		if err = os.RemoveAll(s); err != nil {
			return nil, err
		}
	}
	if t.staging, err = os.MkdirTemp(parent, "."+base+".staging-"); err != nil {
		return nil, err
	}
	return t, os.Chmod(t.staging, 0o755)
}

// replaceable accepts a missing directory, an empty one, or a previous export
// (its manifest.json names this generator). Anything else is refused, so an
// export never replaces files it did not write.
func replaceable(dir string) error {
	info, e := os.Lstat(dir)
	switch {
	case errors.Is(e, os.ErrNotExist):
		return nil
	case e != nil:
		return e
	case !info.IsDir():
		return fmt.Errorf("%s is not a lit export directory (it is not a directory); choose a new or empty directory", dir)
	}
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) == 0 {
		return e
	}
	var m Manifest
	if b, e := os.ReadFile(filepath.Join(dir, "manifest.json")); e == nil && json.Unmarshal(b, &m) == nil && m.Generator == Generator {
		return nil
	}
	return fmt.Errorf("%s is not a lit export (it holds other files); choose a new or empty directory, or a previous export", dir)
}

// write stores one file of the tree in the staging directory.
func (t *target) write(name string, b []byte) error {
	p := filepath.Join(t.staging, filepath.FromSlash(name))
	if e := os.MkdirAll(filepath.Dir(p), 0o755); e != nil {
		return e
	}
	return os.WriteFile(p, b, 0o644)
}

// commit switches the staging directory into place: a rename when the
// directory is new, otherwise an atomic exchange, after which the previous
// tree sits at the staging path and release removes it.
func (t *target) commit() error {
	if _, e := os.Lstat(t.dir); errors.Is(e, os.ErrNotExist) {
		if e = os.Rename(t.staging, t.dir); e == nil {
			t.staging = ""
		}
		return e
	}
	return exchange(t.staging, t.dir)
}

// release removes the staging directory (or the replaced tree) and unlocks.
func (t *target) release() {
	if t.staging != "" {
		_ = os.RemoveAll(t.staging)
	}
	_ = unlock(t.lock)
	_ = t.lock.Close()
}
