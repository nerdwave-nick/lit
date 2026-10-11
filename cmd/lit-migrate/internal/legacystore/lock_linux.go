//go:build linux

// Frozen copy of internal/store at c5af73e; see store.go.

package legacystore

import (
	"os"
	"syscall"
)

func lock(f *os.File) error   { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) }
func unlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
