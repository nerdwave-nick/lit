package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// lines collects log lines from concurrent jobs.
type lines struct {
	mu  sync.Mutex
	all []string
}

func (l *lines) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.all = append(l.all, fmt.Sprintf(format, args...))
}

func (l *lines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.all, "\n")
}

// waitFor polls cond every 10ms until it holds, failing after 10s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A job runs again after a failed or panicking run; each failure is logged,
// and after stop it never runs again.
func TestPeriodicJobSurvivesFailures(t *testing.T) {
	var runs atomic.Int32
	var log lines
	stop := periodic{name: "test job", every: time.Millisecond, run: func(context.Context) error {
		switch runs.Add(1) {
		case 1:
			return errors.New("disk full")
		case 2:
			panic("broken")
		}
		return nil
	}}.start(log.logf)
	waitFor(t, "four runs", func() bool { return runs.Load() >= 4 })
	stop()
	after := runs.Load()
	time.Sleep(20 * time.Millisecond)
	if runs.Load() != after {
		t.Fatalf("ran after stop: %d then %d", after, runs.Load())
	}
	out := log.String()
	if !strings.Contains(out, "test job failed: disk full") || !strings.Contains(out, "test job failed: panic: broken") {
		t.Fatal(out)
	}
	stop() // a second stop is harmless
}

// Stop cancels a run in progress and waits for it to end, without reporting
// the cancellation as a failure.
func TestPeriodicStopCancelsTheRunInProgress(t *testing.T) {
	started, ended := make(chan struct{}), make(chan error, 1)
	var log lines
	stop := periodic{name: "slow job", every: time.Hour, run: func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		ended <- ctx.Err()
		return ctx.Err()
	}}.start(log.logf)
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the job never ran")
	}
	stop()
	select {
	case e := <-ended:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	default:
		t.Fatal("stop returned before the run ended")
	}
	if out := log.String(); strings.Contains(out, "failed") {
		t.Fatal(out)
	}
}

// The first run waits for the first delay; stopping before it runs nothing.
func TestPeriodicWaitsForTheFirstDelay(t *testing.T) {
	var runs atomic.Int32
	var log lines
	stop := periodic{name: "late job", first: time.Hour, every: time.Millisecond, run: func(context.Context) error {
		runs.Add(1)
		return nil
	}}.start(log.logf)
	time.Sleep(20 * time.Millisecond)
	stop()
	if runs.Load() != 0 {
		t.Fatalf("ran %d times before its first delay", runs.Load())
	}
}
