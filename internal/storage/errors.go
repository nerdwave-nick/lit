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

// classify types driver errors; every error leaving this package passes here.
func classify(e error) error {
	var se *sqlite.Error
	if e == nil || !errors.As(e, &se) {
		return e
	}
	if c := se.Code() & 0xff; c == sqlite3.SQLITE_BUSY || c == sqlite3.SQLITE_LOCKED {
		return &BusyError{e}
	}
	return e
}
