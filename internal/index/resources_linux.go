//go:build linux

package index

import (
	"os"
	"strconv"
	"strings"
)

// platformMemTotal returns total physical memory in bytes on Linux by
// parsing the MemTotal line of /proc/meminfo (reported in kB). Returns 0
// when the file is missing, unreadable, or malformed so tuning degrades to
// CPU-only rather than erroring.
func platformMemTotal() int64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || kb < 0 {
			return 0
		}
		return kb * 1024
	}
	return 0
}
