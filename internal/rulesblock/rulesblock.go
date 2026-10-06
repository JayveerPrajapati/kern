// Package rulesblock owns the pure text manipulation for excising
// kern-managed blocks from rule files (AGENTS.md / global AGENTS.md): both
// the marker-delimited format ("<!-- kern:global-rules begin ... -->" ...
// "<!-- kern:global-rules end -->") and the unmarked "# kern usage rules"
// format. It is a pure leaf package (stdlib only) so internal/setup and any
// future writer can share the excision without coupling.
package rulesblock

import (
	"fmt"
	"strings"
)

// Marker anchors for the marker-delimited kern-managed block format.
const (
	markerOpen  = "<!-- kern:global-rules begin"
	markerClose = "<!-- kern:global-rules end -->"
)

// ExciseKernBlock removes EVERY kern-managed block from s — both the
// marker-delimited format ("<!-- kern:global-rules begin ... -->" through
// its matching close marker) and the unmarked "# kern usage rules" format —
// preserving all other content. An unmarked block runs from its heading to
// the next level-1 header, OR — when no H1 follows — through the LAST line
// of the kern rules asset (the global-omit close marker, or the older
// pure-Go build note) so user content after the block without an H1 of its
// own is never swallowed (the old heading→next-H1-or-EOF excise deleted it,
// and a re-run then silently lost it while reporting "already current").
// CRLF-tolerant: the asset-tail anchors accept a trailing CR. An unbalanced
// marker block (open without close) is an error so callers abort instead of
// half-excising the file. Returns s unchanged when no kern block is present.
func ExciseKernBlock(s string) (string, error) {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	buf := make([]string, 0, 8)
	mode := "" // "", "marker" or "unmarked"
	anchored := false
	for _, line := range lines {
		switch {
		case mode != "marker" && strings.HasPrefix(line, "# kern usage rules"):
			mode, anchored = "unmarked", false
			buf = buf[:0]
		case mode != "marker" && strings.HasPrefix(line, markerOpen):
			mode, anchored = "marker", false
			buf = buf[:0]
		case mode == "marker":
			if strings.HasPrefix(line, markerClose) {
				mode = ""
			}
		case mode == "unmarked":
			switch {
			case strings.HasPrefix(line, "# "):
				mode = ""
				out = append(out, line)
			case IsKernBlockTailLine(line):
				anchored = true
				buf = buf[:0]
			default:
				buf = append(buf, line)
			}
		default:
			out = append(out, line)
		}
	}
	if mode == "marker" {
		return "", fmt.Errorf("unbalanced kern-managed marker block (no closing %q)", markerClose)
	}
	if mode == "unmarked" && anchored {
		// Content buffered after the asset-tail anchor is user content;
		// emit it (skipping the separator blank lines) so it is preserved.
		start := 0
		for start < len(buf) && strings.TrimSpace(buf[start]) == "" {
			start++
		}
		out = append(out, buf[start:]...)
	}
	return strings.Join(out, "\n"), nil
}

// IsKernBlockTailLine reports whether line is the last line of the kern
// rules asset for any kern version that wrote the unmarked block: the
// current asset ends with the global-omit close marker; older assets ended
// with the pure-Go build note. CRLF-tolerant.
func IsKernBlockTailLine(line string) bool {
	line = strings.TrimSuffix(line, "\r")
	return line == "<!-- kern:global-omit:end -->" || line == "(`KERN_MCP_FULL=1` only)."
}

// MergePrepend removes any existing kern-managed block from existing and
// prepends the fresh kern block at the top, preserving all other content.
func MergePrepend(existing, kern string) (string, error) {
	cleaned, err := ExciseKernBlock(existing)
	if err != nil {
		return "", err
	}
	cleaned = strings.TrimSpace(cleaned)
	kern = strings.TrimRight(kern, "\n")
	if cleaned == "" {
		return kern + "\n", nil
	}
	return kern + "\n\n" + cleaned + "\n", nil
}

// MergeAppend removes any existing kern-managed block from existing and
// appends the fresh kern block at the end, preserving all other content.
func MergeAppend(existing, kern string) (string, error) {
	cleaned, err := ExciseKernBlock(existing)
	if err != nil {
		return "", err
	}
	cleaned = strings.TrimRight(cleaned, "\n")
	kern = strings.TrimRight(kern, "\n")
	if cleaned == "" {
		return kern + "\n", nil
	}
	return cleaned + "\n\n" + kern + "\n", nil
}

// Omit markers bracket the repo-verbose sections of the canonical rules
// file (assets/AGENTS.md) that must NOT appear in the condensed global
// block — they cannot fit the Windsurf 6000-char whole-file cap. The close
// marker doubles as the asset-tail anchor for the unmarked block excise.
const (
	omitBegin = "<!-- kern:global-omit:begin -->"
	omitEnd   = "<!-- kern:global-omit:end -->"
)

// CondenseKernRules strips every omit-marker region from the canonical rules
// content and collapses runs of 3+ newlines to 2, returning the condensed
// body that fits a host's whole-file cap. An unbalanced omit marker is an
// error so a broken asset is caught at wiring time.
func CondenseKernRules(s string) (string, error) {
	var b strings.Builder
	for {
		begin := strings.Index(s, omitBegin)
		if begin < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:begin])
		s = s[begin+len(omitBegin):]
		end := strings.Index(s, omitEnd)
		if end < 0 {
			return "", fmt.Errorf("unbalanced %s marker in assets/AGENTS.md", omitBegin)
		}
		s = s[end+len(omitEnd):]
	}
	body := strings.TrimSpace(b.String())
	// Collapse 3+ newlines to 2 (the omit-marker lines leave blank runs).
	for strings.Contains(body, "\n\n\n") {
		body = strings.ReplaceAll(body, "\n\n\n", "\n\n")
	}
	return body, nil
}

// ThinKernRules returns the thin repo rules-block content: wiring-only facts
// in ~6 lines — kern is installed, kern_meta is the single entry point, the
// opencode plugin shadows route built-ins to kern, and the env vars that
// widen the tool surface. The full kern usage rules live in the host's
// GLOBAL instructions slot (managed by `kern setup --global-rules`), so a
// thin repo file avoids loading the same rules twice in one session on hosts
// that merge global + project rules.
func ThinKernRules(wired string) string {
	return strings.Join([]string{
		"# kern usage rules — thin (repo)",
		"",
		"Wired agents: " + wired,
		"kern is installed for this repo — call `kern_meta` FIRST for everything; it routes to the right kern_* tool.",
		"On opencode, built-in read/glob/grep/bash route to kern via the plugin shadows.",
		"Set KERN_MCP_FULL=1 for the full 117-tool catalog (KERN_MCP_PHASE for a phase subset).",
	}, "\n") + "\n"
}
