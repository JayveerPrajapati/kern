package intel

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// Change is the impact analysis for a single changed file.
type Change struct {
	File     string      `json:"file"`
	Ranges   []LineRange `json:"ranges,omitempty"` // added-line ranges from the diff (empty = whole file)
	Symbols  []string    `json:"symbols"`
	Risk     float64     `json:"risk"`
	Callers  int         `json:"callers"`   // direct callers of changed symbols
	Blast    int         `json:"blast"`     // transitive callers reachable from changed symbols
	Files    int         `json:"files"`     // distinct files in the blast radius
	Tested   bool        `json:"tested"`    // every changed symbol is covered by tests
	Gaps     []string    `json:"gaps"`      // changed symbols with no test coverage
	CrossPkg bool        `json:"cross_pkg"` // changed symbols called from other packages
}

// ChangesReport is the full change-impact analysis.
type ChangesReport struct {
	Files     []string `json:"files"`
	Changes   []Change `json:"changes"`
	TotalRisk float64  `json:"total_risk"`
	Summary   string   `json:"summary"`
	Untested  int      `json:"untested"`
	MaxBlast  string   `json:"max_blast"`
	// SourceTokens is the estimated token size of reading the changed files in
	// full — the naive baseline the graph analysis avoids.
	SourceTokens int `json:"source_tokens"`
	// DeliveredTokens is the token size of the rendered compact output.
	DeliveredTokens int `json:"delivered_tokens"`
}

// AnalyzeChanges maps changed files onto the index and computes blast radius,
// risk and test-gap information for each one. Every symbol of a changed file is
// considered (no line information).
func AnalyzeChanges(ix *index.Index, files []string) *ChangesReport {
	changes := make([]FileChange, len(files))
	for i, f := range files {
		changes[i] = FileChange{File: f}
	}
	return AnalyzeChangesRanged(ix, changes)
}

// AnalyzeChangesRanged is the line-aware variant: for files with added-line
// ranges, only symbols whose declaration overlaps a changed range count as
// changed. Symbols span [Line, End] in current-file line numbers, matching the
// "+" side of the diff. A one-line edit no longer flags every symbol in a
// 500-line file. Files with empty ranges (deletions, --file, no diff info)
// fall back to whole-file analysis.
func AnalyzeChangesRanged(ix *index.Index, changes []FileChange) *ChangesReport {
	covered := coveredSet(ix)
	fileMap := buildFileMap(ix)
	hubs := hubSet(ix)

	var report ChangesReport
	bestBlast := 0
	for _, fc := range changes {
		f := fc.File
		report.Files = append(report.Files, f)
		// Only files present in the index matter for graph analysis.
		if _, indexed := ix.FileHashes[f]; !indexed {
			continue
		}
		changed := changedSymbols(ix, f, fc.Ranges)
		if len(changed) == 0 {
			continue
		}
		_, blastDist := BlastRadius(ix, changed)
		var callers, transitive int
		seenCaller := map[string]bool{}
		for _, s := range changed {
			for _, c := range prodCallers(ix, s) {
				if !seenCaller[c] {
					seenCaller[c] = true
					callers++
				}
			}
		}
		for s := range blastDist {
			if !seenCaller[s] && !contains(changed, s) {
				transitive++
			}
		}

		risk := 1.0
		risk += math.Log2(1 + float64(callers))
		risk += math.Log2(1 + float64(transitive))

		allTested := true
		var gaps []string
		for _, s := range changed {
			if !isCovered(covered, s) {
				allTested = false
				gaps = append(gaps, s)
			}
			if hubs[s] {
				risk += 1.0
			}
			// A changed symbol that calls into a hub broadcasts its change
			// through every caller of the hub.
			for _, c := range ix.Calls[s] {
				if hubs[c] {
					risk += 0.5
					break
				}
			}
		}
		dirs := map[string]bool{}
		for _, c := range seenCallerSet(ix, changed) {
			if d := dirOf(fileMap, c); d != "" {
				dirs[d] = true
			}
		}
		crossPkg := len(dirs) > 1
		if crossPkg {
			risk += 1.5
		}
		if !allTested {
			risk += 2.0
		}

		blastN := callers + transitive
		report.Changes = append(report.Changes, Change{
			File:     f,
			Ranges:   fc.Ranges,
			Symbols:  changed,
			Risk:     math.Round(risk*10) / 10,
			Callers:  callers,
			Blast:    blastN,
			Files:    len(AffectedFiles(ix, blastKeys(blastDist))),
			Tested:   allTested,
			Gaps:     gaps,
			CrossPkg: crossPkg,
		})
		report.TotalRisk += risk
		if blastN > bestBlast {
			bestBlast = blastN
			report.MaxBlast = f
		}
	}

	sort.Slice(report.Changes, func(i, j int) bool {
		if report.Changes[i].Risk != report.Changes[j].Risk {
			return report.Changes[i].Risk > report.Changes[j].Risk
		}
		return report.Changes[i].File < report.Changes[j].File
	})
	report.TotalRisk = math.Round(report.TotalRisk*10) / 10
	for _, c := range report.Changes {
		report.Untested += len(c.Gaps)
	}
	if len(report.Changes) == 0 {
		report.Summary = "no indexed symbols changed"
	} else {
		report.Summary = fmt.Sprintf("%d files changed, %d with symbol impact, total risk %.1f",
			len(report.Files), len(report.Changes), report.TotalRisk)
	}
	report.SourceTokens = sourceTokensForFiles(ix, report.Files)
	return &report
}

