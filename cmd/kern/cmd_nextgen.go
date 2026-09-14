package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/fit"
	"github.com/JayveerPrajapati/kern/internal/fragility"
	"github.com/JayveerPrajapati/kern/internal/fw"
	"github.com/JayveerPrajapati/kern/internal/mutation"
	"github.com/JayveerPrajapati/kern/internal/refactor"
	"github.com/JayveerPrajapati/kern/internal/repair"
)

func runFitContext(rest []string) {
	f, _, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	maxTok := f.maxTokens
	if maxTok <= 0 {
		maxTok = 8000
	}

	var files []string
	if f.file != "" {
		files = strings.Split(f.file, ",")
	}
	var syms []string
	if f.symbol != "" {
		syms = strings.Split(f.symbol, ",")
	}

	res, err := fit.FitContext(context.Background(), fit.Request{
		Root:      root,
		MaxTokens: maxTok,
		Files:     files,
		Symbols:   syms,
		Query:     f.query,
	})
	if err != nil {
		fatal("fit-context: %v", err)
	}

	if f.json {
		printJSON(res)
		return
	}
	fmt.Print(res.Content)
}

func runRepairDiagnostics(rest []string) {
	f, _, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	compilerOut := f.compilerOutput
	if compilerOut == "" {
		fatalUsage("missing required --compiler-output")
	}

	results, err := repair.RepairRoot(root, compilerOut, f.apply)
	if err != nil {
		fatal("repair-diagnostics: %v", err)
	}

	if f.json {
		printJSON(results)
		return
	}

	repairedCount := 0
	for _, r := range results {
		if r.Repaired {
			repairedCount++
		}
	}

	if f.apply {
		fmt.Printf("Applied repairs to %d files:\n", repairedCount)
	} else {
		fmt.Printf("Proposed %d repairs (dry-run, pass --apply to commit):\n", len(results))
	}
	for _, r := range results {
		status := "PASS"
		if !r.Repaired {
			status = "SKIPPED"
			if r.Error != "" {
				status = "ERROR: " + r.Error
			}
		}
		fmt.Printf("  - [%s] %s: %s\n", status, r.File, r.Action)
	}
}

func runRefactorTransaction(rest []string) {
	f, _, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}

	var edits []refactor.FileEdit
	if f.edits != "" {
		if err := json.Unmarshal([]byte(f.edits), &edits); err != nil {
			fatal("invalid --edits JSON: %v", err)
		}
	} else if len(rest) > 0 {
		// Read from argument or file
		data, err := os.ReadFile(rest[0])
		if err == nil {
			_ = json.Unmarshal(data, &edits)
		}
	}

	if len(edits) == 0 {
		fatalUsage("missing or empty --edits")
	}

	res, err := refactor.ExecuteTransaction(context.Background(), refactor.TransactionRequest{
		Root:           root,
		Edits:          edits,
		CompileCommand: f.cmd,
		Apply:          f.apply,
	})
	if err != nil {
		fatal("refactor-transaction: %v", err)
	}

	if f.json {
		printJSON(res)
		return
	}

	if res.Success {
		if f.apply {
			fmt.Printf("SUCCESS: Atomic refactor committed (%d files)\n", len(res.ModifiedFiles))
		} else {
			fmt.Printf("SUCCESS: Verification passed in sandbox (%d files modified, dry-run)\n", len(res.ModifiedFiles))
		}
		if res.UnifiedDiff != "" {
			fmt.Printf("\n%s\n", res.UnifiedDiff)
		}
	} else {
		fmt.Printf("FAILED: Atomic rollback executed (0 files modified).\nError: %s\n%s\n", res.Error, res.CompilerOutput)
	}
}

