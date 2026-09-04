//go:build windows

package audit

// lockAuditFile is a no-op on Windows: the standard library has no flock(2)
// equivalent. The in-process Writer mutex still serializes writes within one
// process; cross-process append races on Windows are accepted (the append
// remains a single best-effort write). Best-effort contract: the lock is an
// integrity hardening, not a validation gate.
func lockAuditFile(path string) (func(), error) {
	return func() {}, nil
}
