//go:build darwin

package registry

import "syscall"

func lockTrust(dir string) (bool, string) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return false, "an unknown file system (" + err.Error() + ")"
	}
	b := make([]byte, 0, len(st.Fstypename))
	for _, c := range st.Fstypename {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	switch name := string(b); name {
	case "nfs", "smbfs", "afpfs", "webdav", "cifs", "osxfuse", "macfuse", "fusefs", "ftp":
		return false, name
	}
	return true, ""
}
