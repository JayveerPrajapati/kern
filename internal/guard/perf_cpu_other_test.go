//go:build !unix

package guard

import "time"

// processCPUTime is unavailable off unix; callers fall back to wall-clock.
func processCPUTime() (time.Duration, bool) { return 0, false }
