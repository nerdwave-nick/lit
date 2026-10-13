// Package legacystore is a frozen copy of the legacy file store, internal/store
// at commit c5af73e, for lit-migrate's setup import only (see ../legacy). It
// is unchanged apart from its package name; lit-server must never link it.
//
// Package store owns a Linux-local directory and commits recoverable file batches.
// Callers serialize logical reads and transactions; Commit also fences unexpected
// external file changes. A committed journal is authoritative until fully replayed.
package legacystore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Identity struct {
	SchemaVersion int    `json:"schema_version"`
	ServiceID     string `json:"service_id"`
}
type Write struct {
	Path   string `json:"path"`
	Data   []byte `json:"data"`
	Delete bool   `json:"delete,omitempty"`
}
type journal struct {
	SchemaVersion int     `json:"schema_version"`
	ID            string  `json:"id"`
	Writes        []Write `json:"writes"`
}
type marker struct {
	SchemaVersion int    `json:"schema_version"`
	SHA256        string `json:"sha256"`
}
type Store struct {
	Root         string
	Identity     Identity
	mu           sync.Mutex
	lockFile     *os.File
	fingerprints map[string]string
	poisoned     bool
	Fault        func(string) error
}

func Open(root string) (*Store, error) { return OpenWithFault(root, nil) }

