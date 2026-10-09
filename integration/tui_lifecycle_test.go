package integration

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/registry"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/tuitest"
)

// These scenarios drive the real lit-tui binary in a private tmux server
// against a temporary lit-server. Each has its own HOME, XDG directories and
// LIT_STATE_DIR, and LIT_ENDPOINT names the temporary server only. They skip
// without tmux or with -short; every wait is bounded.

var tuiSockets atomic.Int32

var tuiBuild struct {
	once sync.Once
	bin  string
	err  error
	out  []byte
}

func litTUI(t *testing.T) string {
	t.Helper()
	tuiBuild.once.Do(func() {
		tuiBuild.bin = filepath.Join(filepath.Dir(litBin), "lit-tui")
		c := exec.Command("go", "build", "-race", "-o", tuiBuild.bin, "../cmd/lit-tui")
		tuiBuild.out, tuiBuild.err = c.CombinedOutput()
	})
	if tuiBuild.err != nil {
		t.Fatalf("build lit-tui: %v: %s", tuiBuild.err, tuiBuild.out)
	}
	return tuiBuild.bin
}

type tuiEnv struct {
	t        *testing.T
	bin      string // the lit-tui binary; litTUI's race build by default
	srv      *server
	endpoint string // what lit-tui connects to: the server or a delaying proxy
	root     string
	state    string
	socket   string
}

func newTUIEnv(t *testing.T) *tuiEnv {
	t.Helper()
	if testing.Short() {
		t.Skip("tmux scenarios are skipped with -short")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	litTUI(t)
	root := t.TempDir()
	e := &tuiEnv{t: t, bin: litTUI(t), root: root, state: filepath.Join(root, "state"), socket: fmt.Sprintf("lit-tui-%d-%d", os.Getpid(), tuiSockets.Add(1))}
	e.srv = start(t, filepath.Join(root, "data"))
	e.endpoint = e.srv.endpoint
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", e.socket, "kill-server").Run() })
	return e
}

func (e *tuiEnv) tmux(args ...string) string {
	e.t.Helper()
	out, err := exec.Command("tmux", append([]string{"-L", e.socket, "-f", "/dev/null"}, args...)...).CombinedOutput()
	if err != nil {
		e.t.Fatalf("tmux %v: %v: %s", args, err, out)
	}
	return string(out)
}

// slowDisconnect puts a proxy in front of the server that holds every
// disconnect for d before forwarding it, so an exit that does not wait for
// its shutdown leaves the client connected.
func (e *tuiEnv) slowDisconnect(d time.Duration) {
	target, _ := url.Parse(e.srv.endpoint)
	proxy := httputil.NewSingleHostReverseProxy(target)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/disconnect" {
			time.Sleep(d)
		}
		proxy.ServeHTTP(w, r)
	}))
	e.t.Cleanup(front.Close)
	e.endpoint = front.URL
}

// pane is one lit-tui instance in its own tmux session.
type pane struct {
	e     *tuiEnv
	name  string
	entry registry.Entry
	info  registry.Info
	m     litclient.Mapping
}

// launch starts lit-tui in session name and waits until it has registered its
// client. The shell records lit-tui's exit status and keeps the pane open.
func (e *tuiEnv) launch(name string) *pane {
	e.t.Helper()
	before := map[string]bool{}
	for _, x := range e.entries() {
		before[x.ID] = true
	}
	home := filepath.Join(e.root, "home")
	env := []string{"env", "-i", "HOME=" + home, "USER=tester", "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color",
		"XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_STATE_HOME=" + filepath.Join(home, "xdg-state"),
		"LIT_STATE_DIR=" + e.state, "LIT_ENDPOINT=" + e.endpoint, "GORACE=atexit_sleep_ms=0", e.bin}
	script := strings.Join(quoteAll(env), " ") + "; echo EXIT=$? > " + quoteAll([]string{filepath.Join(e.root, name+".exit")})[0] + "; exec sleep 600"
	e.tmux("new-session", "-d", "-s", name, "-x", "200", "-y", "50", "sh -c "+quoteAll([]string{script})[0])
	p := &pane{e: e, name: name}
	eventually(e.t, 30*time.Second, "lit-tui "+name+" to register", func() bool {
		for _, x := range e.entries() {
			if before[x.ID] {
				continue
			}
			info, err := registry.ReadInfo(x)
			m, merr := litclient.ReadMapping(x.SessionPath())
			if err == nil && merr == nil {
				p.entry, p.info, p.m = x, info, m
				return true
			}
		}
		return false
	}, p.screen)
	p.await("the session line", func(s string) bool { return strings.Contains(s, "Session: client "+p.m.ClientID[:8]) })
	return p
}

