//go:build !windows

package governance

import (
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/flock"
)

// lockAllowOnceRegistry acquires the cross-process advisory lock that
// serializes the allow-once registry's read→mark→rename window. On Unix it
// is a blocking flock(2) on the registry's `.lock` sidecar (the kernel
// drops the lock if the holder crashes). The returned *os.File holds the
// lock until releaseAllowOnceRegistry closes it. Lock failure fails closed.
func lockAllowOnceRegistry(path string) (func(), error) {
	f, err := flock.Lock(path + ".lock")
	if err != nil {
		return nil, fmt.Errorf("allow-once registry lock %s: %w", path, err)
	}
	return func() { _ = flock.Release(f) }, nil
}
