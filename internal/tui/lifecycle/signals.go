package lifecycle

import (
	"os"
	"os/signal"
	"syscall"
)

// HandleSignals routes SIGINT, SIGTERM and SIGHUP to the coordinator: the
// first starts the orderly shutdown, a second forces an exit. Bubble Tea's own
// handler must be disabled (tea.WithoutSignalHandler). stop ends the routing.
func (l *Lifecycle) HandleSignals() (stop func()) {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-ch:
				l.Signal(s)
			case <-done:
				return
			}
		}
	}()
	return func() { signal.Stop(ch); close(done) }
}

// Signal handles one received signal: the first starts the orderly shutdown,
// a second forces an exit with status 128+signal.
func (l *Lifecycle) Signal(s os.Signal) {
	code := 1
	if n, ok := s.(syscall.Signal); ok {
		code = 128 + int(n)
	}
	if !l.Shutdown(s.String()) {
		l.Force(code)
	}
}
