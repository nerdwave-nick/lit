package tui

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// While the program is suspended into the editor, EditorRunning names its
// process and the draft file it edits, from any goroutine; afterwards it
// reports none.
func TestEditorRunningNamesTheSuspendedEditor(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	release := filepath.Join(t.TempDir(), "release")
	draft := filepath.Join(t.TempDir(), "my draft--12345678.md")
	s := &suspension{}
	m := Model{ed: &editing{susp: s}}
	if _, _, ok := m.EditorRunning(); ok {
		t.Fatal("an editor before any ran")
	}
	c := exec.Command(sh, "-c", `while [ ! -e "$0" ]; do sleep 0.01; done`, release, draft)
	done := make(chan error, 1)
	go func() { done <- suspended{c: c, s: s}.Run() }()
	waitFor(t, "the editor to be recorded", func() bool { _, _, ok := m.EditorRunning(); return ok })
	if pid, path, _ := m.EditorRunning(); pid != c.Process.Pid || path != draft {
		t.Fatalf("running %d %q, want %d %q", pid, path, c.Process.Pid, draft)
	}
	if err := exec.Command("touch", release).Run(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the editor did not end")
	}
	if _, _, ok := m.EditorRunning(); ok {
		t.Fatal("still recorded after it ended")
	}
	if _, _, ok := (Model{}).EditorRunning(); ok {
		t.Fatal("a model without an editor")
	}
}
