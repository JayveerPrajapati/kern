// Package mcp implements a minimal Model Context Protocol server over stdio.
// It is deliberately dependency-free so the binary stays offline and static.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/lock"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/project"
	"github.com/JayveerPrajapati/kern/internal/stats"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	protocolVersion = "2025-06-18"
	serverName      = "kern"
)

// serverVersion is stamped at build time via -ldflags "-X main.version=...";
// the binary entry points forward it through SetServerVersion. Defaults to
// "dev" when built without ldflags so initialize still reports something sane.
var serverVersion = "dev"

// SetServerVersion overrides the version reported in the initialize response.
// The CLI entry points call it with their ldflags-stamped main.version.
func SetServerVersion(v string) {
	if v != "" {
		serverVersion = v
	}
}

// Tool is an MCP tool definition.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	// Phase tags the tool with the agent phase it belongs to (explore, plan,
	// edit or verify). "meta" (kern_meta itself) and "cross" (phase-agnostic
	// utilities) are always advertised regardless of the active phase; an
	// empty phase means the tool is not phase-filtered.
	Phase string `json:"phase,omitempty"`
}

// Agent phases for phase-aware tool routing (P1.2). Each phase exposes a
// focused shortlist of tools instead of the full catalog; kern_meta routes
// within whatever tools are available. Tools tagged PhaseMeta or PhaseCross
// are always advertised regardless of the active KERN_MCP_PHASE.
const (
	PhaseExplore = "explore"
	PhasePlan    = "plan"
	PhaseEdit    = "edit"
	PhaseVerify  = "verify"
	PhaseMeta    = "meta"
	PhaseCross   = "cross"
)

// NOTE: KERN_MCP_PHASE filters tool ADVERTISEMENT only (tools/list responses),
// not tool EXECUTION (tools/call). A client that knows a tool name can call it
// directly regardless of the phase setting. This is by design so kern_meta can
// route to unadvertised sub-tools. KERN_MCP_PHASE is NOT a security boundary —
// for real per-phase tool restriction, use the KERN_TOOLS allowlist.

// ToolNames returns every registered MCP tool name.
func ToolNames() []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

