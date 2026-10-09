//go:build linux || darwin

package litclient

import (
	"os"
	"syscall"
)

func localLock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) }
func localUnlock(f *os.File)     { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
func syncDirectory(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
