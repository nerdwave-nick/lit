package checkoutcontext

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/workflowstate"
)

// DefaultLockTimeout bounds how long a mutation waits for another invocation's.
const DefaultLockTimeout = 5 * time.Second

// Store keeps one record per checkout below <workflow state>/checkout-context/,
// named by the SHA-256 of the checkout's physical path (<hash>.json), next to
// a <hash>.lock file that serializes mutations of that checkout. It never reads
// or writes the session associations, checkout-location maps, pending
// requests or output caches that live elsewhere.
//
// Mutations (Save, Clear) take the checkout's lock, re-read the record under
// it, and replace it atomically (temporary file, fsync, rename), so the last
// successful mutation wins. Readers take no lock: they see the complete old or
// the complete new record, never a partial one.
type Store struct {
	// LockTimeout bounds the wait for another invocation's mutation of the
	// same checkout; zero means DefaultLockTimeout.
	LockTimeout time.Duration
	dir         string
}

// Cleared reports what Clear removed.
type Cleared struct {
	// Existed reports whether anything was removed.
	Existed bool
	// Previous is the valid preference that was removed, if it was valid.
	Previous *Record
	// Damage is the *RecordError of a damaged or unsupported record that was
	// removed (Previous is then nil).
	Damage error
}

// Open returns the store in the workflow state directory: LIT_WORKFLOW_STATE_DIR
// or the platform default. It creates nothing.
func Open() (*Store, error) {
	dir, err := workflowstate.Dir()
	if err != nil {
		return nil, err
	}
	return OpenAt(dir), nil
}

// OpenAt returns the store below the given workflow state directory.
func OpenAt(stateDir string) *Store { return &Store{dir: filepath.Join(stateDir, "checkout-context")} }

// Load returns the checkout's preference, nil with no error when none is
// saved, and creates nothing. A damaged or unsupported record is a
// *RecordError and stays untouched.
func (s *Store) Load(c Checkout) (*Record, error) {
	if err := c.valid(); err != nil {
		return nil, err
	}
	return s.read(c)
}

// Save remembers p for the checkout, replacing a valid earlier preference. It
// refuses to replace a damaged or unsupported record (clear it first).
func (s *Store) Save(c Checkout, p Preference) (Record, error) {
	if err := c.valid(); err != nil {
		return Record{}, err
	}
	p, err := p.normalize()
	if err != nil {
		return Record{}, err
	}
	unlock, err := s.lock(c)
	if err != nil {
		return Record{}, err
	}
	defer unlock()
	if _, err := s.read(c); err != nil {
		return Record{}, err
	}
	rec := Record{Checkout: c, Preference: p, SavedAt: time.Now().UTC()}
	if err := litclient.WriteJSON(s.path(c), rec.encode()); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// Clear removes the checkout's preference, whatever state it is in, and nothing
// else. Clearing nothing succeeds and creates nothing.
func (s *Store) Clear(c Checkout) (Cleared, error) {
	if err := c.valid(); err != nil {
		return Cleared{}, err
	}
	if _, err := os.Lstat(s.path(c)); errors.Is(err, fs.ErrNotExist) {
		return Cleared{}, nil
	} else if err != nil {
		return Cleared{}, err
	}
	unlock, err := s.lock(c)
	if err != nil {
		return Cleared{}, err
	}
	defer unlock()
	var cleared Cleared
	rec, err := s.read(c)
	var damage *RecordError
	switch {
	case err == nil && rec == nil:
		return Cleared{}, nil // another invocation cleared it meanwhile
	case err == nil:
		cleared.Previous = rec
	case errors.As(err, &damage):
		cleared.Damage = damage
	default:
		return Cleared{}, err
	}
	if err := os.Remove(s.path(c)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Cleared{}, err
	}
	cleared.Existed = true
	return cleared, nil
}

func (s *Store) path(c Checkout) string { return filepath.Join(s.dir, c.key()+".json") }

// read loads the record without locking.
func (s *Store) read(c Checkout) (*Record, error) {
	path := s.path(c)
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, &RecordError{Path: path, Kind: ErrCorrupt, Reason: "not a regular file"}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxRecordBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxRecordBytes {
		return nil, &RecordError{Path: path, Kind: ErrCorrupt, Reason: fmt.Sprintf("larger than %d bytes", maxRecordBytes)}
	}
	rec, err := decode(path, c, b)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// lock takes the checkout's mutation lock. The lock file is created first, so
// that a failure of the lock itself can only be another holder past the timeout.
func (s *Store) lock(c Checkout) (func(), error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(s.dir, c.key()+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	timeout := s.LockTimeout
	if timeout <= 0 {
		timeout = DefaultLockTimeout
	}
	unlock, err := litclient.AcquireLock(path, time.Now().Add(timeout))
	if err != nil {
		return nil, fmt.Errorf("%w (waited %s for %s)", ErrLocked, timeout, path)
	}
	return unlock, nil
}
