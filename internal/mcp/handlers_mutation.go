package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mutation"
)

func (s *Server) handleMutationTest(ctx context.Context, args map[string]any) (string, error) {
	root := argString(args, "root")
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}

	maxMutants := 20
	if mStr := argString(args, "max_mutants"); mStr != "" {
		if n, err := strconv.Atoi(mStr); err == nil && n > 0 {
			maxMutants = n
		}
	}

	var files []string
	if fStr := argString(args, "files"); fStr != "" {
		files = strings.Split(fStr, ",")
	}

	dryRun := argString(args, "dry_run") == "true"
	testCmd := argString(args, "test_command")
	format := argString(args, "format")

	report, err := mutation.Run(ctx, mutation.Options{
		Root:        root,
		Files:       files,
		MaxMutants:  maxMutants,
		DryRun:      dryRun,
		TestCommand: testCmd,
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
	b.WriteString(fmt.Sprintf("=== Mutation Testing Report (%d Mutants Generated) ===\n", report.TotalMutants))
	if !dryRun {
		b.WriteString(fmt.Sprintf("Mutation Score:   %.1f%%\n", report.Score))
		b.WriteString(fmt.Sprintf("Killed Mutants:   %d (Tests caught the regression)\n", report.KilledCount))
		b.WriteString(fmt.Sprintf("Survived Mutants: %d (Test Gap / False-positive tests!)\n", report.SurvivedCount))
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

		b.WriteString(fmt.Sprintf("[%s] %s:%d (%s)\n", statusIcon, m.File, m.Line, m.Operator))
		b.WriteString(fmt.Sprintf("    Original:    %s\n", m.Original))
		b.WriteString(fmt.Sprintf("    Mutated to:  %s\n", m.Replacement))
		b.WriteString("\n")
	}

	return b.String(), nil
}
