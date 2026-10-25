//go:build windows

package governance

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// lockAllowOnceRegistry acquires the cross-process advisory lock that
// serializes the allow-once registry's read→mark→rename window. Windows has
// no flock(2), so the lock is an exclusive-create sidecar (the same pattern
// lockAuditFile uses): create the `.lock` file with O_EXCL to claim it,
// delete it on release. Acquisition is bounded (fail after the timeout
// window) so a stale lock from a crashed holder cannot wedge the registry
// forever. Lock failure fails closed.
func lockAllowOnceRegistry(path string) (func(), error) {
	const (
		totalTimeout = 10 * time.Second
		retrySleep   = 50 * time.Millisecond
	)
	lockPath := path + ".lock"
	deadline := time.Now().Add(totalTimeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err == nil {
			f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("allow-once registry lock %s: timed out waiting for %s", lockPath, lockPath)
		}
		time.Sleep(retrySleep)
	}
}
