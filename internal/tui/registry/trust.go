package registry

// LockTrust reports whether advisory locks in dir can be trusted to show that
// an instance is alive, and names the file system when they cannot. Locks on
// network and user-space file systems (NFS, SMB/CIFS, AFS, 9P, Ceph, FUSE such
// as sshfs, …) may not reach other machines or may be emulated, so a free
// lock there does not prove an instance has gone.
func LockTrust(dir string) (trusted bool, fs string) { return lockTrust(dir) }
