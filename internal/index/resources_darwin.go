//go:build darwin

package index

import (
	"strconv"
	"syscall"
)

// platformMemTotal returns total physical memory in bytes on macOS via
// syscall.Sysctl("hw.memsize") — part of the stdlib syscall package on
// darwin, no cgo. The syscall returns the value in one of two encodings
// depending on the CPU generation: a decimal string on Intel, raw
// little-endian bytes on Apple Silicon — so both are parsed. Returns 0 if
// the sysctl fails or the value is unparseable (degrade to CPU-only).
func platformMemTotal() int64 {
	v, err := syscall.Sysctl("hw.memsize")
	if err != nil {
		return 0
	}
	if n, err := strconv.ParseUint(v, 10, 64); err == nil {
		return int64(n)
	}
	// Raw little-endian uint64 (Apple Silicon). Copy into an 8-byte buffer:
	// trailing NULs from the C string land at the high end and are harmless
	// in little-endian order.
	b := make([]byte, 8)
	copy(b, v)
	var n uint64
	for i := 0; i < 8; i++ {
		n |= uint64(b[i]) << (8 * i)
	}
	if n == 0 {
		return 0
	}
	return int64(n)
}
