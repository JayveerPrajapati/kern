package mcpserve

import (
	"fmt"
	"os"
	"strconv"
	"unicode/utf8"

	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/stats"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// selfRecordingTools are the tools whose handlers already append a stats
// entry with real before/after savings (the optimize family records through
// optimize.record, which stamps the Tool on the entry). The dispatch path
// skips them so the per-tool ledger never double-counts a single call; their
// real savings reach the ledger through their own entries.
var selfRecordingTools = map[string]bool{
	"kern_optimize":  true,
	"kern_run_build": true,
}

// RecordToolCall appends one per-tool stats entry for an executed MCP tool
// call: the tool name, the session (when the caller passed one — the same
// convention the optimize handlers use) and the returned payload token
// estimate via the shared tokenizer. Tools that do not know their
// before/after contribute AfterTokens only (Before=0, Saved=0 — honest, never
// fabricated). Agent attribution: an explicit agent_id argument wins; else
// the client identity captured at initialize (clientInfo.name); else the
// neutral "mcp" bucket so `kern stats --by-agent` never reports
// "(unattributed)" for tool calls. Recording must never fail a tool call, so
// every error path is swallowed.
func RecordToolCall(tool string, args map[string]any, out string, clientName string) {
	if selfRecordingTools[tool] {
		return
	}
	if optimize.Recorder == nil {
		if err := optimize.EnsureRecorder(); err != nil {
			return
		}
	}
	agent := mcpargs.ArgString(args, "agent_id")
	if agent == "" {
		agent = clientName
	}
	if agent == "" {
		agent = "mcp"
	}
	_ = optimize.Recorder.Record(stats.Entry{
		Session:     mcpargs.ArgString(args, "session"),
		Operation:   stats.OpToolCall,
		Tool:        tool,
		Agent:       agent,
		AfterTokens: tokenize.Count(out),
	})
}

// defaultOutputBudget is the MCP output sandbox cap in bytes, used when the
// agent does not pass max_output= and KERN_MCP_MAX_OUTPUT is unset. ~6K tokens
// of safety net for a single tool result.
const defaultOutputBudget = 24 << 10

// outputBudget resolves the global cap from KERN_MCP_MAX_OUTPUT (bytes).
func outputBudget() int {
	if v := os.Getenv("KERN_MCP_MAX_OUTPUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultOutputBudget
}

// CallOutputBudget returns the per-call budget: an explicit max_output=N
// argument (bytes; 0 disables the sandbox) wins over the global cap. A
// malformed max_output is an error, not a silent fallback.
func CallOutputBudget(args map[string]any) (int, error) {
	if v := mcpargs.ArgString(args, "max_output"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("max_output: invalid integer %q", v)
		}
		if n <= 0 {
			return 0, nil // disabled for this call
		}
		return n, nil
	}
	return outputBudget(), nil
}

// SandboxOutput truncates text to budget bytes (when budget > 0) and stamps a
// marker with before/after token counts and a tool-specific recovery hint. The
// marker doubles as the anti-context-flood boundary: an agent that needs more
// can re-call with a larger max_output or a narrower tool.
func SandboxOutput(text string, budget int, tool string) string {
	if budget <= 0 || len(text) <= budget {
		return text
	}
	// Trim to a rune-safe boundary: slicing mid-multi-byte-rune would leave a
	// dangling UTF-8 sequence that corrupts the marker's own token counts and
	// any downstream tokenizer.
	cut := budget
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + fmt.Sprintf("\n\n… [MCP output sandbox: %d → %d chars (%d → %d tokens). %s Pass max_output=N to this tool for more, or narrow the request.]",
		len(text), cut, tokenize.Count(text), tokenize.Count(text[:cut]), recoveryHint(tool))
}

// recoveryHint suggests the narrower tool to recover the truncated detail.
func recoveryHint(tool string) string {
	switch tool {
	case "kern_project_map", "kern_compact_file":
		return "Use kern_context or kern_compact_file for specific symbols instead."
	case "kern_near":
		return "Lower max= or depth=."
	case "kern_context":
		return "Request fewer lines=."
	case "kern_review", "kern_context_budget":
		return "Lower max_tokens=."
	case "kern_doc":
		return "Narrow the query or lower k=."
	case "kern_ast_search":
		return "Tighten the pattern."
	case "kern_exec":
		return "Cap the script's own output with max=."
	case "kern_graph", "kern_arch", "kern_hubs":
		return "This report is inherently large; prefer kern_search/kern_context for specifics."
	default:
		return "Narrow the query."
	}
}
