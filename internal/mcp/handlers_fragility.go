package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/fragility"
)

func (s *Server) handleFragilityHotspots(ctx context.Context, args map[string]any) (string, error) {
	root := argString(args, "root")
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}

	limit := 15
	if lStr := argString(args, "limit"); lStr != "" {
		if n, err := strconv.Atoi(lStr); err == nil && n > 0 {
			limit = n
		}
	}

	commits := 100
	if cStr := argString(args, "commits"); cStr != "" {
		if n, err := strconv.Atoi(cStr); err == nil && n > 0 {
			commits = n
		}
	}

	minFixes := 1
	if mStr := argString(args, "min_fixes"); mStr != "" {
		if n, err := strconv.Atoi(mStr); err == nil && n > 0 {
			minFixes = n
		}
	}

	target := argString(args, "target")
	format := argString(args, "format")

	report, err := fragility.Analyze(ctx, fragility.Options{
		Root:     root,
		Target:   target,
		Limit:    limit,
		Commits:  commits,
		MinFixes: minFixes,
	})
	if err != nil {
		return "", fmt.Errorf("fragility analysis error: %w", err)
	}

	if format == "json" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("=== Causal Defect & Fragility Hotspots (%d commits evaluated, %d bug-fix commits) ===\n\n",
		report.EvaluatedCommits, report.DefectCommitsCount))

	if len(report.Hotspots) == 0 {
		b.WriteString("No fragile components found matching criteria.\n")
		return b.String(), nil
	}

	for i, h := range report.Hotspots {
		riskIcon := "🟢"
		if h.RiskLevel == "CRITICAL" {
			riskIcon = "🔴 CRITICAL"
		} else if h.RiskLevel == "HIGH" {
			riskIcon = "🟠 HIGH"
		} else if h.RiskLevel == "MEDIUM" {
			riskIcon = "🟡 MEDIUM"
		}

		b.WriteString(fmt.Sprintf("[%d] %s: %s (score: %.1f)\n", i+1, riskIcon, h.Target, h.FragilityScore))
		b.WriteString(fmt.Sprintf("    Kind:          %s (in %s)\n", h.Kind, h.File))
		b.WriteString(fmt.Sprintf("    Fix Commits:   %d / %d total commits touched\n", h.DefectCommits, h.TotalCommits))
		b.WriteString(fmt.Sprintf("    Call Graph:    %d dependent callers\n", h.CallerCount))
		if len(h.TopDependents) > 0 {
			b.WriteString(fmt.Sprintf("    Dependents:    %s\n", strings.Join(h.TopDependents, ", ")))
		}
		if len(h.RecentFixes) > 0 {
			b.WriteString(fmt.Sprintf("    Recent Fixes:  %s\n", strings.Join(h.RecentFixes, "; ")))
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}
