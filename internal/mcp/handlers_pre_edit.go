package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// handlePreEdit performs predictive blast-radius, caller sensitivity, and test-gap analysis
// BEFORE code is written or edited. Agents call this to know exactly what can break and which
// callers must be verified before making changes.
func (s *Server) handlePreEdit(ctx context.Context, args map[string]any) (string, error) {
	file := argString(args, "file")
	symbol := argString(args, "symbol")
	linesStr := argString(args, "lines")
	root := resolveRoot(argString(args, "root"))

	if file == "" && symbol == "" {
		return "", fmt.Errorf("at least one of 'file' or 'symbol' must be provided")
	}

	ix, err := s.loadIndex(ctx, root)
	if err != nil {
		return "", fmt.Errorf("load index: %w", err)
	}

	// Parse optional line range, e.g. "45-90" or "45"
	startLine, endLine := -1, -1
	if linesStr != "" {
		parts := strings.Split(linesStr, "-")
		if len(parts) == 1 {
			if n, err := strconv.Atoi(strings.TrimSpace(parts[0])); err == nil {
				startLine, endLine = n, n
			}
		} else if len(parts) >= 2 {
			n1, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
			n2, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err1 == nil && err2 == nil {
				startLine, endLine = n1, n2
				if startLine > endLine {
					startLine, endLine = endLine, startLine
				}
			}
		}
	}

	// Identify symbols targeted by the edit
	var targetSymbols []index.Symbol
	cleanRelFile := ""
	if file != "" {
		cleanRelFile = filepath.Clean(file)
		if filepath.IsAbs(cleanRelFile) {
			if rel, err := filepath.Rel(root, cleanRelFile); err == nil {
				cleanRelFile = rel
			}
		}
	}

	for _, sym := range ix.Symbols {
		if symbol != "" && (sym.Name == symbol || sym.FullName() == symbol) {
			targetSymbols = append(targetSymbols, sym)
			continue
		}
		if cleanRelFile != "" && (filepath.Clean(sym.File) == cleanRelFile || strings.HasSuffix(filepath.Clean(sym.File), cleanRelFile)) {
			if startLine > 0 && endLine > 0 {
				if sym.Line >= startLine && sym.Line <= endLine {
					targetSymbols = append(targetSymbols, sym)
				}
			} else {
				targetSymbols = append(targetSymbols, sym)
			}
		}
	}

	// If a symbol was explicitly passed but not found, check with search
	if len(targetSymbols) == 0 && symbol != "" {
		matches := ix.Search(symbol, 5)
		if len(matches) > 0 {
			targetSymbols = append(targetSymbols, matches[0])
		}
	}

	// Analyze callers and blast radius
	directCallers := make(map[string]bool)
	transitiveCallers := make(map[string]bool)
	untestedSymbols := make(map[string]bool)

	// Load coverage to check test gaps
	cov := intel.AnalyzeCoverage(ix)
	testCoverageMap := make(map[string]bool)
	for _, g := range cov.HotGaps {
		untestedSymbols[g.Symbol] = true
	}

	for _, sym := range targetSymbols {
		fullName := sym.FullName()
		callers := ix.CallersOf(fullName)
		for _, c := range callers {
			directCallers[c] = true
			transitiveCallers[c] = true
			// 2nd degree callers
			for _, c2 := range ix.CallersOf(c) {
				transitiveCallers[c2] = true
			}
		}
	}

	directList := sortedMapKeys(directCallers)
	transitiveList := sortedMapKeys(transitiveCallers)

	// Assess Risk
	risk := "LOW"
	if len(directList) > 10 || len(transitiveList) > 25 {
		risk = "HIGH"
	} else if len(directList) > 3 || len(transitiveList) > 8 {
		risk = "MEDIUM"
	}

	// Check boundary rules if present
	var boundaryWarnings []string
	if boundaries, _ := intel.LoadBoundaries(root); boundaries != nil && len(boundaries.Rules) > 0 && cleanRelFile != "" {
		for _, r := range boundaries.Rules {
			if r.Action == "forbid" {
				if strings.Contains(cleanRelFile, r.From) {
					boundaryWarnings = append(boundaryWarnings, fmt.Sprintf("File %s matches guardrail rule '%s -> %s' (action: forbid)", cleanRelFile, r.From, r.To))
				}
			}
		}
	}

	// Format response
	var b strings.Builder
	fmt.Fprintf(&b, "PRE-EDIT PREDICTIVE IMPACT REPORT [Risk: %s]\n", risk)
	fmt.Fprintf(&b, "=================================================\n")
	if file != "" {
		fmt.Fprintf(&b, "Target File:   %s", file)
		if startLine > 0 {
			fmt.Fprintf(&b, " (lines %d-%d)", startLine, endLine)
		}
		fmt.Fprintln(&b)
	}
	if symbol != "" {
		fmt.Fprintf(&b, "Target Symbol: %s\n", symbol)
	}

	fmt.Fprintf(&b, "Matched Symbols (%d):\n", len(targetSymbols))
	for _, sym := range targetSymbols {
		untestedMark := ""
		if untestedSymbols[sym.FullName()] {
			untestedMark = " [UNTESTED HOTSPOT]"
		}
		fmt.Fprintf(&b, "  • %s %s (%s:%d)%s\n", sym.Kind, sym.FullName(), sym.File, sym.Line, untestedMark)
	}
	if len(targetSymbols) == 0 {
		fmt.Fprintln(&b, "  (No specific AST symbols found in range — edits will affect file/formatting directly)")
	}

	fmt.Fprintf(&b, "\nDirect Callers (%d):\n", len(directList))
	for i, c := range directList {
		if i >= 15 {
			fmt.Fprintf(&b, "  … and %d more direct callers\n", len(directList)-15)
			break
		}
		fmt.Fprintf(&b, "  ← %s\n", c)
	}

	fmt.Fprintf(&b, "\nTransitive Blast Radius (%d symbols across codebase)\n", len(transitiveList))
	if len(boundaryWarnings) > 0 {
		fmt.Fprintf(&b, "\nArchitectural Warnings (%d):\n", len(boundaryWarnings))
		for _, w := range boundaryWarnings {
			fmt.Fprintf(&b, "  ⚠️  %s\n", w)
		}
	}

	fmt.Fprintf(&b, "\nRecommendation: ")
	switch risk {
	case "HIGH":
		fmt.Fprintln(&b, "Critical symbol or hub file. Suggest running tests before/after and checking direct callers.")
	case "MEDIUM":
		fmt.Fprintln(&b, "Moderate blast radius. Verify direct callers after edit.")
	default:
		fmt.Fprintln(&b, "Low impact change. Safe to proceed.")
	}

	_ = testCoverageMap
	return strings.TrimSpace(b.String()), nil
}

func sortedMapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
