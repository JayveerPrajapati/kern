//go:build !windows

package cache

import (
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/flock"
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
	f, err := flock.Lock(path + ".lock")
	if err != nil {
		return fileLockImpl{}, err
	}
	return fileLockImpl{f: f}, nil
}

// release drops the flock and closes the sidecar.
func (l fileLockImpl) release() {
	if l.f == nil {
		return
	}
	_ = flock.Release(l.f)
}
