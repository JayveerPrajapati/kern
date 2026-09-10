//go:build windows

package flock

import (
	"errors"
	"os"
)

// Lock is not supported on Windows: the standard library has no flock(2).
// The per-package Windows lock implementations (exclusive create, LockFileEx,
// no-op) remain authoritative; this stub fails closed so a caller cannot
// silently skip cross-process serialization.
func Lock(path string) (*os.File, error) {
	return nil, errors.New("flock: not supported on windows; use the platform lock implementation")
}

// TryLock is Lock without blocking. On Windows it always fails closed.
func TryLock(path string) (*os.File, error) {
	return nil, errors.New("flock: not supported on windows; use the platform lock implementation")
}

// Release is a no-op on Windows (Lock never succeeds there).
func Release(f *os.File) error {
	return nil
}
