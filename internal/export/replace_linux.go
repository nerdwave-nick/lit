package export

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func lock(f *os.File) error {
	e := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(e, syscall.EWOULDBLOCK) {
		return ErrBusy
	}
	return e
}

func unlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

// exchange swaps the two directories atomically (renameat2 RENAME_EXCHANGE),
// so readers of b see either the old tree or the new one. A file system
// without the exchange falls back to two renames, which leave b missing for
// a moment.
func exchange(a, b string) error {
	e := unix.Renameat2(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_EXCHANGE)
	if !errors.Is(e, unix.EINVAL) && !errors.Is(e, unix.ENOSYS) && !errors.Is(e, unix.EOPNOTSUPP) {
		return e
	}
	return swapByRenames(a, b)
}
