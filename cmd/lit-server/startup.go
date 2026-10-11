package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/service"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
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

// startup opens the data directory and runs the startup checks
// (storage.DB.CheckIntegrity), logging to errOut. A damaged database, whether
// it cannot be opened (storage.ErrFatal) or fails a check
// (storage.ErrIntegrity), is served in degraded mode: lit-server stays up and
// answers 503 recovery_required instead of exiting into a restart loop. Other
// failures, such as an owned or legacy data directory, are errors that stop
// lit-server.
func startup(c service.Config, errOut io.Writer) (started, error) {
	logf := func(format string, args ...any) { fmt.Fprintf(errOut, format+"\n", args...) }
	db, e := storage.OpenWith(c.DataDir, storage.Options{ReadConcurrency: c.ReadConcurrency, BackupDir: c.BackupDir})
	if errors.Is(e, storage.ErrFatal) {
		return degraded(nil, e, errOut), nil
	}
	if e != nil {
		return started{}, e
	}
	e = db.CheckIntegrity(context.Background(), logf)
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
