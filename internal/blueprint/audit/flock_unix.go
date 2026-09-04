//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package audit

import (
	"fmt"
	"os"
	"syscall"
)

// lockAuditFile acquires an exclusive advisory flock(2) on <path>.lock so
// concurrent blueprint processes serialize appends to the same audit file
// (H7). The lock is held across both the last-hash read and the append in
// Writer.Write, which prevents two processes from forking the hash chain by
// both reading genesis and then both appending. The lock file is never
// written to — it exists only as a flock target.
//
// Returns an unlock function that releases the lock and closes the file. The
// caller must defer it. A failed lock is a hard error: proceeding unlocked
// would risk the exact torn-append / chain-fork the lock exists to prevent.
func lockAuditFile(path string) (func(), error) {
	lockPath := path + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", lockPath, err)
	}
	// LOCK_EX blocks until the lock is acquired; no timeout is needed because
	// every holder releases promptly (deferred unlock) and a crashed holder
	// releases the lock when its fd closes.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("flock %s: %w", lockPath, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
