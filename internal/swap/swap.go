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
//
// Policy: this is the unbounded convenience wrapper for callers that hold no
// budget (e.g. an interactive CLI expand). Expansion must never silently
// exceed a budget the context was compacted under — callers that have one
// MUST use ExpandModeBudget, which enforces the same token accounting the
// compact path (FitBlocks) uses. Keeping this wrapper unbounded is a
// deliberate, documented choice: without a caller-supplied budget there is
// nothing to enforce against, so the cap cannot be implicit.
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

// ExpandModeBudget expands `lang:path:summary` blocks back to full file
// content while enforcing maxTokens with the same budget accounting the
// compact path uses (FitBlocks): prose passes through verbatim, and each
// summary block is inflated only while its full content fits the remaining
// allowance; a block that would exceed the budget stays summarized, so the
// output never grows past the budget by re-inflating. Returns the expanded
// document and whether it fits the budget (fits=false only when the input
// prose itself is already over budget — the document is then trimmed lossily
// with budget.Fit as a last resort, mirroring FitBlocks). A maxTokens <= 0
// means unlimited and delegates to ExpandMode.
func ExpandModeBudget(text, root string, maxTokens int) (string, bool) {
	if maxTokens <= 0 {
		return ExpandMode(text, root), true
	}

	type block struct {
		lang string
		path string
		raw  string // original summary block (kept when it cannot be inflated)
		full string // full file content ("" when the file is missing)
	}
	var blocks []block
	var prose []string
	last := 0
	for _, loc := range fencedSummary.FindAllStringSubmatchIndex(text, -1) {
		start, end := loc[0], loc[1]
		prose = append(prose, text[last:start])
		blocks = append(blocks, block{
			lang: text[loc[2]:loc[3]],
			path: text[loc[4]:loc[5]],
			raw:  text[start:end],
			full: fileAt(root, text[loc[4]:loc[5]]),
		})
		last = end
	}
	tail := text[last:]

	var out strings.Builder
	used := 0
	for i, p := range prose {
		used += tokenize.CountKind(p, tokenize.KindGeneric)
		out.WriteString(p)
		if i >= len(blocks) {
			continue
		}
		b := blocks[i]
		if b.full == "" {
			// Missing file: keep the summary block verbatim.
			out.WriteString(b.raw)
			used += tokenize.CountKind(b.raw, tokenize.KindGeneric)
			continue
		}
		rendered := "```" + b.lang + ":" + b.path + "\n" + b.full + "```\n"
		if allowance := maxTokens - used; tokenize.CountKind(rendered, tokenize.KindGeneric) > allowance {
			// Full inflation would exceed the budget: keep the summary the
			// block was compacted to instead of silently blowing the cap.
			out.WriteString(b.raw)
			used += tokenize.CountKind(b.raw, tokenize.KindGeneric)
			continue
		}
		out.WriteString(rendered)
		used += tokenize.CountKind(rendered, tokenize.KindGeneric)
	}
	used += tokenize.CountKind(tail, tokenize.KindGeneric)
	out.WriteString(tail)

	if tokenize.CountKind(out.String(), tokenize.KindGeneric) <= maxTokens {
		return out.String(), true
	}
	// Last resort (input prose itself over budget): lossy document-level fit,
	// reported via fits=false so the caller can warn.
	return budget.Fit(out.String(), maxTokens), false
}

