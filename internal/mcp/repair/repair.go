// Package repair owns compiler diagnostic auto-repair MCP tool bodies (kern_repair_diagnostics)
// as plain functions.
package repair

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/repair"
)

func resolveRoot(root string) string {
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			return filepath.Clean(cwd)
		}
		return "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		return filepath.Clean(abs)
	}
	return root
}

// Repair analyzes compiler diagnostic output and performs AST-level auto-repairs.
func Repair(ctx context.Context, args map[string]any) (string, error) {
	root := resolveRoot(mcpargs.ArgString(args, "root"))

	compilerOutput := mcpargs.ArgString(args, "compiler_output")
	if strings.TrimSpace(compilerOutput) == "" {
		return "", fmt.Errorf("compiler_output is required")
	}

	apply := mcpargs.ArgBool(args, "apply")

	results, err := repair.RepairRoot(root, compilerOutput, apply)
	if err != nil {
		return "", fmt.Errorf("repair error: %w", err)
	}

	if len(results) == 0 {
		return "No auto-repairable compiler diagnostics detected in output.", nil
	}

	if mcpargs.ArgString(args, "format") == "json" {
		data, _ := json.MarshalIndent(results, "", "  ")
		return string(data), nil
	}

	var b strings.Builder
	actionWord := "Previewed"
	if apply {
		actionWord = "Applied"
	}
	fmt.Fprintf(&b, "### Compiler Diagnostic Auto-Repair (%s %d files)\n\n", actionWord, len(results))
	for _, res := range results {
		status := "✅ Repaired"
		if !res.Repaired {
			status = "⚠️ Skipped"
			if res.Error != "" {
				status = fmt.Sprintf("❌ Error (%s)", res.Error)
			}
		}
		fmt.Fprintf(&b, "- **%s**: %s — *%s*\n", res.File, status, res.Action)
	}

	return b.String(), nil
}
