//go:build linux

package storage

import (
	"os"
	"syscall"
)

// statIdentity reads the device, inode and status change time of fi.
func statIdentity(fi os.FileInfo) fileIdentity {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fileIdentity{}
	}
	return fileIdentity{Device: uint64(st.Dev), Inode: st.Ino, ChangeTime: st.Ctim.Nano()}
}
