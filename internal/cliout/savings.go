package cliout

import (
	"fmt"
	"io"

	"github.com/JayveerPrajapati/kern/internal/strutil"
)

// PrintSavingsFooter writes the canonical savings banner as the last
// human-readable line of an optimization command: the percentage saved (vs
// the labeled denominator — "file read raw", "input verbatim", …), the
// number of tokens removed, and the estimated USD saved at the given
// cost-per-token rate (callers pass context.CostPerToken(), which honors the
// KERN_COST_PER_TOKEN override and defaults to 1e-5 $/token). The denominator
// label is mandatory: the percentage must never appear without saying what
// it is relative to (L2/honest reporting). It prints nothing when no tokens
// were actually saved. It is the shared home of the previously triplicated
// printSavingsFooter helper (cmd/kern/helpers.go, internal/graphcli/helpers.go,
// internal/contextcli/cli.go — consolidation tranche 8); call sites use it
// directly, no local wrapper.
func PrintSavingsFooter(w io.Writer, beforeTokens, afterTokens int, costPerToken float64, baseline string) {
	saved := beforeTokens - afterTokens
	if saved <= 0 {
		return
	}
	if baseline == "" {
		baseline = "input"
	}
	_, _ = fmt.Fprintf(w, "[%.0f%% vs %s ──> %d tokens ──> $%.4f]\n",
		strutil.Pct(beforeTokens, afterTokens), baseline, saved, float64(saved)*costPerToken)
}
