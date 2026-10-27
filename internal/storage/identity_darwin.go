//go:build darwin

package storage

import (
	"os"
	"syscall"
)

// statIdentity preserves the same replacement detection used on Linux.
func statIdentity(fi os.FileInfo) fileIdentity {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fileIdentity{}
	}
	return fileIdentity{Device: uint64(st.Dev), Inode: st.Ino, ChangeTime: st.Ctimespec.Nano()}
}
