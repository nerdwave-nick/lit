package hook

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// litHook runs the built lit executable as a host would.
func litHook(t *testing.T, g *rig, stdin []byte, args ...string) reply {
	t.Helper()
	cmd := exec.Command(g.binary, args...)
	cmd.Dir = t.TempDir()
	cmd.Stdin = bytes.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	code := 0
	if e := cmd.Run(); e != nil {
		var exit *exec.ExitError
		if !errors.As(e, &exit) {
			t.Fatal(e)
		}
		code = exit.ExitCode()
	}
	return decodeReply(t, code, out.String(), errOut.String())
}

func TestLitHookCommandEndToEnd(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			in, id := input(t, host, "startup")
			r := litHook(t, g, in, "hook", "--host", host, "session-start")
			a, e := g.association(host, id)
			if e != nil || a.Status != "ready" {
				t.Fatalf("%+v %v", a, e)
			}
			wantContext(t, r, "session: "+a.Session, "endpoint: "+g.endpoint, "runtime ID: "+id)
			again := litHook(t, g, in, "hook", "--host", host, "session-start")
			wantContext(t, again, "session: "+a.Session)
			if g.connects.Load() != 1 {
				t.Fatalf("registered %d clients", g.connects.Load())
			}
		})
	}
}

func TestLitHookCommandRejectsBadInvocations(t *testing.T) {
	g := newRig(t, nil)
	in, _ := input(t, "claude", "startup")
	for name, args := range map[string][]string{
		"no host":       {"hook", "session-start"},
		"unknown host":  {"hook", "--host", "gemini", "session-start"},
		"unknown event": {"hook", "--host", "claude", "nonsense"},
		"no event":      {"hook", "--host", "claude"},
	} {
		t.Run(name, func(t *testing.T) {
			r := litHook(t, g, in, args...)
			if r.Code != 2 || r.Stdout != "" || strings.TrimSpace(r.Stderr) == "" {
				t.Fatalf("%+v", r)
			}
		})
	}
	if g.connects.Load() != 0 {
		t.Fatal("a rejected invocation registered a client")
	}
}
