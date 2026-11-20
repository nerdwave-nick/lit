package hook

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hermeticGit makes the git that readGit runs ignore the user's and the
// system's configuration and any repository a surrounding git hook exports.
func hermeticGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_COMMON_DIR", "GIT_PREFIX"} {
		if old, ok := os.LookupEnv(name); ok {
			os.Unsetenv(name)
			t.Cleanup(func() { os.Setenv(name, old) })
		}
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_AUTHOR_NAME", "lit")
	t.Setenv("GIT_AUTHOR_EMAIL", "lit@example.org")
	t.Setenv("GIT_COMMITTER_NAME", "lit")
	t.Setenv("GIT_COMMITTER_EMAIL", "lit@example.org")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func repository(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(dir, "tracked.txt"), "one\n")
	gitIn(t, dir, "add", "tracked.txt")
	gitIn(t, dir, "commit", "-q", "-m", "initial")
	return dir
}

func readNow(t *testing.T, dir string) (gitInfo, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return readGit(ctx, dir)
}

func TestReadGitInRealDirectories(t *testing.T) {
	hermeticGit(t)
	t.Run("outside a repository", func(t *testing.T) {
		if g, ok := readNow(t, t.TempDir()); ok {
			t.Fatalf("a plain directory is not a repository: %+v", g)
		}
		if _, ok := readNow(t, filepath.Join(t.TempDir(), "missing")); ok {
			t.Fatal("a missing directory is not a repository")
		}
		if _, ok := readNow(t, ""); ok {
			t.Fatal("an unknown directory is not a repository")
		}
	})
	t.Run("clean", func(t *testing.T) {
		dir := repository(t)
		if g, ok := readNow(t, dir); !ok || g != (gitInfo{Branch: "main"}) {
			t.Fatalf("%+v %v", g, ok)
		}
		writeFile(t, filepath.Join(dir, "a", "b", "deep.txt"), "x")
		gitIn(t, dir, "add", "a")
		gitIn(t, dir, "commit", "-q", "-m", "deep")
		if g, ok := readNow(t, filepath.Join(dir, "a", "b")); !ok || g.Branch != "main" || g.Marks != "" {
			t.Fatalf("a subdirectory belongs to its repository: %+v %v", g, ok)
		}
	})
	t.Run("dirty", func(t *testing.T) {
		dir := repository(t)
		writeFile(t, filepath.Join(dir, "tracked.txt"), "two\n")
		if g, _ := readNow(t, dir); g.Marks != "*" {
			t.Fatalf("modified: %+v", g)
		}
		gitIn(t, dir, "add", "tracked.txt")
		if g, _ := readNow(t, dir); g.Marks != "+" {
			t.Fatalf("staged: %+v", g)
		}
		writeFile(t, filepath.Join(dir, "tracked.txt"), "three\n")
		writeFile(t, filepath.Join(dir, "new.txt"), "x")
		if g, _ := readNow(t, dir); g.Marks != "+*?" || g.label(true) != "main+*?" {
			t.Fatalf("everything at once: %+v", g)
		}
	})
	t.Run("ahead and behind", func(t *testing.T) {
		origin := filepath.Join(t.TempDir(), "origin.git")
		gitIn(t, filepath.Dir(origin), "init", "-q", "--bare", "-b", "main", origin)
		mine, theirs := filepath.Join(t.TempDir(), "mine"), filepath.Join(t.TempDir(), "theirs")
		gitIn(t, filepath.Dir(mine), "clone", "-q", origin, mine)
		writeFile(t, filepath.Join(mine, "f.txt"), "1")
		gitIn(t, mine, "add", ".")
		gitIn(t, mine, "commit", "-q", "-m", "first")
		gitIn(t, mine, "push", "-q", "-u", "origin", "main")
		gitIn(t, filepath.Dir(theirs), "clone", "-q", origin, theirs)
		for _, n := range []string{"a", "b"} {
			writeFile(t, filepath.Join(theirs, n+".txt"), n)
			gitIn(t, theirs, "add", ".")
			gitIn(t, theirs, "commit", "-q", "-m", n)
		}
		gitIn(t, theirs, "push", "-q")
		writeFile(t, filepath.Join(mine, "mine.txt"), "m")
		gitIn(t, mine, "add", ".")
		gitIn(t, mine, "commit", "-q", "-m", "mine")
		gitIn(t, mine, "fetch", "-q")
		g, ok := readNow(t, mine)
		if !ok || g.Ahead != 1 || g.Behind != 2 || g.label(true) != "main ↑1 ↓2" {
			t.Fatalf("%+v %v", g, ok)
		}
	})
	t.Run("detached", func(t *testing.T) {
		dir := repository(t)
		sha := gitIn(t, dir, "rev-parse", "HEAD")
		gitIn(t, dir, "checkout", "-q", "--detach")
		if g, _ := readNow(t, dir); g.Branch != sha[:7] {
			t.Fatalf("a detached head shows its short object name: %+v, want %s", g, sha[:7])
		}
	})
	t.Run("a git that does not answer in time", func(t *testing.T) {
		dir := repository(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, ok := readGit(ctx, dir); ok {
			t.Fatal("a cancelled context must not report a repository")
		}
	})
}
