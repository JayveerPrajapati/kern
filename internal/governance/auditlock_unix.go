//go:build darwin || linux

package governance

import (
	"os"
	"syscall"
)

// lockAuditFile acquires a blocking advisory lock on the audit store's lock
// file (creating it if needed), serializing persisted writes across
// processes. The returned unlock func releases the lock and closes the file
// (flock releases automatically on close) and must be called exactly once
// after the critical section.
func lockAuditFile(path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}
