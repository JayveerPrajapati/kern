//go:build darwin || linux

package governance

import (
	"github.com/JayveerPrajapati/kern/internal/flock"
)

// lockAuditFile acquires a blocking advisory lock on the audit store's lock
// file (creating it if needed), serializing persisted writes across
// processes. The returned unlock func releases the lock and closes the file
// (flock releases automatically on close) and must be called exactly once
// after the critical section.
func lockAuditFile(path string) (unlock func(), err error) {
	f, err := flock.Lock(path)
	if err != nil {
		return nil, err
	}
	return func() { _ = flock.Release(f) }, nil
}