func runFWTrace(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	filter := f.pattern
	if filter == "" && len(args) > 0 {
		filter = args[0]
	}

	res, err := fw.TraceRoutes(context.Background(), root, filter)
	if err != nil {
		fatal("fw-trace: %v", err)
	}

	if f.json {
		printJSON(res)
		return
	}

	fmt.Printf("=== Framework Route & Dependency Flow (%d routes traced) ===\n", res.Total)
	if len(res.Frameworks) > 0 {
		fmt.Printf("Detected Frameworks: %s\n", strings.Join(res.Frameworks, ", "))
	}
	fmt.Println()

	for i, r := range res.Routes {
		fmt.Printf("[%d] %s %s (%s)\n", i+1, r.Method, r.Path, r.Framework)
		fmt.Printf("    Declared in: %s:%d\n", r.File, r.Line)
		if len(r.Middleware) > 0 {
			fmt.Printf("    Middleware:  %s\n", strings.Join(r.Middleware, " -> "))
		}
		fmt.Printf("    Handler:     %s\n", r.Handler)
		if len(r.InjectedServices) > 0 {
			fmt.Printf("    Injected DI: %s\n", strings.Join(r.InjectedServices, ", "))
		}
		if len(r.DBModels) > 0 {
			fmt.Printf("    DB Models:   %s\n", strings.Join(r.DBModels, ", "))
		}
		fmt.Printf("    Pipeline:\n")
		for _, step := range r.Steps {
			fmt.Printf("      -> [%s] %s (%s:%d)\n", step.Stage, step.Symbol, step.File, step.Line)
		}
		fmt.Println()
	}
}

func runMutationTest(rest []string) {
	f, _, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	var files []string
	if f.file != "" {
		files = strings.Split(f.file, ",")
	}
	maxMuts := f.max
	if maxMuts <= 0 {
		maxMuts = 20
	}

	report, err := mutation.Run(context.Background(), mutation.Options{
		Root:        root,
		Files:       files,
		MaxMutants:  maxMuts,
		DryRun:      f.dryRun,
		TestCommand: f.cmd,
	})
	if err != nil {
		fatal("mutate: %v", err)
	}

	if f.json {
		printJSON(report)
		return
	}

	fmt.Printf("=== Mutation Testing Report (%d Mutants Generated) ===\n", report.TotalMutants)
	if !f.dryRun {
		fmt.Printf("Mutation Score:   %.1f%%\n", report.Score)
		fmt.Printf("Killed Mutants:   %d (Tests caught the regression)\n", report.KilledCount)
		fmt.Printf("Survived Mutants: %d (Test Gap / False-positive tests!)\n", report.SurvivedCount)
	}
	fmt.Println("\n--- Mutants Evaluated ---")

	for _, m := range report.Mutants {
		statusIcon := "🔍"
		switch m.Status {
		case "killed":
			statusIcon = "✅ KILLED"
		case "survived":
			statusIcon = "🚨 SURVIVED (TEST GAP)"
		case "compile_error":
			statusIcon = "⚠️ COMPILE ERROR"
		}

		fmt.Printf("[%s] %s:%d (%s)\n", statusIcon, m.File, m.Line, m.Operator)
		fmt.Printf("    Original:    %s\n", m.Original)
		fmt.Printf("    Mutated to:  %s\n\n", m.Replacement)
	}
}

func runFragility(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := "."
	if len(args) > 0 {
		root = args[0]
	}
	if f.root != "" {
		root = f.root
	}

	report, err := fragility.Analyze(context.Background(), fragility.Options{
		Root:  root,
		Limit: 20,
	})
	if err != nil {
		fatal("fragility analysis: %v", err)
	}

	if f.json {
		printJSON(report)
		return
	}

	fmt.Println("=== KernOps Fragility & Defect-Churn Map ===")
	fmt.Printf("Repository Root:   %s\n", root)
	fmt.Printf("Analyzed Commits:  %d\n", report.EvaluatedCommits)
	fmt.Printf("Hotspots Found:    %d\n\n", len(report.Hotspots))

	if len(report.Hotspots) == 0 {
		fmt.Println("No fragility hotspots identified matching criteria.")
		return
	}

	for i, h := range report.Hotspots {
		riskBadge := "🟢 LOW"
		switch h.RiskLevel {
		case "CRITICAL":
			riskBadge = "🔥 CRITICAL RISK"
		case "HIGH":
			riskBadge = "🚨 HIGH RISK"
		case "MEDIUM":
			riskBadge = "⚠️ MEDIUM RISK"
		}

		fmt.Printf("[%d] %s (%s) — %s\n", i+1, h.Target, h.Kind, riskBadge)
		fmt.Printf("    Fragility Score: %.2f (Defect Fixes: %d / %d commits, Callers: %d)\n",
			h.FragilityScore, h.DefectCommits, h.TotalCommits, h.CallerCount)
		if len(h.TopDependents) > 0 {
			fmt.Printf("    Top Callers:     %s\n", strings.Join(h.TopDependents, ", "))
		}
		if len(h.RecentFixes) > 0 {
			fmt.Printf("    Recent Fixes:    %s\n", strings.Join(h.RecentFixes, " | "))
		}
		fmt.Println()
	}
}
