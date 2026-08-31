package cache

// Cross-process advisory file locks for JSON-file stores.
// The stores (TaskStore, ArtifactStore, SnapshotStore) read-modify-write a
// single JSON file that is shared by every kern process working on the same
// project root (kern-mcp server, kern-server, the CLI, and parallel test
// binaries). PathLock serializes writers within one process only; without an
// OS-level lock, two processes interleave their load->modify->save critical
// sections and lose each other's updates. LockFile closes that gap with a
// blocking exclusive flock (Unix) / LockFileEx (Windows) on a persistent
// sidecar file. The sidecar is never deleted, so lock identity stays stable
// across processes and a crashed holder never wedges the store (the OS drops
// the lock when the process exits).
// The lock file lives next to the store file as "<path>.lock".
type FileLock struct {
	f fileLockImpl
}

// LockFile takes a blocking exclusive cross-process lock on path. It blocks
// until the lock is available; the caller must Unlock when the critical
// section ends.
func LockFile(path string) (*FileLock, error) {
	l, err := acquireFileLock(path)
	if err != nil {
		return nil, err
	}
	return &FileLock{f: l}, nil
}

// Unlock releases the lock. Safe to call multiple times; the lock is also
// released automatically when the process exits.
func (l *FileLock) Unlock() {
	if l == nil {
		return
	}
	l.f.release()
	l.f = fileLockImpl{}
}
