package intel

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// RenderExploreConcise renders the concise read-path view of an explore
// report: the resolution header plus counts (callers/callees/blast) and a
// risk tier. It is the default output of `kern context` and `kern explore`;
// `--mode evidence` renders the full report (RenderExplore / the context
// slice) unchanged.
func RenderExploreConcise(r *ExploreReport) string {
	if r == nil {
		return ""
	}
	def := r.Definition
	name := r.Resolved
	if name == "" {
		name = def.FullName()
	}
	var b strings.Builder
	// Surface a fuzzy/qualified substitution explicitly, never silently.
	if r.Symbol != "" && r.Symbol != name {
		fmt.Fprintf(&b, "# resolved %s -> %s\n", r.Symbol, name)
	}
	fmt.Fprintf(&b, "# %s (%s) — %s:%d", name, def.Kind, def.File, def.Line)
	if def.End > def.Line {
		fmt.Fprintf(&b, "-%d", def.End)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "callers: %d\n", len(r.Callers))
	fmt.Fprintf(&b, "callees: %d\n", len(r.Callees))
	fmt.Fprintf(&b, "blast: %d symbols / %d files\n", len(r.BlastRadius), len(r.BlastFiles))
	// Risk uses the report's own deduped caller + blast-radius counts with
	// the same thresholds as the pre-edit verdict (AssessTargets). It is NOT
	// name-wide: a bare name such as a test fixture recurs across files, and
	// folding every same-named definition would misreport this one's radius.
	fmt.Fprintf(&b, "risk: %s", conciseRisk(len(r.Callers), len(r.BlastRadius)))
	return b.String()
}

// RenderExploreBalanced renders the middle-ground read-path view of an
// explore report: the concise header and counts plus the top callers,
// callees, and affected files, but no full source block. It is the explicit
// mode between "triage only" and "full evidence".
func RenderExploreBalanced(r *ExploreReport) string {
	if r == nil {
		return ""
	}
	def := r.Definition
	name := r.Resolved
	if name == "" {
		name = def.FullName()
	}
	var b strings.Builder
	if r.Symbol != "" && r.Symbol != name {
		fmt.Fprintf(&b, "# resolved %s -> %s\n", r.Symbol, name)
	}
	fmt.Fprintf(&b, "# %s (%s) — %s:%d", name, def.Kind, def.File, def.Line)
	if def.End > def.Line {
		fmt.Fprintf(&b, "-%d", def.End)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "callers: %d\n", len(r.Callers))
	fmt.Fprintf(&b, "callees: %d\n", len(r.Callees))
	fmt.Fprintf(&b, "blast: %d symbols / %d files\n", len(r.BlastRadius), len(r.BlastFiles))
	fmt.Fprintf(&b, "risk: %s\n", conciseRisk(len(r.Callers), len(r.BlastRadius)))

	const topN = 5
	writeTop := func(label string, values []string, locs map[string]string) {
		if len(values) == 0 {
			return
		}
		sorted := append([]string(nil), values...)
		sort.Strings(sorted)
		if len(sorted) > topN {
			sorted = sorted[:topN]
		}
		fmt.Fprintf(&b, "\n%s:\n", label)
		for _, value := range sorted {
			if loc, ok := locs[value]; ok && loc != "" {
				fmt.Fprintf(&b, "- %s — %s\n", value, loc)
			} else {
				fmt.Fprintf(&b, "- %s\n", value)
			}
		}
	}
	writeTop("top callers", r.Callers, r.CallerLocs)
	writeTop("top callees", r.Callees, nil)
	if len(r.BlastFiles) > 0 {
		files := append([]string(nil), r.BlastFiles...)
		sort.Strings(files)
		if len(files) > topN {
			files = files[:topN]
		}
		b.WriteString("\ntop affected files:\n")
		for _, file := range files {
			fmt.Fprintf(&b, "- %s\n", file)
		}
	}
	b.WriteString("\nfull evidence: mode=evidence\n")
	return strings.TrimSuffix(b.String(), "\n")
}

// conciseRisk maps direct-caller and blast-radius counts to the concise
// renderer's risk tier. RenderExploreConcise and ContextSummaryFor share it
// so the fast context path can never drift from the rendered report.
func conciseRisk(direct, trans int) string {
	if direct > riskHighDirect || trans > riskHighTransitive {
		return "high"
	}
	if direct > riskMedDirect || trans > riskMedTransitive {
		return "medium"
	}
	return "low"
}

// TokenFooter renders the concise-vs-evidence token footer ("tokens: N → M
// (P% smaller)") using internal/tokenize for both counts. It returns "" when
// the concise render is not actually smaller, so callers can append it
// unconditionally. Text output only — JSON paths never call it.
func TokenFooter(evidence, concise string) string {
	before := tokenize.Count(evidence)
	after := tokenize.Count(concise)
	if before <= 0 || after >= before {
		return ""
	}
	pct := ((before-after)*100 + before/2) / before
	return fmt.Sprintf("tokens: %d → %d (%d%% smaller)", before, after, pct)
}
