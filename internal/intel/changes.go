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
	File     string   `json:"file"`
	Symbols  []string `json:"symbols"`
	Risk     float64  `json:"risk"`
	Callers  int      `json:"callers"`   // direct callers of changed symbols
	Blast    int      `json:"blast"`     // transitive callers reachable from changed symbols
	Files    int      `json:"files"`     // distinct files in the blast radius
	Tested   bool     `json:"tested"`    // every changed symbol is covered by tests
	Gaps     []string `json:"gaps"`      // changed symbols with no test coverage
	CrossPkg bool     `json:"cross_pkg"` // changed symbols called from other packages
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
// risk and test-gap information for each one.
func AnalyzeChanges(ix *index.Index, files []string) *ChangesReport {
	covered := coveredSet(ix)
	fileMap := buildFileMap(ix)
	hubs := hubSet(ix)

	var report ChangesReport
	bestBlast := 0
	for _, f := range files {
		report.Files = append(report.Files, f)
		// Only files present in the index matter for graph analysis.
		if _, indexed := ix.FileHashes[f]; !indexed {
			continue
		}
		changed := symbolsForFile(ix, f)
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
		fmt.Fprintf(&b, "  %-38s risk %-4.1f  %2d callers  %3d blast  %2d files  %-8s%s\n",
			c.File, c.Risk, c.Callers, c.Blast, c.Files, status, flag)
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
// gaps — sized to fit within maxTokens.
func Review(ix *index.Index, files []string, maxTokens int) string {
	if maxTokens <= 0 {
		maxTokens = 8000
	}
	report := AnalyzeChanges(ix, files)
	var b strings.Builder
	fmt.Fprintf(&b, "# kern review\n\n%s\n\n", report.Summary)

	for _, c := range report.Changes {
		fmt.Fprintf(&b, "## %s (risk %.1f)\n", c.File, c.Risk)
		fmt.Fprintf(&b, "changed (%d): %s\n", len(c.Symbols), strings.Join(c.Symbols, ", "))
		fmt.Fprintf(&b, "blast radius: %d callers (direct) · %d transitive · %d files", c.Callers, c.Blast-c.Callers, c.Files)
		if c.CrossPkg {
			b.WriteString(" · CROSS-PACKAGE")
		}
		b.WriteString("\n")
		for _, s := range c.Symbols {
			callers := dedupe(prodCallers(ix, s))
			if len(callers) > 0 {
				fmt.Fprintf(&b, "  %s callers: %s\n", s, strings.Join(callers[:min(len(callers), 8)], ", "))
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
