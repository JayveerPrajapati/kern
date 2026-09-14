package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/repair"
)

func (s *Server) handleRepairDiagnostics(ctx context.Context, args map[string]any) (string, error) {
	root := argString(args, "root")
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}

	compilerOutput := argString(args, "compiler_output")
	if strings.TrimSpace(compilerOutput) == "" {
		return "", fmt.Errorf("compiler_output is required")
	}

	apply := argString(args, "apply") == "true"

	results, err := repair.RepairRoot(root, compilerOutput, apply)
	if err != nil {
		return "", fmt.Errorf("repair error: %w", err)
	}

	if len(results) == 0 {
		return "No auto-repairable compiler diagnostics detected in output.", nil
	}

	if argString(args, "format") == "json" {
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
