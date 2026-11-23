//go:build unix

package guard

import (
	"syscall"
	"time"
)

// processCPUTime returns this process's consumed user+system CPU time. Unlike
// wall-clock, it is unaffected by other test binaries competing for cores, so
// a performance gate can assert real work done instead of scheduler noise.
func processCPUTime() (time.Duration, bool) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, false
	}
	// Utime/Stime are Timeval; On darwin/linux both Sec and Usec are int64.
	return time.Duration(ru.Utime.Sec+ru.Stime.Sec)*time.Second +
		time.Duration(ru.Utime.Usec+ru.Stime.Usec)*time.Microsecond, true
}