// sourceTokensForFiles estimates how many tokens it would cost to read the
// given files in full (the baseline the compact analysis avoids).
// changedSymbols returns the non-test symbols of f that the diff ranges touch.
// Empty ranges mean the whole file is considered changed.
func changedSymbols(ix *index.Index, f string, ranges []LineRange) []string {
	syms := ix.SymbolsByFile[f]
	out := make([]string, 0, len(syms))
	for _, s := range syms {
		if isTestFile(s.File) {
			continue
		}
		if len(ranges) > 0 && !overlap(s.Line, s.End, ranges) {
			continue
		}
		out = append(out, s.FullName())
	}
	sort.Strings(out)
	return out
}

// overlap reports whether the inclusive symbol span [line, end] touches any of
// the changed ranges.
func overlap(line, end int, ranges []LineRange) bool {
	if end == 0 {
		end = line
	}
	for _, r := range ranges {
		if line <= r.End && end >= r.Start {
			return true
		}
	}
	return false
}

func sourceTokensForFiles(ix *index.Index, files []string) int {
	total := 0
	for _, f := range files {
		if _, indexed := ix.FileHashes[f]; !indexed {
			continue
		}
		if src, err := os.ReadFile(filepath.Join(ix.Root, f)); err == nil {
			total += tokenize.Count(string(src))
		}
	}
	return total
}

// SavingsPanel renders the compact token-savings line used by changes/review.
func SavingsPanel(source, delivered int) string {
	if source <= 0 || delivered >= source {
		return ""
	}
	saved := source - delivered
	pct := 100.0 * float64(saved) / float64(source)
	return fmt.Sprintf("Token Savings: full files ~%d tokens → %d delivered (saved %d, %.1f%%)",
		source, delivered, saved, pct)
}

func blastKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	return out
}

