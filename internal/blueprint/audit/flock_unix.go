//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package audit

import (
	"github.com/JayveerPrajapati/kern/internal/flock"
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
	f, err := flock.Lock(path + ".lock")
	if err != nil {
		return nil, err
	}
	return func() { _ = flock.Release(f) }, nil
}