// toolAllowlist reads the KERN_TOOLS allowlist from the environment. A
// comma-separated list restricts which tools the server exposes and executes;
// unset or empty means everything is allowed.
func toolAllowlist() []string {
	v := strings.TrimSpace(os.Getenv("KERN_TOOLS"))
	if v == "" {
		return nil
	}
	var out []string
	for _, n := range strings.Split(v, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// highLevelOnly reports whether to expose only high-level tools (per the MCP
// spec) via KERN_MCP_HIGH_LEVEL_ONLY=1; otherwise all tools are registered.
func highLevelOnly() bool {
	return os.Getenv("KERN_MCP_HIGH_LEVEL_ONLY") == "1"
}

// singleTool reports whether to expose only the kern meta-tool via
// KERN_MCP_SINGLE_TOOL=1. Agents that find the tool catalog overwhelming
// can point their MCP config at this mode and interact with kern through the
// single natural-language `kern` entry point.
func singleTool() bool {
	return os.Getenv("KERN_MCP_SINGLE_TOOL") == "1"
}

// fullCatalog reports whether to advertise the full tool catalog via
// KERN_MCP_FULL=1. By default only the minimal defaultTools surface is
// advertised; this opts back in to the full 84-tool catalog for power users
// and direct sub-tool callers. Phase-aware routing (KERN_MCP_PHASE) still
// filters the advertised list within the full catalog.
func fullCatalog() bool {
	return os.Getenv("KERN_MCP_FULL") == "1"
}

// mcpPhase returns the active agent phase from KERN_MCP_PHASE. An unset or
// invalid value returns "" which means no phase filtering: the whole tier
// surface (default/high-level/full) is advertised as before.
func mcpPhase() string {
	p := strings.ToLower(strings.TrimSpace(os.Getenv("KERN_MCP_PHASE")))
	if !validPhase(p) {
		return ""
	}
	return p
}

// validPhase reports whether p is one of the four agent phases.
func validPhase(p string) bool {
	switch p {
	case PhaseExplore, PhasePlan, PhaseEdit, PhaseVerify:
		return true
	}
	return false
}

// phaseToolAllowed reports whether a tool should be advertised for the active
// phase. Tools tagged "meta" or "cross" are always available; otherwise the
// tool's phase must equal the active phase. An empty phase allows everything.
func phaseToolAllowed(t Tool, phase string) bool {
	if phase == "" {
		return true
	}
	switch t.Phase {
	case PhaseMeta, PhaseCross:
		return true
	}
	return t.Phase == phase
}

// toolAllowed reports whether name passes the KERN_TOOLS allowlist. toolsList
// is the (already filtered or full) registered catalog; a nil allowlist
// allows everything, otherwise name must appear in both the allowlist and
// the catalog.
func toolAllowed(toolsList []Tool, name string) bool {
	allowed := toolAllowlist()
	if len(allowed) == 0 {
		return true
	}
	inCatalog := false
	for _, t := range toolsList {
		if t.Name == name {
			inCatalog = true
			break
		}
	}
	if !inCatalog {
		return false
	}
	for _, a := range allowed {
		if a == name {
			return true
		}
	}
	return false
}

// highLevelTools is the set of tools kept when KERN_MCP_HIGH_LEVEL_ONLY=1.
// It includes the 5 high-level orchestration tools (kern_analyze, kern_plan,
// kern_execute, kern_verify, kern_incident) plus a minimal set of essential
// primitives that high-level agents still need.
var highLevelTools = map[string]bool{
	"kern_analyze":         true,
	"kern_plan":            true,
	"kern_execute":         true,
	"kern_verify":          true,
	"kern_incident":        true,
	"kern_what_if":         true,
	"kern_impact":          true,
	"kern_search":          true,
	"kern_context":         true,
	"kern_explore":         true,
	"kern_graph":           true,
	"kern_memory_add":      true,
	"kern_memory_list":     true,
	"kern_memory_recall":   true,
	"kern_memory":          true,
	"kern_review":          true,
	"kern_security":        true,
	"kern_validate":        true,
	"kern_run_build":       true,
	"kern_exec":            true,
	"kern_sandbox":         true,
	"kern_commitmsg":       true,
	"kern_pack":            true,
	"kern_project_map":     true,
	"kern_compact_file":    true,
	"kern_buddy":           true,
	"kern_usage_guide":     true,
	"kern_mask_pii":        true,
	"kern_optimize_prompt": true,
	"kern_optimize_log":    true,
	"kern_doc_search":      true,
	"kern_doc_fetch":       true,
	"kern_doc_index":       true,
	"kern_context_budget":  true,
	"kern_swap":            true,
	"kern_verify_output":   true,
	"kern_schema_validate": true,
	"kern_stats":           true,
}

// defaultTools is the minimal surface advertised by default. The full
// 84-tool catalog is gated behind KERN_MCP_FULL=1, and phase-aware routing
// (KERN_MCP_PHASE) filters either surface down to the active phase's
// shortlist. kern_meta's NL router
// still reaches every sub-tool handler internally regardless of what is
// advertised, so no capability is lost — only the advertised surface
// shrinks. This implements the MCP spec's "high-level tools, not dozens
// of tiny low-value tools" guidance.
var defaultTools = map[string]bool{
	"kern_meta":              true, // NL router → all sub-tools
	"kern_explore":           true, // symbol source + callers/callees + blast radius
	"kern_impact":            true, // blast radius of a change
	"kern_review":            true, // token-optimised review context
	"kern_search":            true, // ranked symbol search
	"kern_context":           true, // minimal source slice
	"kern_optimize_prompt":   true, // compress prompts
	"kern_plan":              true, // implementation plan
	"kern_verify":            true, // unified verification
	"kern_run":               true, // orchestrate a whole task
	"kern_authorize_context": true, // authorized-context primitive (P0.1)
}

// filteredTools returns the registered tools minus any excluded by the
// KERN_TOOLS allowlist, intersected with the active agent phase from
// KERN_MCP_PHASE. By default only the minimal defaultTools surface is
// advertised; KERN_MCP_FULL=1 opts back in to the full catalog, the legacy
// KERN_MCP_HIGH_LEVEL_ONLY mode keeps the mid-size highLevelTools set for
// backward compat, and KERN_MCP_SINGLE_TOOL=1 collapses to kern_meta alone.
// Phase-aware routing (KERN_MCP_PHASE=explore|plan|edit|verify) keeps only
// the active phase's tools plus the always-on meta/cross tools; an unset or
// invalid phase advertises the whole tier surface. It lazily reads the env
// once per server lifetime and caches the result.
func (s *Server) filteredTools() []Tool {
	s.toolsMu.Lock()
	defer s.toolsMu.Unlock()
	if s.filtered != nil {
		return s.filtered
	}
	if singleTool() {
		for _, t := range tools {
			if t.Name == "kern_meta" {
				s.filtered = []Tool{t}
				return s.filtered
			}
		}
	}
	allowed := toolAllowlist()
	// KERN_MCP_FULL=1 → advertise the full catalog (with KERN_TOOLS filter).
	if fullCatalog() {
		out := make([]Tool, 0, len(tools))
		for _, t := range tools {
			if len(allowed) > 0 {
				in := false
				for _, a := range allowed {
					if a == t.Name {
						in = true
						break
					}
				}
				if !in {
					continue
				}
			}
			if !phaseToolAllowed(t, mcpPhase()) {
				continue
			}
			out = append(out, t)
		}
		s.filtered = out
		return s.filtered
	}
	// KERN_MCP_HIGH_LEVEL_ONLY=1 → the legacy 38-tool middle set (deprecated;
	// prefer the default minimal set or KERN_MCP_FULL). Otherwise the NEW
	// DEFAULT: the minimal 11-tool defaultTools surface.
	var keep map[string]bool
	if highLevelOnly() {
		keep = highLevelTools
	} else {
		keep = defaultTools
	}
	out := make([]Tool, 0, len(keep))
	for _, t := range tools {
		if !keep[t.Name] {
			continue
		}
		if len(allowed) > 0 {
			in := false
			for _, a := range allowed {
				if a == t.Name {
					in = true
					break
				}
			}
			if !in {
				continue
			}
		}
		if !phaseToolAllowed(t, mcpPhase()) {
			continue
		}
		out = append(out, t)
	}
	s.filtered = out
	return s.filtered
}

func schema(props map[string]any, required []string) map[string]any {
	s := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

var tools = []Tool{
	{
		Name:        "kern_optimize_prompt",
		Phase:       "cross",
		Description: "Compress and clean a raw prompt before sending it to an LLM. Returns the optimized prompt plus token savings. Use this to reduce context cost for large or noisy prompts. When OLLAMA_HOST points at a non-local (remote) LLM, secrets/PII are masked automatically before processing and restored in the output (the result may contain [MASKED_*] placeholders).",
		InputSchema: schema(map[string]any{
			"prompt":       strProp("The raw prompt text to optimize"),
			"attached_log": strProp("Optional noisy log output to compress and attach"),
			"session":      strProp("Optional session identifier for stats tracking"),
			"model":        strProp("Optional model name for cost estimation"),
			"mask":         strProp("If true, strip secrets/PII before processing and restore placeholders in the output (default false; also auto-enabled for non-local LLM hosts)"),
			"mask_names":   strProp("Comma-separated client/project names to mask as [MASKED_NAME_N]"),
			"cache":        strProp("If true, serve identical requests from the local response cache (default false)"),
			"few_shot":     strProp("If true, inject top recalled lessons from project memory as baseline examples (default false)"),
			"root":         strProp("Project root used for few-shot memory (defaults to current directory)"),
		}, []string{"prompt"}),
	},
	{
		Name:        "kern_optimize_output",
		Phase:       "cross",
		Description: "Compress an LLM's response (assistant output) by stripping filler, pleasantries and hedge language while preserving code blocks, lists, errors and technical content. Deterministic and local, no LLM involved. Use on verbose model replies before they are stored or echoed back into context.",
		InputSchema: schema(map[string]any{
			"text": strProp("The LLM output text to compress"),
		}, []string{"text"}),
	},
	{
		Name:        "kern_memory_add",
		Phase:       "cross",
		Description: "Persist a distilled, cross-session lesson for a project (the project 'brain'). Agents record what they learned so future sessions can recall it. Appends to the project memory store (most recent 50 entries kept).",
		InputSchema: schema(map[string]any{
			"lesson": strProp("The lesson to remember, e.g. 'deploy tags are pushed from a manual release workflow, not CI'"),
			"root":   strProp("Project root whose memory store to append (defaults to current directory)"),
		}, []string{"lesson"}),
	},
	{
		Name:        "kern_memory_list",
		Phase:       "cross",
		Description: "List all stored lessons for a project, most recent first with timestamps.",
		InputSchema: schema(map[string]any{
			"root": strProp("Project root (defaults to current directory)"),
		}, nil),
	},
	{
		Name:        "kern_memory_recall",
		Phase:       "cross",
		Description: "Recall the up-to-k most relevant past lessons for a prompt by keyword overlap. Returns only lessons whose tokens match; deterministic and local.",
		InputSchema: schema(map[string]any{
			"prompt": strProp("Query to match lessons against"),
			"root":   strProp("Project root (defaults to current directory)"),
			"k":      strProp("Max lessons to return (default 5)"),
		}, []string{"prompt"}),
	},
	{
		Name:        "kern_mask_pii",
		Phase:       "cross",
		Description: "Locally scan text for secrets and PII (API keys, passwords, tokens, URLs with credentials, IPs, emails) and replace them with safe [MASKED_*] placeholders. Use before sending any text to a remote LLM. Pure local, deterministic, reversible via the returned mapping.",
		InputSchema: schema(map[string]any{
			"text":       strProp("The raw text to mask"),
			"mask_names": strProp("Optional comma-separated client/project names to mask"),
		}, []string{"text"}),
	},
	{
		Name:        "kern_security",
		Phase:       "verify",
		Description: "Local security scan of a project's source files: hardcoded secrets, dynamic SQL, shell command injection, weak crypto, insecure randomness and unsafe deserialization. Deterministic and line-scoped. Use before reviewing code or shipping changes.",
		InputSchema: schema(map[string]any{
			"root":     strProp("Project root to scan (defaults to current directory)"),
			"severity": strProp("Comma-separated severities to include: error,warning,info (default all)"),
			"max":      strProp("Max findings to return (default 100; 0 = no cap)"),
			"format":   strProp("Output format: text or json (default text)"),
		}, nil),
	},
	{
		Name:        "kern_safe_delete",
		Phase:       "edit",
		Description: "Check whether a symbol can be safely deleted: reports in-project callers (production vs test-only), whether it is exported or an entry point, and a conservative SAFE/NOT SAFE verdict. Use before removing dead code.",
		InputSchema: schema(map[string]any{
			"symbol": strProp("Symbol name (simple name like 'greet' or qualified like 'User.Login')"),
			"root":   strProp("Project root (defaults to current directory)"),
			"format": strProp("Output format: text or json (default text)"),
		}, []string{"symbol"}),
	},
	{
		Name:        "kern_doc_search",
		Phase:       "cross",
		Description: "Local vector search over a project's documents (markdown, text, rst, adoc). Chunks and embeds docs locally with deterministic n-gram hashing (no ML deps) and returns only the most relevant fragments. Use instead of pasting whole documents into context.",
		InputSchema: schema(map[string]any{
			"query": strProp("Natural-language or keyword query"),
			"root":  strProp("Project root (defaults to current directory)"),
			"k":     strProp("Max fragments to return (default 5)"),
		}, []string{"query"}),
	},
	{
		Name:        "kern_doc_index",
		Phase:       "cross",
		Description: "Pre-index a project's documents for kern_doc_search. Run once after documents change; searches auto-index on first use. Pass semantic=true to also embed chunks with a local Ollama embedding model (KERN_EMBED_MODEL, default nomic-embed-text); queries then fuse a real-meaning dense signal with the deterministic n-gram vectors and BM25.",
		InputSchema: schema(map[string]any{
			"root":     strProp("Project root (defaults to current directory)"),
			"semantic": strProp("If true, add dense Ollama embeddings to the index (requires a local Ollama with the embedding model pulled)"),
		}, nil),
	},
	{
		Name:        "kern_doc_fetch",
		Phase:       "cross",
		Description: "Fetch a public documentation page and merge it into the project's local doc index so kern_doc_search can find it. This is the ONLY network call in kern and is invoked explicitly by the user; everything else stays local. The page is HTML-stripped, capped, stored under cache/data/docs-fetch and indexed as fetch/<name>.md (re-fetching a name replaces it). Pass semantic=true to also attach dense embeddings via the local Ollama model so the page ranks in semantic search.",
		InputSchema: schema(map[string]any{
			"url":      strProp("https URL of the documentation page to fetch"),
			"root":     strProp("Project root whose doc index receives the page (defaults to current directory)"),
			"name":     strProp("Optional index name (default derived from the URL host+path)"),
			"semantic": strProp("If true, attach dense embeddings via the local Ollama model (skipped when the model is unavailable)"),
		}, []string{"url"}),
	},
	{
		Name:        "kern_commitmsg",
		Phase:       "edit",
		Description: "Generate a deterministic conventional-commit message (type, scope, subject, per-file body) from the git diff — rule-based, no LLM, no network; the same diff always yields the same message. Use when a commit needs a starting message the human can tweak.",
		InputSchema: schema(map[string]any{
			"root":   strProp("Project root (defaults to current directory)"),
			"staged": strProp("If true, read the staged diff (git diff --cached) instead of the working tree vs HEAD"),
			"range":  strProp("Optional commit range like a..b; overrides staged and HEAD defaults"),
		}, nil),
	},
	{
		Name:        "kern_precache",
		Phase:       "verify",
		Description: "Speculative pre-caching (#20): scan the project once and fill the code-summary and document-vector caches so later kern calls are instant. Run periodically or after bulk edits.",
		InputSchema: schema(map[string]any{
			"root": strProp("Project root (defaults to current directory)"),
		}, nil),
	},
	{
		Name:        "kern_swap",
		Phase:       "plan",
		Description: "Budget swapping (#18): in a context document, replace fenced code blocks tagged `lang:path` with per-file symbolic signatures to fit a token budget, or expand `lang:path:summary` blocks back to full file contents. Returns the budget-fitted document.",
		InputSchema: schema(map[string]any{
			"text":       strProp("The context document containing fenced code blocks"),
			"root":       strProp("Project root used to resolve block paths (defaults to current directory)"),
			"max_tokens": strProp("Token budget; if the document exceeds it, blocks are swapped to summaries"),
			"mode":       strProp("force mode: summary, expand, or fit (default)"),
		}, []string{"text"}),
	},
	{
		Name:        "kern_sandbox",
		Phase:       "edit",
		Description: "Run a risky command inside a snapshot of the project (#15): on non-zero exit the tree is rolled back exactly (files restored, new files removed). Success keeps changes. Use before destructive operations, migrations, or agent-applied edits. Gated by the command-execution governance firewall (KERN_ALLOW_EXEC / KERN_TOOLS) and command output is PII/secret-masked before return.",
		InputSchema: schema(map[string]any{
			"root":    strProp("Project root to snapshot and run in (defaults to current directory)"),
			"command": strProp("Full command to run, e.g. \"make migrate\" or \"sh -c 'npm test'\" (shell words, not a shell string)"),
			"timeout": strProp("Timeout in seconds (default 120)"),
		}, []string{"command"}),
	},
	{
		Name:        "kern_diff_files",
		Phase:       "verify",
		Description: "Delta streaming (#13): compute a unified line diff between two files (or two versions of the same file) using pure Go. Returns the full patch, or a note when files are identical. Feed the output back to the model as a compact edit description.",
		InputSchema: schema(map[string]any{
			"a":    strProp("Path to the old/base file"),
			"b":    strProp("Path to the new/changed file"),
			"root": strProp("Project root; when set, a and b must stay inside it (defaults to unrestricted)"),
		}, []string{"a", "b"}),
	},
	{
		Name:        "kern_heal",
		Phase:       "edit",
		Description: "Self-correction loop (#9): run validation; on failure ask a local Ollama model to rewrite the failing files, apply the fix inside a throwaway snapshot, re-validate, and report a diff to review. Never edits the user's working tree. Requires Ollama at localhost:11434.",
		InputSchema: schema(map[string]any{
			"root":       strProp("Project root (defaults to current directory)"),
			"task":       strProp("The original task the code is meant to fulfil"),
			"model":      strProp("Ollama model (default KERN_MODEL or llama3.2)"),
			"max_rounds": strProp("Correction attempts (default 3)"),
			"timeout":    strProp("Validation timeout in seconds (default 120)"),
		}, nil),
	},
	{
		Name:        "kern_validate",
		Phase:       "verify",
		Description: "Auto-validation (#7): detect the project's language-appropriate build/test/syntax command and run it. Returns exit status, truncated output and duration. Use after editing code to gate correctness before final answers.",
		InputSchema: schema(map[string]any{
			"root":    strProp("Project root (defaults to current directory)"),
			"command": strProp("Optional override command, e.g. \"go test ./...\" (defaults to auto-detected)"),
			"timeout": strProp("Timeout in seconds (default 120)"),
		}, nil),
	},
	{
		Name:        "kern_schema_validate",
		Phase:       "verify",
		Description: "Deterministically validate JSON output against a JSON schema (subset: object/array/primitives, required, enum, min/max/length, pattern, additionalProperties). Returns either a conform message or one line per violation.",
		InputSchema: schema(map[string]any{
			"data":   strProp("The JSON output to validate"),
			"schema": strProp("The JSON schema to validate against"),
		}, []string{"data", "schema"}),
	},
	{
		Name:        "kern_verify_output",
		Phase:       "verify",
		Description: "Hallucination check: extract file:line, symbol-name and route references from an agent's output text and confirm each against the real source tree and index. Returns ok/MISS verdicts for every reference.",
		InputSchema: schema(map[string]any{
			"text": strProp("The agent output text to verify"),
			"root": strProp("Project root (defaults to current directory)"),
		}, []string{"text"}),
	},
	{
		Name:        "kern_compact_file",
		Phase:       "explore",
		Description: "Return a compact symbolic summary of a source file (functions, types, line numbers) instead of reading the whole file. Use before reading files in large codebases. Optional tier: 'summary' (default, symbol list), 'full' (entire source), or 'folded' (signatures kept, bodies replaced with 'body elided: N lines' placeholders).",
		InputSchema: schema(map[string]any{
			"path": strProp("Absolute or relative path of the file to summarize"),
			"root": strProp("Project root; when set, path must stay inside it (defaults to unrestricted)"),
			"tier": strProp("'summary' (default), 'full', or 'folded'"),
		}, []string{"path"}),
	},
	{
		Name:        "kern_project_map",
		Phase:       "explore",
		Description: "Return a compressed map of a whole project: every source file with its symbols and line counts. Use instead of listing/reading every file in a repo.",
		InputSchema: schema(map[string]any{
			"root":      strProp("Project root directory"),
			"max_files": strProp("Maximum number of files to include (default 500)"),
		}, []string{"root"}),
	},
	{
		Name:        "kern_pack",
		Phase:       "plan",
		Description: "Pack a whole project into one paste-ready bundle: project instructions, a directory tree with per-file token counts, and file contents, sized to fit max_tokens. Use when an agent needs the full working picture (source to edit against), not just a map. Files are ordered by sha256 of their relative path so re-packs of the same tree are byte-identical (LLM prompt-cache friendly). Set fold=true to pack signatures with bodies elided.",
		InputSchema: schema(map[string]any{
			"root":         strProp("Project root directory"),
			"max_tokens":   strProp("Token budget for the bundle (default 8000; 0 = unlimited — use with max_output=0 to avoid the output sandbox)"),
			"format":       strProp("'text' (default) or 'json'"),
			"instructions": strProp("'true' to include root-level docs as instructions (default), 'false' to skip them"),
			"fold":         strProp("'true' to pack tier=folded content (signatures kept, bodies elided with line counts)"),
			"tier":         strProp("Content tier: 'full' (default), 'folded', or 'summary'"),
		}, []string{"root"}),
	},
	{
		Name:        "kern_buddy",
		Phase:       "explore",
		Description: "Session onboarding digest for any agent: the project's conventions, layout, entry points and gotchas distilled from the index, docs and recent history. Call once at the start of a session on an unfamiliar repo.",
		InputSchema: schema(map[string]any{
			"root":       strProp("Project root (defaults to current directory)"),
			"max_output": strProp("Raise the MCP output sandbox cap for this call (bytes; 0 disables). The digest is compact by design; use kern_project_map for the full map."),
		}, nil),
	},
	{
		Name:        "kern_run_build",
		Phase:       "edit",
		Description: "Run a build/test command locally and return only the compact result (exit status + errors), not full output. Use for builds, tests, linting to save context.",
		InputSchema: schema(map[string]any{
			"command": strProp("Shell command to run"),
			"dir":     strProp("Working directory for the command"),
		}, []string{"command"}),
	},
	{
		Name:        "kern_optimize_log",
		Phase:       "cross",
		Description: "Strip noise from log output: keeps errors, warnings, stack traces and build failures, removes timestamps and chatter. Use before pasting logs into context.",
		InputSchema: schema(map[string]any{
			"log": strProp("The log text to compress"),
		}, []string{"log"}),
	},
	{
		Name:        "kern_context_budget",
		Phase:       "plan",
		Description: "Fit text into a token budget: deduplicate lines, keep the head plus important lines (errors, stack frames), then trim. Use to manage a crowded context window before adding more content.",
		InputSchema: schema(map[string]any{
			"text":       strProp("The text (log output, file dump, conversation) to fit into the budget"),
			"max_tokens": strProp("Maximum tokens the result may use (default 4000)"),
		}, []string{"text"}),
	},
	{
		Name:        "kern_stats",
		Phase:       "cross",
		Description: "Return before/after token savings and cost estimates from kern optimizations, optionally filtered to today or a session.",
		InputSchema: schema(map[string]any{
			"days":    strProp("Aggregate over the last N days (default 7)"),
			"session": strProp("Filter to a session identifier"),
		}, nil),
	},
	{
		Name:        "kern_semcache",
		Phase:       "cross",
		Description: "Inspect and manage the semantic cache that serves similar (not just identical) prior queries instantly. Actions: 'stats' (default) lists entries per namespace (prompt/log), 'list' shows the stored inputs of a namespace, 'clear' wipes it (or all), 'similarity' reports the Jaccard overlap of two inputs so you can predict whether a near-duplicate will hit. Use to verify or reset the fuzzy layer.",
		InputSchema: schema(map[string]any{
			"action":    strProp("'stats' (default), 'list', 'clear', or 'similarity'"),
			"namespace": strProp("prompt or log (default: all)"),
			"a":         strProp("First input for similarity"),
			"b":         strProp("Second input for similarity"),
		}, []string{"action"}),
	},
	{
		Name:        "kern_ast_search",
		Phase:       "explore",
		Description: "AST-level symbol search across a Go project. Supports patterns like 'func greet', 'type *User*', 'method *', '*Handler*'. Returns definitions with file:line.",
		InputSchema: schema(map[string]any{
			"pattern": strProp("Symbol pattern. Prefixes: func, method, struct, interface, type, const, var. '*' wildcards supported"),
			"root":    strProp("Project root (defaults to current directory)"),
			"limit":   strProp("Max results (default 50)"),
		}, []string{"pattern"}),
	},
	{
		Name:        "kern_frameworks",
		Phase:       "explore",
		Description: "Detect the frameworks and libraries a project uses (Spring, Rails, Django, Express, gin, etc.) by scanning manifests and source markers. Use to know what stack the codebase is on.",
		InputSchema: schema(map[string]any{
			"root": strProp("Project root (defaults to current directory)"),
		}, nil),
	},
	{
		Name:        "kern_entry_points",
		Phase:       "explore",
		Description: "List framework entry points found in the index: handlers, controllers and route targets with their framework and route (e.g. spring-mvc UserController.list /api/users). Search for all symbols with the 'entry' kind prefix via kern_ast_search.",
		InputSchema: schema(map[string]any{
			"root":    strProp("Project root (defaults to current directory)"),
			"limit":   strProp("Max results (default 50)"),
			"pattern": strProp("Optional route/name wildcard filter, e.g. '*admin*'"),
		}, nil),
	},
	{
		Name:        "kern_search",
		Phase:       "explore",
		Description: "Ranked free-text symbol search: returns symbols matching a query by name or file, best matches first. Forgiving lookup for humans — 'load index' or 'login handler' work, and prose hits camelCase symbols by name segment ('state machine' -> OrderStateMachine), plural-folded ('user services' -> UserService), accent-normalized ('résolution' -> ResolveResolution), or as a camelCase query ('stateMachine'). Set semantic=true to re-rank results by dense embeddings from a local Ollama server (embedding model " + "KERN_EMBED_MODEL, default nomic-embed-text).",
		InputSchema: schema(map[string]any{
			"query":    strProp("Free-text query (symbol name, path fragment, or partial name)"),
			"root":     strProp("Project root (defaults to current directory)"),
			"limit":    strProp("Max results (default 20)"),
			"semantic": strProp("When 'true', re-rank by Ollama dense embeddings (requires embedding model)"),
		}, []string{"query"}),
	},
	{
		Name:        "kern_repo_search",
		Phase:       "explore",
		Description: "Ranked free-text symbol search across every repo in the kern multi-repo registry (kern repos add). Returns matches tagged with their repo name, best hits first. Set semantic=true to re-rank pooled results by Ollama dense embeddings.",
		InputSchema: schema(map[string]any{
			"query":    strProp("Free-text query (symbol name, path fragment, or partial name)"),
			"limit":    strProp("Max results (default 20)"),
			"semantic": strProp("When 'true', re-rank by Ollama dense embeddings (requires embedding model)"),
		}, []string{"query"}),
	},
	{
		Name:        "kern_why",
		Phase:       "explore",
		Description: "Rationale and doc-reference report for a symbol: its doc comment, who depends on it and why (each caller's own doc line), and its in/out edge counts. Use to answer 'why does this exist and who needs it'.",
		InputSchema: schema(map[string]any{
			"symbol": strProp("Symbol name or Receiver.Name"),
			"root":   strProp("Project root (defaults to current directory)"),
		}, []string{"symbol"}),
	},
	{
		Name:        "kern_code_graph",
		Phase:       "explore",
		Description: "Return the call graph neighbourhood of a symbol: its definition, its callers, and what it calls. Use to understand dependencies without reading whole files.",
		InputSchema: schema(map[string]any{
			"symbol": strProp("Symbol name (e.g. 'greet' or 'User.Login')"),
			"root":   strProp("Project root (defaults to current directory)"),
		}, []string{"symbol"}),
	},
	{
		Name:        "kern_inherits",
		Phase:       "explore",
		Description: "Return the inheritance edges of a symbol: its supertypes (extends/implements/embeds) and subtypes (what extends/implements/embeds it). Use to see class hierarchies without reading whole files.",
		InputSchema: schema(map[string]any{
			"symbol": strProp("Symbol name (e.g. 'Item' or 'Logger')"),
			"root":   strProp("Project root (defaults to current directory)"),
		}, []string{"symbol"}),
	},
	{
		Name:        "kern_context",
		Phase:       "explore",
		Description: "Return the minimal relevant source slice for a symbol: its definition source, its callers, and what it calls. Use instead of reading an entire file.",
		InputSchema: schema(map[string]any{
			"symbol":         strProp("Symbol name (e.g. 'greet')"),
			"root":           strProp("Project root (defaults to current directory)"),
			"lines":          strProp("Lines of source context around the definition (default 12)"),
			"agent_id":       strProp("Agent identity for governed mode (P1.2): enables authorized-context filtering — results are scoped to what this agent may read. Omit for raw (ungoverned) mode."),
			"task":           strProp("Task ID for governed mode; pairs with agent_id to scope authorization to the task paths."),
			"scope":          map[string]any{"type": "object", "description": "Optional task scope object {paths, denied_paths, services, envs, artifacts} for governed mode."},
			"with_freshness": strProp("When 'true', append a ---freshness-proof--- footer with the index's content-addressed freshness proof"),
		}, []string{"symbol"}),
	},
	{
		Name:        "kern_changes",
		Phase:       "verify",
		Description: "Line-aware change-impact analysis for a diff: scopes each changed file to the symbols its added lines actually touch (from git diff hunks), then computes blast radius (transitive callers), risk scores, and test gaps. Use to review what a PR could break before reading files.",
		InputSchema: schema(map[string]any{
			"root":  strProp("Project root (defaults to current directory)"),
			"range": strProp("Git range like 'HEAD~2..HEAD'. Empty = working-tree changes"),
			"file":  strProp("Optional comma-separated explicit file list, overrides git range"),
		}, nil),
	},
	{
		Name:        "kern_review",
		Phase:       "verify",
		Description: "Token-optimised code-review context for changed files: line-scoped changed symbols (with file:line spans), their callers, blast radius, risk and test gaps, sized to fit a token budget. The smallest answer a reviewer needs.",
		InputSchema: schema(map[string]any{
			"root":       strProp("Project root (defaults to current directory)"),
			"range":      strProp("Git range like 'HEAD~2..HEAD'. Empty = working-tree changes"),
			"file":       strProp("Optional comma-separated explicit file list, overrides git range"),
			"max_tokens": strProp("Maximum tokens for the review context (default 8000)"),
		}, nil),
	},
	{
		Name:        "kern_hubs",
		Phase:       "explore",
		Description: "Architectural hotspots: the most depended-on symbols (hubs) and cross-package bridges where a change in one subsystem can break another.",
		InputSchema: schema(map[string]any{
			"root":  strProp("Project root (defaults to current directory)"),
			"limit": strProp("Max hubs to return (default 10)"),
		}, nil),
	},
	{
		Name:        "kern_test_gaps",
		Phase:       "plan",
		Description: "Test-coverage analysis from the call graph: what percent of callable symbols are exercised by tests, plus untested hotspots (called by many, covered by none).",
		InputSchema: schema(map[string]any{
			"root":  strProp("Project root (defaults to current directory)"),
			"limit": strProp("Max hotspots to list (default 10)"),
		}, nil),
	},
	{
		Name:        "kern_path",
		Phase:       "explore",
		Description: "Shortest call path between two symbols, following in-project call edges in either direction. Traces how two things connect without reading files.",
		InputSchema: schema(map[string]any{
			"root": strProp("Project root (defaults to current directory)"),
			"from": strProp("Source symbol (simple name or Type.Method)"),
			"to":   strProp("Target symbol (simple name or Type.Method)"),
		}, []string{"from", "to"}),
	},
	{
		Name:        "kern_dead",
		Phase:       "explore",
		Description: "Dead-code detection: symbols nothing in the project calls. Private names are dead for certain; public names may be external API. Sorted by size so the biggest cleanup wins show first. Callers reached through function values or interface dispatch are invisible to the index and are reported as dead — confirm before removing.",
		InputSchema: schema(map[string]any{
			"root":  strProp("Project root (defaults to current directory)"),
			"limit": strProp("Max entries (default all)"),
		}, nil),
	},
	{
		Name:        "kern_larges",
		Phase:       "explore",
		Description: "Find the largest function/method declarations by source lines. Use to locate god functions that beg for refactoring.",
		InputSchema: schema(map[string]any{
			"root":      strProp("Project root (defaults to current directory)"),
			"min_lines": strProp("Size threshold in source lines (default 60)"),
			"limit":     strProp("Max results (default all)"),
		}, nil),
	},
	{
		Name:        "kern_arch",
		Phase:       "explore",
		Description: "Architecture overview from call-graph communities: subsystems with their hubs/packages, plus coupling warnings ranking the cross-community call bundles that make changes ripple.",
		InputSchema: schema(map[string]any{
			"root": strProp("Project root (defaults to current directory)"),
		}, nil),
	},
	{
		Name:        "kern_communities",
		Phase:       "explore",
		Description: "Call-graph communities (label propagation): which symbols cluster together as subsystems, with each cluster's size and hub. Use to name the architecture's parts before refactoring.",
		InputSchema: schema(map[string]any{
			"root":  strProp("Project root (defaults to current directory)"),
			"limit": strProp("Max communities to return (default all)"),
		}, nil),
	},
	{
		Name:        "kern_churn",
		Phase:       "explore",
		Description: "Change-frequency risk: which files were touched by the most commits in a range, whether they are being edited right now, and how risky they are in the call graph.",
		InputSchema: schema(map[string]any{
			"root":  strProp("Project root (defaults to current directory)"),
			"range": strProp("Git range like 'HEAD~10..HEAD' (default last 30 commits)"),
		}, nil),
	},
	{
		Name:        "kern_near",
		Phase:       "explore",
		Description: "Dependency-tree expansion: every symbol within N hops of a symbol, in both directions (callers + callees), budget-capped. The graph-guided traversal primitive that replaces blind grep — e.g. 'everything two degrees from this database model' in one call.",
		InputSchema: schema(map[string]any{
			"symbol": strProp("Root symbol (simple name or Type.Method)"),
			"depth":  strProp("Number of hops to expand (default 2)"),
			"max":    strProp("Maximum nodes to return (default 100)"),
			"root":   strProp("Project root (defaults to current directory)"),
		}, []string{"symbol"}),
	},
	{
		Name:        "kern_walk",
		Phase:       "explore",
		Description: "Graph-guided walk: the /walk-graph primitive. Returns an indented parent-child dependency tree of every symbol up to N hops away from a symbol, across files, with file:line per node. Alias of kern_near with a tree-oriented description; use instead of grepping or reading whole files to locate code.",
		InputSchema: schema(map[string]any{
			"symbol": strProp("Root symbol (simple name or Type.Method)"),
			"depth":  strProp("Number of hops to expand (default 2)"),
			"max":    strProp("Maximum nodes to return (default 100)"),
			"root":   strProp("Project root (defaults to current directory)"),
		}, []string{"symbol"}),
	},
	{
		Name:        "kern_probe",
		Phase:       "explore",
		Description: "Query-driven micro-context router: given a task (bug report, prompt, error text), extract the symbol names it mentions, resolve them against the index, and return a budget-capped bundle of definitions, callers, callees and tests. The graph is the retrieval index, never the payload.",
		InputSchema: schema(map[string]any{
			"task":       strProp("Natural-language task, bug report or error text mentioning symbols"),
			"root":       strProp("Project root (defaults to current directory)"),
			"max_tokens": strProp("Token budget for the bundle (default 4000)"),
		}, []string{"task"}),
	},
	{
		Name:        "kern_trace",
		Phase:       "plan",
		Description: "Runtime-impact overlay: parse a pprof -top dump, a crash stack trace, or a plain list of function names and map the hot symbols onto the call graph — file:line, blast radius, test coverage and risk. Use to see what a hot path touches at runtime.",
		InputSchema: schema(map[string]any{
			"trace": strProp("The trace text (pprof -top, stack trace, or symbol list)"),
			"root":  strProp("Project root (defaults to current directory)"),
			"limit": strProp("Max hot symbols to return (default all)"),
		}, []string{"trace"}),
	},
	{
		Name:        "kern_lock",
		Phase:       "edit",
		Description: "Acquire an advisory workspace lock on a scope (flock-based). Held by this server until kern_unlock. Lets concurrent agents coordinate before touching shared files. Errors when the scope is already held.",
		InputSchema: schema(map[string]any{
			"scope": strProp("Lock scope, e.g. 'db-models' or 'checkout'"),
			"root":  strProp("Project root (defaults to current directory)"),
		}, []string{"scope"}),
	},
	{
		Name:        "kern_unlock",
		Phase:       "edit",
		Description: "Release a workspace lock previously acquired via kern_lock.",
		InputSchema: schema(map[string]any{
			"scope": strProp("Lock scope to release"),
		}, []string{"scope"}),
	},
	{
		Name:        "kern_lock_status",
		Phase:       "edit",
		Description: "List workspace locks with whether each is held and by which PID. Use to see what other agents are working on.",
		InputSchema: schema(map[string]any{
			"root": strProp("Project root (defaults to current directory)"),
		}, nil),
	},
	{
		Name:        "kern_guard_check",
		Phase:       "edit",
		Description: "Deterministic architectural guardrails: validate changed files against .kern/boundaries.json rules and return every forbidden dependency crossing (e.g. a frontend importing a backend DB model) with file evidence. Rejects a proposal before it touches the filesystem. Use format=sarif for a SARIF 2.1.0 report (GitHub code scanning / Azure DevOps) and threshold=N to fail (isError) when the violation count exceeds N.",
		InputSchema: schema(map[string]any{
			"root":      strProp("Project root (defaults to current directory)"),
			"file":      strProp("Optional comma-separated explicit file list, overrides git range"),
			"range":     strProp("Git range like 'HEAD~2..HEAD'. Empty = working-tree changes"),
			"format":    strProp("Output format: text (default) or sarif"),
			"threshold": strProp("Fail (isError) when the violation count exceeds this number (default 0 = any violation fails)"),
		}, nil),
	},
	{
		Name:        "kern_authorize_context",
		Phase:       "cross",
		Description: "Authorized-context primitive (P0.1): compute the exact set of symbols and call edges an agent may legally read for a task, filtered by the agent's identity (firewall context.read permission) and an optional task scope, and return it with an auditable authorization proof (decision, fingerprint, index freshness). Denied symbols are listed with their denial stage and reason. Use before retrieval when a task must not leak out-of-scope code.",
		InputSchema: schema(map[string]any{
			"agent_id":      strProp("Agent ID to authorize (must be registered, e.g. via identity.RegisterAgent / kern_agent)"),
			"task":          strProp("Task ID the authorization is scoped to"),
			"root":          strProp("Project root (defaults to current directory)"),
			"symbol_filter": strProp("Optional substring filter applied to the allowed symbols only"),
			"scope":         strProp("Optional task scope object: {paths: [], denied_paths: [], services: [], envs: [], artifacts: []}"),
		}, []string{"agent_id", "task"}),
	},
	{
		Name:        "kern_rename",
		Phase:       "edit",
		Description: "Structural symbol rename on the AST index (P0-5): previews every definition/reference for a Go package-level symbol (types, funcs, vars, consts) with file:line:col edits, then applies them transactionally when apply=true. Edits come from a real go/ast parse, so strings, comments, struct-field names, composite-literal keys, import aliases and the package clause are never touched; cross-package references (pkg.Symbol) are handled for exported symbols. Before applying, every touched file is backed up under <root>/.kern/rename-backup/ and a mid-flight failure restores all files. Method rename and non-Go symbols are refused. Returns the preview (or apply result) as text.",
		InputSchema: schema(map[string]any{
			"root":     strProp("Project root (defaults to current directory)"),
			"symbol":   strProp("Symbol to rename (package-level Go name, e.g. Widget)"),
			"new_name": strProp("New identifier"),
			"apply":    strProp("If true, commit the rename (with backups + rollback); otherwise return the preview only"),
		}, []string{"symbol", "new_name"}),
	},
	{
		Name:        "kern_exec",
		Phase:       "edit",
		Description: "Run code in an isolated local runtime and return ONLY stdout — the 'Think in Code' surface. Language is selected by --lang or a shebang line; runtimes are resolved from PATH (python3, node, go, bash, perl, ruby, php, lua, julia, R, bun, deno, rust, ...). The script runs in a fresh temp dir with a hard timeout (default 10s, override timeout=N), a stdout byte cap (default 16KiB, override max=N), and a sanitized environment (HOME/XDG pointed into the sandbox, secrets stripped). Isolation is enforced: the script runs in a private network namespace when the platform supports it, and the run refuses to execute if network isolation is unavailable (never silently runs with full network). stderr is never mixed into stdout and is only surfaced on failure. Use it to compute things (math, data munging, JSON transforms) without polluting context.",
		InputSchema: schema(map[string]any{
			"code":       strProp("The script body (required)"),
			"lang":       strProp("Language override (e.g. python3, node, bash, go); otherwise detected from the shebang"),
			"timeout":    strProp("Timeout in seconds (default 10)"),
			"max":        strProp("Max stdout bytes to return (default 16384)"),
			"stdin":      strProp("Input piped to the script's stdin"),
			"list":       strProp("If true, return the installed runtimes and supported languages and do nothing else"),
			"no_isolate": strProp("Ignored unless the local operator sets KERN_ALLOW_NO_ISOLATE=1; isolation is enforced by default"),
		}, []string{"code"}),
	},
	{
		Name:        "kern_explore",
		Phase:       "explore",
		Description: "Single-call explore (#2): return a symbol's verbatim source, direct call flow (callers + callees) and transitive blast radius (with affected files) in one shot. The primitive that replaces three separate calls (graph/near/path) for 'what touches this and how'. Pass depth=N to cap the blast radius to N hops and max=N to cap node count.",
		InputSchema: schema(map[string]any{
			"symbol":         strProp("Symbol name (e.g. 'greet' or 'User.Login')"),
			"root":           strProp("Project root (defaults to current directory)"),
			"depth":          strProp("Cap blast radius to N hops from the symbol (default 0 = unlimited)"),
			"max":            strProp("Maximum blast-radius symbols to return (default 0 = unlimited)"),
			"agent_id":       strProp("Agent identity for governed mode (P1.2): enables authorized-context filtering — results are scoped to what this agent may read. Omit for raw (ungoverned) mode."),
			"task":           strProp("Task ID for governed mode; pairs with agent_id to scope authorization to the task paths."),
			"scope":          map[string]any{"type": "object", "description": "Optional task scope object {paths, denied_paths, services, envs, artifacts} for governed mode."},
			"with_freshness": strProp("When 'true', append a ---freshness-proof--- footer with the index's content-addressed freshness proof"),
		}, []string{"symbol"}),
	},
	{
		Name:        "kern_graph",
		Phase:       "explore",
		Description: "One-call graph context: token-budgeted names-only adjacency for a symbol — callers first (the direction that matters for impact), then callees, every edge tagged EXTRACTED/INFERRED/AMBIGUOUS, plus community membership. Calls to interface methods carry dispatch hints listing the concrete implementations they can reach. Parity with code-review-graph's minimal_context: the minimal caller-first answer sized to the context window, no source text.",
		InputSchema: schema(map[string]any{
			"symbol":         strProp("Symbol name (simple name or Type.Method)"),
			"root":           strProp("Project root (defaults to current directory)"),
			"max_tokens":     strProp("Token budget for the names-only adjacency (default 400)"),
			"agent_id":       strProp("Agent identity for governed mode (P1.2): enables authorized-context filtering — results are scoped to what this agent may read. Omit for raw (ungoverned) mode."),
			"task":           strProp("Task ID for governed mode; pairs with agent_id to scope authorization to the task paths."),
			"scope":          map[string]any{"type": "object", "description": "Optional task scope object {paths, denied_paths, services, envs, artifacts} for governed mode."},
			"with_freshness": strProp("When 'true', append a ---freshness-proof--- footer with the index's content-addressed freshness proof"),
		}, []string{"symbol"}),
	},
	{
		Name:        "kern_fts_search",
		Phase:       "explore",
		Description: "FTS5 full-text search (#3) over the SQLite symbol index. Supports MATCH syntax ('greet', 'func AND greet', `file:\"main.go\"`). Requires a build with -tags sqlite and a persisted index. Falls back to a clear error on the default build.",
		InputSchema: schema(map[string]any{
			"query": strProp("FTS5 MATCH query over symbols"),
			"root":  strProp("Project root (defaults to current directory)"),
			"limit": strProp("Max results (default 20)"),
		}, []string{"query"}),
	},
	{
		Name:        "kern_bridges",
		Phase:       "explore",
		Description: "Bridge detection (#4): symbols called from two or more distinct packages/directories — the coupling points where a change in one subsystem can break another. Ranks bridges by number of calling packages then caller count.",
		InputSchema: schema(map[string]any{
			"root":  strProp("Project root (defaults to current directory)"),
			"limit": strProp("Max bridges to return (default 15)"),
		}, nil),
	},
	{
		Name:        "kern_cochange",
		Phase:       "explore",
		Description: "Co-change mode (#6): which files are actually changed together in the same commits (from git history), independent of the call graph. Grades change risk by co-change frequency: files that co-change with the current edits are the ones most likely to break next. Use before a commit to see what else must change in lockstep.",
		InputSchema: schema(map[string]any{
			"root":  strProp("Project root (defaults to current directory)"),
			"range": strProp("Git range like 'HEAD~10..HEAD' (default last 30 commits)"),
			"limit": strProp("Max co-change pairs to return (default 20)"),
		}, nil),
	},
	{
		Name:        "kern_usage_guide",
		Phase:       "plan",
		Description: "Categorized usage guide for every kern MCP tool with performance tiers (fast/moderate/expensive), recommended workflows, and pitfalls. Consult this first when deciding which tool fits a task.",
		InputSchema: schema(map[string]any{}, nil),
	},
	{
		Name:        "kern_analyze",
		Phase:       "plan",
		Description: "HIGH-LEVEL (ADR-0006): analyze a proposed change against the whole system — relevant code, architecture, dependencies, historical memory, blast radius, risks, evidence, and required validation. This is the Kern 2.0 killer workflow 'Analyze this proposed change' exposed over MCP.",
		InputSchema: schema(map[string]any{
			"root":   strProp("Project root (defaults to current directory)"),
			"change": strProp("The change/symbol to analyze, e.g. 'Add a Greet function' or 'helper'"),
		}, []string{"change"}),
	},
	{
		Name:        "kern_plan",
		Phase:       "plan",
		Description: "HIGH-LEVEL (ADR-0006): produce an implementation plan for a proposed change — affected files, dependencies, risks and required validation. Deterministic plan over the analysis; no LLM required.",
		InputSchema: schema(map[string]any{
			"root":   strProp("Project root (defaults to current directory)"),
			"change": strProp("The change to plan, e.g. 'Add a Greet function to main.go'"),
		}, []string{"change"}),
	},
	{
		Name:        "kern_execute",
		Phase:       "edit",
		Description: "HIGH-LEVEL (ADR-0006): execute a change inside an isolated sandbox worktree (autonomy L2). Applies the given unified diff, verifies it builds, and returns the resulting diff. Never mutates the live repository.",
		InputSchema: schema(map[string]any{
			"root":  strProp("Project root (defaults to current directory)"),
			"patch": strProp("A unified diff to apply to the sandbox worktree"),
		}, []string{"patch"}),
	},
	{
		Name:        "kern_verify",
		Phase:       "verify",
		Description: "HIGH-LEVEL (ADR-0006): verify a change with the unified verification engine — build, unit tests, security, architecture, dependency. Returns the typed verdict (PASS/FAIL/WARN) and per-check summary.",
		InputSchema: schema(map[string]any{
			"root":  strProp("Project root (defaults to current directory)"),
			"types": strProp("Comma-separated checks: build,test,security,architecture,dependency (default 'build,test')"),
		}, nil),
	},
	{
		Name:        "kern_incident",
		Phase:       "cross",
		Description: "HIGH-LEVEL (ADR-0006): investigate a production incident end-to-end — correlate an alert to the affected service and evidence, derive the root cause and hypotheses, and summarize. Provide the alert as JSON; optionally a runtime snapshot (events/deployments/commits) as JSON.",
		InputSchema: schema(map[string]any{
			"root":     strProp("Project root (defaults to current directory)"),
			"alert":    strProp("JSON of a domain.Alert: {id,severity,message,service,source,occurred_at}"),
			"snapshot": strProp("Optional JSON of a runtime snapshot: {events,deployments,commits}"),
		}, []string{"alert"}),
	},
	{
		Name:        "kern_what_if",
		Phase:       "plan",
		Description: "HIGH-LEVEL (Workflow C / ADR-0012): simulate the impact of a hypothetical change on the knowledge graph — transitively affected symbols, files, services, tests, a deterministic risk level, and a typed RECOMMENDATION claim. Read-only; never mutates the graph or index.",
		InputSchema: schema(map[string]any{
			"root":       strProp("Project root (defaults to current directory)"),
			"change":     strProp("The symbol to change/remove (qualified name), e.g. 'helper'"),
			"kind":       strProp("Change kind: 'remove_symbol' (default) or 'change_dependency'"),
			"new_target": strProp("For change_dependency: the symbol Target now depends on"),
		}, []string{"change"}),
	},
	{
		Name:        "kern_impact",
		Phase:       "plan",
		Description: "HIGH-LEVEL: estimate the impact/blast-radius of a change to a symbol — transitively affected symbols/files/services/tests, deterministic risk, and typed claims. Read-only.",
		InputSchema: schema(map[string]any{
			"root":       strProp("Project root (defaults to current directory)"),
			"change":     strProp("The symbol to change/remove (qualified name), e.g. 'helper'"),
			"kind":       strProp("Change kind: 'remove_symbol' (default) or 'change_dependency'"),
			"new_target": strProp("For change_dependency: the symbol Target now depends on"),
		}, []string{"change"}),
	},
	{
		Name:        "kern_memory",
		Phase:       "cross",
		Description: "HIGH-LEVEL (Workflow E): manage engineering memory — add a lesson, list stored lessons, or recall the most relevant lessons for a prompt.",
		InputSchema: schema(map[string]any{
			"action": strProp("Action to perform: 'add', 'list', or 'recall'"),
			"lesson": strProp("For 'add': the lesson to remember"),
			"prompt": strProp("For 'recall': query to match lessons against"),
			"root":   strProp("Project root (defaults to current directory)"),
		}, []string{"action"}),
	},
	{
		Name:        "kern_agents",
		Phase:       "cross",
		Description: "HIGH-LEVEL (Workflow E): build the standard specialist team and list its roster — name, role, capabilities — plus the current task states from the agent registry. Read-only and deterministic.",
		InputSchema: schema(map[string]any{
			"root": strProp("Project root (defaults to current directory)"),
		}, nil),
	},
	{
		Name:        "kern_loop",
		Phase:       "cross",
		Description: "HIGH-LEVEL (Workflow E): run the closed autonomy loop against an intent string and return the stage timeline plus the deployed / observed-healthy / learned outcome. The autonomy level (L0-L5, default L0 read-only) gates which stages run; the AI stages use the deterministic no-op step by default and are pluggable via the loop's StepFunc mechanism.",
		InputSchema: schema(map[string]any{
			"root":   strProp("Project root (defaults to current directory)"),
			"intent": strProp("The intent/goal to run the loop against"),
			"level":  strProp("Autonomy level L0-L5 (default L0, read-only)"),
		}, []string{"intent"}),
	},
	{
		Name:        "kern_run",
		Phase:       "cross",
		Description: "HIGH-LEVEL (Workflow E): run an intent through the full task pipeline — compiles the intent, selects workflow + capabilities + agents, creates a Task, runs policy precheck, and returns the run result (task id, workflow, risk/approval, capabilities, tools, agents, next action). This is the single entry point that orchestrates the whole workflow from one call.",
		InputSchema: schema(map[string]any{
			"root":   strProp("Project root (defaults to current directory)"),
			"intent": strProp("The intent/goal to run through the pipeline"),
		}, []string{"intent"}),
	},
	{
		Name:        "kern_meta",
		Phase:       "meta",
		Description: "Single entry point: describe what you need in natural language and kern classifies the request and runs the right tool(s) internally. Examples: 'how does dispatch work?' → kern_explore, 'what breaks if I change dispatch?' → kern_impact, 'compress this log: ...' → kern_optimize_log, 'mask secrets in: ...' → kern_mask_pii, 'find the dispatch function' → kern_search, 'show me the architecture' → kern_arch. Prefer this over calling individual kern_* tools — it picks the right one for you.",
		InputSchema: schema(map[string]any{
			"request":  strProp("Natural-language request describing what you need from kern"),
			"phase":    strProp("Agent phase: explore|plan|edit|verify. Hints the phase context for routing; the advertised tool list is filtered server-wide via KERN_MCP_PHASE. Optional."),
			"agent_id": strProp("Agent identity for governed mode (P1.2): enables authorized-context filtering — results are scoped to what this agent may read. Omit for raw (ungoverned) mode."),
			"task":     strProp("Task ID for governed mode; pairs with agent_id to scope authorization to the task paths."),
			"scope":    map[string]any{"type": "object", "description": "Optional task scope object {paths, denied_paths, services, envs, artifacts} for governed mode."},
			"root":     strProp("Project root (defaults to current directory)"),
		}, []string{"request"}),
	},
	{
		Name:        "kern_workflow",
		Phase:       "cross",
		Description: "HIGH-LEVEL (Workflow E): select and coordinate the agent team without the external caller manually sequencing it. Classifies the intent, registers the kind-specific workflow (only the specialists that apply), wires the standard team, and drives the steps (analyze → plan → [human approval gate] → code → verify → pr for code changes; kind-specific stages for incident/documentation/modernization tasks). The run parks at the human approval gate before the first execution step: the returned error carries the approval ID, resolve it via kern_approve then call kern_workflow again with the same task_id to resume.",
		InputSchema: schema(map[string]any{
			"root":    strProp("Project root (defaults to current directory)"),
			"intent":  strProp("The intent/goal to run through the agent team"),
			"task_id": strProp("Resume an approval-parked run for this task (omit to start a new run)"),
		}, []string{"intent"}),
	},
	{
		Name:        "kern_onboard",
		Phase:       "cross",
		Description: "Session-start onboarding: ensure the working directory is fully wired to kern in one call. Checks whether the repo is registered (repos registry) and indexed; if not, registers it, builds/refreshes the index, and writes AGENTS.md rules if missing. Returns a status report (registered, indexed, wired, symbols/edges/files). Call this at session start in a new project instead of manually indexing or re-exploring with read/grep/glob.",
		InputSchema: schema(map[string]any{
			"root": strProp("Project root (defaults to current directory)"),
		}, nil),
	},
	{
		Name:        "kern_audit",
		Phase:       "verify",
		Description: "HIGH-LEVEL: return the tamper-evident governance audit log for the project (every firewall decision/approval). CLI-equivalent: kern audit. Backs the AUDIT intent workflow.",
		InputSchema: schema(map[string]any{
			"root": strProp("Project root (defaults to current directory)"),
		}, nil),
	},
	{
		Name:        "kern_approve",
		Phase:       "edit",
		Description: "HIGH-LEVEL: resolve a governance approval gate. With no id, lists pending approvals. With an id, approves it; set reject=true to reject instead. CLI-equivalent: kern approve. Agents hit this when kern_run/kern_workflow parks at the human approval gate — the returned error carries the approval ID.",
		InputSchema: schema(map[string]any{
			"root":     strProp("Project root (defaults to current directory)"),
			"id":       strProp("Approval ID to approve/reject (omit to list pending approvals)"),
			"reject":   strProp("If true, reject the approval instead of approving it (default false)"),
			"reason":   strProp("Optional reason for the decision"),
			"approver": strProp("Optional approver identity (defaults to 'mcp-user')"),
		}, nil),
	},
	{
		Name:        "kern_correlate",
		Phase:       "cross",
		Description: "HIGH-LEVEL: correlate a production alert against the runtime to produce a deep evidence chain (alert→service→deployment→commit→symbol→task/pr/agent). Deterministic — derived from runtime source and git history, not LLM.",
		InputSchema: schema(map[string]any{
			"root":     strProp("Project root (defaults to current directory)"),
			"alert":    strProp("JSON of a domain.Alert: {id,severity,message,service,source,occurred_at}"),
			"snapshot": strProp("Optional JSON of a runtime snapshot: {events,deployments,commits}"),
		}, []string{"alert"}),
	},
	{
		Name:        "kern_learn",
		Phase:       "cross",
		Description: "HIGH-LEVEL: extract recurring patterns from engineering memory and surface those above a threshold. Patterns are promoted to memory (evidence-based). Deterministic — the LLM may explain but does not create patterns.",
		InputSchema: schema(map[string]any{
			"root":      strProp("Project root (defaults to current directory)"),
			"threshold": strProp("Minimum pattern count to surface (default 3)"),
		}, nil),
	},
	{
		Name:        "kern_modernize",
		Phase:       "cross",
		Description: "HIGH-LEVEL: analyze the monolith and produce a phased modernization plan (communities→bridges→churn→candidate boundaries→impact→risk→migration plan). Each extraction phase becomes an auditable Task.",
		InputSchema: schema(map[string]any{
			"root": strProp("Project root (defaults to current directory)"),
		}, nil),
	},
}

// Server handles MCP requests over a stdio stream or HTTP.
type Server struct {
	in       io.Reader // raw stdio reader, used to rebuild the scanner after an oversized line
	out      io.Writer
	mu       sync.Mutex
	toolsMu  sync.Mutex
	filtered []Tool // cached KERN_TOOLS-filtered tool list (nil = not computed)
	locks    map[string]*lock.Lock
	inflight map[string]context.CancelFunc
	sessions map[string]*project.Session
	// platforms caches one application Platform per project root, keyed to
	// the exact index instance it was built from. High-level handlers used to
	// rebuild the whole Platform (call graph + 4 twin extractors, each a full
	// tree walk) on every tool call; the cache reuses it while the session
	// serves the same index instance and rebuilds only after a real index
	// rebuild (new instance pointer).
	platforms map[string]*platformEntry
	transport string // "stdio" (default) or "http"
	// sem bounds how many tool calls may build an index concurrently (each
	// call can construct a full project index). Acquired before a tools/call
	// goroutine is spawned and released when it finishes.
	sem chan struct{}
	// roots confine every tool root/dir argument; KERN_ROOTS when set, else
	// the server's startup directory.
	roots []string
	// gate confines every tool call's path-typed arguments (root, dir, or any
	// key containing "path") to the KERN_MCP_ROOTS roots, resolving symlinks
	// before containment. A nil gate preserves the default behavior exactly.
	gate *Gate
	// commits caches the short HEAD commit per project root so git is spawned
	// at most once per root per server lifetime.
	commits map[string]string
	// indexOnce defers background index preloading until the first MCP
	// request (initialize or tools/call) instead of server startup, so
	// setup is instant and the index cost is paid while the user waits.
	indexOnce sync.Once
	// indexedRoots records which project roots have completed at least one
	// index build this process, so the "first build in progress" notice is
	// printed once per root instead of on every stale rebuild.
	indexedRoots sync.Map
	// preTool is an optional hook invoked before every tools/call execution.
	// It receives the tool name and its arguments and returns nil to allow the
	// call or an error to deny it (denial surfaces as a tool error response,
	// isError=true, no side effects). A nil hook preserves the default
	// behavior exactly — callers that never set it see zero change.
	preTool func(name string, args map[string]any) error
}

// WithPreToolHook registers a pre-tool-use hook. NewServer wires the
// KERN_MCP_ROOTS confinement gate as the default hook (opt out via
// KERN_MCP_NO_CONFINE=1); calling WithPreToolHook replaces that default with
// the caller's own governance/allowlist/accounting hook. A nil hook leaves
// every tools/call untouched, so callers that explicitly pass nil get the
// fully default behavior.
func (s *Server) WithPreToolHook(fn func(name string, args map[string]any) error) *Server {
	s.preTool = fn
	return s
}

// NewServer returns a *Server wired to the given reader/writer.
func NewServer(in io.Reader, out io.Writer) *Server {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<20), 64<<20)
	s := &Server{in: in, out: out, sem: make(chan struct{}, 8), locks: map[string]*lock.Lock{}, inflight: map[string]context.CancelFunc{}, sessions: map[string]*project.Session{}, transport: "stdio", roots: defaultWorkspaceRoots(), gate: confinementGate(), commits: map[string]string{}}
	// P0.1: register the built-in default agent so calls without an explicit
	// agent_id are governed (cwd-scoped) instead of raw. KERN_MCP_PERMISSIVE=1
	// remains the explicit opt-out that restores raw mode.
	registerDefaultAgent()
	// Confinement is default-on: the KERN_MCP_ROOTS gate runs as the
	// pre-tool-use hook, so a tool call whose path-typed arguments resolve
	// outside the allowed roots is denied before any handler side effect runs.
	// KERN_MCP_NO_CONFINE=1 opts out entirely. With no KERN_MCP_ROOTS the gate
	// fails closed to the process cwd; KERN_MCP_PERMISSIVE=1 restores the old
	// allow-all loopback-client-trust behavior.
	if s.gate != nil {
		s.preTool = s.gate.Check
	}
	return s
}

// confinementGate builds the KERN_MCP_ROOTS confinement gate, or nil when
// KERN_MCP_NO_CONFINE=1 opts out of confinement. A nil gate allows every
// call; a non-nil gate is enabled unless KERN_MCP_PERMISSIVE=1 opts out, and
// defaults its roots to the process cwd when KERN_MCP_ROOTS is unset.
func confinementGate() *Gate {
	if os.Getenv("KERN_MCP_NO_CONFINE") == "1" {
		return nil
	}
	return NewGateFromEnv()
}

// defaultWorkspaceRoots returns the roots tools may target: KERN_ROOTS when
// set, else the startup directory. Every tool root/dir is confined to these.
func defaultWorkspaceRoots() []string {
	var roots []string
	if env := os.Getenv("KERN_ROOTS"); env != "" {
		for _, r := range strings.FieldsFunc(env, func(r rune) bool { return r == ':' || r == ',' }) {
			r = strings.TrimSpace(r)
			if r != "" {
				roots = append(roots, resolveAbs(r))
			}
		}
	}
	if len(roots) == 0 {
		if cwd, err := os.Getwd(); err == nil {
			roots = []string{resolveAbs(cwd)}
		}
	}
	return roots
}

// resolveAbs cleans p to an absolute path.
func resolveAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(p)
}

