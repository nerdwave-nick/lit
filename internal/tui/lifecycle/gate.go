package lifecycle

import (
	"context"
	"sync"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// gate serializes every use of the session and admits nothing once draining.
type gate struct {
	sem      chan struct{} // holds one token while the session is in use
	mu       sync.Mutex
	draining bool
	inflight sync.WaitGroup
}

func (g *gate) init() { g.sem = make(chan struct{}, 1) }

// do runs fn alone, unless draining has started.
func (g *gate) do(ctx context.Context, fn func(ctx context.Context) error) error {
	g.mu.Lock()
	if g.draining {
		g.mu.Unlock()
		return ErrShuttingDown
	}
	g.inflight.Add(1)
	g.mu.Unlock()
	defer g.inflight.Done()
	select {
	case g.sem <- struct{}{}:
	case <-ctx.Done():
		return protocol.E(503, "timeout", "lit-tui is busy: "+ctx.Err().Error())
	}
	defer func() { <-g.sem }()
	return fn(ctx)
}

// close refuses new work from now on.
func (g *gate) close() {
	g.mu.Lock()
	g.draining = true
	g.mu.Unlock()
}

// drain refuses new work, waits up to timeout for admitted work to finish and
// then keeps the session for the caller. It reports false, without taking
// the session, when the deadline passed first.
func (g *gate) drain(timeout time.Duration) bool {
	g.close()
	idle := make(chan struct{})
	go func() {
		g.inflight.Wait()
		close(idle)
	}()
	select {
	case <-idle:
	case <-time.After(timeout):
		return false
	}
	g.sem <- struct{}{} // never released: the session is shutdown's now
	return true
}
