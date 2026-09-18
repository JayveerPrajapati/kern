package intel

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// SemanticDiffReport summarizes changes at the AST symbol and architectural level
// rather than raw line changes, drastically reducing token count for AI agents.
type SemanticDiffReport struct {
	FromRef         string         `json:"from_ref"`
	ToRef           string         `json:"to_ref"`
	FilesChanged    int            `json:"files_changed"`
	AddedSymbols    []index.Symbol `json:"added_symbols"`
	RemovedSymbols  []index.Symbol `json:"removed_symbols"`
	ModifiedSymbols []ModifiedSym  `json:"modified_symbols"`
	ImpactedCallers []string       `json:"impacted_callers"`
	EstimatedRisk   string         `json:"estimated_risk"`
}

// ModifiedSym records changes in a symbol's signature or definition bounds.
type ModifiedSym struct {
	Symbol      string   `json:"symbol"`
	Kind        string   `json:"kind"`
	File        string   `json:"file"`
	OldLine     int      `json:"old_line"`
	NewLine     int      `json:"new_line"`
	OldParams   []string `json:"old_params,omitempty"`
	NewParams   []string `json:"new_params,omitempty"`
	OldReturns  []string `json:"old_returns,omitempty"`
	NewReturns  []string `json:"new_returns,omitempty"`
	SigChanged  bool     `json:"sig_changed"`
	CallersGain []string `json:"callers_gain,omitempty"`
}

// SemanticDiff compares an index against diff file changes to compute symbol-level diffs.
func SemanticDiff(ix *index.Index, root, from, to string) (*SemanticDiffReport, error) {
	fileChanges, err := FilesForRangeL(root, from, to)
	if err != nil {
		return nil, fmt.Errorf("files for range: %w", err)
	}

	report := &SemanticDiffReport{
		FromRef:      from,
		ToRef:        to,
		FilesChanged: len(fileChanges),
	}
	if from == "" && to == "" {
		report.FromRef = "HEAD"
		report.ToRef = "working-tree"
	}

	changedFilesMap := make(map[string]FileChange)
	for _, fc := range fileChanges {
		changedFilesMap[fc.File] = fc
	}

	impactedMap := make(map[string]bool)

	for _, sym := range ix.Symbols {
		fc, ok := changedFilesMap[sym.File]
		if !ok {
			continue
		}

		overlaps := false
		symEnd := sym.End
		if symEnd <= 0 {
			symEnd = sym.Line + 10
		}
		if len(fc.Ranges) == 0 {
			overlaps = true // whole file changed
		} else {
			for _, r := range fc.Ranges {
				if !(r.End < sym.Line || r.Start > symEnd) {
					overlaps = true
					break
				}
			}
		}
		// Comment-only edits inside a symbol's span are not real modifications
		// (e.g. a trailing comment appended after a method). When the changed
		// ranges are known, require at least one non-comment added line in the
		// span before reporting the symbol as Modified.
		if overlaps && len(fc.Ranges) > 0 && !addedCodeInSpan(root, from, to, sym.File, sym.Line, symEnd) {
			overlaps = false
		}
		if overlaps {
			mod := ModifiedSym{
				Symbol:     sym.FullName(),
				Kind:       sym.Kind,
				File:       sym.File,
				NewLine:    sym.Line,
				NewParams:  sym.Params,
				NewReturns: sym.Returns,
			}
			report.ModifiedSymbols = append(report.ModifiedSymbols, mod)

			// Record impacted callers
			for _, c := range ix.CallersOf(sym.FullName()) {
				impactedMap[c] = true
			}
		}
	}

	for c := range impactedMap {
		report.ImpactedCallers = append(report.ImpactedCallers, c)
	}
	sort.Strings(report.ImpactedCallers)

	// Assess Risk
	if len(report.ImpactedCallers) > 15 || len(report.ModifiedSymbols) > 10 {
		report.EstimatedRisk = "HIGH"
	} else if len(report.ImpactedCallers) > 3 || len(report.ModifiedSymbols) > 3 {
		report.EstimatedRisk = "MEDIUM"
	} else {
		report.EstimatedRisk = "LOW"
	}

	return report, nil
}