// checkRootArg rejects any non-empty root/dir tool argument that resolves
// outside the server's workspace roots. Called once per tool call before
// dispatch so confinement cannot be forgotten for a new tool.
func (s *Server) checkRootArg(args map[string]any) error {
	for _, key := range []string{"root", "dir"} {
		v := argString(args, key)
		if v == "" {
			continue
		}
		if err := s.checkWithinWorkspace(v); err != nil {
			return fmt.Errorf("%s %q: %w", key, v, err)
		}
	}
	return nil
}

// checkWithinWorkspace reports whether p (absolute or relative) resolves
// inside one of the workspace roots, following symlinks. The resolved target
// must be the root itself or a descendant; a symlink pointing outside is
// rejected even though its text lives inside.
func (s *Server) checkWithinWorkspace(p string) error {
	real, err := realPath(p)
	if err != nil {
		return err
	}
	for _, r := range s.roots {
		rr, err := realPath(r)
		if err != nil {
			return err
		}
		if real == rr || within(rr, real) {
			return nil
		}
	}
	var roots []string
	for _, r := range s.roots {
		roots = append(roots, resolveAbs(r))
	}
	return fmt.Errorf("outside the allowed workspace (roots: %s)", strings.Join(roots, ", "))
}

// realPath resolves p to an absolute, symlink-resolved path. For paths that do
// not exist yet it resolves the nearest existing ancestor and re-appends the
// remaining components.
func realPath(p string) (string, error) {
	abs := resolveAbs(p)
	var rem []string
	probe := abs
	for {
		real, err := filepath.EvalSymlinks(probe)
		if err == nil {
			if len(rem) == 0 {
				return real, nil
			}
			return filepath.Join(append([]string{real}, rem...)...), nil
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return abs, fmt.Errorf("cannot resolve %q", p)
		}
		rem = append([]string{filepath.Base(probe)}, rem...)
		probe = parent
	}
}

