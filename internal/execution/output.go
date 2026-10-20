package execution

// Output shaping, anchor persistence and the raw-dump steering footer for
// kern_exec — the kern_verify contract (output=summary|tail:N|lines:A-B|full,
// kept runs re-sliceable via anchor=) ported to script stdout, plus the
// post-hoc steering footer that teaches dump-style callers the cheaper
// surfaces. Default is full: kern_exec's contract is EXACT answers, so
// shaping is always opt-in and the footer never mutates or blocks output.
//
// This file mirrors internal/mcp/exec/output.go (the same surface the MCP
// kern_exec tool uses) because the CLI entry point cannot import it: the
// import graph mcp/exec → incident → execution makes any execution→mcp/exec
// edge a cycle. The mirror is behavior-identical and deterministic; keep the
// two in sync when the shaping contract changes.
//
// The exec-run STORE is NOT mirrored: SaveExecRun/LoadExecRun/ExecRunsDir
// live in this package (see execrun.go) — the single canonical
// implementation shared with the MCP kern_exec tool (internal/mcp/exec,
// which imports this package); no cycle since mcp/exec → incident →
// execution is the pre-existing import direction.

import (
	"fmt"
	"strconv"
	"strings"
)

// steerThresholdBytes is the stdout size above which a script that also
// statically looks like a file read is flagged as a raw dump. Computed
// answers print small (a telemetry probe walks a whole file tree and prints
// ~100 bytes); raw dumps print kilobytes of file content. Both signals
// together keep false positives near zero.
const steerThresholdBytes = 4096

// fileReadPrimitives are the static signatures (any language) that a script
// reads files rather than computing from its arguments. Advisory only —
// used solely to decide whether the steering footer applies.
var fileReadPrimitives = []string{
	"open(", "os.ReadFile", "ReadFile(", "readFileSync", ".read()",
	"io.ReadAll", "Get-Content", "cat ", "grep ", "sed -n", "awk ", "rg ",
}

// looksLikeFileRead reports whether the script text carries any static
// file-read signature.
func looksLikeFileRead(code string) bool {
	for _, p := range fileReadPrimitives {
		if strings.Contains(code, p) {
			return true
		}
	}
	return false
}

// DumpSteer returns the steering footer when BOTH signals fire: the script
// statically reads files AND its stdout is large. Empty when the run looks
// like a legitimate computed answer.
func DumpSteer(code, out, anchor string) string {
	if !looksLikeFileRead(code) || len(out) < steerThresholdBytes {
		return ""
	}
	return fmt.Sprintf("kern exec: raw dump detected — this script reads files and returned %d raw bytes (only PII-masked, no shaping). Code reads belong on kern_explore/kern_search (compact, token-metered surfaces); for this tool pass output=summary|tail:N|lines:A-B. Full output kept: re-slice with anchor=%s output=tail:50.",
		len(out), anchor)
}

// ExecMeta carries the run metadata shaped slices use for their summary line.
type ExecMeta struct {
	Runtime string
	DurMS   int64
	Exit    int
	Anchor  string
}

// ShapeExecOutput applies the requested output mode to a full stdout.
// Modes mirror kern_verify: "" / "full" (exact, untouched), "summary"
// (deterministic stats + first/last lines), "tail:N" (last N lines,
// numbered), "lines:A-B" (numbered window). "failures" is rejected: a
// script's stdout has no test semantics.
func ShapeExecOutput(mode, out string, m ExecMeta) (string, error) {
	switch {
	case mode == "" || mode == "full":
		return out, nil
	case mode == "summary":
		return summaryExecOutput(out, m), nil
	case mode == "failures":
		return "", fmt.Errorf("output=failures is a kern_verify mode; a script's stdout has no test semantics — use summary")
	case strings.HasPrefix(mode, "tail:"):
		n, err := strconv.Atoi(strings.TrimPrefix(mode, "tail:"))
		if err != nil || n <= 0 {
			return "", fmt.Errorf("output=tail:N wants a positive N, got %q", mode)
		}
		return numberedTail(out, n) + moreHint(m.Anchor), nil
	case strings.HasPrefix(mode, "lines:"):
		lo, hi, err := parseExecRange(strings.TrimPrefix(mode, "lines:"))
		if err != nil {
			return "", fmt.Errorf("output=lines:A-B: %v", err)
		}
		return numberedWindow(out, lo, hi) + moreHint(m.Anchor), nil
	}
	return "", fmt.Errorf("output: unknown mode %q (want full|summary|tail:N|lines:A-B)", mode)
}

// moreHint is appended to shaped (non-full) slices so the caller can always
// widen without re-running (the kern_verify moreHint convention).
func moreHint(anchor string) string {
	if anchor == "" {
		return ""
	}
	return fmt.Sprintf("\n(anchor %s — full via output=full, windows via output=lines:A-B)\n", anchor)
}

// summaryExecOutput renders the deterministic summary: run stats plus the
// first and last lines, so a shape-only call loses no directional signal.
func summaryExecOutput(out string, m ExecMeta) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	first, last := 5, 5
	if len(lines) < first {
		first = len(lines)
	}
	if len(lines)-first < last {
		last = len(lines) - first
	}
	var b strings.Builder
	fmt.Fprintf(&b, "kern exec: summary — runtime %s, exit %d, %dms, stdout %d bytes (%d lines)\n",
		m.Runtime, m.Exit, m.DurMS, len(out), len(lines))
	if first > 0 {
		b.WriteString("--- first lines ---\n")
		for _, ln := range lines[:first] {
			b.WriteString(ln + "\n")
		}
	}
	if last > 0 {
		b.WriteString("--- last lines ---\n")
		for _, ln := range lines[len(lines)-last:] {
			b.WriteString(ln + "\n")
		}
	}
	b.WriteString(moreHint(m.Anchor))
	return b.String()
}

// numberedTail returns the last n lines, numbered 1..n.
func numberedTail(out string, n int) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if n > len(lines) {
		n = len(lines)
	}
	var b strings.Builder
	for i, ln := range lines[len(lines)-n:] {
		fmt.Fprintf(&b, "%d\t%s\n", i+1, ln)
	}
	return b.String()
}

// numberedWindow returns lines[lo..hi] (1-based, inclusive), numbered by
// their absolute line number.
func numberedWindow(out string, lo, hi int) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if lo < 1 {
		lo = 1
	}
	if hi > len(lines) {
		hi = len(lines)
	}
	var b strings.Builder
	for i := lo; i <= hi && i <= len(lines); i++ {
		fmt.Fprintf(&b, "%d\t%s\n", i, lines[i-1])
	}
	return b.String()
}

// parseExecRange parses "A-B" or a bare "A" (window of 1) into 1-based
// inclusive bounds.
func parseExecRange(s string) (int, int, error) {
	parts := strings.SplitN(s, "-", 2)
	lo, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || lo < 1 {
		return 0, 0, fmt.Errorf("bad start %q", parts[0])
	}
	hi := lo
	if len(parts) == 2 {
		hi, err = strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || hi < lo {
			return 0, 0, fmt.Errorf("bad end %q", parts[1])
		}
	}
	return lo, hi, nil
}
