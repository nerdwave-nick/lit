package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/hook"
)

// An installed hook that names an event this lit has no command for must not
// fail with a usage error: for a Stop hook the exit status 2 would block the
// agent from stopping. It is a logged no-op; every other invalid invocation
// still exits 2.
func TestHookEventsThisLitDoesNotKnowAreANoOp(t *testing.T) {
	offlineCompletion(t)
	t.Setenv("LIT_WORKFLOW_STATE_DIR", t.TempDir())
	for _, host := range []string{"claude", "codex"} {
		for _, argv := range [][]string{
			{"hook", "--host", host, "from-the-future"},
			{"hook", "--host=" + host, "from-the-future"},
			{"hook", "from-the-future", "--host", host},
		} {
			var out, errOut bytes.Buffer
			if code := Run(argv, &out, &errOut); code != 0 || out.Len() != 0 || errOut.Len() != 0 {
				t.Errorf("%v: %d out=%q err=%q", argv, code, &out, &errOut)
			}
		}
	}
	failures, err := hook.RecentFailures(timeHourAgo())
	if err != nil || len(failures) != 6 || !strings.Contains(failures[0].Message, "from-the-future") {
		t.Fatalf("every unknown event is logged: %+v %v", failures, err)
	}
}

func TestOtherInvalidHookInvocationsStillExitTwo(t *testing.T) {
	offlineCompletion(t)
	t.Setenv("LIT_WORKFLOW_STATE_DIR", t.TempDir())
	for _, argv := range [][]string{
		{"hook", "from-the-future"},
		{"hook", "--host", "gemini", "from-the-future"},
		{"hook", "--host", "claude"},
		{"hook", "--host", "claude", "stop", "--unknown-flag"},
	} {
		var out, errOut bytes.Buffer
		if code := Run(argv, &out, &errOut); code != 2 || errOut.Len() == 0 {
			t.Errorf("%v: %d out=%q err=%q", argv, code, &out, &errOut)
		}
	}
	if failures, _ := hook.RecentFailures(timeHourAgo()); len(failures) != 0 {
		t.Fatalf("an argument error is not a hook failure: %+v", failures)
	}
}

func timeHourAgo() time.Time { return time.Now().Add(-time.Hour) }
