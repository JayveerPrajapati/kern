//go:build !linux && !darwin

package index

// platformMemTotal returns 0 on platforms without a cheap stdlib-only
// memory probe (Windows, the BSDs, ...): tuning falls back to CPU-only.
// This is a deliberate degrade-to-CPU-only path, never an error.
func platformMemTotal() int64 {
	return 0
}
