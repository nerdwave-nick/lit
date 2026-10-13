package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/nerdwave-nick/lit/internal/service"
	"github.com/nerdwave-nick/lit/internal/storage"
)

// started is what lit-server serves after its startup checks.
type started struct {
	db      *storage.DB // nil when the database could not be opened
	handler http.Handler
	// fatal delivers the first database failure met while serving
	// (storage.ErrFatal); lit-server then exits for its supervisor to
	// restart it. It is nil in degraded mode.
	fatal <-chan error
}

// startup opens the data directory and, after an unclean shutdown, runs the
// full integrity scan (storage.DB.CheckIntegrity), logging to errOut which
// path it took. A clean shutdown (storage.CleanMarker, consumed by the open
// before anything is served) and a database the open just created skip the
// scan. A damaged database, whether it cannot be opened (storage.ErrFatal) or
// fails the scan (storage.ErrIntegrity), is served in degraded mode:
// lit-server stays up and answers 503 recovery_required instead of exiting
// into a restart loop. Other failures, such as an owned or legacy data
// directory, are errors that stop lit-server.
func startup(c service.Config, errOut io.Writer) (started, error) {
	logf := func(format string, args ...any) { fmt.Fprintf(errOut, format+"\n", args...) }
	db, e := storage.OpenWith(c.DataDir, storage.Options{ReadConcurrency: c.ReadConcurrency, BackupDir: c.BackupDir})
	if errors.Is(e, storage.ErrFatal) {
		return degraded(nil, e, errOut), nil
	}
	if e != nil {
		return started{}, e
	}
	switch prior := db.PriorShutdown(); prior {
	case storage.ShutdownClean, storage.ShutdownNone:
		logf("lit-server: %s: full integrity scan skipped", prior)
	default:
		logf("lit-server: %s: running full integrity scan", prior)
		e = db.CheckIntegrity(context.Background(), logf)
	}
	if errors.Is(e, storage.ErrIntegrity) || errors.Is(e, storage.ErrFatal) {
		return degraded(db, e, errOut), nil
	}
	if e != nil {
		db.Close()
		return started{}, e
	}
	h, e := service.New(db, c)
	if e != nil {
		db.Close()
		return started{}, e
	}
	fatal := make(chan error, 1)
	db.OnFatal(func(e error) {
		select {
		case fatal <- e:
		default:
		}
	})
	return started{db, h, fatal}, nil
}

// degraded serves degraded mode for the startup failure e. An opened database
// stays open, so its owner lock keeps a second lit-server away.
func degraded(db *storage.DB, e error, errOut io.Writer) started {
	fmt.Fprintln(errOut, "lit-server: the startup check failed; serving degraded mode (503 recovery_required) until an operator restores a backup or repairs the database:", e)
	return started{db: db, handler: service.Degraded(e.Error())}
}