// addedCodeInSpan reports whether the diff between from..to adds any
// non-comment line within the new-side span [start, end] of file. Comment-only
// additions (e.g. a trailing comment appended after a method) report false so
// symbols aren't flagged as Modified for doc/comment churn. A git failure is
// conservative: it reports true (keep the symbol flagged).
func addedCodeInSpan(root, from, to, file string, start, end int) bool {
	args := []string{"-C", root, "diff", "-U0"}
	if from == "" && to == "" {
		args = append(args, "HEAD")
	} else {
		args = append(args, from+".."+to)
	}
	args = append(args, "--", file)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return true
	}
	newLine := 0
	inHunk := false
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "@@") {
			// @@ -a,b +c,d @@ — new-side start is c.
			parts := strings.Split(line, " ")
			if len(parts) >= 3 && strings.HasPrefix(parts[2], "+") {
				ns := strings.TrimPrefix(strings.Split(parts[2], ",")[0], "+")
				if n, err := strconv.Atoi(ns); err == nil {
					newLine = n - 1
				}
			}
			inHunk = true
			continue
		}
		if !inHunk {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			newLine++
			if newLine >= start && newLine <= end && !commentOnly(line[1:], file) {
				return true
			}
		case strings.HasPrefix(line, "-"):
			// Deletions don't advance the new-side line counter.
		default:
			newLine++ // Context lines advance it too.
		}
	}
	return false
}

// commentOnly reports whether the (possibly indented) content is a pure
// comment line for the file's language. Unknown languages are treated as code
// (conservative: a symbol is kept flagged).
func commentOnly(content, file string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return true
	}
	switch strings.ToLower(filepath.Ext(file)) {
	case ".py", ".rb", ".sh", ".yaml", ".yml", ".toml", ".ini", ".cfg":
		return strings.HasPrefix(trimmed, "#")
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".rs", ".c", ".h", ".cpp", ".hpp",
		".java", ".swift", ".kt", ".php", ".css":
		return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*")
	default:
		return false
	}
}

// Render formats the SemanticDiffReport into a clean, concise, token-optimized summary.
func (r *SemanticDiffReport) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "SEMANTIC DIFF: %s → %s [Risk: %s]\n", r.FromRef, r.ToRef, r.EstimatedRisk)
	fmt.Fprintf(&b, "====================================================\n")
	fmt.Fprintf(&b, "Files changed: %d | Modified AST symbols: %d | Impacted callers: %d\n\n",
		r.FilesChanged, len(r.ModifiedSymbols), len(r.ImpactedCallers))

	if len(r.ModifiedSymbols) > 0 {
		fmt.Fprintf(&b, "Modified Symbols:\n")
		for _, m := range r.ModifiedSymbols {
			fmt.Fprintf(&b, "  • %s %s (%s:%d)\n", m.Kind, m.Symbol, m.File, m.NewLine)
		}
		fmt.Fprintln(&b)
	}

	if len(r.ImpactedCallers) > 0 {
		fmt.Fprintf(&b, "Impacted Callers (%d):\n", len(r.ImpactedCallers))
		for i, c := range r.ImpactedCallers {
			if i >= 15 {
				fmt.Fprintf(&b, "  … and %d more callers\n", len(r.ImpactedCallers)-15)
				break
			}
			fmt.Fprintf(&b, "  ← %s\n", c)
		}
		fmt.Fprintln(&b)
	}

	if len(r.ModifiedSymbols) == 0 && r.FilesChanged > 0 {
		fmt.Fprintln(&b, "Notice: Changes appear to be in comments, formatting, or files without callable symbols.")
	}

	return strings.TrimSpace(b.String())
}
