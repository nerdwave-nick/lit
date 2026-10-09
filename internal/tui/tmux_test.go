package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/detail"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/prefs"
)

// demoEnv names a Markdown file. When it is set, the test binary runs
// lit-tui's root model showing that file instead of running tests, so tmux
// tests drive the real program in a real terminal. The endpoint is unused:
// nothing connects to a service.
const demoEnv = "LIT_TUI_TEST_DEMO"

func TestMain(m *testing.M) {
	if path := os.Getenv(demoEnv); path != "" {
		os.Exit(demo(path))
	}
	os.Exit(m.Run())
}

func demo(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	p, err := prefs.Load(prefs.Flags{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	m := NewModel("http://127.0.0.1:1").WithPrefs(p).WithDocument(detail.Section{RecordID: "demo", Revision: 1, Content: string(b)})
	if err := Run(context.Background(), m); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// pane is the demo running in a private tmux server.
type pane struct {
	t      *testing.T
	socket string
}

// startDemo runs the demo for content in a w×h tmux window with env (on
// top of an empty environment).
func startDemo(t *testing.T, content string, w, h int, env ...string) *pane {
	t.Helper()
	if testing.Short() {
		t.Skip("tmux tests are skipped with -short")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	dir := t.TempDir()
	doc := filepath.Join(dir, "demo.md")
	if err := os.WriteFile(doc, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := &pane{t: t, socket: fmt.Sprintf("lit-tui-test-%d-%s", os.Getpid(), strings.ReplaceAll(t.Name(), "/", "-"))}
	args := []string{"env", "-i", "HOME=" + dir, "XDG_CONFIG_HOME=" + filepath.Join(dir, "config"), "PATH=" + os.Getenv("PATH"),
		"TERM=xterm-256color", demoEnv + "=" + doc}
	args = append(append(args, env...), self)
	p.tmux("new-session", "-d", "-s", "demo", "-x", strconv.Itoa(w), "-y", strconv.Itoa(h), strings.Join(quote(args), " "))
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", p.socket, "kill-server").Run() })
	return p
}

func quote(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return out
}

func (p *pane) tmux(args ...string) string {
	p.t.Helper()
	out, err := exec.Command("tmux", append([]string{"-L", p.socket, "-f", "/dev/null"}, args...)...).CombinedOutput()
	if err != nil {
		p.t.Fatalf("tmux %v: %v: %s", args, err, out)
	}
	return string(out)
}

func (p *pane) lines() []string {
	return strings.Split(p.tmux("capture-pane", "-p", "-t", "demo"), "\n")
}

func (p *pane) keys(keys ...string) { p.tmux(append([]string{"send-keys", "-t", "demo"}, keys...)...) }

// await polls the screen until ok holds, for at most ten seconds.
func (p *pane) await(what string, ok func([]string) bool) []string {
	p.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		ls := p.lines()
		if ok(ls) {
			return ls
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("timed out waiting for %s; screen:\n%s", what, strings.Join(ls, "\n"))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func lineWith(ls []string, s string) (int, bool) {
	for i, l := range ls {
		if strings.Contains(l, s) {
			return i, true
		}
	}
	return -1, false
}

func tmuxSections(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "## Section %d\n\nParagraph%d alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega and more words.\n\n```go\nfunc section%d() {}\n```\n\n", i, i, i)
	}
	return b.String()
}

// Resizing mid-scroll keeps the same content at the top of the detail.
func TestTmuxResizeMidScrollKeepsTheAnchor(t *testing.T) {
	p := startDemo(t, tmuxSections(40), 100, 30)
	p.await("the first render", func(ls []string) bool { _, ok := lineWith(ls, "Section 0"); return ok })
	// One line at a time, each press awaited, so slow (-race) runs do not
	// overshoot.
	for i := 0; ; i++ {
		screen := strings.Join(p.lines(), "\n")
		if strings.Contains(p.lines()[1], "Section 12") {
			break
		}
		if i > 400 {
			t.Fatalf("never scrolled to Section 12:\n%s", screen)
		}
		p.keys("j")
		p.await("the view to scroll", func(ls []string) bool { return strings.Join(ls, "\n") != screen })
	}
	for _, tc := range []struct {
		w     int
		kappa bool // the paragraph's first line reaches "kappa" only when wide
	}{{60, false}, {140, true}, {100, true}} {
		p.tmux("resize-window", "-t", "demo", "-x", strconv.Itoa(tc.w), "-y", "30")
		p.await(fmt.Sprintf("Section 12 on top at width %d", tc.w), func(ls []string) bool {
			i, ok := lineWith(ls, "Paragraph12")
			return strings.Contains(ls[1], "Section 12") && ok && strings.Contains(ls[i], "kappa") == tc.kappa
		})
	}
}

const tmuxCodeA = "A000.001.002.003.004.005.006.007.008.009.010.011.012.013.014.015.016.017.018.019.020.021.022.023.024.025.026.027.028.029.030.031.032.033.034.035.036.037.038.039"
const tmuxCodeB = "Babcdefghij-abcdefghij-abcdefghij-abcdefghij-abcdefghij-abcdefghij-abcdefghij-abcdefghij-abcdefghij-abcdefghij-end"

var tmuxWide = "# Horizontal scroll\n\nProse above the blocks, long enough to wrap at eighty columns, which must never move sideways at all.\n\n" +
	"```text\n" + tmuxCodeA + "\nshort\n```\n\nProse between.\n\n```text\n" + tmuxCodeB + "\n```\n"

// The focused code block scrolls on its own; prose and the other block stay.
func TestTmuxHorizontalScrollMovesOnlyTheFocusedBlock(t *testing.T) {
	p := startDemo(t, tmuxWide, 80, 24)
	before := p.await("the code blocks", func(ls []string) bool {
		_, a := lineWith(ls, tmuxCodeA[:20])
		_, b := lineWith(ls, tmuxCodeB[:20])
		return a && b
	})
	a, _ := lineWith(before, tmuxCodeA[:20])
	b, _ := lineWith(before, tmuxCodeB[:20])
	if !strings.HasPrefix(before[a], "▌") || !strings.HasSuffix(before[a], "›") || strings.HasPrefix(before[b], "▌") {
		t.Fatalf("markers before scrolling:\n%s", strings.Join(before, "\n"))
	}
	// After three presses view column 2 shows the line's column 2+3*ScrollStep;
	// the code starts at the line's column start.
	start := len([]rune(before[a][:strings.Index(before[a], "A")]))
	want := "▌‹" + tmuxCodeA[2+3*detail.ScrollStep-start:][:20]
	p.keys("l", "l", "l")
	after := p.await("the first block to scroll by three steps", func(ls []string) bool { return strings.HasPrefix(ls[a], want) })
	for i := range before {
		if i != a && i != a+1 && after[i] != before[i] {
			t.Errorf("line %d moved:\n%q\n%q", i, before[i], after[i])
		}
	}
	p.keys("Tab", "l")
	next := p.await("the second block to scroll", func(ls []string) bool { return strings.HasPrefix(ls[b], "▌‹") })
	if next[a] != " "+after[a][len("▌"):] {
		t.Errorf("the first block moved with the second:\n%q\n%q", after[a], next[a])
	}
}

// Without styling the focused block is marked with > and clipped edges with $.
func TestTmuxPlainTerminalsUseTextMarkers(t *testing.T) {
	for _, env := range [][]string{{"TERM=dumb"}, {"NO_COLOR=1"}} {
		t.Run(env[0], func(t *testing.T) {
			p := startDemo(t, tmuxWide, 80, 24, env...)
			ls := p.await("the code blocks", func(ls []string) bool { _, ok := lineWith(ls, tmuxCodeA[:20]); return ok })
			a, _ := lineWith(ls, tmuxCodeA[:20])
			if !strings.HasPrefix(ls[a], "> ") || !strings.HasSuffix(ls[a], "$") {
				t.Errorf("plain markers: %q", ls[a])
			}
		})
	}
}
