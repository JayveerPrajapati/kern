//go:build darwin || linux

package governance

import (
	"github.com/JayveerPrajapati/kern/internal/flock"
)

// lockAuditFile acquires a NON-BLOCKING advisory lock on the audit store's
// lock file (creating it if needed), serializing persisted writes across
// processes. It returns an error immediately when another process holds the
// lock instead of blocking indefinitely: the caller owns the bounded retry
// budget (auditLockRetries / auditLockRetrySleep), so a contended lock can
// never hang a governance write — the approval decision itself is already
// durable in .kern/approvals.json before the audit append runs, and a
// skipped chain link is repairable via `kern audit repair`. The returned
// unlock func releases the lock and closes the file (flock releases
// automatically on close) and must be called exactly once after the critical
// section.
func lockAuditFile(path string) (unlock func(), err error) {
	f, err := flock.TryLock(path)
	if err != nil {
		return nil, err
	}
	return func() { _ = flock.Release(f) }, nil
}
