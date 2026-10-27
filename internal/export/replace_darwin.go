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
func unlock(f *os.File) error    { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
func exchange(a, b string) error { return unix.RenamexNp(a, b, unix.RENAME_SWAP) }
