package edit

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// The watcher reports each change of the content, including an editor's
// save by replacement, and nothing once stopped.
func TestWatchReportsContentChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draft.md")
	if err := os.WriteFile(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	var n atomic.Int32
	stop := Watch(path, 10*time.Millisecond, func() { n.Add(1) })
	wait := func(want int32) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for n.Load() < want {
			if time.Now().After(deadline) {
				t.Fatalf("%d changes seen, want %d", n.Load(), want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if n.Load() != 0 {
		t.Fatalf("an unchanged file reported %d changes", n.Load())
	}
	if err := os.WriteFile(path, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	wait(1)
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte("three"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	wait(2)
	stop()
	stop()
	seen := n.Load()
	if err := os.WriteFile(path, []byte("four"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if n.Load() != seen {
		t.Fatal("a stopped watcher reported a change")
	}
}
