package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/claims"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/comments"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/create"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/drafts"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/edit"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/lifecycle"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/prefs"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/recovery"
)

// serve runs one lit-tui instance on the terminal: a fresh client recorded in
// the registry, the program with Bubble Tea's own signal handler disabled,
// and the lifecycle's orderly shutdown on every exit path. It returns 0 after
// a clean exit, 4 when an outcome is uncertain or unknown, 1 when the service
// cannot be reached, and 128+signal (130 for Ctrl-C) when an exit is forced.
func serve(endpoint string, pref prefs.Prefs, stdin, stdout *os.File, stderr io.Writer) int {
	stateDir, err := litclient.StateDir()
	if err != nil {
		fmt.Fprintln(stderr, "lit-tui: cannot find the client state directory:", err)
		return 1
	}
	host, _ := os.Hostname()
	l, err := lifecycle.New(lifecycle.Config{StateDir: stateDir, Endpoint: endpoint, HTTP: &http.Client{}, Actor: actor(), Host: host, PID: os.Getpid()})
	if err != nil {
		fmt.Fprintln(stderr, "lit-tui:", recovery.Describe(err))
		return 1
	}
	stop := l.HandleSignals()
	defer stop()
	if err := l.Connect(context.Background()); err != nil {
		fmt.Fprintf(stderr, "lit-tui: cannot connect to %s: %s\n", endpoint, recovery.Describe(err))
		l.Shutdown("connect failed")
		return finish(l, stderr, 1)
	}
	// The holder is the lifecycle's renewer: shutdown stops and joins it
	// before the disconnect that releases its claims.
	h := claims.Start(l, claims.Config{Idle: pref.ClaimIdle, Lease: pref.ClaimLease})
	ed := edit.New(l, h, drafts.New(stateDir))
	comments.Register(ed)
	create.Register(ed)
	m := NewModel(endpoint).WithPrefs(pref).WithLifecycle(l).WithClaims(h).WithEditor(ed)
	p := tea.NewProgram(m, tea.WithInput(stdin), tea.WithOutput(stdout), tea.WithoutSignalHandler())
	l.Attach(p)
	if l.Started() { // a signal arrived while connecting
		return finish(l, stderr, 0)
	}
	ran := make(chan error, 1)
	go func() {
		_, err := p.Run()
		ran <- err
	}()
	// Shutdown never kills an open editor: once remote cleanup is done the
	// program quits after the editor exits.
	waited := make(chan string, 1)
	go func() {
		<-l.Done()
		if _, path, ok := m.EditorRunning(); ok {
			waited <- path
		}
	}()
	select {
	case err = <-ran:
	case <-l.Forced():
		if pid, path, ok := m.EditorRunning(); ok {
			// The editor holds the terminal: restoring it now would break
			// the editor's input. Leave both to the editor.
			fmt.Fprintf(stderr, "lit-tui: exit forced while the editor (pid %d) was open: it was not killed and keeps the draft file %s\n", pid, path)
			return forced(l, stderr)
		}
		p.Kill()
		select {
		case <-ran:
		case <-time.After(2 * time.Second):
		}
		return forced(l, stderr)
	}
	select {
	case path := <-waited:
		fmt.Fprintf(stderr, "lit-tui: shutdown finished while the editor was open and then waited for it to exit; the draft is kept: %s\n", path)
	default:
	}
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		fmt.Fprintln(stderr, "lit-tui:", err)
	}
	// The program can end before shutdown (the terminal went away): start it
	// if needed and wait for it, bounded, before the process exits.
	l.Shutdown("program ended")
	return finish(l, stderr, 0)
}

// finish waits, bounded, for shutdown and reports a result that is not clean.
func finish(l *lifecycle.Lifecycle, stderr io.Writer, clean int) int {
	select {
	case <-l.Done():
	case <-l.Forced():
		return forced(l, stderr)
	case <-time.After(l.HardDeadline()):
		fmt.Fprintf(stderr, "lit-tui: shutdown did not finish within %s\n", l.HardDeadline())
		report(stderr, l.Outstanding())
		return 1
	}
	r := l.Result()
	if r.Outcome == lifecycle.Clean {
		return clean
	}
	fmt.Fprintf(stderr, "lit-tui: exit outcome %s: %s\n", r.Outcome, r.Reason)
	report(stderr, r)
	return 4
}

func forced(l *lifecycle.Lifecycle, stderr io.Writer) int {
	r := l.Outstanding()
	select {
	case <-l.Done():
		r = l.Result()
	default:
		fmt.Fprintln(stderr, "lit-tui: exit forced before shutdown finished")
	}
	report(stderr, r)
	return l.ForceCode()
}

func report(w io.Writer, r lifecycle.Result) {
	for _, line := range lifecycle.ReportLines(r) {
		fmt.Fprintln(w, "lit-tui:", line)
	}
}

// actor names the person, as lit connect does by default.
func actor() protocol.Actor {
	name := os.Getenv("USER")
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	if name == "" {
		name = "human"
	}
	return protocol.Actor{Name: name, Kind: "human"}
}
