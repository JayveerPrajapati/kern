// Package mutation owns the mutation testing MCP tool bodies (kern_mutation_test)
// as plain functions.
package mutation

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/mcp/root"
	"github.com/JayveerPrajapati/kern/internal/mutation"
)

// Test executes mutation testing and renders the resulting report.
func Test(ctx context.Context, args map[string]any) (string, error) {
	root := root.ResolveRoot(mcpargs.ArgString(args, "root"))

	maxMutants := 20
	if mStr := mcpargs.ArgString(args, "max_mutants"); mStr != "" {
		if n, err := strconv.Atoi(mStr); err == nil && n > 0 {
			maxMutants = n
		}
	}

	var files []string
	if fStr := mcpargs.ArgString(args, "files"); fStr != "" {
		files = strings.Split(fStr, ",")
	}

	dryRun := mcpargs.ArgString(args, "dry_run") == "true"
	testCmd := mcpargs.ArgString(args, "test_command")
	format := mcpargs.ArgString(args, "format")

	// Security: test_command is a client-supplied exec surface — parts[0] is
	// attacker-picked and runs arbitrary host code with the operator's
	// permissions — so it must pass the governance exec firewall, the same
	// KERN_ALLOW_EXEC / KERN_TOOLS gate as kern_exec/kern_sandbox. The
	// engine's derived default (go test . -count=1) is a fixed constant
	// inherent to the mutation feature and is not gated; only a
	// client-supplied command is.
	if strings.TrimSpace(testCmd) != "" {
		if err := governance.CheckExecCommand(testCmd, root, "kern_mutate_test"); err != nil {
			return "", err
		}
	}

	report, err := mutation.Run(ctx, mutation.Options{
		Root:        root,
		Files:       files,
		MaxMutants:  maxMutants,
		DryRun:      dryRun,
		TestCommand: testCmd,
		Isolate:     true, // mutants are evaluated in a worktree copy; the real tree is never touched
	})
	if err != nil {
		return "", fmt.Errorf("mutation test execution error: %w", err)
	}

	if format == "json" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "=== Mutation Testing Report (%d Mutants Generated) ===\n", report.TotalMutants)
	if !dryRun {
		fmt.Fprintf(&b, "Mutation Score:   %.1f%%\n", report.Score)
		fmt.Fprintf(&b, "Killed Mutants:   %d (Tests caught the regression)\n", report.KilledCount)
		fmt.Fprintf(&b, "Survived Mutants: %d (Test Gap / False-positive tests!)\n", report.SurvivedCount)
	}
	b.WriteString("\n--- Mutants Evaluated ---\n")

	for _, m := range report.Mutants {
		statusIcon := "🔍"
		if m.Status == "killed" {
			statusIcon = "✅ KILLED"
		} else if m.Status == "survived" {
			statusIcon = "🚨 SURVIVED (TEST GAP)"
		} else if m.Status == "compile_error" {
			statusIcon = "⚠️ COMPILE ERROR"
		}

		fmt.Fprintf(&b, "[%s] %s:%d (%s)\n", statusIcon, m.File, m.Line, m.Operator)
		fmt.Fprintf(&b, "    Original:    %s\n", m.Original)
		fmt.Fprintf(&b, "    Mutated to:  %s\n\n", m.Replacement)
	}

	return b.String(), nil
}
