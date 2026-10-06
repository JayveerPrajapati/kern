package verdict

import (
	"fmt"
	"strings"
)

const maxFailureOutput = 4000

// isDiagnosticLine reports whether a line of go test output is an actual
// compiler/vet diagnostic ("pkg/file.go:12:34: ...") or a known environment
// limitation, as opposed to a truncated fragment of a passing run's log.
func isDiagnosticLine(t string) bool {
	if strings.Contains(t, "cannot nest sandbox-exec") || strings.HasPrefix(t, "vet:") {
		return true
	}
	for i := 0; ; {
		j := strings.Index(t[i:], ".go:")
		if j < 0 {
			return false
		}
		k := i + j + len(".go:")
		if k < len(t) && t[k] >= '0' && t[k] <= '9' {
			return true
		}
		i = k
	}
}

// boundedFailureOutput keeps a failed run's failure-relevant output, so a
// passing-but-vet-failed run does not dump its whole log into the verdict.
// Passing-test noise lines are stripped first (F13) so the byte budget is
// spent on the failing tests' own lines; when nothing recognizable remains
// (a run that failed without any --- FAIL markers) the raw tail still shows.
func boundedFailureOutput(s string) string {
	s = strings.TrimSpace(s)
	names := ""
	if failed := failedTestNames(s); len(failed) > 0 {
		names = "failed tests: " + strings.Join(failed, ", ") + "\n"
	}
	if stripped := stripPassingTestNoise(s); strings.TrimSpace(stripped) != "" {
		s = stripped
	}
	if len(s) <= maxFailureOutput {
		return names + s
	}
	tail := s[len(s)-maxFailureOutput:]
	if i := strings.IndexByte(tail, '\n'); i >= 0 {
		tail = tail[i+1:]
	}
	return names + "... (output truncated, showing last lines)\n" + tail
}

// stripPassingTestNoise removes `go test -v` lines that carry no failure
// signal — === RUN/CONT/NAME markers, --- PASS/SKIP/CONT markers, "ok <pkg>"
// summaries and the bare PASS — so only failing markers, their indented
// diagnostics and package-level FAIL lines remain (F13). Non-Go runner
// output keeps its lines verbatim (nothing matches the noise prefixes).
func stripPassingTestNoise(s string) string {
	var b strings.Builder
	for _, ln := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || t == "PASS" ||
			strings.HasPrefix(t, "=== ") ||
			strings.HasPrefix(t, "--- PASS") ||
			strings.HasPrefix(t, "--- SKIP") ||
			strings.HasPrefix(t, "--- CONT") ||
			strings.HasPrefix(t, "ok ") {
			continue
		}
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// TestFailureExcerpt is the single-run shaper behind the CLI's default
// `kern verify` tests render (F13): passing-test noise stripped, bounded to
// maxLines with an honest more-lines note, "" when nothing remains.
func TestFailureExcerpt(s string, maxLines int) string {
	s = stripPassingTestNoise(s)
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], fmt.Sprintf("... %d more lines in the full log", len(lines)-maxLines))
	}
	return strings.Join(lines, "\n")
}

// failedTestNames lists the tests a `go test -v` log marks "--- FAIL:", so a
// failed run names its failures even when the log tail does not reach them.
func failedTestNames(output string) []string {
	const maxNames = 20
	var out []string
	seen := map[string]bool{}
	for _, ln := range strings.Split(output, "\n") {
		f := strings.Fields(ln)
		if len(f) < 3 || f[0] != "---" || f[1] != "FAIL:" || seen[f[2]] {
			continue
		}
		seen[f[2]] = true
		out = append(out, f[2])
		if len(out) == maxNames {
			break
		}
	}
	return out
}
