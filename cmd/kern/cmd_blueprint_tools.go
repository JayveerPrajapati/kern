package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	bpmcp "github.com/JayveerPrajapati/kern/internal/blueprint/mcp"
)

// Blueprint change-firewall CLI surface. These are the thin CLI twins of
// the kern_validate_proposed / kern_explain_finding / kern_repair_guidance
// MCP tools (same handlers); kern_validate_staged is served by the existing
// `kern diff-gate`.

// runBlueprintToolCLI executes one blueprint handler with map args built
// from --root/--source/--files/--finding flags and prints the result.
func runBlueprintToolCLI(rest []string, h bpmcp.ToolHandler, build func(root, source, payload string) map[string]any, usage string) int {
	root, source, payload := ".", "agent", ""
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--root":
			if i+1 < len(rest) {
				root = rest[i+1]
				i++
			}
		case "--source":
			if i+1 < len(rest) {
				source = rest[i+1]
				i++
			}
		case "--files", "--finding":
			if i+1 < len(rest) {
				payload = rest[i+1]
				i++
			}
		case "-h", "--help":
			fmt.Fprintln(os.Stderr, usage)
			return 0
		default:
			fmt.Fprintf(os.Stderr, "unknown flag %q\n%s\n", rest[i], usage)
			return 2
		}
	}
	if payload == "" {
		fmt.Fprintf(os.Stderr, "missing required payload flag\n%s\n", usage)
		return 2
	}
	args := build(root, source, payload)
	var raw json.RawMessage
	if b, err := json.Marshal(args); err == nil {
		raw = b
	} else {
		fmt.Fprintf(os.Stderr, "invalid arguments: %v\n", err)
		return 2
	}
	res := h.Handle(bgCtx(), raw)
	var texts []string
	for _, c := range res.Content {
		if c.Type == "text" {
			texts = append(texts, c.Text)
		}
	}
	out := strings.TrimSpace(strings.Join(texts, "\n"))
	if out != "" {
		fmt.Println(out)
	}
	if res.IsError {
		return 1
	}
	return 0
}

func bgCtx() context.Context { return context.Background() }

func runValidateProposed(rest []string) {
	usage := "usage: kern validate-proposed --files <json> [--root ROOT] [--source SRC]\n  options:\n    --files            JSON array of proposed changes [{\"path\",\"content\",\"op\"}]\n    --root             repository root (default: .)\n    --source           agent identity (default: agent)"
	build := func(root, source, payload string) map[string]any {
		var files []any
		_ = json.Unmarshal([]byte(payload), &files)
		return map[string]any{"repo": root, "source": source, "files": files}
	}
	os.Exit(runBlueprintToolCLI(rest, bpmcp.ValidateProposedHandler{}, build, usage))
}

func runExplainFinding(rest []string) {
	usage := "usage: kern explain-finding --finding <json> [--root ROOT]"
	build := func(root, source, payload string) map[string]any {
		var finding any
		_ = json.Unmarshal([]byte(payload), &finding)
		return map[string]any{"repo": root, "finding": finding}
	}
	os.Exit(runBlueprintToolCLI(rest, bpmcp.ExplainFindingHandler{}, build, usage))
}

func runRepairGuidance(rest []string) {
	usage := "usage: kern repair-guidance --finding <json> [--root ROOT]"
	build := func(root, source, payload string) map[string]any {
		var finding any
		_ = json.Unmarshal([]byte(payload), &finding)
		return map[string]any{"repo": root, "finding": finding}
	}
	os.Exit(runBlueprintToolCLI(rest, bpmcp.RepairGuidanceHandler{}, build, usage))
}
