package storage

import (
	"errors"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ErrBusy matches (with errors.Is) every error caused by SQLite lock
// contention (SQLITE_BUSY or SQLITE_LOCKED, including extended codes) that
// outlasted the busy timeout. Callers map it to a retryable response instead of
// a storage failure.
var ErrBusy = errors.New("storage is busy")

// BusyError keeps the driver error behind ErrBusy.
type BusyError struct{ Err error }

func (e *BusyError) Error() string        { return "storage is busy: " + e.Err.Error() }
func (e *BusyError) Unwrap() error        { return e.Err }
func (e *BusyError) Is(target error) bool { return target == ErrBusy }

// ErrFatal matches (with errors.Is) every error meaning that the database
// file is damaged or unreadable: SQLITE_CORRUPT, SQLITE_NOTADB or an I/O error
// (SQLITE_IOERR), including extended codes. lit-server exits on it while
// serving, and serves degraded mode when opening meets it.
var ErrFatal = errors.New("database is damaged or unreadable")

// FatalError keeps the driver error behind ErrFatal.
type FatalError struct{ Err error }

func (e *FatalError) Error() string        { return "database is damaged or unreadable: " + e.Err.Error() }
func (e *FatalError) Unwrap() error        { return e.Err }
func (e *FatalError) Is(target error) bool { return target == ErrFatal }

// classify types driver errors; every error leaving this package passes here.
func classify(e error) error {
	var se *sqlite.Error
	if e == nil || !errors.As(e, &se) {
		return e
	}
	switch c := se.Code() & 0xff; c {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return &BusyError{e}
	case sqlite3.SQLITE_CORRUPT, sqlite3.SQLITE_NOTADB, sqlite3.SQLITE_IOERR:
		return &FatalError{e}
	}
	return e
}

// OnFatal registers f, which is called with every ErrFatal error that a read
// snapshot or write transaction of db meets, possibly more than once and from
// several goroutines. f must not block. lit-server registers it after its
// startup checks, and exits on the first call.
func (db *DB) OnFatal(f func(error)) { db.fatal.Store(&f) }

// report classifies e and passes it to the OnFatal function when it is fatal.
func (db *DB) report(e error) error {
	e = classify(e)
	if errors.Is(e, ErrFatal) {
		if f := db.fatal.Load(); f != nil {
			(*f)(e)
		}
	}
	return e
}
