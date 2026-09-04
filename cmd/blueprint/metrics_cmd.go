package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/JayveerPrajapati/kern/internal/blueprint/metrics"
)

// runMetrics implements `blueprint metrics` — prints accumulated local metrics.
func runMetrics(args []string) int {
	fs := flag.NewFlagSet("metrics", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	repoRoot := fs.String("repo", "", "repository root (default: current directory)")
	jsonOut := fs.Bool("json", false, "emit JSON instead of human-readable")
	reset := fs.Bool("reset", false, "reset all metrics to zero")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0 // -h/--help: usage already printed; exit clean
		}
		return 2
	}

	root := *repoRoot
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "blueprint: %v\n", err)
			return 2
		}
		root = cwd
	}

	path := metrics.DefaultPath(root)

	if *reset {
		m := metrics.New()
		if err := m.Save(path); err != nil {
			fmt.Fprintf(os.Stderr, "blueprint: cannot reset metrics: %v\n", err)
			return 2
		}
		fmt.Fprintln(os.Stderr, "Metrics reset.")
		return 0
	}

	m, err := metrics.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "blueprint: cannot load metrics: %v\n", err)
		return 2
	}

	stats := m.ComputeStats()

	if *jsonOut {
		b, _ := json.MarshalIndent(stats, "", "  ")
		fmt.Println(string(b))
		return 0
	}

	// Human-readable report goes to stdout (the primary output of the
	// command); errors and status messages stay on stderr.
	fmt.Fprintln(os.Stdout, "━━━ Blueprint Metrics ━━━")
	fmt.Fprintf(os.Stdout, "Validations:    %d (pass=%d block=%d warn=%d error=%d)\n",
		stats.ValidationCount, stats.PassCount, stats.BlockedCount, stats.WarningCount, stats.ErrorCount)
	fmt.Fprintf(os.Stdout, "Latency p50:    %.2fms\n", stats.ValidationP50Ms)
	fmt.Fprintf(os.Stdout, "Latency p95:    %.2fms\n", stats.ValidationP95Ms)
	if stats.SandboxP50Ms > 0 {
		fmt.Fprintf(os.Stdout, "Sandbox p50:    %.2fms\n", stats.SandboxP50Ms)
		fmt.Fprintf(os.Stdout, "Sandbox p95:    %.2fms\n", stats.SandboxP95Ms)
	}
	if len(stats.PerCheckP50Ms) > 0 {
		fmt.Fprintln(os.Stdout, "Per-check latency:")
		for name, p50 := range stats.PerCheckP50Ms {
			p95 := stats.PerCheckP95Ms[name]
			fmt.Fprintf(os.Stdout, "  %-30s p50=%.2fms p95=%.2fms\n", name, p50, p95)
		}
	}
	if stats.RepairSuccessRate > 0 || stats.ValidationCount > 0 {
		fmt.Fprintf(os.Stdout, "Repair success: %.0f%%\n", stats.RepairSuccessRate*100)
	}
	if stats.FalsePositiveOverrides > 0 {
		fmt.Fprintf(os.Stdout, "FP overrides:   %d\n", stats.FalsePositiveOverrides)
	}
	fmt.Fprintf(os.Stdout, "Metrics file:   %s\n", path)
	return 0
}