// within reports whether child is parent or a descendant of parent.
func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// isNotification reports whether the request is a JSON-RPC notification: it
// has no id, so the client expects no response.
func (r rpcRequest) isNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

func (s *Server) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.out.Write(append(data, '\n'))
	return err
}

// progress sends a notifications/progress message for a tool call. Notifications
// are only pushed on transports that can deliver them (stdio); HTTP answers
// each request with a single response body and has no push channel (SSE is
// not supported). ctx guards against emitting progress after the request was
// cancelled or answered.
func (s *Server) progress(ctx context.Context, id string, tool string, pct int, msg string) {
	if s.transport != "stdio" {
		return
	}
	if ctx != nil && ctx.Err() != nil {
		return
	}
	n := map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/progress",
		"params": map[string]any{
			"progressToken": id,
			"progress":      pct,
			"total":         100,
			"message":       msg,
		},
	}
	_ = s.write(n)
}

// startProgress emits an initial 0% notification for a slow tool and returns a
// stop func that emits 100% and halts the background ticker. stop blocks until
// the ticker goroutine has exited, so a stale "still running" emission can
// never arrive after "finished"; every emission is gated on ctx.Err() and the
// writer mutex, so a progress message can never arrive after the final
// response.
func (s *Server) startProgress(ctx context.Context, id, tool string) func() {
	done := make(chan struct{})
	var wg sync.WaitGroup
	s.progress(ctx, id, tool, 0, tool+" running")
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				s.progress(ctx, id, tool, -1, tool+" still running")
			}
		}
	}()
	return func() {
		close(done)
		wg.Wait()
		s.progress(ctx, id, tool, 100, tool+" finished")
	}
}

