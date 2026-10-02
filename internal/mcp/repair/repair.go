// Package repair owns compiler diagnostic auto-repair MCP tool bodies
// (kern_repair action=diagnostics|guidance) as plain functions.
package repair

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcp/blueprint"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/mcp/root"
	"github.com/JayveerPrajapati/kern/internal/repair"
)

// Tool is the consolidated kern_repair dispatcher: the action argument
// selects the diagnostics or guidance body. Guidance is delegated to the
// blueprint leaf (the former kern_repair_guidance body) so behavior stays identical.
func Tool(ctx context.Context, args map[string]any) (string, error) {
	action := mcpargs.ArgString(args, "action")
	if action == "" {
		return "", fmt.Errorf("kern_repair: 'action' is required")
	}
	switch action {
	case "diagnostics":
		return Diagnostics(ctx, args)
	case "guidance":
		return blueprint.RepairGuidance(ctx, args)
	default:
		return "", fmt.Errorf("kern_repair: unknown action %q (want diagnostics|guidance)", action)
	}
}

// Diagnostics analyzes compiler diagnostic output and performs AST-level auto-repairs.
func Diagnostics(ctx context.Context, args map[string]any) (string, error) {
	root := root.ResolveRoot(mcpargs.ArgString(args, "root"))

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
