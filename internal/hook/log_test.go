package hook

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/workflowsession"
)

func TestRecentFailuresListOnlyErrorsInTheWindow(t *testing.T) {
	t.Setenv("LIT_WORKFLOW_STATE_DIR", t.TempDir())
	clock := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	log := func(host, event, runtime string) *Log {
		return &Log{Host: host, Event: event, Runtime: runtime, now: func() time.Time { return clock }}
	}
	if failures, e := RecentFailures(clock.Add(-time.Hour)); e != nil || len(failures) != 0 {
		t.Fatalf("without a log: %+v %v", failures, e)
	}
	log("claude", "session-start", "old").Error("too old")
	clock = clock.Add(30 * time.Minute)
	log("claude", "session-start", "warned").Warn("not a failure")
	log("codex", "session-start", "recent").Error("line one\nline two")
	failures, e := RecentFailures(clock.Add(-10 * time.Minute))
	if e != nil || len(failures) != 1 {
		t.Fatalf("%+v %v", failures, e)
	}
	f := failures[0]
	if f.Host != "codex" || f.Event != "session-start" || f.RuntimeID != "recent" || f.Message != "line one line two" || !f.Time.Equal(clock) {
		t.Fatalf("%+v", f)
	}
	dir, _ := workflowsession.StateDir()
	raw, _ := os.ReadFile(filepath.Join(dir, "hooks", "hook.log"))
	if lines := strings.Count(string(raw), "\n"); lines != 3 {
		t.Fatalf("one JSON line per entry, got %d lines:\n%s", lines, raw)
	}
}

func TestTheLogRotatesWithoutLosingRecentFailures(t *testing.T) {
	t.Setenv("LIT_WORKFLOW_STATE_DIR", t.TempDir())
	now := time.Now()
	l := &Log{Host: "claude", Event: "session-start", Runtime: "r", now: func() time.Time { return now }, limit: 2048}
	const written = 40
	for i := 0; i < written; i++ {
		l.Error(fmt.Sprintf("failure %02d %s", i, strings.Repeat("x", 100)))
	}
	dir, _ := workflowsession.StateDir()
	if _, e := os.Stat(filepath.Join(dir, "hooks", "hook.log.1")); e != nil {
		t.Fatalf("no rotation: %v", e)
	}
	failures, e := RecentFailures(now.Add(-time.Minute))
	if e != nil || len(failures) < 10 || len(failures) == written {
		t.Fatalf("rotation keeps two files, not all and not one: %d %v", len(failures), e)
	}
	for i, f := range failures {
		if want := fmt.Sprintf("failure %02d ", written-len(failures)+i); !strings.HasPrefix(f.Message, want) {
			t.Fatalf("failure %d is %q, want a contiguous run ending at the newest", i, f.Message[:10])
		}
	}
}

func TestAnUnwritableLogFallsBackToStderr(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "file")
	if e := os.WriteFile(blocked, nil, 0o600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("LIT_WORKFLOW_STATE_DIR", filepath.Join(blocked, "state"))
	var fallback strings.Builder
	l := &Log{Host: "claude", Event: "session-start", fallback: &fallback, now: time.Now}
	l.Error("cannot be stored")
	if !strings.Contains(fallback.String(), "cannot be stored") {
		t.Fatalf("lost the error: %q", fallback.String())
	}
}
