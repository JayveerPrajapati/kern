//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package flock

import (
	"fmt"
	"os"
	"syscall"
)

// Lock opens (creating if needed) the lock file at path and takes a blocking
// exclusive advisory flock on it. The returned *os.File holds the lock until
// Release is called (or the process exits — the kernel drops the flock when
// the fd closes). The lock file is never deleted, so lock identity stays
// stable across processes.
func Lock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("flock %s: %w", path, err)
	}
	return f, nil
}

// TryLock is Lock without blocking: it returns ErrLocked (with the file
// closed) when another process holds the lock. Any flock failure is treated
// as contention, mirroring the historical callers' behavior.
func TryLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrLocked
	}
	return f, nil
}

// Release drops the flock (LOCK_UN) and closes the file. It is safe to call
// once; after Release the file handle must not be used again. The lock is
// also released automatically when the process exits.
func Release(f *os.File) error {
	if f == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	err := f.Close()
	if unlockErr != nil && err == nil {
		return unlockErr
	}
	return err
}
