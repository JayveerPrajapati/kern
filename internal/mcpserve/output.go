package mcpserve

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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

// explicitEnvBudget returns the KERN_MCP_MAX_OUTPUT value (bytes) when it is
// EXPLICITLY set to a valid positive integer, else 0 (unset, empty, zero or
// malformed → not an upper bound). CallOutputBudget treats a non-zero value
// as a hard ceiling on table tools (A4); outputBudget keeps the plain
// last-resort semantics (valid env wins, else the built-in default).
func explicitEnvBudget() int {
	if v := os.Getenv("KERN_MCP_MAX_OUTPUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// outputBudget resolves the global cap from KERN_MCP_MAX_OUTPUT (bytes).
func outputBudget() int {
	if n := explicitEnvBudget(); n > 0 {
		return n
	}
	return defaultOutputBudget
}

// perToolOutputBudgets are the R7 per-tool default output caps (bytes),
// consulted by CallOutputBudget BETWEEN an explicit per-call max_output and
// the global cap (KERN_MCP_MAX_OUTPUT / defaultOutputBudget): resolution
// order is per-call max_output > per-tool table > global default, and an
// EXPLICIT KERN_MCP_MAX_OUTPUT acts as an UPPER BOUND on the table (A4 —
// effective = min(table, env)). Keep this data, not logic: every entry must
// be justifiable by the tool's observed output shape, and an EMPTY table
// behaves exactly like today (every tool gets the global cap). The sandbox
// marker's slice=<anchor>:lines:A-B|tail:N cursor (R7) makes a tighter
// default recoverable rather than lossy, so chatty list tools can safely
// default smaller while source-carrying tools get headroom.
var perToolOutputBudgets = map[string]int{
	// kern_search / kern_ast_search answer with ranked symbol lists; a broad
	// query can emit hundreds of file:line rows. A tighter default keeps the
	// answer scannable — slice=tail:N recovers the rest.
	"kern_search":     8 << 10,
	"kern_ast_search": 8 << 10,
	// kern_explore / kern_compact_file ship VERBATIM source alongside call
	// flow / symbol summaries: at the 24 KiB global cap a dense symbol
	// truncates mid-source. Doubling the headroom keeps the first slice
	// useful.
	"kern_explore":      48 << 10,
	"kern_compact_file": 48 << 10,
}

// CallOutputBudget returns the per-call budget for one tool: an explicit
// max_output=N argument (bytes; 0 disables the sandbox) is the per-call
// budget, CAPPED at an explicitly-set KERN_MCP_MAX_OUTPUT (ADV-2: the
// operator's hard ceiling applies to per-call values too — effective budget
// is min(max_output, env); a value at/below the cap keeps today's semantics,
// and 0 stays "disabled for this call"); otherwise the tool's per-tool
// default cap (perToolOutputBudgets, R7) applies, CAPPED at an
// explicitly-set KERN_MCP_MAX_OUTPUT (A4: an operator setting the env
// intends a hard ceiling — effective budget is min(per-tool table, env));
// with the env unset the table value stands. Tools with no table entry
// resolve to the global cap (KERN_MCP_MAX_OUTPUT or the built-in default). A
// malformed max_output is an error, not a silent fallback.
func CallOutputBudget(tool string, args map[string]any) (int, error) {
	if v := mcpargs.ArgString(args, "max_output"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("max_output: invalid integer %q", v)
		}
		if n <= 0 {
			return 0, nil // disabled for this call
		}
		// ADV-2: an EXPLICIT KERN_MCP_MAX_OUTPUT is the operator's hard
		// ceiling even against a per-call max_output — a value above the cap
		// clamps to it (min), a value at/below the cap keeps today's
		// semantics; max_output=0 (disabled) is at/below any cap and stays
		// disabled.
		if env := explicitEnvBudget(); env > 0 {
			return min(n, env), nil
		}
		return n, nil
	}
	if b, ok := perToolOutputBudgets[tool]; ok {
		if env := explicitEnvBudget(); env > 0 {
			return min(b, env), nil
		}
		return b, nil
	}
	return outputBudget(), nil
}

// OutputCut returns the rune-safe byte cut of text at budget — the exact cut
// SandboxOutput applies. Exported so a caller that needs the ELIDED remainder
// (the A3 chained-cursor path: a truncated slice response retains text[cut:]
// under a fresh anchor) retains precisely what the marker hides, keeping the
// marker's token counts and the retained remainder consistent.
func OutputCut(text string, budget int) int {
	if budget <= 0 || len(text) <= budget {
		return len(text)
	}
	// Trim to a rune-safe boundary: slicing mid-multi-byte-rune would leave a
	// dangling UTF-8 sequence that corrupts the marker's own token counts and
	// any downstream tokenizer.
	cut := budget
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return cut
}

// SandboxOutput truncates text to budget bytes (when budget > 0) and stamps a
// marker with before/after token counts and a tool-specific recovery hint. The
// marker doubles as the anti-context-flood boundary: an agent that needs more
// can re-call with a larger max_output or a narrower tool.
func SandboxOutput(text string, budget int, tool string) string {
	if budget <= 0 || len(text) <= budget {
		return text
	}
	cut := OutputCut(text, budget)
	return text[:cut] + fmt.Sprintf("\n\n… [MCP output sandbox: %d → %d bytes (%d → %d tokens). %s Pass max_output=N to this tool for more, or narrow the request.]",
		len(text), cut, tokenize.Count(text), tokenize.Count(text[:cut]), recoveryHint(tool))
}

// SandboxOutputRetained truncates like SandboxOutput and, when truncation
// actually happened AND anchor is non-empty, extends the marker with the R7
// "more" cursor: the caller can re-call the tool with
// slice=<anchor>:lines:A-B|tail:N to read the elided part of the retained
// output WITHOUT re-executing the tool (the retained text lives in the mcp
// root's bounded cursor store, keyed by anchor). anchor is the id that store
// minted; pass "" for the plain SandboxOutput marker (used by tools whose
// output is too large to retain). On a slice re-read (A3) the caller mints
// the anchor for the elided REMAINDER of the slice itself, so a truncated
// slice CHAINS instead of flooding. The plain marker shape is preserved so
// existing marker assertions keep passing — only the bracketed hint grows.
func SandboxOutputRetained(text string, budget int, tool, anchor string) string {
	out := SandboxOutput(text, budget, tool)
	if out == text || anchor == "" {
		return out
	}
	return strings.TrimSuffix(out, "]") + " slice=" + anchor + ":lines:A-B|tail:N to read the elided part.]"
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
