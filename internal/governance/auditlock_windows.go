//go:build windows

package governance

import (
	"fmt"
	"os"
	"time"
)

// lockAuditFile acquires an advisory lock on the audit store's lock file via
// exclusive create (Windows has no flock), serializing persisted writes
// across processes. It retries with a bounded timeout while another process
// holds the lock. The returned unlock func deletes the lock file and must be
// called exactly once after the critical section.
func lockAuditFile(path string) (unlock func(), err error) {
	const (
		totalTimeout = 10 * time.Second
		retrySleep   = 50 * time.Millisecond
	)
	deadline := time.Now().Add(totalTimeout)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err == nil {
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("audit lock: timed out waiting for %s", path)
		}
		time.Sleep(retrySleep)
	}
}
