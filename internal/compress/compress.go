// Package compress strips noise from logs and prompts deterministically.
// Everything here is rule-based and offline: no model calls.
package compress

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/terse"
)

var (
	timestampRe  = regexp.MustCompile(`(?i)(^\s*\[?(([\d]{2,4}[-/:][\d]{2}[-/:][\d]{2,4}[ T])?[\d]{2}:[\d]{2}(:[\d]{2}([\.,]\d+)?)?(Z|[+-][\d:]{2,5})?)\]?\s*)`)
	infoLevelRe  = regexp.MustCompile(`(?i)(^\s*(INFO|DEBUG|TRACE|VERBOSE|NOTICE)\s*[:=#]?\s*)`)
	warnLevelRe  = regexp.MustCompile(`(?i)(^\s*(WARN|WARNING|ERROR|ERR|FAIL|FATAL|CRITICAL|SEVERE|PANIC|EXCEPTION|EXCEPTION\b)\s*[:=#]?\s*)`)
	stackFrameRe = regexp.MustCompile(`^\s*(at |\t|from |\.go:\d+|\.py:\d+|\.java:\d+|\d+\) )`)
	buildErrRe   = regexp.MustCompile(`(?i)(error|failed|failure|undefined|unresolved|exception|cannot find|no such)`)
	separatorRe  = regexp.MustCompile(`(?m)^\s*([-=_#*]{3,}|\.{3,}|[<>]{3,})\s*$`)
)

// Options controls log compression behaviour.
type Options struct {
	// MaxLines caps the number of output lines.
	MaxLines int
	// KeepContext keeps one neutral line before each error cluster to give
	// surrounding context without retaining full noise.
	KeepContext bool
}

// CompressLog reduces noisy log output to its meaningful core: error/warning
// lines, stack traces and build failures. Repeated lines are deduplicated.
func CompressLog(text string, opts Options) string {
	if opts.MaxLines <= 0 {
		opts.MaxLines = 200
	}
	raw := strings.Split(text, "\n")
	var out []string
	seen := make(map[string]bool)
	blankPend := false

	flush := func() {
		if blankPend && len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
		blankPend = false
	}

	for idx, line := range raw {
		normalized := timestampRe.ReplaceAllString(line, "")
		trimmed := strings.TrimSpace(normalized)
		if trimmed == "" {
			blankPend = true
			continue
		}
		if separatorRe.MatchString(trimmed) {
			continue
		}
		important := false
		if stackFrameRe.MatchString(trimmed) || warnLevelRe.MatchString(trimmed) || buildErrRe.MatchString(trimmed) {
			important = true
		}
		if !important && infoLevelRe.MatchString(trimmed) {
			continue
		}
		if !important && !isUsefulLogLine(trimmed) {
			continue
		}
		key := trimmed
		if seen[key] {
			continue
		}
		seen[key] = true
		flush()
		out = append(out, strings.TrimRight(normalized, " \t"))
		if len(out) >= opts.MaxLines {
			// Say the cap was hit so a truncated compression is never mistaken
			// for the full log.
			if remaining := len(raw) - idx - 1; remaining > 0 {
				out = append(out, fmt.Sprintf("… (%d lines omitted)", remaining))
			}
			break
		}
	}
	return strings.Join(out, "\n")
}

func isUsefulLogLine(s string) bool {
	// Keep lines that mention files, modules, IPs or key/value pairs; drop pure
	// chatter like heartbeats and periodic counters.
	if len(s) > 200 {
		return true
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "heartbeat") || strings.Contains(lower, "keepalive") || strings.Contains(lower, "polling") {
		return false
	}
	if strings.ContainsAny(s, "=:{}[]") || strings.Contains(s, "/") {
		return true
	}
	return len(s) > 40
}

// CompressPrompt normalizes a raw prompt: collapses whitespace, trims repeated
// empty lines and drops leading/trailing filler lines.
func CompressPrompt(text string) string {
	var out []string
	blankPend := false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			blankPend = true
			continue
		}
		if blankPend && len(out) > 0 {
			out = append(out, "")
		}
		blankPend = false
		out = append(out, t)
	}
	joined := strings.Join(out, "\n")
	// Collapse runs of consecutive identical lines into a single line.
	lines := strings.Split(joined, "\n")
	final := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if i > 0 && lines[i] == lines[i-1] {
			continue
		}
		final = append(final, lines[i])
	}
	// Strip unambiguous conversational filler (greetings, thanks, hedges).
	// Conservative by design: only clichés that cannot carry technical payload;
	// lines with paths, code, "file:line" or braces always survive.
	cleaned, _ := terse.StripPromptFluff(strings.Join(final, "\n"))
	return cleaned
}
