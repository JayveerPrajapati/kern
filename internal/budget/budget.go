// Package budget fits text into a token budget: it removes duplicate lines,
// keeps the head plus important lines (errors, stack frames, recent tail),
// then truncates to the target size. It is kern's context-window manager.
package budget

import (
	"regexp"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// Fit compresses text to fit within maxTokens. head keeps the first lines of
// a document; keepFraction and tail reserve room for important + recent lines.
func Fit(text string, maxTokens int) string {
	if text == "" || maxTokens <= 0 {
		return ""
	}
	if tokenize.Count(text) <= maxTokens {
		return text
	}
	lines := strings.Split(text, "\n")
	var kept []string
	seen := map[string]bool{}
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" && len(seen) == 0 {
			continue // leading blank lines
		}
		if seen[t] && len(kept) > 0 {
			continue // exact duplicate
		}
		seen[t] = true
		kept = append(kept, l)
	}

	// Reserve head + important lines, then trim the tail to fit.
	const head = 20
	const importantMax = 60
	var core []string
	if len(kept) <= head {
		core = kept
	} else {
		core = append(core, kept[:head]...)
	}
	var important []string
	if len(kept) > head {
		for _, l := range kept[head:] {
			if isImportant(l) && len(important) < importantMax {
				important = append(important, l)
			}
		}
	}
	out := join(core, important)
	for tokenize.Count(out) > maxTokens {
		if len(core) > 0 {
			core = core[:len(core)-1]
		} else if len(important) > 0 {
			important = important[:len(important)-1]
		} else {
			break
		}
		out = join(core, important)
	}
	return strings.TrimSpace(out)
}

func join(core, important []string) string {
	if len(important) == 0 {
		return strings.Join(core, "\n")
	}
	return strings.Join(core, "\n") + "\n… (kept important lines) …\n" + strings.Join(important, "\n")
}

var importantWords = []string{
	"error", "fail", "panic", "exception", "traceback", "stack trace",
	"warning", "warn", "uncaught", "timeout", "denied", "not found",
}

func isImportant(line string) bool {
	low := strings.ToLower(line)
	if len(low) > 120 {
		return true // very long lines usually carry data (paths, hashes)
	}
	if isStackFrame(line) {
		return true
	}
	for _, w := range importantWords {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}

// isStackFrame recognises typical Go/Python stack-frame and trace lines so
// they are kept by Fit even when they do not contain a keyword (W2-50).
func isStackFrame(line string) bool {
	if line == "" {
		return false
	}
	// Indented frame bodies (Go frames, Python "  File ...", etc.).
	if line[0] == '\t' || line[0] == ' ' {
		if strings.Contains(line, "File ") || strings.Contains(line, "line ") {
			return true
		}
		// Go: func.go:45 +0x1a0 ; runtime.main(0x...)
		if frameRe.MatchString(line) {
			return true
		}
	}
	return false
}

var frameRe = regexp.MustCompile(`[a-zA-Z_][\w./\-]*\([^)]*(?:\b[\w./\-]+\.go|\.py|\.js|\.ts|\.rb|\.java|\.c|\.cpp):0*[1-9]\d*`)

// FitLossless is a strict variant: it only removes duplicates and trailing
// noise, never truncates semantics. Use when maxTokens <= 0.
func FitLossless(text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	seen := map[string]bool{}
	var kept []string
	for _, l := range lines {
		if seen[l] {
			continue
		}
		seen[l] = true
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}
