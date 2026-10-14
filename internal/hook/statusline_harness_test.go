package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// statusRig is a rig for status line tests: the home directory is fixed so
// that the recorded cwd (/home/example/project) shortens to ~/project, and the
// terminal width is unknown unless a test says otherwise.
type statusRig struct {
	*rig
	id    string
	calls *tally
}

// tally counts the requests that reach the service.
type tally struct {
	mu    sync.Mutex
	paths []string
}

func (c *tally) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.paths = append(c.paths, r.Method+" "+r.URL.Path)
		c.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

// count is how many requests since the last reset had a path with this prefix.
func (c *tally) count(prefix string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, p := range c.paths {
		if strings.Contains(p, " "+prefix) {
			n++
		}
	}
	return n
}

func (c *tally) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.paths)
}

func (c *tally) reset() {
	c.mu.Lock()
	c.paths = nil
	c.mu.Unlock()
}

func newStatusRig(t *testing.T, wrap func(http.Handler) http.Handler) *statusRig {
	t.Helper()
	calls := &tally{}
	g := newRig(t, func(next http.Handler) http.Handler {
		if wrap != nil {
			next = wrap(next)
		}
		return calls.wrap(next)
	})
	t.Setenv("HOME", "/home/example")
	t.Setenv("USERPROFILE", "/home/example")
	t.Setenv("COLUMNS", "")
	_, id := input(t, "claude", "startup")
	return &statusRig{rig: g, id: id, calls: calls}
}

// start runs the session start hook, which is what gives the runtime its lit
// session.
func (g *statusRig) start() {
	g.t.Helper()
	in, _ := input(g.t, "claude", "startup")
	if r := g.hook("claude", "session-start", in, Options{}); r.Context == "" {
		g.t.Fatalf("no session: %+v", r)
	}
	g.calls.reset()
}

// work selects a project, claims issues and sets the stage as the session.
func (g *statusRig) work(claims int, stage string) {
	g.t.Helper()
	g.run("claude", g.id, "projects", "create", "--project-title", "feat/tracker", "--content", "x")
	project := jsonString(g.tracker("claude", g.id, "projects", "list")[0]["id"])
	set := []string{"session", "set", "--project", project}
	if stage != "" {
		set = append(set, "--stage", stage)
	}
	g.run("claude", g.id, set...)
	var titles []string
	for i := 0; i < claims; i++ {
		title := fmt.Sprintf("work %d", i)
		g.run("claude", g.id, "issues", "create", "--issue", title, "--content", "x")
		titles = append(titles, title)
	}
	if claims > 0 {
		g.run("claude", g.id, append([]string{"claims", "acquire"}, titles...)...)
	}
	g.calls.reset()
}

// body is a recorded status line input; edit changes it before it is sent.
func body(t *testing.T, fixture string, edit func(map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(fixtureBody(t, fixture), &m); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(m)
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func inDir(dir string) func(map[string]any) {
	return func(m map[string]any) {
		m["cwd"] = dir
		m["workspace"].(map[string]any)["current_dir"] = dir
		m["workspace"].(map[string]any)["project_dir"] = dir
	}
}

type status struct {
	Out  string
	Code int
	Err  string
}

// statusline runs `lit hook --host claude statusline` in a terminal cols wide
// (0 when its width is unknown).
func (g *statusRig) statusline(in []byte, cols int, opt Options) status {
	g.t.Helper()
	return statusLineRun(g.t, "claude", in, cols, opt)
}

func statusLineRun(t *testing.T, host string, in []byte, cols int, opt Options) status {
	t.Helper()
	if cols > 0 {
		t.Setenv("COLUMNS", strconv.Itoa(cols))
	} else {
		t.Setenv("COLUMNS", "")
	}
	var out, errOut bytes.Buffer
	code := Run(host, "statusline", bytes.NewReader(in), &out, &errOut, opt)
	return status{out.String(), code, errOut.String()}
}

// line runs the status line and expects a quiet success.
func (g *statusRig) line(in []byte, cols int) string {
	g.t.Helper()
	s := g.statusline(in, cols, Options{})
	if s.Code != 0 || s.Err != "" {
		g.t.Fatalf("a status line must exit 0 quietly: %+v", s)
	}
	return s.Out
}

// age makes the host session's cache entry older.
func (g *statusRig) age(checked, title time.Duration) {
	g.t.Helper()
	path, err := statusCachePath("claude", g.id)
	if err != nil {
		g.t.Fatal(err)
	}
	e, ok := readStatusEntry(path)
	if !ok {
		g.t.Fatal("no cache entry to age")
	}
	e.CheckedAt = e.CheckedAt.Add(-checked)
	if !e.TitleAt.IsZero() {
		e.TitleAt = e.TitleAt.Add(-title)
	}
	if err = writeStatusEntry(path, e); err != nil {
		g.t.Fatal(err)
	}
}

// logError writes an error line to the hook log, at the given time.
func logError(t *testing.T, host, message string, at time.Time) {
	t.Helper()
	l := &Log{Host: host, Event: "session-start", now: func() time.Time { return at }}
	l.Error(message)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
