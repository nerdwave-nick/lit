package checkoutcontext_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nerdwave-nick/lit/internal/checkoutcontext"
)

func TestInsideGitEverySubdirectoryResolvesToTheWorktreeRoot(t *testing.T) {
	root := repository(t)
	for _, dir := range []string{root, filepath.Join(root, "a"), filepath.Join(root, "a", "b", "c"), filepath.Join(root, ".git"), filepath.Join(root, ".git", "refs")} {
		if got := resolve(t, dir); got != (checkoutcontext.Checkout{Path: root, Git: true}) {
			t.Errorf("Resolve(%s) = %+v, want the root %s", dir, got, root)
		}
	}
	if kind := resolve(t, root).Kind(); kind != "git" {
		t.Errorf("kind = %q", kind)
	}
}

func TestSeparateWorktreesAreSeparateCheckouts(t *testing.T) {
	root := repository(t)
	linked := filepath.Join(physical(t), "linked")
	git(t, root, "worktree", "add", "-q", "-b", "other", linked)
	mkdir(t, filepath.Join(linked, "pkg"))
	main, other := resolve(t, filepath.Join(root, "a")), resolve(t, filepath.Join(linked, "pkg"))
	if main.Path != root || other.Path != linked || !main.Git || !other.Git {
		t.Fatalf("main %+v, linked %+v; want %s and %s", main, other, root, linked)
	}
}

func TestSymlinkAliasesResolveToThePhysicalCheckout(t *testing.T) {
	root := repository(t)
	aliases := physical(t)
	toRoot, toSub := filepath.Join(aliases, "root-alias"), filepath.Join(aliases, "sub-alias")
	for alias, target := range map[string]string{toRoot: root, toSub: filepath.Join(root, "a", "b")} {
		if err := os.Symlink(target, alias); err != nil {
			t.Skipf("symlinks are unavailable: %v", err)
		}
	}
	want := checkoutcontext.Checkout{Path: root, Git: true}
	for _, dir := range []string{toRoot, toSub, filepath.Join(toRoot, "a", "b"), filepath.Join(toSub, "c")} {
		if got := resolve(t, dir); got != want {
			t.Errorf("Resolve(%s) = %+v, want %+v", dir, got, want)
		}
	}
	plain := mkdir(t, filepath.Join(physical(t), "plain"))
	if err := os.Symlink(plain, filepath.Join(aliases, "plain-alias")); err != nil {
		t.Fatal(err)
	}
	if got := resolve(t, filepath.Join(aliases, "plain-alias")); got != (checkoutcontext.Checkout{Path: plain}) {
		t.Errorf("alias of a plain directory = %+v, want %s", got, plain)
	}
}

func TestNestedRepositoriesNeverInheritTheOuterCheckout(t *testing.T) {
	outer := repository(t)
	inner := mkdir(t, filepath.Join(outer, "vendor", "inner"))
	git(t, inner, "init", "-q", "-b", "main")
	mkdir(t, filepath.Join(inner, "src"))
	if got := resolve(t, filepath.Join(inner, "src")); got != (checkoutcontext.Checkout{Path: inner, Git: true}) {
		t.Errorf("inside the nested repository: %+v, want %s", got, inner)
	}
	if got := resolve(t, filepath.Join(outer, "vendor")); got.Path != outer {
		t.Errorf("beside the nested repository: %+v, want %s", got, outer)
	}
}

