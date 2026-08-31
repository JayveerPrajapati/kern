//go:build windows

package cache

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// fileLockImpl is the platform-specific lock handle. On Windows it is an open
// fd with a held LockFileEx region; the lock is released on Unlock or process
// exit.
type fileLockImpl struct {
	f *os.File
}

// acquireFileLock opens (creating if needed) the persistent "<path>.lock"
// sidecar and takes a blocking exclusive byte-range lock on it. The parent
// directory is created on demand (the store creates it later on first save).
func acquireFileLock(path string) (fileLockImpl, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fileLockImpl{}, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fileLockImpl{}, err
	}
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0, 1, 0, ol,
	); err != nil {
		f.Close()
		return fileLockImpl{}, err
	}
	return fileLockImpl{f: f}, nil
}

// release drops the byte-range lock and closes the sidecar.
func (l fileLockImpl) release() {
	if l.f == nil {
		return
	}
	_ = windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, new(windows.Overlapped))
	_ = l.f.Close()
}
