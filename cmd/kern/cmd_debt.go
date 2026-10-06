package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/fragility"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// debtReport composes two existing engines into one debt view; every field
// is engine output passed through untouched (no new analysis lives here).
type debtReport struct {
	Root               string              `json:"root"`
	EvaluatedCommits   int                 `json:"evaluated_commits"`
	DefectCommitsCount int                 `json:"defect_commits_count"`
	Hotspots           []fragility.Hotspot `json:"hotspots"`
	Cycles             []intel.ImportCycle `json:"cycles"`
}

// runDebt prints a unified technical-debt report: defect-churn debt from
// fragility.Analyze (git fix history x call graph) and structural debt from
// intel.ImportCycles (Tarjan SCC over the package import graph). It is
// deliberately a pure composition layer over existing engines — the same
// analyses behind `kern fragility` and `kern cycles`, one prioritized view.
func runDebt(rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := "."
	if len(args) > 0 {
		root = args[0]
	}
	if f.root != "" {
		root = f.root
	}

	frag, err := fragility.Analyze(context.Background(), fragility.Options{
		Root:  root,
		Limit: 20,
	})
	if err != nil {
		fatal("fragility analysis: %v", err)
	}

	ix, err := index.LoadOrBuild(root)
	if err != nil {
		fatal("index load: %v", err)
	}
	cycles := intel.ImportCycles(ix)

	if f.json {
		printJSON(debtReport{
			Root:               root,
			EvaluatedCommits:   frag.EvaluatedCommits,
			DefectCommitsCount: frag.DefectCommitsCount,
			Hotspots:           frag.Hotspots,
			Cycles:             cycles,
		})
		return
	}

	fmt.Println("=== Kern Technical Debt Report ===")
	fmt.Printf("Repository Root:  %s\n", frag.Root)
	fmt.Printf("Commits Analyzed: %d (defect commits: %d)\n\n", frag.EvaluatedCommits, frag.DefectCommitsCount)

	fmt.Println("--- Defect-Churn Hotspots (fragility) ---")
	if len(frag.Hotspots) == 0 {
		fmt.Println("no fragility hotspots identified matching criteria")
	}
	for i, h := range frag.Hotspots {
		fmt.Printf("[%d] %s (%s) — %s\n", i+1, h.Target, h.Kind, fragilityRiskBadge(h.RiskLevel))
		fmt.Printf("    Fragility Score: %.2f (Defect Fixes: %d / %d commits, Callers: %d)\n",
			h.FragilityScore, h.DefectCommits, h.TotalCommits, h.CallerCount)
		if len(h.RecentFixes) > 0 {
			fmt.Printf("    Recent Fixes:    %s\n", strings.Join(h.RecentFixes, " | "))
		}
	}
	fmt.Println()
	fmt.Println("--- Import Cycles (structural debt) ---")
	fmt.Println(intel.RenderCycles(cycles))

	high := 0
	for _, h := range frag.Hotspots {
		if h.RiskLevel == "CRITICAL" || h.RiskLevel == "HIGH" {
			high++
		}
	}
	fmt.Printf("\nSummary: %d debt items (%d hotspots, %d critical/high; %d import cycles)\n",
		len(frag.Hotspots)+len(cycles), len(frag.Hotspots), high, len(cycles))
}

// fragilityRiskBadge renders the terminal risk badge for a fragility risk
// level (shared by `kern fragility` and `kern debt`).
func fragilityRiskBadge(level string) string {
	switch level {
	case "CRITICAL":
		return "🔥 CRITICAL RISK"
	case "HIGH":
		return "🚨 HIGH RISK"
	case "MEDIUM":
		return "⚠️ MEDIUM RISK"
	}
	return "🟢 LOW"
}