func TestSubmoduleStyleGitFileIsItsOwnCheckout(t *testing.T) {
	outer := repository(t)
	gitDir := filepath.Join(outer, ".git", "modules", "sub")
	for _, d := range []string{"objects", "refs"} {
		mkdir(t, filepath.Join(gitDir, d))
	}
	write(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/main\n")
	sub := mkdir(t, filepath.Join(outer, "sub"))
	write(t, filepath.Join(sub, ".git"), "gitdir: ../.git/modules/sub\n")
	mkdir(t, filepath.Join(sub, "lib"))
	if got := resolve(t, filepath.Join(sub, "lib")); got != (checkoutcontext.Checkout{Path: sub, Git: true}) {
		t.Errorf("submodule checkout = %+v, want %s", got, sub)
	}
}

func TestOutsideGitTheExactDirectoryIsTheCheckout(t *testing.T) {
	base := physical(t)
	plain, sub := mkdir(t, filepath.Join(base, "plain")), mkdir(t, filepath.Join(base, "plain", "sub"))
	for _, dir := range []string{plain, sub} {
		if got := resolve(t, dir); got != (checkoutcontext.Checkout{Path: dir}) {
			t.Errorf("Resolve(%s) = %+v, want that exact directory", dir, got)
		}
	}
	if kind := resolve(t, plain).Kind(); kind != "directory" {
		t.Errorf("kind = %q", kind)
	}
}

func TestGitLookalikesAreNotRepositories(t *testing.T) {
	base := physical(t)
	for name, setup := range map[string]func(parent string){
		"empty .git directory": func(p string) { mkdir(t, filepath.Join(p, ".git")) },
		"directory lacking refs": func(p string) {
			mkdir(t, filepath.Join(p, ".git", "objects"))
			write(t, filepath.Join(p, ".git", "HEAD"), "ref: refs/heads/main\n")
		},
		"regular file with junk": func(p string) { write(t, filepath.Join(p, ".git"), "not a repository\n") },
		"gitdir file to nowhere": func(p string) { write(t, filepath.Join(p, ".git"), "gitdir: "+filepath.Join(p, "missing")+"\n") },
		"gitdir file to a file": func(p string) {
			write(t, filepath.Join(p, "target"), "x")
			write(t, filepath.Join(p, ".git"), "gitdir: target\n")
		},
		"empty .git file":           func(p string) { write(t, filepath.Join(p, ".git"), "") },
		"gitdir without a target":   func(p string) { write(t, filepath.Join(p, ".git"), "gitdir:\n") },
		"unrelated key in the file": func(p string) { write(t, filepath.Join(p, ".git"), "commondir: .\n") },
	} {
		t.Run(name, func(t *testing.T) {
			parent := mkdir(t, filepath.Join(base, "parent-"+filepath.Base(t.Name())))
			setup(parent)
			child := mkdir(t, filepath.Join(parent, "child"))
			if got := resolve(t, child); got != (checkoutcontext.Checkout{Path: child}) {
				t.Errorf("a decoy .git in the parent made %+v of %s", got, child)
			}
		})
	}
}

func TestAMovedCheckoutIsADifferentCheckout(t *testing.T) {
	root := repository(t)
	moved := filepath.Join(physical(t), "moved")
	before := resolve(t, root)
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	after := resolve(t, filepath.Join(moved, "a"))
	if after.Path != moved || after == before {
		t.Fatalf("before %+v, after %+v; the moved checkout must be identified by its new path", before, after)
	}
	store := checkoutcontext.OpenAt(physical(t))
	if _, err := store.Save(before, preference(1)); err != nil {
		t.Fatal(err)
	}
	if rec, err := store.Load(after); err != nil || rec != nil {
		t.Fatalf("the moved checkout inherited %+v (err %v)", rec, err)
	}
}

func TestResolveRejectsMissingDirectoriesAndFiles(t *testing.T) {
	base := physical(t)
	file := filepath.Join(base, "file")
	write(t, file, "x")
	for _, dir := range []string{filepath.Join(base, "missing"), file, ""} {
		if got, err := checkoutcontext.Resolve(dir); err == nil {
			t.Errorf("Resolve(%q) = %+v, want an error", dir, got)
		}
	}
}

func TestCurrentResolvesTheWorkingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Chdir semantics differ on Windows")
	}
	root := repository(t)
	t.Chdir(filepath.Join(root, "a", "b"))
	got, err := checkoutcontext.Current()
	if err != nil || got != (checkoutcontext.Checkout{Path: root, Git: true}) {
		t.Fatalf("Current() = %+v, %v; want %s", got, err, root)
	}
}
