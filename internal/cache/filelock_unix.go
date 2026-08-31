//go:build !windows

package cache

import (
	"os"
	"path/filepath"
	"syscall"
)

// fileLockImpl is the platform-specific lock handle. On Unix it is an open
// fd with a held flock; the lock is released on Unlock or process exit.
type fileLockImpl struct {
	f *os.File
}

// acquireFileLock opens (creating if needed) the persistent "<path>.lock"
// sidecar and takes a blocking exclusive flock on it. The parent directory is
// created on demand: the store's own saveLocked creates it later, so the lock
// must not assume it exists yet (fresh cache dir, first save).
func acquireFileLock(path string) (fileLockImpl, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fileLockImpl{}, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fileLockImpl{}, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return fileLockImpl{}, err
	}
	return fileLockImpl{f: f}, nil
}

// release drops the flock and closes the sidecar.
func (l fileLockImpl) release() {
	if l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
}
