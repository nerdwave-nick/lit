package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// periodic is a job lit-server runs at an interval while it serves, such as
// the scheduled backup. Each job runs in its own goroutine, off the request
// path, one run at a time: the next run starts every after the previous one
// ended. A failed or panicking run is logged and the job carries on, so a job
// never affects request serving.
type periodic struct {
	name  string        // for log lines, such as "scheduled backup"
	first time.Duration // delay before the first run
	every time.Duration // delay from the end of one run to the next
	run   func(context.Context) error
}

// start runs the job until stop is called. stop cancels a run in progress
// through its context and waits until the job's goroutine has ended, so
// lit-server calls it before closing the database. Calling stop again is
// harmless. Failures go to logf; a run ended by stop is not one.
func (p periodic) start(logf func(format string, args ...any)) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTimer(p.first)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if e := p.once(ctx); e != nil && ctx.Err() == nil {
				logf("lit-server: %s failed: %v; serving continues, next attempt in %s", p.name, e, p.every)
			}
			t.Reset(p.every)
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}

// once runs the job a single time, turning a panic into an error.
func (p periodic) once(ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return p.run(ctx)
}
