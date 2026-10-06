package verifycmd

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/compress"
)

const (
	summaryErrorLines  = 40
	failuresErrorLines = 120
	maxFailedNames     = 20
	maxFullBytes       = 1 << 20
)

// shapeOutput turns a command's full output into the slice the caller asked
// for. Modes: summary (default), failures, full, tail:N, lines:A-B.
func shapeOutput(output string, exit int, dur time.Duration, anchor, mode string) (string, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "summary"
	}
	lines := splitLines(output)
	header := fmt.Sprintf("exit=%d lines=%d anchor=%s", exit, len(lines), anchor)
	if dur > 0 {
		header = fmt.Sprintf("exit=%d (%s) lines=%d anchor=%s", exit, dur.Round(10*time.Millisecond), len(lines), anchor)
	}

	switch {
	case mode == "summary":
		body := summarizeOutput(output, lines, exit)
		return header + "\n" + body + "\n" + moreHint(anchor), nil
	case mode == "failures":
		return header + "\n" + failureOutput(output, lines) + "\n" + moreHint(anchor), nil
	case mode == "full":
		if len(output) > maxFullBytes {
			return header + "\n" + output[:maxFullBytes] + "\n... truncated at 1 MiB; use output=lines:A-B for the rest", nil
		}
		return header + "\n" + output, nil
	case strings.HasPrefix(mode, "tail:"):
		n, err := strconv.Atoi(strings.TrimPrefix(mode, "tail:"))
		if err != nil || n < 1 {
			return "", fmt.Errorf("output=tail:N needs a positive number")
		}
		if n > len(lines) {
			n = len(lines)
		}
		return header + "\n" + numbered(lines, len(lines)-n+1, len(lines)), nil
	case strings.HasPrefix(mode, "lines:"):
		a, b, err := parseRange(strings.TrimPrefix(mode, "lines:"))
		if err != nil {
			return "", err
		}
		if a > len(lines) {
			return "", fmt.Errorf("lines:%d-%d is past the end (%d lines)", a, b, len(lines))
		}
		if b > len(lines) {
			b = len(lines)
		}
		return header + "\n" + numbered(lines, a, b), nil
	}
	return "", fmt.Errorf("unknown output mode %q (summary|failures|full|tail:N|lines:A-B)", mode)
}

func moreHint(anchor string) string {
	if anchor == "" {
		return ""
	}
	return "more: kern_verify anchor=" + anchor + " output=failures|tail:N|lines:A-B|full"
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func numbered(lines []string, from, to int) string {
	var b strings.Builder
	for i := from; i <= to; i++ {
		fmt.Fprintf(&b, "%d: %s\n", i, lines[i-1])
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func parseRange(s string) (int, int, error) {
	parts := strings.SplitN(s, "-", 2)
	a, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || a < 1 {
		return 0, 0, fmt.Errorf("output=lines:A-B needs numbers, e.g. lines:400-460")
	}
	b := a + defaultFileWindow - 1
	if len(parts) == 2 {
		b, err = strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || b < a {
			return 0, 0, fmt.Errorf("output=lines:A-B needs A <= B")
		}
	}
	return a, b, nil
}

// goTestCounts reads `go test` package result lines.
func goTestCounts(lines []string) (ok, failed, noTests int, isGoTest bool) {
	for _, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "ok \t") || strings.HasPrefix(ln, "ok  \t"):
			ok++
		case strings.HasPrefix(ln, "FAIL\t"):
			failed++
		case strings.HasPrefix(ln, "?") && strings.Contains(ln, "[no test files]"):
			noTests++
		}
	}
	return ok, failed, noTests, ok+failed > 0
}

// failedTests lists tests a `go test -v` log marks "--- FAIL:".
func failedTests(lines []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, ln := range lines {
		f := strings.Fields(ln)
		if len(f) < 3 || f[0] != "---" || f[1] != "FAIL:" || seen[f[2]] {
			continue
		}
		seen[f[2]] = true
		out = append(out, f[2])
		if len(out) == maxFailedNames {
			break
		}
	}
	return out
}

func summarizeOutput(output string, lines []string, exit int) string {
	var parts []string
	if ok, failed, noTests, isGo := goTestCounts(lines); isGo {
		parts = append(parts, fmt.Sprintf("go test: %d ok, %d failed, %d without tests", ok, failed, noTests))
	}
	if exit == 0 {
		if len(parts) == 0 {
			parts = append(parts, "PASS")
			parts = append(parts, lastNonEmpty(lines, 3)...)
		}
		return strings.Join(parts, "\n")
	}
	return strings.Join(append(parts, failureParts(output, lines, summaryErrorLines)...), "\n")
}

func failureOutput(output string, lines []string) string {
	return strings.Join(failureParts(output, lines, failuresErrorLines), "\n")
}

// failureParts is what a failed run shows: the failing test names with their
// own output and any compiler diagnostics; when none of that is recognisable
// (another toolchain) the compressed error lines stand in.
func failureParts(output string, lines []string, maxLines int) []string {
	var parts []string
	if names := failedTests(lines); len(names) > 0 {
		parts = append(parts, "failed tests: "+strings.Join(names, ", "))
	}
	details := append(failureDetails(lines), compileDiagnostics(lines)...)
	if len(details) == 0 {
		return append(parts, compressErrors(output, maxLines))
	}
	if len(details) > maxLines {
		details = append(details[:maxLines], fmt.Sprintf("... %d more lines in the anchor", len(details)-maxLines))
	}
	return append(parts, details...)
}

const detailContext = 10

func isIndented(ln string) bool {
	return strings.HasPrefix(ln, " ") || strings.HasPrefix(ln, "\t")
}

// failureDetails returns each top-level "--- FAIL:" line with the indented
// output lines right before it (go test -v order) and right after it.
func failureDetails(lines []string) []string {
	var out []string
	for i, ln := range lines {
		if !strings.HasPrefix(ln, "--- FAIL:") {
			continue
		}
		start := i
		for start > 0 && i-start < detailContext && isIndented(lines[start-1]) {
			start--
		}
		out = append(out, lines[start:i+1]...)
		for j := i + 1; j < len(lines) && j-i <= detailContext && isIndented(lines[j]); j++ {
			out = append(out, lines[j])
		}
	}
	return out
}

var compileDiagRe = regexp.MustCompile(`\.go:\d+:\d+:`)

// compileDiagnostics returns go build/vet style "file.go:12:34: message" lines.
func compileDiagnostics(lines []string) []string {
	var out []string
	for _, ln := range lines {
		if compileDiagRe.MatchString(ln) {
			out = append(out, ln)
		}
	}
	return out
}

func compressErrors(output string, maxLines int) string {
	out := compress.CompressLog(output, compress.Options{
		MaxLines:          maxLines,
		Cluster:           true,
		SemcacheNamespace: "verify-command",
	})
	if strings.TrimSpace(out) == "" {
		return "(no error lines found; see anchor for the full output)"
	}
	return out
}

func lastNonEmpty(lines []string, n int) []string {
	var out []string
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			out = append([]string{lines[i]}, out...)
		}
	}
	return out
}