// cancelAll cancels every in-flight tool call and releases every lock held by
// this server. It is invoked on graceful shutdown (SIGINT/SIGTERM) so slow
// tools stop promptly and the workspace is left unlocked. The inflight map is
// deliberately NOT cleared here: each running tool goroutine removes its own
// entry (unregisterInflight) once it finishes, so Inflight() keeps reporting
// still-running tools and the shutdown drain can wait for them to exit.
func (s *Server) cancelAll() {
	s.mu.Lock()
	for _, cancel := range s.inflight {
		cancel()
	}
	var held []*lock.Lock
	for _, lk := range s.locks {
		held = append(held, lk)
	}
	s.locks = map[string]*lock.Lock{}
	s.mu.Unlock()
	for _, lk := range held {
		_ = lk.Release()
	}
}

// CancelAll aborts in-flight tools and releases held locks. Safe to call from
// a signal handler or after Serve returns.
func (s *Server) CancelAll() { s.cancelAll() }

// Inflight returns the number of tool calls currently registered as
// in-flight. Graceful shutdown polls it after CancelAll so it can wait for
// cancelled tools to drain their responses before exiting.
func (s *Server) Inflight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.inflight)
}

// registerInflight stores the cancel func for a request id so $/cancelRequest
// and graceful shutdown can abort it.
func (s *Server) registerInflight(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	if s.inflight == nil {
		s.inflight = map[string]context.CancelFunc{}
	}
	s.inflight[id] = cancel
	s.mu.Unlock()
}