func quoteAll(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return out
}

func (e *tuiEnv) entries() []registry.Entry {
	e.t.Helper()
	got, err := registry.Scan(e.state, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	return got
}

func (p *pane) screen() string {
	out, _ := exec.Command("tmux", "-L", p.e.socket, "capture-pane", "-p", "-t", p.name).CombinedOutput()
	return string(out)
}

func (p *pane) keys(k ...string) { p.e.tmux(append([]string{"send-keys", "-t", p.name}, k...)...) }

func (p *pane) alternate() string {
	return strings.TrimSpace(p.e.tmux("display", "-p", "-t", p.name, "#{alternate_on}"))
}

func (p *pane) await(what string, ok func(string) bool) string {
	p.e.t.Helper()
	var s string
	eventually(p.e.t, 30*time.Second, what, func() bool { s = p.screen(); return ok(s) }, p.screen)
	return s
}

// exit waits for lit-tui's exit status.
func (p *pane) exit() int {
	p.e.t.Helper()
	var code int
	eventually(p.e.t, 60*time.Second, p.name+" to exit", func() bool {
		b, err := os.ReadFile(filepath.Join(p.e.root, p.name+".exit"))
		if err != nil {
			return false
		}
		code, err = strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(string(b)), "EXIT="))
		return err == nil
	}, p.screen)
	return code
}

func (p *pane) gone() {
	p.e.t.Helper()
	eventually(p.e.t, 60*time.Second, fmt.Sprintf("pid %d to end", p.info.PID), func() bool {
		return syscall.Kill(p.info.PID, 0) != nil
	}, p.screen)
}

func (p *pane) client() string { return clientStatus(p.e.t, p.e.srv.endpoint, p.m.ClientID) }

func (p *pane) published() bool {
	_, err := os.Stat(p.entry.InfoPath())
	return err == nil
}

// cleanlyGone asserts an orderly exit: client disconnected, only the entry's
// own files removed.
func (p *pane) cleanlyGone() {
	p.e.t.Helper()
	if st := p.client(); st != "disconnected" {
		p.e.t.Fatalf("%s: client %s", p.name, st)
	}
	for _, f := range []string{p.entry.InfoPath(), p.entry.SessionPath(), p.entry.LockPath()} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			p.e.t.Fatalf("%s: %s kept: %v", p.name, f, err)
		}
	}
}

func clientStatus(t *testing.T, endpoint, id string) string {
	t.Helper()
	return tuitest.ClientStatus(t, endpoint, id)
}

