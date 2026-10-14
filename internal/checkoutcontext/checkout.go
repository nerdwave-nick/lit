// Package checkoutcontext remembers a user's explicit project choice for one
// checkout in a machine-local workflow-state record. The record holds project
// context only: never a client identity, claim token or session name.
package checkoutcontext

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Checkout identifies the place a preference belongs to: a Git worktree root,
// or, outside Git, one exact directory.
//
// The rules: inside Git the nearest worktree root wins, so every subdirectory
// shares it while linked worktrees, nested repositories and submodules are
// their own checkouts; symlink aliases resolve to the physical location;
// outside Git the exact directory is used and parents are never searched. A
// moved checkout has a new path and so inherits nothing.
//
// Limits: Git is detected from the files on disk, without the git executable,
// and GIT_DIR and GIT_WORK_TREE are ignored. Paths are compared as spelled by
// the file system, so case-only differences on case-insensitive file systems
// and Windows 8.3 short names are not folded together.
type Checkout struct {
	// Path is the physical (symlink-free) absolute path of the worktree root
	// or exact directory.
	Path string
	// Git reports whether Path is a Git worktree root.
	Git bool
}

// Resolve finds the checkout containing dir.
func Resolve(dir string) (Checkout, error) {
	if dir == "" {
		return Checkout{}, errors.New("checkout directory is empty")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Checkout{}, err
	}
	physical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Checkout{}, err
	}
	info, err := os.Stat(physical)
	if err != nil {
		return Checkout{}, err
	}
	if !info.IsDir() {
		return Checkout{}, fmt.Errorf("checkout must be a directory: %s", physical)
	}
	for p := physical; ; {
		if isWorktreeRoot(p) {
			return Checkout{Path: p, Git: true}, nil
		}
		parent := filepath.Dir(p)
		if parent == p {
			return Checkout{Path: physical}, nil
		}
		p = parent
	}
}

// Current resolves the process's working directory.
func Current() (Checkout, error) {
	wd, err := os.Getwd()
	if err != nil {
		return Checkout{}, err
	}
	return Resolve(wd)
}

// Kind names the checkout for output: "git" or "directory".
func (c Checkout) Kind() string {
	if c.Git {
		return "git"
	}
	return "directory"
}

// key names the checkout's files: the digest of its physical path.
func (c Checkout) key() string {
	sum := sha256.Sum256([]byte(c.Path))
	return hex.EncodeToString(sum[:])
}

// isWorktreeRoot reports whether dir holds a usable .git: a repository
// directory (HEAD, objects and refs, as git itself requires) or a file
// pointing at one, as linked worktrees and submodules have.
func isWorktreeRoot(dir string) bool {
	dotGit := filepath.Join(dir, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return isGitDirectory(dotGit)
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return false
	}
	b, err := os.ReadFile(dotGit)
	if err != nil {
		return false
	}
	first, _, _ := strings.Cut(string(b), "\n")
	target, ok := strings.CutPrefix(strings.TrimRight(first, "\r"), "gitdir:")
	target = strings.TrimSpace(target)
	if !ok || target == "" {
		return false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	info, err = os.Stat(target)
	return err == nil && info.IsDir()
}

func isGitDirectory(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		return false
	}
	for _, name := range []string{"objects", "refs"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}
