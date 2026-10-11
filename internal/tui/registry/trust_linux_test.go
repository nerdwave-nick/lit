//go:build linux

package registry

import "testing"

func TestNetworkAndUserSpaceFileSystemsAreUntrusted(t *testing.T) {
	for magic, want := range map[uint32]string{
		0x6969: "nfs", 0x517B: "smb", 0xFE534D42: "smb2", 0xFF534D42: "cifs", 0x65735546: "fuse",
		0x01021997: "9p", 0x5346414F: "afs", 0x00C36400: "ceph", 0x01161970: "gfs2", 0x7461636F: "ocfs2",
		0xEF53: "", 0x01021994: "", 0x58465342: "", 0x9123683E: "",
	} {
		if got := untrustedMagic(magic); got != want {
			t.Errorf("magic %#x: %q, want %q", magic, got, want)
		}
	}
}

func TestLocalTemporaryDirectoryIsTrusted(t *testing.T) {
	if ok, fs := LockTrust(t.TempDir()); !ok {
		t.Skipf("the test temporary directory is on %s", fs)
	}
	if ok, fs := LockTrust("/proc/self/fdinfo/no-such-dir"); ok || fs == "" {
		t.Errorf("an unreadable directory is trusted: %v %q", ok, fs)
	}
}
