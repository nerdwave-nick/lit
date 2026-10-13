package checkoutcontext_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdwave-nick/lit/internal/checkoutcontext"
)

// physical returns a fresh temporary directory without symlinks in its path
// (t.TempDir may sit below a symlink such as macOS's /var).
func physical(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// git runs the real git binary in dir with no user, system or inherited
// repository configuration, so the fixtures behave the same everywhere (and
// inside a git hook, which exports GIT_DIR).
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_SYSTEM=" + os.DevNull,
		"GIT_AUTHOR_NAME=lit", "GIT_AUTHOR_EMAIL=lit@example.org", "GIT_COMMITTER_NAME=lit", "GIT_COMMITTER_EMAIL=lit@example.org",
		"GIT_ALLOW_PROTOCOL=file", "GIT_TERMINAL_PROMPT=0"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// repository creates a Git repository with one commit and nested directories.
func repository(t *testing.T) string {
	t.Helper()
	root := physical(t)
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "commit", "-q", "--allow-empty", "-m", "initial")
	mkdir(t, filepath.Join(root, "a", "b", "c"))
	return root
}

func mkdir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func resolve(t *testing.T, dir string) checkoutcontext.Checkout {
	t.Helper()
	c, err := checkoutcontext.Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve(%s): %v", dir, err)
	}
	return c
}

// preference returns a valid preference; distinct n give distinct projects.
func preference(n int) checkoutcontext.Preference {
	return checkoutcontext.Preference{
		ServiceID:    "5e5e5e5e-1111-4111-8111-abcdefabcdef",
		ProjectID:    fmt.Sprintf("%08x-0000-4000-8000-%012x", 0x22220000+n, n),
		ProjectTitle: fmt.Sprintf("feat/project-%d", n),
		Endpoint:     "http://127.0.0.1:7411",
	}
}

// The on-disk layout is part of the documented contract (docs/cli-help.md):
// one record and one lock file per checkout, named by the SHA-256 of its path,
// below checkout-context/ in the workflow state directory.
func recordDir(state string) string { return filepath.Join(state, "checkout-context") }

func recordKey(c checkoutcontext.Checkout) string {
	sum := sha256.Sum256([]byte(c.Path))
	return hex.EncodeToString(sum[:])
}

func recordFile(state string, c checkoutcontext.Checkout) string {
	return filepath.Join(recordDir(state), recordKey(c)+".json")
}

func lockFile(state string, c checkoutcontext.Checkout) string {
	return filepath.Join(recordDir(state), recordKey(c)+".lock")
}

// validRecord is what a stored record for c and p looks like.
func validRecord(c checkoutcontext.Checkout, p checkoutcontext.Preference) map[string]any {
	return map[string]any{"schema_version": 1, "checkout": c.Path, "service_id": p.ServiceID, "project_id": p.ProjectID,
		"project_title": p.ProjectTitle, "endpoint": p.Endpoint, "saved_at": "2026-10-14T00:00:00Z"}
}

func encode(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// snapshot maps every file below dir (skipping the named top-level entries)
// to a digest of its content, directories to "dir", so that tests can prove
// neighbours were left alone.
func snapshot(t *testing.T, dir string, skip ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == "." {
			return nil
		}
		// A mutation attempt may leave the (empty) lock file of its checkout.
		if filepath.Dir(rel) == "checkout-context" && strings.HasSuffix(rel, ".lock") {
			return nil
		}
		for _, s := range skip {
			if rel == s {
				return filepath.SkipDir
			}
		}
		if d.IsDir() {
			out[rel] = "dir"
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return out
}
