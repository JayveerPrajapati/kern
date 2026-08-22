package context

import (
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// NormalizeToolResult converts a large raw tool output into a compact
// ToolResultSummary. The raw output is stored outside active model context
// (as an artifact reference); only the summary stays in context. Strict Plan
// Phase 5 P1.
//
// Extraction heuristics (deterministic, no LLM):
//   - Facts: lines containing definitions, declarations, or assignments.
//   - Errors: lines containing "error", "fail", "panic", or "fatal".
//   - Evidence: lines containing file:line references.
//   - Summary: first N non-empty lines (configurable, default 5).
//   - References: file paths and symbol names extracted from the output.
func NormalizeToolResult(tool, raw string, maxSummaryLines int) domain.ToolResultSummary {
	if maxSummaryLines <= 0 {
		maxSummaryLines = 5
	}
	summary := domain.ToolResultSummary{Tool: tool}

	lines := strings.Split(raw, "\n")
	var summaryLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Extract errors.
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "error") || strings.Contains(lower, "fail") ||
			strings.Contains(lower, "panic") || strings.Contains(lower, "fatal") {
			summary.Errors = append(summary.Errors, trimmed)
		}

		// Extract facts (lines with = or := or definitions).
		if strings.Contains(trimmed, " = ") || strings.Contains(trimmed, ":=") ||
			strings.Contains(trimmed, "type ") || strings.Contains(trimmed, "func ") {
			summary.Facts = append(summary.Facts, trimmed)
		}

		// Extract evidence (file:line references).
		if hasFileLineRef(trimmed) {
			summary.Evidence = append(summary.Evidence, trimmed)
		}

		// Collect first N non-empty lines for summary.
		if len(summaryLines) < maxSummaryLines {
			summaryLines = append(summaryLines, trimmed)
		}
	}
	summary.Summary = strings.Join(summaryLines, "\n")

	// Extract references (file paths).
	summary.References = extractFilePaths(raw)

	// Token saved = raw tokens - summary tokens (approximate by character count / 4).
	// The summary replaces the raw output in active context; facts/errors are
	// stored separately and not counted against the active context budget.
	rawTokens := len(raw) / 4
	summaryTokens := len(summary.Summary) / 4
	if rawTokens > summaryTokens {
		summary.TokenSaved = rawTokens - summaryTokens
	}

	return summary
}

// hasFileLineRef reports whether a line contains a file:line reference like
// "main.go:42" or "src/foo.py:10".
func hasFileLineRef(s string) bool {
	for i := 0; i < len(s)-1; i++ {
		if s[i] == ':' && i > 0 {
			// Check if the char before ':' could be part of a filename and
			// the char after is a digit.
			if s[i-1] != ' ' && s[i-1] != ':' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
				return true
			}
		}
	}
	return false
}

// extractFilePaths finds file paths in text (heuristic: word ending in a known
// extension).
func extractFilePaths(s string) []string {
	var refs []string
	seen := map[string]bool{}
	words := strings.Fields(s)
	for _, w := range words {
		w = strings.TrimRight(w, ",;:()[]{}\"'")
		for _, ext := range []string{".go", ".py", ".ts", ".js", ".java", ".rs", ".rb", ".php", ".c", ".cpp", ".h", ".md", ".yaml", ".yml", ".json"} {
			if strings.HasSuffix(w, ext) && !seen[w] {
				refs = append(refs, w)
				seen[w] = true
				break
			}
		}
	}
	return refs
}