// unregisterInflight removes the cancel func once the tool call has finished.
func (s *Server) unregisterInflight(id string) {
	s.mu.Lock()
	delete(s.inflight, id)
	s.mu.Unlock()
}

// Serve runs until the stream ends.
// preloadIndexes builds and caches each workspace root's index in the
// background so a later tool call reuses it instead of blocking on a cold
// build. It is triggered lazily on the first MCP request (see indexOnce) and
// runs in a goroutine so setup never blocks on it. Skipped for filesystem
// roots and when KERN_PRELOAD=0 (test/CI guard).
func (s *Server) preloadIndexes() {
	if os.Getenv("KERN_PRELOAD") == "0" {
		return
	}
	for _, r := range s.workspaceRoots() {
		if isFilesystemRoot(r) {
			continue
		}
		go func(root string) {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "kern-mcp: preload index %s: panic recovered: %v\n", root, r)
				}
			}()
			_, err := s.sessionFor(root).Index()
			if err != nil {
				fmt.Fprintf(os.Stderr, "kern-mcp: preload index %s: %v\n", root, err)
				return
			}
			// Record the build so a later graph tool call that reuses this
			// warm index does not print the "first build in progress" notice.
			s.indexedRoots.Store(root, true)
		}(r)
	}
}

// isFilesystemRoot reports whether p is a filesystem root ("/" on Unix, or a
// drive root on Windows) that must never be treated as a project to index.
func isFilesystemRoot(p string) bool {
	vol := filepath.VolumeName(p)
	return filepath.Clean(p) == filepath.Clean(vol+string(filepath.Separator))
}

// workspaceRoots returns the roots field, falling back to the default when
// unset (defensive; NewServer always initializes it).
func (s *Server) workspaceRoots() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.roots) == 0 {
		return defaultWorkspaceRoots()
	}
	out := append([]string(nil), s.roots...)
	return out
}

func (s *Server) Serve() error {
	if s.sem == nil {
		s.sem = make(chan struct{}, 8)
	}
	var wg sync.WaitGroup
	defer wg.Wait() // drain in-flight tool calls before returning on EOF
	// newScanner rebuilds the stdio line scanner. A scanner cannot be reused
	// after it hits bufio.ErrTooLong, so it is recreated from the raw reader.
	newScanner := func() *bufio.Scanner {
		sc := bufio.NewScanner(s.in)
		sc.Buffer(make([]byte, 64<<20), 64<<20)
		return sc
	}
	for {
		sc := newScanner()
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 {
				continue
			}
			var req rpcRequest
			if err := json.Unmarshal(line, &req); err != nil {
				// A malformed line is a protocol error, not a silent drop.
				if err := s.write(errorResponse(nil, -32700, "parse error: "+err.Error())); err != nil {
					return err
				}
				continue
			}
			if req.Method == "tools/call" {
				// Run tools concurrently so a slow tool (build, heal, LLM
				// optimize) can never freeze the stdio server: $/cancelRequest,
				// progress and other tool calls keep being served while it runs.
				// Response writes are serialized by s.write's mutex, and the
				// request is captured by value so the loop can move on.
				// Concurrency is bounded by s.sem so a burst of tool calls cannot
				// spawn unbounded goroutines that each build a full project index.
				wg.Add(1)
				s.sem <- struct{}{}
				go func(req rpcRequest) {
					defer wg.Done()
					defer func() { <-s.sem }()
					if resp := s.safeDispatch(req); resp != nil {
						if err := s.write(resp); err != nil {
							fmt.Fprintf(os.Stderr, "kern-mcp: write response: %v\n", err)
						}
					}
				}(req)
				continue
			}
			if resp := s.safeDispatch(req); resp != nil {
				if err := s.write(resp); err != nil {
					return err
				}
			}
		}
		// A single oversized message (>64MB) must not kill the server: the
		// scanner hit bufio.ErrTooLong, so skip the oversized token (in 64MB
		// chunks until its newline passes), recreate the scanner and keep
		// serving instead of terminating on the error.
		if sc.Err() == bufio.ErrTooLong {
			fmt.Fprintf(os.Stderr, "kern-mcp: skipping oversized input line (> %d bytes); continuing\n", 64<<20)
			continue
		}
		return sc.Err()
	}
}

// safeDispatch computes a request's response, converting any panic into an
// internal-error response instead of crashing the server.
func (s *Server) safeDispatch(req rpcRequest) (r any) {
	defer func() {
		if rec := recover(); rec != nil {
			r = errorResponse(req.ID, -32603, fmt.Sprintf("internal error: %v", rec))
			fmt.Fprintf(os.Stderr, "kern-mcp: panic serving %s: %v\n%s\n", req.Method, rec, debug.Stack())
		}
	}()
	return s.dispatch(req)
}

// Close stops all background file watchers associated with this server's
// sessions. It is safe to call multiple times.
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sess := range s.sessions {
		sess.Close()
	}
}

// dispatch computes the JSON-RPC response for a request. A nil return means
// the request needs no response (e.g. a notification). The response is a
// transport-neutral object so both stdio and HTTP can send it.
func (s *Server) dispatch(req rpcRequest) any {
	// KERN_MCP_ROOTS confinement gate (memory-#31): every tools/call whose
	// path-typed arguments (root, dir, or any key containing "path") resolve
	// outside the allowed roots is rejected here, before any handler runs.
	// The rejection uses the same result shape a handler error produces — a
	// tool result with isError=true — so the client sees a clean tool error
	// rather than a panic or a JSON-RPC error. A nil or disabled gate is a
	// no-op, keeping the default (loopback-client trust) behavior identical.
	if s.gate != nil && s.gate.enabled && req.Method == "tools/call" {
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if json.Unmarshal(req.Params, &p) == nil {
			if err := s.gate.Check(p.Name, p.Arguments); err != nil {
				return map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"result": map[string]any{
						"content": []any{map[string]any{"type": "text", "text": "pre-tool-use denied: " + err.Error()}},
						"isError": true,
					},
				}
			}
		}
	}
	// $/cancelRequest is a notification per JSON-RPC (no response expected),
	// but must still be processed: dispatch returns nil for notifications
	// below, so it is handled here before the short-circuit.
	if req.Method == "$/cancelRequest" {
		var p struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(req.Params, &p)
		key := idKey(p.ID)
		s.mu.Lock()
		cancel, ok := s.inflight[key]
		s.mu.Unlock()
		if ok && cancel != nil {
			cancel()
		}
		if req.isNotification() {
			return nil
		}
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}}
	}
	if req.isNotification() {
		return nil
	}
	switch req.Method {
	case "initialize":
		// Kick off background indexing on the first connection so setup stays
		// instant: graph tools block until the build finishes, while tools
		// that don't need the index serve immediately. indexOnce guards the
		// trigger so it fires exactly once per server lifetime.
		s.indexOnce.Do(func() { go s.preloadIndexes() })
		caps := map[string]any{
			"tools":   map[string]any{"listChanged": false},
			"prompts": map[string]any{"listChanged": false},
		}
		if s.transport == "http" {
			caps["streamableHttpCapabilities"] = map[string]any{"sse": false}
		}
		// Echo the client's negotiated protocol version when it is one we
		// support; otherwise report the version we implement.
		version := protocolVersion
		var initParams struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(req.Params, &initParams) == nil && supportedProtocolVersions[initParams.ProtocolVersion] {
			version = initParams.ProtocolVersion
		}
		return map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{
				"protocolVersion": version,
				"capabilities":    caps,
				"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
			},
		}
	case "notifications/initialized":
		return nil
	case "ping":
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}}
	case "tools/list":
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": s.filteredTools()}}
	case "tools/call":
		return s.toolCallResponse(req.ID, req.Params)
	case "prompts/list":
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"prompts": prompts}}
	case "prompts/get":
		return s.promptGetResponse(req.ID, req.Params)
	default:
		return errorResponse(req.ID, -32601, "method not found: "+req.Method)
	}
}

