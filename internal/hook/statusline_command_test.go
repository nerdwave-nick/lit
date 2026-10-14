package hook

import (
	"bytes"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// litStatusline runs the built lit executable as Claude Code runs a status line
// command: input on stdin, the terminal width in COLUMNS.
func litStatusline(t *testing.T, g *statusRig, stdin []byte, cols int, args ...string) (string, string, int, time.Duration) {
	t.Helper()
	cmd := exec.Command(g.binary, append([]string{"hook", "--host", "claude", "statusline"}, args...)...)
	cmd.Dir = t.TempDir()
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = append(cmd.Environ(), "COLUMNS="+strconv.Itoa(cols))
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	start := time.Now()
	code := 0
	if e := cmd.Run(); e != nil {
		var exit *exec.ExitError
		if !errors.As(e, &exit) {
			t.Fatal(e)
		}
		code = exit.ExitCode()
	}
	return out.String(), errOut.String(), code, time.Since(start)
}

func TestLitHookStatuslineEndToEnd(t *testing.T) {
	g := newStatusRig(t, nil)
	g.start()
	g.work(2, "impl")
	in := body(t, "statusline-session.json", nil)

	out, errOut, code, cold := litStatusline(t, g, in, 200)
	if out != wideSession || errOut != "" || code != 0 {
		t.Fatalf("the built command:\n got %q (%q, exit %d)\nwant %q", out, errOut, code, wideSession)
	}
	g.calls.reset()
	out, _, _, warm := litStatusline(t, g, in, 200)
	if out != wideSession || g.calls.total() != 0 {
		t.Fatalf("a second status line is answered from the cache: %q after %d requests", out, g.calls.total())
	}
	t.Logf("status line of the built binary: %s with a cold cache, %s from the cache", cold, warm)
	if warm > 1500*time.Millisecond {
		t.Errorf("the status line took %s from the cache", warm)
	}

	original := script(t, `printf 'wrapped'`)
	if out, _, code, _ = litStatusline(t, g, in, 200, "--original", original); out != "wrapped | "+litAlone || code != 0 {
		t.Fatalf("--original: %q exit %d", out, code)
	}
	if out, _, code, _ = litStatusline(t, g, in, 200, "--original="+original); out != "wrapped | "+litAlone || code != 0 {
		t.Fatalf("--original=COMMAND: %q exit %d", out, code)
	}
	if out, errOut, code, _ = litStatusline(t, g, in, 200, "--original"); code != 2 || out != "" || strings.TrimSpace(errOut) == "" {
		t.Fatalf("--original needs a command: %q %q exit %d", out, errOut, code)
	}
	noFailures(t)
}
