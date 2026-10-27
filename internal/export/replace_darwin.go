//go:build darwin

package export

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func lock(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrBusy
	}
	return err
}
func unlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

// exchange swaps the two directories atomically (renamex_np RENAME_SWAP), so
// readers of b see either the old tree or the new one. A file system without
// the swap, such as a non-APFS volume, falls back to two renames, which leave
// b missing for a moment.
func exchange(a, b string) error {
	e := unix.RenamexNp(a, b, unix.RENAME_SWAP)
	if !errors.Is(e, unix.ENOTSUP) && !errors.Is(e, unix.EOPNOTSUPP) && !errors.Is(e, unix.EINVAL) && !errors.Is(e, unix.ENOSYS) {
		return e
	}
	return swapByRenames(a, b)
}
