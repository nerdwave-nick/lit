package workflowskills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

func hash(b []byte) string   { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func ptr(s string) *string   { return &s }
func same(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func exists(p string) bool   { _, e := os.Lstat(p); return !os.IsNotExist(e) }
func safe(p string) error {
	p, e := filepath.Abs(p)
	if e != nil {
		return e
	}
	for {
		v, e := os.Lstat(p)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if e == nil && v.Mode()&os.ModeSymlink != 0 {
			// Preserve the reviewed macOS system aliases; reject user aliases.
			if runtime.GOOS == "darwin" && (p == "/tmp" || p == "/var") {
				target, err := os.Readlink(p)
				if err != nil {
					return err
				}
				if target == "private"+p || target == "/private"+p {
					return safe("/private" + p)
				}
			}
			return fmt.Errorf("refusing symlink: %s", p)
		}
		q := filepath.Dir(p)
		if q == p {
			return nil
		}
		p = q
	}
}
func fingerprint(p string) (string, error) {
	if e := safe(p); e != nil {
		return "", e
	}
	v, e := os.Stat(p)
	if e != nil {
		return "", e
	}
	if !v.IsDir() {
		return "", fmt.Errorf("expected directory: %s", p)
	}
	var names []string
	e = filepath.WalkDir(p, func(f string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink: %s", f)
		}
		if !d.IsDir() {
			if !d.Type().IsRegular() {
				return fmt.Errorf("nonregular skill file: %s", f)
			}
			r, _ := filepath.Rel(p, f)
			names = append(names, r)
		}
		return nil
	})
	if e != nil {
		return "", e
	}
	sort.Slice(names, func(i, j int) bool { return legacyPathLess(names[i], names[j], runtime.GOOS == "windows") })
	h := sha256.New()
	for _, n := range names {
		b, e := os.ReadFile(filepath.Join(p, n))
		if e != nil {
			return "", e
		}
		h.Write([]byte(n))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func fileHash(p string) (*string, error) {
	if e := safe(p); e != nil {
		return nil, e
	}
	b, e := os.ReadFile(p)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return ptr(hash(b)), nil
}
func copyTree(src, dst string) error {
	if _, e := fingerprint(src); e != nil {
		return e
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		r, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, r)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		return os.WriteFile(target, b, 0644)
	})
}
func readJSON(p string, v any) error {
	if e := safe(p); e != nil {
		return e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}
func jsonBytes(v any) []byte { b, _ := json.MarshalIndent(v, "", "  "); return append(b, '\n') }
func validHash(s *string) bool {
	if s == nil {
		return true
	}
	b, e := hex.DecodeString(*s)
	return e == nil && len(b) == 32
}
func exact(m map[string]*string, names []string) bool {
	if len(m) != len(names) {
		return false
	}
	for _, n := range names {
		v, ok := m[n]
		if !ok || !validHash(v) {
			return false
		}
	}
	return true
}

func atomicWrite(p string, b []byte, mode fs.FileMode) error {
	f, e := os.CreateTemp(filepath.Dir(p), ".lit-write-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(tmp, p)
}
func restoreTree(src, dst string) error {
	tmp, e := os.MkdirTemp(filepath.Dir(dst), ".lit-restore-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(tmp)
	if e = copyTree(src, tmp); e != nil {
		return e
	}
	return os.Rename(tmp, dst)
}

// pathlib sorts path components; WindowsPath additionally folds their case.
// Hash input retains the original native separators and spelling on both hosts.
func legacyPathLess(left, right string, windows bool) bool {
	sep := "/"
	if windows {
		sep = "\\"
		left = strings.ToLower(left)
		right = strings.ToLower(right)
	}
	a, b := strings.Split(left, sep), strings.Split(right, sep)
	for k := 0; k < len(a) && k < len(b); k++ {
		if a[k] != b[k] {
			return a[k] < b[k]
		}
	}
	return len(a) < len(b)
}