func errorResponse(id json.RawMessage, code int, msg string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": code, "message": msg},
	}
}

// idKey canonicalizes a JSON-RPC id into the map key used to track in-flight
// requests, so tools/call and $/cancelRequest agree on the same key whether
// the client used a JSON number (77) or string ("77") id. A raw id that does
// not parse is used verbatim.
func idKey(id json.RawMessage) string {
	var v any
	if err := json.Unmarshal(id, &v); err != nil || v == nil {
		return string(id)
	}
	switch t := v.(type) {
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case string:
		return t
	default:
		return fmt.Sprintf("%v", v)
	}
}
func (s *Server) toolCallResponse(id json.RawMessage, params json.RawMessage) any {
	// First real use: start background indexing (no-op if initialize already
	// triggered it — indexOnce fires exactly once per server lifetime).
	s.indexOnce.Do(func() { go s.preloadIndexes() })
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResponse(id, -32602, "invalid params")
	}
	key := idKey(id)
	// Pre-tool-use hook: deny the call before any side effect runs. The hook
	// is optional (nil = no-op) and returns nil to allow, or an error to
	// reject — rejection is reported as a tool error (isError=true) so the
	// agent sees why the call was blocked.
	if s.preTool != nil {
		if err := s.preTool(p.Name, p.Arguments); err != nil {
			return map[string]any{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]any{
					"content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("pre-tool-use denied: %s", err)}},
					"isError": true,
				},
			}
		}
	}
	// A generous 30-minute per-call ceiling so a hung subprocess (an
	// unresponsive Ollama during plan/analyze, or a slow index build) can
	// never wedge the server goroutine forever; long legitimate operations
	// (kern_execute sandbox builds, kern_verify full suites) run within it.
	// This is the effective cap for plugin-driven calls: it must be >= the
	// plugin MAX_CEILING_MS (kern.ts) so the agent's requested timeout
	// governs. The exec-family handlers install their own shorter deadlines
	// on top.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	// The per-call scope carries the index loaded during this tool's execution
	// so provenance is stamped from this call's index, never another's. It is
	// scoped here instead of on the Server struct to avoid cross-talk between
	// concurrent tool calls.
	scope := &indexScope{}
	ctx = context.WithValue(ctx, indexScopeKey{}, scope)
	s.registerInflight(key, cancel)
	defer func() {
		cancel()
		s.unregisterInflight(key)
	}()
	text, err := func() (out string, runErr error) {
		defer func() {
			if rec := recover(); rec != nil {
				runErr = fmt.Errorf("panic in tool %s: %v", p.Name, rec)
				fmt.Fprintf(os.Stderr, "kern-mcp: panic running %s: %v\n%s\n", p.Name, rec, debug.Stack())
			}
		}()
		return s.runTool(ctx, key, p.Name, p.Arguments)
	}()
	// Cap every tool response at the output budget so a large result cannot
	// flood the agent's context. Overridable per call with max_output=N.
	if err == nil {
		var budget int
		budget, err = callOutputBudget(p.Arguments)
		if err == nil {
			text = sandboxOutput(text, budget, p.Name)
		}
	}
	result := map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
		"isError": false,
	}
	// Structured provenance (P1.2): retrieval handlers stamp it on the
	// per-call scope; any other tool that loaded an index gets
	// index-identity-only raw provenance. The one-line summary appended to
	// the content text is derived from the same structured field, so there
	// is a single source of truth for index evidence.
	if scope.prov == nil && scope.ix != nil {
		scope.prov = s.rawProvenance(scope.ix, nil)
	}
	if err != nil {
		if scope.prov != nil {
			// Errors can carry provenance too: governed denials attach the
			// auditable authorizing rule alongside the error text.
			text = err.Error() + "\n" + s.provenanceSummary(scope.ix, scope.prov)
			result["provenance"] = scope.prov
		} else {
			text = err.Error()
		}
		result["content"] = []any{map[string]any{"type": "text", "text": text}}
		result["isError"] = true
	} else if scope.prov != nil {
		result["provenance"] = scope.prov
		result["content"] = []any{map[string]any{"type": "text", "text": text + "\n" + s.provenanceSummary(scope.ix, scope.prov)}}
	}
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
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

// callOutputBudget returns the per-call budget: an explicit max_output=N
// argument (bytes; 0 disables the sandbox) wins over the global cap. A
// malformed max_output is an error, not a silent fallback.
func callOutputBudget(args map[string]any) (int, error) {
	if v := argString(args, "max_output"); v != "" {
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

// sandboxOutput truncates text to budget bytes (when budget > 0) and stamps a
// marker with before/after token counts and a tool-specific recovery hint. The
// marker doubles as the anti-context-flood boundary: an agent that needs more
// can re-call with a larger max_output or a narrower tool.
func sandboxOutput(text string, budget int, tool string) string {
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
	case "kern_walk":
		return "Use a shallower depth= or a different root symbol."
	case "kern_near":
		return "Lower max= or depth=."
	case "kern_context":
		return "Request fewer lines=."
	case "kern_review", "kern_context_budget":
		return "Lower max_tokens=."
	case "kern_doc_search":
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

func (s *Server) promptGetResponse(id json.RawMessage, params json.RawMessage) any {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResponse(id, -32602, "invalid params")
	}
	var def *Prompt
	for i := range prompts {
		if prompts[i].Name == p.Name {
			def = &prompts[i]
			break
		}
	}
	if def == nil {
		return errorResponse(id, -32602, "prompt not found: "+p.Name)
	}
	if p.Arguments == nil {
		p.Arguments = map[string]any{}
	}
	return map[string]any{
		"jsonrpc": "2.0", "id": id,
		"result": map[string]any{
			"description": def.Description,
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": map[string]any{
						"type": "text",
						"text": promptText(p.Name, p.Arguments),
					},
				},
			},
		},
	}
}

func argString(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

// argBool reads an optional boolean tool argument. Accepts native bools and
// the strings "true"/"1" (MCP clients often pass everything as strings).
func argBool(args map[string]any, key string) bool {
	v, ok := args[key]
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		s := strings.TrimSpace(t)
		return s == "true" || s == "1"
	case float64:
		return t != 0
	}
	return false
}

// atoiArg parses an integer tool argument, falling back to def for empty
// input. A malformed value is an error, not a silent default, so a typo'd
// number can't quietly zero out a limit or mis-size a buffer.
func atoiArg(v string, def int) (int, error) {
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid integer %q", v)
	}
	return n, nil
}

// rootedPath resolves p for a file-reading tool. When root is given, the path
// must stay inside it (rejecting "..", absolute paths outside, and symlink
// escapes). A rootless call may only reference a path relative to the current
// working directory: an absolute path is rejected outright, since otherwise a
// caller could pass e.g. path=/etc/shadow and read any file on the system
// outside the confined workspace.
func rootedPath(root, p string) (string, error) {
	if root == "" {
		if filepath.IsAbs(p) {
			return "", fmt.Errorf("absolute path requires root argument")
		}
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(cwd, p), nil
	}
	return withinRoot(root, p)
}

func (s *Server) runTool(ctx context.Context, id string, name string, args map[string]any) (out string, runErr error) {
	// Record every incoming tool call and its duration; the defer covers all
	// return paths, and runErr is non-nil exactly when dispatch returned an error.
	metrics.Default().RecordRequest()
	start := time.Now()
	defer func() {
		metrics.Default().RecordToolCall(time.Since(start))
		if runErr != nil {
			metrics.Default().RecordError()
		}
	}()
	name, err := s.precheckTool(name, args)
	if err != nil {
		return "", err
	}
	return s.dispatchTool(ctx, id, name, args)
}

// precheckTool validates a tool name against the KERN_TOOLS allowlist and the
// root argument before dispatch, resolving blocked tools to their
// policy-approved fallback. It returns the (possibly fallback) tool name to
// dispatch, or an error when the tool is not allowed and no allowed
// alternative exists, or when the root argument fails validation.
func (s *Server) precheckTool(name string, args map[string]any) (string, error) {
	// Validates against the full registered catalog by design — phase
	// filtering (KERN_MCP_PHASE) only affects advertisement, never execution.
	// Resolve against the FULL registered catalog, not the advertised
	// (filtered) set: kern_meta's NL router reaches every sub-tool handler
	// internally even when the sub-tool is not advertised, and an explicit
	// KERN_TOOLS allowlist still gates execution here.
	if !toolAllowed(tools, name) {
		// Tool fallback: when a tool is blocked by the KERN_TOOLS
		// allowlist, route to its policy-approved alternative if one exists and
		// IS allowed, so a restricted deployment still gets an equivalent
		// result instead of a hard failure. Fail closed when no allowed
		// alternative exists.
		if alt := app.FallbackFor(name); alt != "" {
			if toolAllowed(tools, alt) {
				name = alt
			}
		}
		// Fail closed only when no allowed alternative exists.
		if !toolAllowed(tools, name) {
			return "", fmt.Errorf("tool %q is not allowed (KERN_TOOLS allowlist)", name)
		}
	}
	if err := s.checkRootArg(args); err != nil {
		return "", err
	}
	if root := argString(args, "root"); root != "" {
		if err := validateRoot(root); err != nil {
			return "", err
		}
	}
	return name, nil
}

