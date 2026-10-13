package main

import (
	"context"
	"fmt"
	"github.com/nerdwave-nick/lit/internal/service"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main()    { os.Exit(run()) }
func run() int { return runCLI(os.Args[1:], os.Stdout, os.Stderr) }
func runService(c service.Config, errOut io.Writer) int {
	s, e := startup(c, errOut)
	if e != nil {
		fmt.Fprintln(errOut, e)
		return 1
	}
	if s.db != nil {
		defer s.db.Close()
	}
	stop := func() {
		if h, ok := s.handler.(interface{ Stop() }); ok {
			h.Stop()
		}
	}
	ln, activated, e := listen(c.Listen)
	if e != nil {
		fmt.Fprintln(errOut, "cannot listen:", e)
		return 1
	}
	srv := &http.Server{Handler: s.handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	if activated {
		fmt.Fprintln(errOut, "lit-server listening", ln.Addr(), "(socket-activated)")
	} else {
		fmt.Fprintln(errOut, "lit-server listening", ln.Addr())
	}
	// Periodic jobs end before the deferred db.Close.
	stopJobs := startJobs(s, c, func(format string, args ...any) { fmt.Fprintf(errOut, format+"\n", args...) })
	defer stopJobs()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	select {
	case <-sig:
	case e = <-done:
		if e != http.ErrServerClosed {
			fmt.Fprintln(errOut, e)
			return 1
		}
	case e = <-s.fatal:
		// Requests in flight lose their connection; clients reconcile
		// interrupted mutations as after any crash.
		fmt.Fprintln(errOut, "lit-server: database failure while serving; exiting so that the supervisor restarts lit-server and its startup check runs:", e)
		stopJobs()
		stop()
		srv.Close()
		return 1
	}
	fmt.Fprintln(errOut, "lit-server shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel() // Stop the listener immediately; existing handlers drain before store ownership ends.
	stopJobs()
	stop()
	if e = srv.Shutdown(ctx); e != nil {
		fmt.Fprintln(errOut, "shutdown deadline reached; restart will recover committed work")
		os.Exit(1)
	}
	stop()
	// Only a healthy database that closes without a failure, after the jobs
	// stopped and the requests drained, is left clean; degraded mode, a
	// database failure, the deadline exit above and every error return leave
	// the next start to run the full integrity scan.
	if s.fatal != nil {
		if e = s.db.CloseClean(); e != nil {
			fmt.Fprintf(errOut, "lit-server: %v; the next start runs the full integrity scan\n", e)
		}
	}
	fmt.Fprintln(errOut, "lit-server stopped")
	return 0
}
