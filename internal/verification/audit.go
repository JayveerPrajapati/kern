package verification

// Captured output logging (F4).
//
// The engine embeds raw sub-process output in its result structs
// (BuildResult.Output, TestResult.Output, ...). Unbounded, a noisy `go test
// -v` or build can bloat the verification result to megabytes — a measured
// verify --json run carried 606KB of `go test -v` log. At CAPTURE time we
// clip the embedded output — PASSing checks to ~32KB (tail), FAILing checks
// to ~256KB (tail), keeping the actionable end (errors, stack traces, FAIL
// lines) — and persist the FULL output to
// <root>/.kern/audit/<run-id>/verify-<check>.log. The root-relative log path
// is carried on the result (LogPath) so the JSON/print side can point
// operators at the complete log without re-transmitting it. Existing callers
// that ignore LogPath are unaffected (it is "" when nothing was captured).

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// passOutputCap bounds the embedded output of a PASSing check. The full
	// log is always persisted; only the in-result copy is clipped.
	passOutputCap = 32 << 10 // 32 KiB
	// failOutputCap bounds the embedded output of a FAILing check. A failed
	// check keeps more context than a passing one, but still bounded.
	failOutputCap = 256 << 10 // 256 KiB
)

// runID derives the per-run audit subdirectory from the run's generation
// timestamp. Nanosecond precision makes collisions between runs in the same
// second practically impossible.
func runID(now time.Time) string {
	return now.Format("20060102-150405.000000000")
}

// captureOutput clips output per the PASS/FAIL caps and persists the full
// text to <root>/.kern/audit/<run-id>/verify-<check>.log. It returns the
// clipped text (embedded in the result) and the root-relative log path.
// Empty output writes no log (logPath ""). A failed write is best-effort:
// it degrades to just the clipped output and never fails or changes a
// result — the audit dir is an operator aid, not a gate.
func captureOutput(root string, now time.Time, check, output string, ok bool) (clipped, logPath string) {
	if strings.TrimSpace(output) == "" {
		return "", ""
	}
	lim := passOutputCap
	if !ok {
		lim = failOutputCap
	}
	rel := filepath.Join(".kern", "audit", runID(now), "verify-"+check+".log")
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return tail(output, lim), ""
	}
	if err := os.WriteFile(abs, []byte(output), 0o644); err != nil {
		return tail(output, lim), ""
	}
	return tail(output, lim), rel
}

// tail returns the last n runes of s — the actionable end of a noisy log
// (errors, stack traces, FAIL lines). Strings shorter than n pass through
// unchanged.
func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}