// FitBlocks fits the document to maxTokens by replacing tagged fenced code
// blocks with budget-fitted summaries. Blocks are processed in document
// order; each block's summary is trimmed with budget.Fit to the remaining
// allowance (mirroring how internal/pack reserves the instruction cost and
// then fits files first-fit). Returns the fitted document, whether it now
// fits, and the paths of the blocks that were swapped. Blocks whose file is
// missing on disk (or whose summary renders empty) are left untouched. When
// the summary swap still cannot meet the budget the document is trimmed
// lossily with budget.Fit as a last resort (fits=false). A maxTokens <= 0
// means unlimited: the document is returned unchanged with fits=true.
func FitBlocks(text, root string, maxTokens int) (string, bool, []string) {
	if maxTokens <= 0 {
		return text, true, nil
	}
	if tokenize.CountKind(text, tokenize.KindGeneric) <= maxTokens {
		return text, true, nil
	}

	// Split the document into prose segments and tagged fenced blocks.
	type block struct {
		lang    string
		path    string
		raw     string // original block text (used when it cannot be swapped)
		summary string // rendered symbolic summary ("" when kept verbatim)
	}
	var blocks []block
	var prose []string
	last := 0
	for _, loc := range fencedBlock.FindAllStringSubmatchIndex(text, -1) {
		start, end := loc[0], loc[1]
		prose = append(prose, text[last:start])
		raw := text[start:end]
		lang, path := text[loc[2]:loc[3]], text[loc[4]:loc[5]]
		b := block{lang: lang, path: path, raw: raw}
		if full := fileAt(root, path); full != "" {
			if sum := code.Summarize(path, []byte(full), 80); strings.TrimSpace(sum.Render()) != "" {
				b.summary = sum.Render()
			}
		}
		blocks = append(blocks, b)
		last = end
	}
	tail := text[last:]

	// Rebuild: prose verbatim (capped to the remaining allowance with an
	// explicit marker when a single prose block would blow the budget), each
	// block replaced by a summary fitted to the remaining allowance (first
	// block claims the budget first, like pack's first-fit file budget).
	var out strings.Builder
	var swapped []string
	used := 0
	for i, p := range prose {
		if allowance := maxTokens - used; allowance > 0 && tokenize.CountKind(p, tokenize.KindGeneric) > allowance {
			p = fitProse(p, allowance)
		}
		used += tokenize.CountKind(p, tokenize.KindGeneric)
		out.WriteString(p)
		if i >= len(blocks) {
			continue
		}
		b := blocks[i]
		if b.summary == "" {
			// Missing file / empty summary: keep the original block verbatim.
			out.WriteString(b.raw)
			used += tokenize.CountKind(b.raw, tokenize.KindGeneric)
			continue
		}
		content := b.summary
		// The rendered block carries scaffolding tokens around the summary
		// (fence, language, path, ":summary", trailing fence); reserve them so
		// `used` reflects the bytes actually written and the budget is tight.
		scaf := tokenize.CountKind("```"+b.lang+":"+b.path+":summary\n", tokenize.KindGeneric) +
			tokenize.CountKind("```\n", tokenize.KindGeneric)
		if allowance := maxTokens - used; tokenize.CountKind(content, tokenize.KindGeneric)+scaf > allowance {
			content = budget.Fit(content, allowance-scaf)
		}
		out.WriteString("```" + b.lang + ":" + b.path + ":summary\n" + content + "```\n")
		used += tokenize.CountKind(content, tokenize.KindGeneric) + scaf
		swapped = append(swapped, b.path)
	}
	// The trailing prose is a prose block too: cap it so the total respects
	// the budget instead of relying solely on the lossy last resort.
	if allowance := maxTokens - used; allowance > 0 && tokenize.CountKind(tail, tokenize.KindGeneric) > allowance {
		tail = fitProse(tail, allowance)
	}
	used += tokenize.CountKind(tail, tokenize.KindGeneric)
	out.WriteString(tail)

	if tokenize.CountKind(out.String(), tokenize.KindGeneric) <= maxTokens {
		return out.String(), true, swapped
	}
	// Last resort: lossy document-level fit (can break fences; reported via
	// fits=false so the caller can warn).
	return budget.Fit(out.String(), maxTokens), false, swapped
}

// proseTrimMarker marks a prose block that was truncated to fit the budget,
// so the loss is explicit in the output rather than silent.
const proseTrimMarker = "\n… (prose trimmed to fit budget) …\n"

// fitProse truncates a single prose/document block so it fits within room
// tokens, appending proseTrimMarker to make the truncation visible. When not
// even the marker plus a sliver fits, the prose is replaced by the marker
// alone. room must be > 0; the result never exceeds room tokens.
func fitProse(p string, room int) string {
	if tokenize.CountKind(p, tokenize.KindGeneric) <= room {
		return p
	}
	m := tokenize.CountKind(proseTrimMarker, tokenize.KindGeneric)
	if room <= m {
		return proseTrimMarker
	}
	return budget.FitProportional(p, room-m) + proseTrimMarker
}

// Fit ensures the document stays within maxTokens: if it already fits it is
// returned unchanged with fits=true; otherwise it is swapped to budget-fitted
// summaries and re-checked. Returns the best-effort document and whether it
// now fits.
func Fit(text, root string, maxTokens int) (string, bool) {
	out, fits, _ := FitBlocks(text, root, maxTokens)
	return out, fits
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