func seenCallerSet(ix *index.Index, symbols []string) []string {
	seen := map[string]bool{}
	for _, s := range symbols {
		for _, c := range ix.Callers[s] {
			seen[c] = true
		}
	}
	var out []string
	for c := range seen {
		out = append(out, c)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// RenderChanges returns a compact human-readable change-impact table.
func RenderChanges(report *ChangesReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "range: %s\n", report.Summary)
	for _, c := range report.Changes {
		status := "tested"
		if !c.Tested {
			status = "TEST GAP"
		}
		flag := ""
		if c.CrossPkg {
			flag = "  CROSS-PACKAGE"
		}
		where := ""
		if len(c.Ranges) > 0 {
			where = "  " + renderRanges(c.Ranges)
		}
		fmt.Fprintf(&b, "  %-38s risk %-4.1f  %2d callers  %3d blast  %2d files  %-8s%s%s\n",
			c.File, c.Risk, c.Callers, c.Blast, c.Files, status, flag, where)
		if len(c.Gaps) > 0 {
			fmt.Fprintf(&b, "      gaps: %s\n", strings.Join(c.Gaps, ", "))
		}
	}
	if len(report.Changes) == 0 {
		b.WriteString("  (no indexed symbols in the changed files)\n")
	}
	out := strings.TrimSuffix(b.String(), "\n")
	report.DeliveredTokens = tokenize.Count(out)
	if panel := SavingsPanel(report.SourceTokens, report.DeliveredTokens); panel != "" {
		out += "\n" + panel
	}
	return out
}

// Review renders a token-optimised review context for the changed files:
// the changed symbols, their direct callers, blast radius, risk and test
// gaps — sized to fit within maxTokens. Every symbol of a changed file is
// considered.
func Review(ix *index.Index, files []string, maxTokens int) string {
	changes := make([]FileChange, len(files))
	for i, f := range files {
		changes[i] = FileChange{File: f}
	}
	return ReviewRanged(ix, changes, maxTokens)
}

// ReviewRanged is the line-aware variant of Review: symbol impact is scoped to
// the added-line ranges of the diff, and each changed symbol is shown with its
// file:line span.
func ReviewRanged(ix *index.Index, changes []FileChange, maxTokens int) string {
	if maxTokens <= 0 {
		maxTokens = 8000
	}
	report := AnalyzeChangesRanged(ix, changes)
	var b strings.Builder
	fmt.Fprintf(&b, "# kern review\n\n%s\n\n", report.Summary)

	for _, c := range report.Changes {
		span := ""
		if len(c.Ranges) > 0 {
			span = " " + renderRanges(c.Ranges)
		}
		fmt.Fprintf(&b, "## %s (risk %.1f)%s\n", c.File, c.Risk, span)
		fmt.Fprintf(&b, "changed (%d): %s\n", len(c.Symbols), strings.Join(c.Symbols, ", "))
		fmt.Fprintf(&b, "blast radius: %d callers (direct) · %d transitive · %d files", c.Callers, c.Blast-c.Callers, c.Files)
		if c.CrossPkg {
			b.WriteString(" · CROSS-PACKAGE")
		}
		b.WriteString("\n")
		spans := symbolSpans(ix, c.File, c.Symbols)
		for _, s := range c.Symbols {
			where := ""
			if r, ok := spans[s]; ok {
				where = fmt.Sprintf("  (%s:%d-%d)", c.File, r.Start, r.End)
			}
			callers := dedupe(prodCallers(ix, s))
			if len(callers) > 0 {
				fmt.Fprintf(&b, "  %s%s callers: %s\n", s, where, strings.Join(callers[:min(len(callers), 8)], ", "))
			} else {
				fmt.Fprintf(&b, "  %s%s\n", s, where)
			}
		}
		if len(c.Gaps) > 0 {
			fmt.Fprintf(&b, "  TEST GAPS: %s\n", strings.Join(c.Gaps, ", "))
		}
		b.WriteString("\n")
	}

	if report.Untested > 0 {
		fmt.Fprintf(&b, "untested changes: %d — add tests for the gapped symbols above\n", report.Untested)
	} else {
		b.WriteString("all changed symbols are covered by tests\n")
	}

	raw := strings.TrimSuffix(b.String(), "\n")
	report.DeliveredTokens = tokenize.Count(raw)
	if maxTokens > 0 && tokenize.Count(raw) > maxTokens {
		raw = budget.Fit(raw, maxTokens)
		report.DeliveredTokens = tokenize.Count(raw)
	}
	if panel := SavingsPanel(report.SourceTokens, report.DeliveredTokens); panel != "" {
		raw += "\n\n" + panel
	}
	return raw
}

// renderRanges renders changed line ranges as "lines 12-14, 90".
func renderRanges(ranges []LineRange) string {
	var parts []string
	for _, r := range ranges {
		if r.Start == r.End {
			parts = append(parts, fmt.Sprintf("%d", r.Start))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", r.Start, r.End))
		}
	}
	return "lines " + strings.Join(parts, ", ")
}

// symbolSpans maps changed symbol FullNames to their declaration spans in f.
func symbolSpans(ix *index.Index, f string, names []string) map[string]LineRange {
	out := map[string]LineRange{}
	for _, s := range ix.SymbolsByFile[f] {
		if s.End == 0 {
			s.End = s.Line
		}
		out[s.FullName()] = LineRange{Start: s.Line, End: s.End}
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
