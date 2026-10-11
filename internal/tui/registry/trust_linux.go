//go:build linux

package registry

import "syscall"

func lockTrust(dir string) (bool, string) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return false, "an unknown file system (" + err.Error() + ")"
	}
	if fs := untrustedMagic(uint32(st.Type)); fs != "" {
		return false, fs
	}
	return true, ""
}

// untrustedMagic names network and user-space file systems by their statfs
// magic number (linux/magic.h and the file systems' own headers).
func untrustedMagic(magic uint32) string {
	switch magic {
	case 0x6969:
		return "nfs"
	case 0x517B:
		return "smb"
	case 0xFE534D42:
		return "smb2"
	case 0xFF534D42:
		return "cifs"
	case 0x65735546:
		return "fuse"
	case 0x01021997:
		return "9p"
	case 0x5346414F:
		return "afs"
	case 0x00C36400:
		return "ceph"
	case 0x01161970:
		return "gfs2"
	case 0x7461636F:
		return "ocfs2"
	}
	return ""
}
