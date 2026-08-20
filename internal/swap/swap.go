// Package swap swaps code blocks in a context document between full source
// and per-file signatures depending on a token budget. When a document
// is too large, each fenced block tagged `lang:path` is replaced by its
// symbolic summary; when budget is available, summary markers are expanded
// back to full file contents. Deterministic, dependency-free.
package swap

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// fencedBlock matches a fenced code block whose opener is `lang:path`.
var fencedBlock = regexp.MustCompile("(?s)\x60\x60\x60(\\w+):([^\\n\x60]+)\\n(.*?)\x60\x60\x60\\n?")

// fencedSummary matches a block opener `lang:path:summary` (expanded form).
var fencedSummary = regexp.MustCompile("(?s)\x60\x60\x60(\\w+):([^\\n\x60]+):summary\\n(.*?)\x60\x60\x60\\n?")

// SummaryMode replaces each tagged code block with its symbolic summary.
// Blocks that reference files missing on disk are left untouched.
func SummaryMode(text, root string) string {
	return fencedBlock.ReplaceAllStringFunc(text, func(block string) string {
		m := fencedBlock.FindStringSubmatch(block)
		if m == nil {
			return block
		}
		path := m[2]
		full := fileAt(root, path)
		if full == "" {
			return block
		}
		sum := code.Summarize(path, []byte(full), 80)
		render := sum.Render()
		if strings.TrimSpace(render) == "" {
			return block
		}
		return "```" + m[1] + ":" + path + ":summary\n" + render + "```\n"
	})
}

// ExpandMode replaces `lang:path:summary` blocks with the full file content.
func ExpandMode(text, root string) string {
	return fencedSummary.ReplaceAllStringFunc(text, func(block string) string {
		m := fencedSummary.FindStringSubmatch(block)
		if m == nil {
			return block
		}
		full := fileAt(root, m[2])
		if full == "" {
			return block
		}
		return "```" + m[1] + ":" + m[2] + "\n" + full + "```\n"
	})
}

// Fit ensures the document stays within maxTokens: if it already fits it is
// returned unchanged with fits=true; otherwise it is swapped to summaries and
// re-checked. Returns the best-effort document and whether it now fits.
func Fit(text, root string, maxTokens int) (string, bool) {
	toks := tokenize.CountKind(text, tokenize.KindGeneric)
	if maxTokens <= 0 || toks <= maxTokens {
		return text, true
	}
	summed := SummaryMode(text, root)
	stoks := tokenize.CountKind(summed, tokenize.KindGeneric)
	if stoks < toks {
		// The swap actually shrank the document; only trim further if it
		// still exceeds the hard budget.
		if stoks <= maxTokens {
			return summed, true
		}
		return budget.Fit(summed, maxTokens), false
	}
	// No shrink possible: trim the original with the lossy fitter.
	return budget.Fit(text, maxTokens), false
}

func fileAt(root, path string) string {
	absRoot := ""
	if root != "" {
		r, err := filepath.Abs(root)
		if err != nil {
			return ""
		}
		absRoot = r
	}
	var full string
	if filepath.IsAbs(path) {
		full = path
	} else if absRoot != "" {
		full = filepath.Join(absRoot, path)
	} else {
		full = path
	}
	full = filepath.Clean(full)
	// Refuse any path that escapes root (e.g. via ../).
	if absRoot != "" {
		rel, err := filepath.Rel(absRoot, full)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ""
		}
	}
	// Cap file size to avoid unbounded reads (10MB).
	info, err := os.Stat(full)
	if err != nil || info.Size() > 10<<20 {
		return ""
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return ""
	}
	return string(b)
}