func eventually(t *testing.T, d time.Duration, what string, ok func() bool, screen func() string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; screen:\n%s", what, screen())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// session opens a running or dead instance's session without its lock, to
// write fabricated evidence for its client.
func (p *pane) session() *litclient.Session {
	p.e.t.Helper()
	s := &litclient.Session{StateDir: p.e.state, HTTP: &http.Client{Timeout: 10 * time.Second}, Context: context.Background(), Mapping: p.m}
	if err := s.Open(litclient.OpenOptions{AllowDisconnected: true}); err != nil {
		p.e.t.Fatal(err)
	}
	return s
}

func TestTUILifecycleInTmux(t *testing.T) {
	t.Run("quit", func(t *testing.T) {
		e := newTUIEnv(t)
		p := e.launch("quit")
		if p.alternate() != "1" || p.client() != "connected" {
			t.Fatalf("running: alt %s client %s", p.alternate(), p.client())
		}
		p.keys("q")
		if code := p.exit(); code != 0 {
			t.Fatalf("exit %d:\n%s", code, p.screen())
		}
		p.cleanlyGone()
		if p.alternate() != "0" {
			t.Fatal("terminal left in the alternate screen")
		}
	})
	t.Run("ctrl-c", func(t *testing.T) {
		e := newTUIEnv(t)
		p := e.launch("ctrlc")
		p.keys("C-c")
		if code := p.exit(); code != 0 {
			t.Fatalf("exit %d:\n%s", code, p.screen())
		}
		p.cleanlyGone()
	})
	t.Run("SIGHUP when the session is killed", func(t *testing.T) {
		e := newTUIEnv(t)
		e.slowDisconnect(time.Second)
		p := e.launch("hup")
		e.tmux("kill-session", "-t", "hup")
		p.gone()
		p.cleanlyGone()
	})
	t.Run("SIGTERM", func(t *testing.T) {
		e := newTUIEnv(t)
		e.slowDisconnect(time.Second)
		p := e.launch("term")
		if err := syscall.Kill(p.info.PID, syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		if code := p.exit(); code != 0 {
			t.Fatalf("exit %d:\n%s", code, p.screen())
		}
		p.cleanlyGone()
		if p.alternate() != "0" {
			t.Fatal("terminal left in the alternate screen")
		}
	})
	t.Run("kill -9 then relaunch cleans the orphan", func(t *testing.T) {
		e := newTUIEnv(t)
		a := e.launch("killed")
		if err := syscall.Kill(a.info.PID, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		if code := a.exit(); code != 128+int(syscall.SIGKILL) {
			t.Fatalf("exit %d", code)
		}
		// Nothing ran: the client stays connected and the terminal is left in
		// the alternate screen (docs/tui.md: run reset).
		if a.client() != "connected" || !a.published() || a.alternate() != "1" {
			t.Fatalf("after kill -9: client %s, entry %v, alt %s", a.client(), a.published(), a.alternate())
		}
		b := e.launch("next")
		b.await("the orphan note", func(s string) bool { return strings.Contains(s, "Cleaned up 1 orphaned lit-tui session") })
		a.cleanlyGone()
		if b.client() != "connected" {
			t.Fatal("the new instance lost its client")
		}
		b.keys("q")
		if code := b.exit(); code != 0 {
			t.Fatalf("exit %d", code)
		}
		b.cleanlyGone()
	})
	t.Run("an orphan with uncertain evidence is not disconnected", func(t *testing.T) {
		e := newTUIEnv(t)
		a := e.launch("uncertain")
		path := tuitest.UncertainAcquire(t, a.session())
		before, _ := os.ReadFile(path)
		if err := syscall.Kill(a.info.PID, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		a.exit()
		id := strings.TrimSuffix(filepath.Base(path), ".json")
		b := e.launch("observer")
		b.await("the uncertain request", func(s string) bool {
			return strings.Contains(s, id) && strings.Contains(s, "not disconnected") && strings.Contains(s, a.entry.ID[:8])
		})
		if a.client() != "connected" || !a.published() {
			t.Fatalf("orphan touched: client %s entry %v", a.client(), a.published())
		}
		if after, err := os.ReadFile(path); err != nil || !bytes.Equal(before, after) {
			t.Fatal("evidence changed or removed")
		}
		b.keys("Escape")
		b.await("the panel to close", func(s string) bool { return !strings.Contains(s, id) })
		b.keys("q")
		if code := b.exit(); code != 0 {
			t.Fatalf("exit %d:\n%s", code, b.screen())
		}
		b.cleanlyGone()
		if a.client() != "connected" || !a.published() {
			t.Fatal("orphan touched at exit")
		}
	})
	t.Run("a second live instance is never disconnected", func(t *testing.T) {
		e := newTUIEnv(t)
		a := e.launch("first")
		b := e.launch("second")
		b.await("the live-instance note", func(s string) bool { return strings.Contains(s, "1 other lit-tui session is running") })
		if a.client() != "connected" || !a.published() {
			t.Fatal("the live instance was touched")
		}
		b.keys("q")
		if code := b.exit(); code != 0 {
			t.Fatalf("exit %d", code)
		}
		b.cleanlyGone()
		if a.client() != "connected" || !a.published() || a.alternate() != "1" {
			t.Fatal("the first instance was affected")
		}
		a.keys("q")
		if code := a.exit(); code != 0 {
			t.Fatalf("exit %d", code)
		}
		a.cleanlyGone()
	})
	t.Run("SIGHUP with an uncertain request still exits", func(t *testing.T) {
		e := newTUIEnv(t)
		a := e.launch("huncertain")
		path := tuitest.UncertainAcquire(t, a.session())
		e.tmux("kill-session", "-t", "huncertain")
		a.gone() // no terminal is left to take a keypress
		if a.client() != "connected" || !a.published() {
			t.Fatalf("client %s entry %v", a.client(), a.published())
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal("evidence removed")
		}
	})
	t.Run("an uncertain exit keeps the entry and needs a second keypress", func(t *testing.T) {
		e := newTUIEnv(t)
		a := e.launch("exit")
		path := tuitest.UncertainAcquire(t, a.session())
		id := strings.TrimSuffix(filepath.Base(path), ".json")
		a.keys("q")
		a.await("the exit report", func(s string) bool { return strings.Contains(s, id) && strings.Contains(s, "Press any key to exit") })
		if syscall.Kill(a.info.PID, 0) != nil {
			t.Fatal("exited without the second keypress")
		}
		a.keys("x")
		if code := a.exit(); code != 4 {
			t.Fatalf("exit %d", code)
		}
		if a.client() != "connected" || !a.published() {
			t.Fatalf("uncertain exit: client %s entry %v", a.client(), a.published())
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal("evidence removed")
		}
	})
}
