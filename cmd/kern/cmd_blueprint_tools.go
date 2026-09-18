package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	bpmcp "github.com/JayveerPrajapati/kern/internal/bpcli/mcp"
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
	// A BLOCK verdict arrives as a SUCCESSFUL (isError=false) JSON payload
	// ({"status":"BLOCK","exit_code":1,...}) — the check itself ran fine but
	// the change must not pass. Mapping the CLI exit on IsError alone let a
	// blocked change exit 0, silently failing the pipeline (F6). Mirror the
	// verdict: BLOCK/ERROR status or a non-zero payload exit code → rc=1;
	// PASS (and non-blocking WARN) → 0.
	if status, code := blueprintVerdict(out); status == "BLOCK" || status == "ERROR" || code != 0 {
		return 1
	}
	return 0
}

// blueprintVerdict extracts the verdict from a validate-* JSON payload
// ({"status": ..., "exit_code": ...}). It returns ("", 0) when out is not a
// JSON object with a status field (e.g. explain-finding prose), so the caller
// falls back to res.IsError alone.
func blueprintVerdict(out string) (status string, exitCode int) {
	var v struct {
		Status   string `json:"status"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return "", 0
	}
	return strings.ToUpper(v.Status), v.ExitCode
}

func bgCtx() context.Context { return context.Background() }

func runValidateProposed(rest []string) {
	usage := "usage: kern validate-proposed --files <json> [--root ROOT] [--source SRC]\n  options:\n    --files            JSON array of proposed changes [{\"path\",\"content\",\"op\"}] (op: write|edit|delete|rename|commit, default write)\n    --root             repository root (default: .)\n    --source           agent identity (default: agent)"
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
		if payload != "" {
			if err := json.Unmarshal([]byte(payload), &finding); err != nil {
				fatalUsage("explain-finding: --finding must be a JSON object: %v", err)
			}
			if finding == nil {
				fatalUsage("explain-finding: --finding must be a JSON object (e.g. {\"rule_id\": \"format:gofmt\", \"file\": \"x.go\"})")
			}
		}
		return map[string]any{"repo": root, "finding": finding}
	}
	os.Exit(runBlueprintToolCLI(rest, bpmcp.ExplainFindingHandler{}, build, usage))
}

func runRepairGuidance(rest []string) {
	usage := "usage: kern repair-guidance --finding <json> [--root ROOT]"
	build := func(root, source, payload string) map[string]any {
		var finding any
		if payload != "" {
			if err := json.Unmarshal([]byte(payload), &finding); err != nil {
				fatalUsage("repair-guidance: --finding must be a JSON object: %v", err)
			}
		}
		return map[string]any{"repo": root, "finding": finding}
	}
	os.Exit(runBlueprintToolCLI(rest, bpmcp.RepairGuidanceHandler{}, build, usage))
}