// OpenWithFault exposes deterministic replay interruption to storage tests.
// Production startup uses Open and has no environment-driven failure switches.
func OpenWithFault(root string, fault func(string) error) (s *Store, err error) {
	if err = durableMkdir(root); err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(root, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lock(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("storage is already owned: %w", err)
	}
	s = &Store{Root: root, lockFile: f, Fault: fault}
	owned := s
	defer func() {
		if err != nil {
			owned.Close()
		}
	}()
	if _, err = s.scan(); err != nil {
		return nil, err
	}
	// Establish storage identity before cleanup or journal replay can alter any
	// accepted state or evidence. Unknown stores require explicit host repair.
	b, e := os.ReadFile(filepath.Join(root, "service.json"))
	if os.IsNotExist(e) {
		entries, x := os.ReadDir(root)
		if x != nil {
			return nil, x
		}
		for _, v := range entries {
			if v.Name() == ".lock" {
				continue
			}
			// Only an interrupted first atomic identity write may precede
			// service.json. Journals or record directories imply an existing
			// store and must not be removed to manufacture an empty one.
			if v.Type().IsRegular() && strings.HasPrefix(v.Name(), ".lit-tmp-") {
				continue
			}
			return nil, fmt.Errorf("missing service identity in populated storage; preserve files for host repair")
		}
		s.Identity = Identity{1, protocol.UUID()}
		b, _ = json.Marshal(s.Identity)
		if err = atomicWrite(filepath.Join(root, "service.json"), b); err != nil {
			return nil, err
		}
	} else if e != nil {
		return nil, e
	} else {
		if err = protocol.Decode(b, &s.Identity); err != nil {
			return nil, fmt.Errorf("invalid service identity: %w", err)
		}
		if s.Identity.SchemaVersion != 1 || !protocol.ValidUUID(s.Identity.ServiceID) {
			return nil, fmt.Errorf("unsupported or invalid service identity")
		}
	}
	if err = s.removeTemps(); err != nil {
		return nil, err
	}
	if err = s.recover(); err != nil {
		return nil, err
	}
	s.fingerprints, err = s.scan()
	return s, err
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lockFile == nil {
		return nil
	}
	e := unlock(s.lockFile)
	x := s.lockFile.Close()
	s.lockFile = nil
	if e != nil {
		return e
	}
	return x
}
func validPath(p string) bool {
	return p != "" && !filepath.IsAbs(p) && filepath.ToSlash(filepath.Clean(p)) == p && p != ".." && !strings.HasPrefix(p, "../") && !strings.HasPrefix(p, ".") && p != "journal" && !strings.HasPrefix(p, "journal/") && p != "service.json"
}
func hash(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func (s *Store) scan() (map[string]string, error) {
	m := map[string]string{}
	e := filepath.WalkDir(s.Root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(s.Root, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in authoritative store: %s", rel)
		}
		if rel == ".lock" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in authoritative store: %s", rel)
		}
		if d.IsDir() {
			return nil
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		if !strings.HasPrefix(rel, "journal/") {
			m[rel] = hash(b)
		}
		return nil
	})
	return m, e
}
func (s *Store) Check() error {
	if s.lockFile == nil {
		return fmt.Errorf("storage is closed")
	}
	if s.poisoned {
		return fmt.Errorf("storage requires restart recovery")
	}
	m, e := s.scan()
	if e != nil {
		return e
	}
	if len(m) != len(s.fingerprints) {
		return fmt.Errorf("authoritative storage changed outside lit")
	}
	for p, h := range m {
		if s.fingerprints[p] != h {
			return fmt.Errorf("authoritative storage changed outside lit: %s", p)
		}
	}
	return nil
}
func (s *Store) Read(path string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.Root, filepath.FromSlash(path)))
}
func (s *Store) List(dir string) ([]os.DirEntry, error) {
	v, e := os.ReadDir(filepath.Join(s.Root, dir))
	if os.IsNotExist(e) {
		return nil, nil
	}
	return v, e
}
func (s *Store) fail(point string) error {
	if s.Fault != nil {
		return s.Fault(point)
	}
	return nil
}
func (s *Store) Commit(writes []Write) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.Check(); e != nil {
		return e
	}
	if len(writes) == 0 {
		return nil
	}
	seen := map[string]bool{}
	for _, w := range writes {
		if !validPath(w.Path) || seen[w.Path] {
			return fmt.Errorf("invalid or duplicate transaction path %q", w.Path)
		}
		seen[w.Path] = true
	}
	sort.Slice(writes, func(i, j int) bool { return writes[i].Path < writes[j].Path })
	j := journal{1, protocol.UUID(), writes}
	b, _ := json.Marshal(j)
	dir := filepath.Join(s.Root, "journal")
	if e := durableMkdir(dir); e != nil {
		return e
	}
	if e := syncDir(s.Root); e != nil {
		return e
	}
	if e := s.fail("after_journal_directory"); e != nil {
		s.poisoned = true
		return e
	}
	if e := atomicWrite(filepath.Join(dir, "pending.json"), b); e != nil {
		return e
	}
	if e := s.fail("after_payload"); e != nil {
		s.poisoned = true
		return e
	}
	if e := s.fail("before_commit"); e != nil {
		s.poisoned = true
		return e
	}
	mb, _ := json.Marshal(marker{1, hash(b)})
	if e := atomicWrite(filepath.Join(dir, "commit.json"), mb); e != nil {
		s.poisoned = true
		return e
	}
	s.poisoned = true
	if e := s.fail("after_commit"); e != nil {
		return e
	}
	if e := s.apply(j); e != nil {
		return e
	}
	if e := s.cleanup(); e != nil {
		return e
	}
	var e error
	s.fingerprints, e = s.scan()
	if e == nil {
		s.poisoned = false
	}
	return e
}
func (s *Store) apply(j journal) error {
	for i, w := range j.Writes {
		p := filepath.Join(s.Root, filepath.FromSlash(w.Path))
		if w.Delete {
			if e := os.Remove(p); e != nil && !os.IsNotExist(e) {
				return e
			}
			if _, e := os.Stat(filepath.Dir(p)); e == nil {
				if e = syncDir(filepath.Dir(p)); e != nil {
					return e
				}
			}
		} else {
			if e := atomicWrite(p, w.Data); e != nil {
				return e
			}
		}
		if e := s.fail(fmt.Sprintf("after_write:%d", i+1)); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) recover() error {
	dir := filepath.Join(s.Root, "journal")
	entries, e := os.ReadDir(dir)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if len(entries) == 0 {
		return os.Remove(dir)
	}
	for _, v := range entries {
		if v.Name() != "pending.json" && v.Name() != "commit.json" {
			return fmt.Errorf("unrecognized journal evidence: %s", v.Name())
		}
	}
	b, e := os.ReadFile(filepath.Join(dir, "pending.json"))
	if e != nil {
		return fmt.Errorf("journal missing payload: %w", e)
	}
	var j journal
	if e = protocol.Decode(b, &j); e != nil {
		return fmt.Errorf("damaged journal: %w", e)
	}
	if j.SchemaVersion != 1 || !protocol.ValidUUID(j.ID) || len(j.Writes) == 0 {
		return fmt.Errorf("unsupported or invalid journal")
	}
	seen := map[string]bool{}
	for _, w := range j.Writes {
		if !validPath(w.Path) || seen[w.Path] {
			return fmt.Errorf("unsafe journal path")
		}
		seen[w.Path] = true
	}
	mb, e := os.ReadFile(filepath.Join(dir, "commit.json"))
	if os.IsNotExist(e) {
		return s.cleanup()
	}
	if e != nil {
		return e
	}
	var m marker
	if e = protocol.Decode(mb, &m); e != nil || m.SchemaVersion != 1 || m.SHA256 != hash(b) {
		return fmt.Errorf("damaged journal commit marker")
	}
	if e = s.apply(j); e != nil {
		return e
	}
	return s.cleanup()
}
func (s *Store) cleanup() error {
	dir := filepath.Join(s.Root, "journal")
	if e := os.Remove(filepath.Join(dir, "commit.json")); e != nil && !os.IsNotExist(e) {
		return e
	}
	if e := syncDir(dir); e != nil {
		return e
	}
	if e := s.fail("cleanup_after_marker"); e != nil {
		return e
	}
	if e := os.Remove(filepath.Join(dir, "pending.json")); e != nil && !os.IsNotExist(e) {
		return e
	}
	if e := s.fail("cleanup_after_payload"); e != nil {
		return e
	}
	if e := os.Remove(dir); e != nil {
		return e
	}
	return syncDir(s.Root)
}
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func atomicWrite(path string, b []byte) error {
	dir := filepath.Dir(path)
	if e := durableMkdir(dir); e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".lit-tmp-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if e = os.Rename(tmp, path); e != nil {
		return e
	}
	if e = syncDir(dir); e != nil {
		return e
	}
	return syncDir(filepath.Dir(dir))
}
func JSONWrite(path string, v any) Write {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return Write{Path: path, Data: b}
}

// Flush each newly created directory and its parent; deep project/history paths
// must survive power failure before the journal can be discarded.
func durableMkdir(path string) error {
	if info, e := os.Lstat(path); e == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe directory %s", path)
		}
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	parent := filepath.Dir(path)
	if e := durableMkdir(parent); e != nil {
		return e
	}
	if e := os.Mkdir(path, 0700); e != nil && !os.IsExist(e) {
		return e
	}
	if e := syncDir(path); e != nil {
		return e
	}
	return syncDir(parent)
}
func (s *Store) removeTemps() error {
	return filepath.WalkDir(s.Root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), ".lit-tmp-") {
			if e = os.Remove(p); e != nil {
				return e
			}
			return syncDir(filepath.Dir(p))
		}
		return nil
	})
}