// dispatchTool routes a prechecked tool name to its handler. Every registered
// kern_* tool has a case here; runTool applies allowlist and root validation
// via precheckTool before dispatching, so dispatchTool only ever sees a tool
// that passed the gate.
func (s *Server) dispatchTool(ctx context.Context, id string, name string, args map[string]any) (string, error) {
	switch name {
	case "kern_meta":
		return s.handleMeta(ctx, args)
	case "kern_optimize_prompt":
		return s.handleOptimizePrompt(ctx, args)
	case "kern_memory_add":
		return s.handleMemoryAdd(ctx, args)
	case "kern_memory_list":
		return s.handleMemoryList(ctx, args)
	case "kern_memory_recall":
		return s.handleMemoryRecall(ctx, args)
	case "kern_mask_pii":
		return s.handleMaskPII(ctx, args)
	case "kern_security":
		return s.handleSecurity(ctx, args)
	case "kern_safe_delete":
		return s.handleSafeDelete(ctx, args)
	case "kern_doc_search":
		return s.handleDocSearch(ctx, args)
	case "kern_doc_index":
		return s.handleDocIndex(ctx, args)
	case "kern_doc_fetch":
		return s.handleDocFetch(ctx, args)
	case "kern_commitmsg":
		return s.handleCommitmsg(ctx, args)
	case "kern_precache":
		return s.handlePrecache(ctx, args)
	case "kern_swap":
		return s.handleSwap(ctx, args)
	case "kern_sandbox":
		return s.handleSandbox(ctx, id, args)
	case "kern_diff_files":
		return s.handleDiffFiles(ctx, args)
	case "kern_heal":
		return s.handleHeal(ctx, id, args)
	case "kern_validate":
		return s.handleValidate(ctx, args)
	case "kern_schema_validate":
		return s.handleSchemaValidate(ctx, args)
	case "kern_verify_output":
		return s.handleVerifyOutput(ctx, args)
	case "kern_compact_file":
		return s.handleCompact(ctx, args)
	case "kern_buddy":
		return s.handleBuddy(ctx, args)
	case "kern_project_map":
		return s.handleProjectMap(ctx, args)
	case "kern_pack":
		return s.handlePack(ctx, args)
	case "kern_run_build":
		return s.handleRunBuild(ctx, id, args)
	case "kern_optimize_log":
		return s.handleOptimizeLog(ctx, args)
	case "kern_optimize_output":
		return s.handleOptimizeOutput(ctx, args)
	case "kern_stats":
		return s.handleStats(ctx, args)
	case "kern_semcache":
		return s.handleSemcache(ctx, args)
	case "kern_context_budget":
		return s.handleContextBudget(ctx, args)
	case "kern_ast_search":
		return s.handleAstSearch(ctx, args)
	case "kern_frameworks":
		return s.handleFrameworks(ctx, args)
	case "kern_entry_points":
		return s.handleEntryPoints(ctx, args)
	case "kern_search":
		return s.handleSearch(ctx, args)
	case "kern_repo_search":
		return s.handleRepoSearch(ctx, args)
	case "kern_why":
		return s.handleWhy(ctx, args)
	case "kern_code_graph":
		return s.handleCodeGraph(ctx, args)
	case "kern_inherits":
		return s.handleInherits(ctx, args)
	case "kern_context":
		return s.handleContext(ctx, args)
	case "kern_changes":
		return s.handleChanges(ctx, args)
	case "kern_review":
		return s.handleReview(ctx, args)
	case "kern_hubs":
		return s.handleHubs(ctx, args)
	case "kern_test_gaps":
		return s.handleTestGaps(ctx, args)
	case "kern_path":
		return s.handlePath(ctx, args)
	case "kern_dead":
		return s.handleDead(ctx, args)
	case "kern_larges":
		return s.handleLarges(ctx, args)
	case "kern_arch":
		return s.handleArch(ctx, args)
	case "kern_communities":
		return s.handleCommunities(ctx, args)
	case "kern_churn":
		return s.handleChurn(ctx, args)
	case "kern_near", "kern_walk":
		return s.handleNear(ctx, args)
	case "kern_graph":
		return s.handleGraph(ctx, args)
	case "kern_explore":
		return s.handleExplore(ctx, args)
	case "kern_fts_search":
		return s.handleFtsSearch(ctx, args)
	case "kern_bridges":
		return s.handleBridges(ctx, args)
	case "kern_cochange":
		return s.handleCochange(ctx, args)
	case "kern_probe":
		return s.handleProbe(ctx, args)
	case "kern_trace":
		return s.handleTrace(ctx, args)
	case "kern_lock":
		return s.handleLock(ctx, args)
	case "kern_unlock":
		return s.handleUnlock(ctx, args)
	case "kern_lock_status":
		return s.handleLockStatus(ctx, args)
	case "kern_usage_guide":
		return s.handleUsageGuide(ctx, args)
	case "kern_guard_check":
		return s.handleGuardCheck(ctx, args)
	case "kern_authorize_context":
		return s.handleAuthorizeContext(ctx, args)
	case "kern_rename":
		return s.handleRename(ctx, args)
	case "kern_exec":
		return s.handleExec(ctx, args)
	case "kern_analyze":
		return s.handleAnalyze(ctx, args)
	case "kern_plan":
		return s.handlePlan(ctx, args)
	case "kern_execute":
		return s.handleExecute(ctx, args)
	case "kern_verify":
		return s.handleVerify(ctx, args)
	case "kern_incident":
		return s.handleIncident(ctx, args)
	case "kern_what_if":
		return s.handleWhatIf(ctx, args)
	case "kern_impact":
		return s.handleImpact(ctx, args)
	case "kern_memory":
		return s.handleMemory(ctx, args)
	case "kern_agents":
		return s.handleAgents(ctx, args)
	case "kern_loop":
		return s.handleLoop(ctx, args)
	case "kern_run":
		return s.handleRun(ctx, args)
	case "kern_workflow":
		return s.handleWorkflow(ctx, args)
	case "kern_onboard":
		return s.handleOnboard(ctx, args)
	case "kern_audit":
		return s.handleAudit(ctx, args)
	case "kern_approve":
		return s.handleApprove(ctx, args)
	case "kern_correlate":
		return s.handleCorrelate(ctx, args)
	case "kern_learn":
		return s.handleLearn(ctx, args)
	case "kern_modernize":
		return s.handleModernize(ctx, args)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// analyzeChange and simulateChange have been migrated to internal/app.Platform.
// The MCP handlers now call app.New(root) + p.Analyze/WhatIf so the
// orchestration is shared with CLI and REST instead of duplicated here.

// changedContext resolves the changed files for a tool call: an explicit
// comma-separated file list wins; otherwise the git range (empty = working
// tree). Returns line-aware FileChanges so blast radius can be scoped to the
// changed hunks.
func (s *Server) changedContext(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
	root := resolveRoot(argString(args, "root"))
	ix, err := s.loadIndex(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	if files := argString(args, "file"); files != "" {
		var out []intel.FileChange
		for _, p := range strings.Split(files, ",") {
			if p = strings.TrimSpace(p); p != "" {
				resolved, err := withinRoot(root, p)
				if err != nil {
					return nil, nil, err
				}
				rel, err := filepath.Rel(root, resolved)
				if err != nil {
					return nil, nil, err
				}
				out = append(out, intel.FileChange{File: rel})
			}
		}
		return out, ix, nil
	}
	from, to := "", ""
	if r := argString(args, "range"); r != "" {
		if p := strings.SplitN(r, "..", 2); len(p) == 2 {
			from, to = p[0], p[1]
		} else {
			from = r
		}
	}
	changes, err := intel.FilesForRangeL(root, from, to)
	if err != nil {
		return nil, nil, err
	}
	return changes, ix, nil
}

func pct(before, after int) float64 {
	if before <= 0 {
		return 0
	}
	return float64(before-after) / float64(before) * 100
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func truncateMCP(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (truncated)"
}

func splitLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// sessionFor returns the project session for root, creating and caching one
// per root so index state and stats identity are shared across tool calls.
func (s *Server) sessionFor(root string) *project.Session {
	root = resolveRoot(root)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = map[string]*project.Session{}
	}
	sess, ok := s.sessions[root]
	if !ok {
		sess = project.New(root, "")
		s.sessions[root] = sess
	}
	return sess
}

// resolveRoot cleans a tool root argument to an absolute path and requires it
// to be an existing directory. An empty root falls back to the current working
// directory. This guards every index-using tool against traversal-style root
// values and turns confusing downstream index errors into clear ones.
func resolveRoot(root string) string {
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = filepath.Clean(abs)
	}
	return root
}

// withinRoot resolves file against root (absolute paths are used as-is) and
// requires the result to stay inside root, rejecting `..` escapes, absolute
// paths that point outside the project boundary, and symlink escapes (a
// symlink inside the project that points outside). It returns the resolved
// absolute path.
func withinRoot(root, file string) (string, error) {
	var abs string
	if filepath.IsAbs(file) {
		abs = filepath.Clean(file)
	} else {
		abs = filepath.Join(root, file)
	}
	// Resolve symlinks on both the root and the candidate so a symlink inside
	// the project that points outside cannot read/escape the project boundary.
	// If either resolution fails (e.g. the file does not exist yet), fall back
	// to the lexical Clean+Rel check below.
	if rRoot, rerr := filepath.EvalSymlinks(root); rerr == nil {
		if rAbs, aerr := filepath.EvalSymlinks(abs); aerr == nil {
			rel, err := filepath.Rel(rRoot, rAbs)
			if err != nil {
				return "", fmt.Errorf("resolve %q: %w", file, err)
			}
			if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
				return "", fmt.Errorf("path %s escapes project root %s", abs, root)
			}
			return abs, nil
		}
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", file, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %s escapes project root %s", abs, root)
	}
	return abs, nil
}

// validateRoot rejects a tool root that exists on disk but is not a directory
// (e.g. a file). A nonexistent root is allowed because several tools create the
// target directory before indexing it. The filesystem root ("/") is also
// rejected: it is never a project to index. It returns "" on success so it can
// be called inline as `if msg := validateRoot(root); msg != "" { return "", msg }`.
func validateRoot(root string) error {
	root = resolveRoot(root)
	if isFilesystemRoot(root) {
		return fmt.Errorf("root %q is a filesystem root and cannot be treated as a project", root)
	}
	if st, err := os.Stat(root); err == nil {
		if !st.IsDir() {
			return fmt.Errorf("root %q exists but is not a directory", root)
		}
	}
	return nil
}

// indexScope carries the symbol index loaded during one tool call so provenance
// can be stamped onto that same call's response. It lives on the per-request
// context instead of the Server struct, so concurrent tool calls never share a
// mutable lastIndex and read each other's provenance.
type indexScope struct {
	ix   *index.Index
	prov *Provenance // structured evidence stamped by retrieval handlers
}
type indexScopeKey struct{}

// loadIndex returns the session's symbol index, reused while fresh and rebuilt
// when stale or missing (see project.Session.Index). The returned index is
// recorded on the per-call scope so the tool response can be stamped with
// provenance.
func (s *Server) loadIndex(ctx context.Context, root string) (*index.Index, error) {
	// First time this root is indexed in-process (including while a lazy
	// background preload is still running): tell the user the wait is the
	// initial build, not a hang. Subsequent stale rebuilds stay silent.
	if _, built := s.indexedRoots.Load(root); !built {
		fmt.Fprintf(os.Stderr, "kern-mcp: first index build for %s in progress, tool call waiting...\n", root)
	}
	ix, err := s.sessionFor(root).Index()
	if err != nil {
		return nil, err
	}
	s.indexedRoots.Store(root, true)
	if scope, ok := ctx.Value(indexScopeKey{}).(*indexScope); ok {
		scope.ix = ix
	}
	return ix, nil
}

// platformEntry ties a cached Platform to the exact index instance it was
// built from. The Platform owns the call graph and the twin-merged knowledge
// graph; constructing it runs intelligence.FromIndex plus four twin
// extractors, each a full filesystem walk. Reusing it while the session
// serves the same index instance turns ~5 tree walks per high-level tool
// call into zero.
type platformEntry struct {
	ix *index.Index
	p  *app.Platform
}

// platformFor returns the shared application Platform for root, caching it
// per root keyed by the index instance it was built from. While
// project.Session serves the same *index.Index pointer (fresh index, 1s
// staleness cooldown), the cached Platform is returned; a real index rebuild
// allocates a new instance, so the next call rebuilds the Platform exactly
// once. The Platform and its graph are treated as read-only after
// construction (same contract as web.App), so sharing across tool calls is
// safe. Handlers that previously called loadIndex + app.NewWithIndex per
// call should use this instead.
func (s *Server) platformFor(ctx context.Context, root string) (*app.Platform, error) {
	ix, err := s.loadIndex(ctx, root)
	if err != nil {
		return nil, err
	}
	root = resolveRoot(root)
	s.mu.Lock()
	if s.platforms == nil {
		s.platforms = map[string]*platformEntry{}
	}
	if e, ok := s.platforms[root]; ok && e.ix == ix {
		p := e.p
		s.mu.Unlock()
		return p, nil
	}
	s.mu.Unlock()
	p, err := app.NewWithIndex(root, ix)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	// Another caller may have populated the cache while we built; prefer the
	// existing entry when it matches the same index instance (identical
	// content, saves the duplicate graph).
	if e, ok := s.platforms[root]; ok && e.ix == ix {
		p = e.p
	} else {
		s.platforms[root] = &platformEntry{ix: ix, p: p}
	}
	s.mu.Unlock()
	return p, nil
}

// commit returns the short HEAD commit of root, cached per root. Git is
// optional: a non-repo root or missing git yields "" (omitted from the stamp).
func (s *Server) commit(root string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.commits[root]; ok {
		return c
	}
	c := ""
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--short", "HEAD").Output(); err == nil {
		c = strings.TrimSpace(string(out))
	}
	s.commits[root] = c
	return c
}

// splitShellLine tokenizes a command line into argv, honoring single and
// double quotes so the documented `sh -c 'cmd ...'` (and Windows `cmd /c "..."`)
// form is preserved as a single argument instead of being split by whitespace.
func splitShellLine(line string) []string {
	var (
		out      []string
		cur      strings.Builder
		inSingle bool
		inDouble bool
		started  bool
	)
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			} else {
				cur.WriteByte(c)
			}
		case inDouble:
			if c == '"' {
				inDouble = false
			} else {
				cur.WriteByte(c)
			}
		case c == '\'':
			inSingle = true
			started = true
		case c == '"':
			inDouble = true
			started = true
		case c == ' ' || c == '\t':
			if started {
				out = append(out, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteByte(c)
			started = true
		}
	}
	if started {
		out = append(out, cur.String())
	}
	return out
}

func renderOptimize(label string, res optimize.Result) string {
	return fmt.Sprintf("%s (tokens: %d -> %d, saved %d (%.1f%%)):\n%s",
		label, res.BeforeTokens, res.AfterTokens, res.SavedTokens, res.SavedPercent, res.Output)
}

// clipForMarker shortens a matched-input preview so a multi-megabyte log match
// never floods the "served from semantic cache" marker.
func clipForMarker(s string) string {
	const max = 80
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// clip trims a string to n bytes for a tool summary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// sanitizeDocName constrains a doc name to a safe cache filename:
// lowercase alphanumerics and dashes only. Path separators, dot-dot and
// other punctuation are replaced (or collapse to nothing), so a name can
// never escape the cache root via ../ or produce a bogus index key.
// Empty input yields an error.
func sanitizeDocName(name string) (string, error) {
	name = strings.TrimSpace(name)
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	if out := strings.Trim(b.String(), "-"); out != "" {
		return out, nil
	}
	return "", fmt.Errorf("invalid doc name %q", name)
}

// docSearchSlug derives a filesystem-safe doc name from a URL, e.g.
// https://react.dev/reference/usestate -> react-dev-reference-usestate.
func docSearchSlug(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "doc"
	}
	name := u.Hostname() + u.Path
	name = strings.TrimSuffix(name, "/")
	if out, err := sanitizeDocName(name); err == nil {
		return out
	}
	return "doc"
}

func renderStats(daysStr, session string) (string, error) {
	days := 7
	if daysStr != "" {
		if _, err := fmt.Sscanf(daysStr, "%d", &days); err != nil {
			return "", fmt.Errorf("invalid days: %s", daysStr)
		}
	}
	rec, err := stats.NewRecorder()
	if err != nil {
		return "", err
	}
	sum, err := rec.Summarize(days, session)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("operations=%d before=%d after=%d saved=%d (%.1f%%) cost_saved=$%.4f",
		sum.Operations, sum.BeforeTotal, sum.AfterTotal, sum.SavedTotal, sum.SavedPct, sum.CostSaved), nil
}

// ensureRecorder wires the shared stats recorder used by optimize operations.
func ensureRecorder() error {
	rec, err := stats.NewRecorder()
	if err != nil {
		return err
	}
	optimize.Recorder = rec
	return nil
}
