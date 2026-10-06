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
// the kern_validate_proposed / kern_explain_finding / kern_repair (action=
// guidance) MCP tools (same handlers); kern_validate_staged is served by the
// existing `kern diff-gate`.

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
			if !strings.HasPrefix(rest[i], "-") {
				// L8: these are flag-only commands — a natural-language
				// positional ("add a Greet command to cmd/kern") must not read
				// as "unknown flag". Name the expected flags instead.
				fmt.Fprintf(os.Stderr, "kern: unexpected argument %q — this command takes flags only; expected: %s\n%s\n", rest[i], expectedFlags(usage), usage)
				return 2
			}
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

// expectedFlags extracts the "--flag" tokens from a usage string — its
// options block when present, and the first (usage:) line otherwise — so an
// NL-positional error can name what the command expects (L8: "unknown flag
// \"add a Greet command...\"" was unactionable).
func expectedFlags(usage string) string {
	seen := map[string]bool{}
	var flags []string
	add := func(t string) {
		if t == "" || seen[t] {
			return
		}
		seen[t] = true
		flags = append(flags, t)
	}
	for _, l := range strings.Split(usage, "\n") {
		for _, tok := range strings.Fields(l) {
			if strings.HasPrefix(tok, "--") {
				add(tok)
			}
		}
	}
	if len(flags) == 0 {
		return "--files <json> | --finding <json> | --root <dir> | --source <agent>"
	}
	return strings.Join(flags, ", ")
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
	usage := "usage: kern explain-finding --finding <json> [--root ROOT]\n  options:\n    --finding          blueprint gate finding JSON (required)\n    --root             repository root (default: .)"
	build := func(root, source, payload string) map[string]any {
		var finding any
		if payload != "" {
			if err := json.Unmarshal([]byte(payload), &finding); err != nil {
				fatalUsage("explain-finding: --finding must be a JSON object: %v", err)
			}
			if finding == nil {
				fatalUsage("explain-finding: --finding must be a JSON object (e.g. {\"rule_id\": \"format:gofmt\", \"file\": \"x.go\"})")
			}
			// L8: a finding without a rule_id is INCOMPLETE data — the
			// handler renders an empty body and exits 0. Fail loud (exit 1)
			// with a JSON parse error instead.
			if m, ok := finding.(map[string]any); ok {
				if rid, _ := m["rule_id"].(string); rid == "" {
					fatal("explain-finding: JSON parse error: finding missing required field \"rule_id\" (got: %s)", payload)
				}
			} else {
				fatal("explain-finding: JSON parse error: --finding must be a JSON object (got: %s)", payload)
			}
		}
		return map[string]any{"repo": root, "finding": finding}
	}
	os.Exit(runBlueprintToolCLI(rest, bpmcp.ExplainFindingHandler{}, build, usage))
}

func runRepairGuidance(rest []string) {
	usage := "usage: kern repair-guidance --finding <json> [--root ROOT]\n  options:\n    --finding          blueprint gate finding JSON (required)\n    --root             repository root (default: .)"
	build := func(root, source, payload string) map[string]any {
		var finding any
		if payload != "" {
			if err := json.Unmarshal([]byte(payload), &finding); err != nil {
				fatalUsage("repair-guidance: --finding must be a JSON object: %v", err)
			}
			if finding == nil {
				fatalUsage("repair-guidance: --finding must be a JSON object (e.g. {\"rule_id\": \"format:gofmt\", \"file\": \"x.go\"})")
			}
			// P2-D3: a finding without a rule_id is INCOMPLETE data — the
			// handler renders hollow guidance ("Finding () at :0:", exit 0).
			// Fail loud (exit 1) exactly like explain-finding does, so both
			// commands agree on the same malformed finding.
			if m, ok := finding.(map[string]any); ok {
				if rid, _ := m["rule_id"].(string); rid == "" {
					fatal("repair-guidance: JSON parse error: finding missing required field \"rule_id\" (got: %s)", payload)
				}
			} else {
				fatal("repair-guidance: JSON parse error: --finding must be a JSON object (got: %s)", payload)
			}
		}
		return map[string]any{"repo": root, "finding": finding}
	}
	os.Exit(runBlueprintToolCLI(rest, bpmcp.RepairGuidanceHandler{}, build, usage))
}
