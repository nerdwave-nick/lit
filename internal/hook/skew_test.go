package hook

import (
	"bytes"
	"strings"
	"testing"
)

// A hook entry installed by a newer lit can name an event this lit does not
// know. For Claude's and Codex's Stop hook an exit status of 2 blocks the stop,
// so a stale install must not fail: the event is a quiet no-op that is logged.
func TestAnEventThisLitDoesNotKnowIsALoggedNoOp(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			g := newRig(t, nil)
			in, _ := stopInput(t, host, false)
			var out, errOut bytes.Buffer
			code := Run(host, "from-the-future", bytes.NewReader(in), &out, &errOut, Options{Binary: g.binary})
			if code != 0 || out.Len() != 0 || errOut.Len() != 0 {
				t.Fatalf("an unknown event must do nothing quietly: %d %q %q", code, &out, &errOut)
			}
			got := failures(t)
			if len(got) != 1 || got[0].Host != host || got[0].Event != "from-the-future" || !strings.Contains(got[0].Message, "update lit") {
				t.Fatalf("an unknown event is a logged failure that says what to do: %+v", got)
			}
			if g.connects.Load() != 0 {
				t.Fatal("an unknown event registered a client")
			}
		})
	}
}

func TestAnUnknownHostIsStillAnArgumentError(t *testing.T) {
	g := newRig(t, nil)
	var out, errOut bytes.Buffer
	if code := Run("gemini", "stop", bytes.NewReader(nil), &out, &errOut, Options{Binary: g.binary}); code != 2 || out.Len() != 0 || errOut.Len() == 0 {
		t.Fatalf("%d %q %q", code, &out, &errOut)
	}
	noFailures(t)
}

func TestLitHookCommandIgnoresAnEventItDoesNotKnow(t *testing.T) {
	g := newRig(t, nil)
	in, _ := stopInput(t, "claude", false)
	code, out, errOut := execLit(t, g, in, "hook", "--host", "claude", "from-the-future")
	if code != 0 || out != "" || errOut != "" {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	if got := failures(t); len(got) != 1 || got[0].Event != "from-the-future" {
		t.Fatalf("%+v", got)
	}
	if code, _, errOut = execLit(t, g, in, "hook", "--host", "gemini", "from-the-future"); code != 2 || errOut == "" {
		t.Fatalf("%d %q", code, errOut)
	}
}
