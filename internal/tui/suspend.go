package tui

import (
	"io"
	"os/exec"
	"sync"

	tea "charm.land/bubbletea/v2"
)

// suspension records the editor holding the terminal while the program is
// suspended (tea.ExecProcess), so main can tell, from any goroutine, that
// shutdown must wait for it and that a forced exit must leave it alone.
type suspension struct {
	mu   sync.Mutex
	pid  int
	path string // the draft file being edited
}

// exec suspends the program into c, recording it while it runs.
func (s *suspension) exec(c *exec.Cmd, done tea.ExecCallback) tea.Cmd {
	return tea.Exec(suspended{c: c, s: s}, done)
}

// Running reports the editor that holds the terminal: its process ID and
// the draft file it edits.
func (s *suspension) Running() (pid int, path string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pid, s.path, s.pid != 0
}

func (s *suspension) set(pid int, path string) {
	s.mu.Lock()
	s.pid, s.path = pid, path
	s.mu.Unlock()
}

// suspended is an editor command run by tea.Exec. Like tea.ExecProcess it
// runs c with the program's terminal; it records c while it runs.
type suspended struct {
	c *exec.Cmd
	s *suspension
}

func (x suspended) Run() error {
	if err := x.c.Start(); err != nil {
		return err
	}
	path := ""
	if n := len(x.c.Args); n > 1 {
		path = x.c.Args[n-1] // edit.Command passes the draft last
	}
	x.s.set(x.c.Process.Pid, path)
	defer x.s.set(0, "")
	return x.c.Wait()
}

func (x suspended) SetStdin(r io.Reader) {
	if x.c.Stdin == nil {
		x.c.Stdin = r
	}
}

func (x suspended) SetStdout(w io.Writer) {
	if x.c.Stdout == nil {
		x.c.Stdout = w
	}
}

func (x suspended) SetStderr(w io.Writer) {
	if x.c.Stderr == nil {
		x.c.Stderr = w
	}
}

// EditorRunning reports the editor that holds the terminal, if any: its
// process ID and the draft file it edits. It is safe to call from any
// goroutine; main uses it to wait for the editor and never kill it.
func (m Model) EditorRunning() (pid int, path string, ok bool) {
	if m.ed == nil || m.ed.susp == nil {
		return 0, "", false
	}
	return m.ed.susp.Running()
}
