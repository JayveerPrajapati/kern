// Package mcp implements a minimal Model Context Protocol server over stdio.
// It is deliberately dependency-free so the binary stays offline and static.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/commitmsg"
	"github.com/JayveerPrajapati/kern/internal/diff"
	"github.com/JayveerPrajapati/kern/internal/docsearch"
	"github.com/JayveerPrajapati/kern/internal/fetch"
	"github.com/JayveerPrajapati/kern/internal/fw"
	"github.com/JayveerPrajapati/kern/internal/heal"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/lock"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/pack"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/precache"
	"github.com/JayveerPrajapati/kern/internal/project"
	"github.com/JayveerPrajapati/kern/internal/rename"
	"github.com/JayveerPrajapati/kern/internal/sandbox"
	jsonschema "github.com/JayveerPrajapati/kern/internal/schema"
	"github.com/JayveerPrajapati/kern/internal/script"
	"github.com/JayveerPrajapati/kern/internal/sec"
	"github.com/JayveerPrajapati/kern/internal/semcache"
	"github.com/JayveerPrajapati/kern/internal/stats"
	"github.com/JayveerPrajapati/kern/internal/swap"
	"github.com/JayveerPrajapati/kern/internal/terse"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
	"github.com/JayveerPrajapati/kern/internal/validate"
	"github.com/JayveerPrajapati/kern/internal/verify"
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
}

// ToolNames returns every registered MCP tool name. Used by tests to assert
// catalog parity with downstream surfaces (e.g. the opencode plugin must not
// drift from the MCP server that all agents actually consume).
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

// toolAllowed reports whether name passes the KERN_TOOLS allowlist. tools is
// the (already filtered or full) registered catalog; a nil allowlist allows
// everything, otherwise membership in the catalog decides.
func toolAllowed(toolsList []Tool, name string) bool {
	allowed := toolAllowlist()
	if len(allowed) == 0 {
		return true
	}
	for _, t := range toolsList {
		if t.Name == name {
			return true
		}
	}
	return false
}

// filteredTools returns the registered tools minus any excluded by KERN_TOOLS.
// It lazily reads the env once per server lifetime and caches the result.
func (s *Server) filteredTools() []Tool {
	s.toolsMu.Lock()
	defer s.toolsMu.Unlock()
	if s.filtered != nil {
		return s.filtered
	}
	allowed := toolAllowlist()
	if len(allowed) == 0 {
		s.filtered = tools
		return s.filtered
	}
	in := func(n string) bool {
		for _, a := range allowed {
			if a == n {
				return true
			}
		}
		return false
	}
	out := make([]Tool, 0, len(allowed))
	for _, t := range tools {
		if in(t.Name) {
			out = append(out, t)
		}
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
		Description: "Compress an LLM's response (assistant output) by stripping filler, pleasantries and hedge language while preserving code blocks, lists, errors and technical content. Deterministic and local, no LLM involved. Use on verbose model replies before they are stored or echoed back into context.",
		InputSchema: schema(map[string]any{
			"text": strProp("The LLM output text to compress"),
		}, []string{"text"}),
	},
	{
		Name:        "kern_memory_add",
		Description: "Persist a distilled, cross-session lesson for a project (the project 'brain'). Agents record what they learned so future sessions can recall it. Appends to the project memory store (most recent 50 entries kept).",
		InputSchema: schema(map[string]any{
			"lesson": strProp("The lesson to remember, e.g. 'deploy tags are pushed from a manual release workflow, not CI'"),
			"root":   strProp("Project root whose memory store to append (defaults to current directory)"),
		}, []string{"lesson"}),
	},
	{
		Name:        "kern_memory_list",
		Description: "List all stored lessons for a project, most recent first with timestamps.",
		InputSchema: schema(map[string]any{
			"root": strProp("Project root (defaults to current directory)"),
		}, nil),
	},
	{
		Name:        "kern_memory_recall",
		Description: "Recall the up-to-k most relevant past lessons for a prompt by keyword overlap. Returns only lessons whose tokens match; deterministic and local.",
		InputSchema: schema(map[string]any{
			"prompt": strProp("Query to match lessons against"),
			"root":   strProp("Project root (defaults to current directory)"),
			"k":      strProp("Max lessons to return (default 5)"),
		}, []string{"prompt"}),
	},
	{
		Name:        "kern_mask_pii",
		Description: "Locally scan text for secrets and PII (API keys, passwords, tokens, URLs with credentials, IPs, emails) and replace them with safe [MASKED_*] placeholders. Use before sending any text to a remote LLM. Pure local, deterministic, reversible via the returned mapping.",
		InputSchema: schema(map[string]any{
			"text":       strProp("The raw text to mask"),
			"mask_names": strProp("Optional comma-separated client/project names to mask"),
		}, []string{"text"}),
	},
	{
		Name:        "kern_security",
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
		Description: "Check whether a symbol can be safely deleted: reports in-project callers (production vs test-only), whether it is exported or an entry point, and a conservative SAFE/NOT SAFE verdict. Use before removing dead code.",
		InputSchema: schema(map[string]any{
			"symbol": strProp("Symbol name (simple name like 'greet' or qualified like 'User.Login')"),
			"root":   strProp("Project root (defaults to current directory)"),
			"format": strProp("Output format: text or json (default text)"),
		}, []string{"symbol"}),
	},
	{
		Name:        "kern_doc_search",
		Description: "Local vector search over a project's documents (markdown, text, rst, adoc). Chunks and embeds docs locally with deterministic n-gram hashing (no ML deps) and returns only the most relevant fragments. Use instead of pasting whole documents into context.",
		InputSchema: schema(map[string]any{
			"query": strProp("Natural-language or keyword query"),
			"root":  strProp("Project root (defaults to current directory)"),
			"k":     strProp("Max fragments to return (default 5)"),
		}, []string{"query"}),
	},
	{
		Name:        "kern_doc_index",
		Description: "Pre-index a project's documents for kern_doc_search. Run once after documents change; searches auto-index on first use. Pass semantic=true to also embed chunks with a local Ollama embedding model (KERN_EMBED_MODEL, default nomic-embed-text); queries then fuse a real-meaning dense signal with the deterministic n-gram vectors and BM25.",
		InputSchema: schema(map[string]any{
			"root":     strProp("Project root (defaults to current directory)"),
			"semantic": strProp("If true, add dense Ollama embeddings to the index (requires a local Ollama with the embedding model pulled)"),
		}, nil),
	},
	{
		Name:        "kern_doc_fetch",
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
		Description: "Generate a deterministic conventional-commit message (type, scope, subject, per-file body) from the git diff — rule-based, no LLM, no network; the same diff always yields the same message. Use when a commit needs a starting message the human can tweak.",
		InputSchema: schema(map[string]any{
			"root":   strProp("Project root (defaults to current directory)"),
			"staged": strProp("If true, read the staged diff (git diff --cached) instead of the working tree vs HEAD"),
			"range":  strProp("Optional commit range like a..b; overrides staged and HEAD defaults"),
		}, nil),
	},
	{
		Name:        "kern_precache",
		Description: "Speculative pre-caching (#20): scan the project once and fill the code-summary and document-vector caches so later kern calls are instant. Run periodically or after bulk edits.",
		InputSchema: schema(map[string]any{
			"root": strProp("Project root (defaults to current directory)"),
		}, nil),
	},
	{
		Name:        "kern_swap",
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
		Description: "Run a risky command inside a snapshot of the project (#15): on non-zero exit the tree is rolled back exactly (files restored, new files removed). Success keeps changes. Use before destructive operations, migrations, or agent-applied edits.",
		InputSchema: schema(map[string]any{
			"root":    strProp("Project root to snapshot and run in (defaults to current directory)"),
			"command": strProp("Full command to run, e.g. \"make migrate\" or \"sh -c 'npm test'\" (shell words, not a shell string)"),
			"timeout": strProp("Timeout in seconds (default 120)"),
		}, []string{"command"}),
	},
	{
		Name:        "kern_diff_files",
		Description: "Delta streaming (#13): compute a unified line diff between two files (or two versions of the same file) using pure Go. Returns the full patch, or a note when files are identical. Feed the output back to the model as a compact edit description.",
		InputSchema: schema(map[string]any{
			"a":    strProp("Path to the old/base file"),
			"b":    strProp("Path to the new/changed file"),
			"root": strProp("Project root; when set, a and b must stay inside it (defaults to unrestricted)"),
		}, []string{"a", "b"}),
	},
	{
		Name:        "kern_heal",
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
		Description: "Auto-validation (#7): detect the project's language-appropriate build/test/syntax command and run it. Returns exit status, truncated output and duration. Use after editing code to gate correctness before final answers.",
		InputSchema: schema(map[string]any{
			"root":    strProp("Project root (defaults to current directory)"),
			"command": strProp("Optional override command, e.g. \"go test ./...\" (defaults to auto-detected)"),
			"timeout": strProp("Timeout in seconds (default 120)"),
		}, nil),
	},
	{
		Name:        "kern_schema_validate",
		Description: "Deterministically validate JSON output against a JSON schema (subset: object/array/primitives, required, enum, min/max/length, pattern, additionalProperties). Returns either a conform message or one line per violation.",
		InputSchema: schema(map[string]any{
			"data":   strProp("The JSON output to validate"),
			"schema": strProp("The JSON schema to validate against"),
		}, []string{"data", "schema"}),
	},
	{
		Name:        "kern_verify_output",
		Description: "Hallucination check: extract file:line, symbol-name and route references from an agent's output text and confirm each against the real source tree and index. Returns ok/MISS verdicts for every reference.",
		InputSchema: schema(map[string]any{
			"text": strProp("The agent output text to verify"),
			"root": strProp("Project root (defaults to current directory)"),
		}, []string{"text"}),
	},
	{
		Name:        "kern_compact_file",
		Description: "Return a compact symbolic summary of a source file (functions, types, line numbers) instead of reading the whole file. Use before reading files in large codebases.",
		InputSchema: schema(map[string]any{
			"path": strProp("Absolute or relative path of the file to summarize"),
			"root": strProp("Project root; when set, path must stay inside it (defaults to unrestricted)"),
		}, []string{"path"}),
	},
	{
		Name:        "kern_project_map",
		Description: "Return a compressed map of a whole project: every source file with its symbols and line counts. Use instead of listing/reading every file in a repo.",
		InputSchema: schema(map[string]any{
			"root":      strProp("Project root directory"),
			"max_files": strProp("Maximum number of files to include (default 500)"),
		}, []string{"root"}),
	},
	{
		Name:        "kern_pack",
		Description: "Pack a whole project into one paste-ready bundle: project instructions, a directory tree with per-file token counts, and file contents, sized to fit max_tokens. Use when an agent needs the full working picture (source to edit against), not just a map.",
		InputSchema: schema(map[string]any{
			"root":         strProp("Project root directory"),
			"max_tokens":   strProp("Token budget for the bundle (default 8000; 0 = unlimited — use with max_output=0 to avoid the output sandbox)"),
			"format":       strProp("'text' (default) or 'json'"),
			"instructions": strProp("'true' to include root-level docs as instructions (default), 'false' to skip them"),
		}, []string{"root"}),
	},
	{
		Name:        "kern_run_build",
		Description: "Run a build/test command locally and return only the compact result (exit status + errors), not full output. Use for builds, tests, linting to save context.",
		InputSchema: schema(map[string]any{
			"command": strProp("Shell command to run"),
			"dir":     strProp("Working directory for the command"),
		}, []string{"command"}),
	},
	{
		Name:        "kern_optimize_log",
		Description: "Strip noise from log output: keeps errors, warnings, stack traces and build failures, removes timestamps and chatter. Use before pasting logs into context.",
		InputSchema: schema(map[string]any{
			"log": strProp("The log text to compress"),
		}, []string{"log"}),
	},
	{
		Name:        "kern_context_budget",
		Description: "Fit text into a token budget: deduplicate lines, keep the head plus important lines (errors, stack frames), then trim. Use to manage a crowded context window before adding more content.",
		InputSchema: schema(map[string]any{
			"text":       strProp("The text (log output, file dump, conversation) to fit into the budget"),
			"max_tokens": strProp("Maximum tokens the result may use (default 4000)"),
		}, []string{"text"}),
	},
	{
		Name:        "kern_stats",
		Description: "Return before/after token savings and cost estimates from kern optimizations, optionally filtered to today or a session.",
		InputSchema: schema(map[string]any{
			"days":    strProp("Aggregate over the last N days (default 7)"),
			"session": strProp("Filter to a session identifier"),
		}, nil),
	},
	{
		Name:        "kern_semcache",
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
		Description: "AST-level symbol search across a Go project. Supports patterns like 'func greet', 'type *User*', 'method *', '*Handler*'. Returns definitions with file:line.",
		InputSchema: schema(map[string]any{
			"pattern": strProp("Symbol pattern. Prefixes: func, method, struct, interface, type, const, var. '*' wildcards supported"),
			"root":    strProp("Project root (defaults to current directory)"),
			"limit":   strProp("Max results (default 50)"),
		}, []string{"pattern"}),
	},
	{
		Name:        "kern_frameworks",
		Description: "Detect the frameworks and libraries a project uses (Spring, Rails, Django, Express, gin, etc.) by scanning manifests and source markers. Use to know what stack the codebase is on.",
		InputSchema: schema(map[string]any{
			"root": strProp("Project root (defaults to current directory)"),
		}, nil),
	},
	{
		Name:        "kern_entry_points",
		Description: "List framework entry points found in the index: handlers, controllers and route targets with their framework and route (e.g. spring-mvc UserController.list /api/users). Search for all symbols with the 'entry' kind prefix via kern_ast_search.",
		InputSchema: schema(map[string]any{
			"root":    strProp("Project root (defaults to current directory)"),
			"limit":   strProp("Max results (default 50)"),
			"pattern": strProp("Optional route/name wildcard filter, e.g. '*admin*'"),
		}, nil),
	},
	{
		Name:        "kern_search",
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
		Description: "Ranked free-text symbol search across every repo in the kern multi-repo registry (kern repos add). Returns matches tagged with their repo name, best hits first. Set semantic=true to re-rank pooled results by Ollama dense embeddings.",
		InputSchema: schema(map[string]any{
			"query":    strProp("Free-text query (symbol name, path fragment, or partial name)"),
			"limit":    strProp("Max results (default 20)"),
			"semantic": strProp("When 'true', re-rank by Ollama dense embeddings (requires embedding model)"),
		}, []string{"query"}),
	},
	{
		Name:        "kern_why",
		Description: "Rationale and doc-reference report for a symbol: its doc comment, who depends on it and why (each caller's own doc line), and its in/out edge counts. Use to answer 'why does this exist and who needs it'.",
		InputSchema: schema(map[string]any{
			"symbol": strProp("Symbol name or Receiver.Name"),
			"root":   strProp("Project root (defaults to current directory)"),
		}, []string{"symbol"}),
	},
	{
		Name:        "kern_code_graph",
		Description: "Return the call graph neighbourhood of a symbol: its definition, its callers, and what it calls. Use to understand dependencies without reading whole files.",
		InputSchema: schema(map[string]any{
			"symbol": strProp("Symbol name (e.g. 'greet' or 'User.Login')"),
			"root":   strProp("Project root (defaults to current directory)"),
		}, []string{"symbol"}),
	},
	{
		Name:        "kern_inherits",
		Description: "Return the inheritance edges of a symbol: its supertypes (extends/implements/embeds) and subtypes (what extends/implements/embeds it). Use to see class hierarchies without reading whole files.",
		InputSchema: schema(map[string]any{
			"symbol": strProp("Symbol name (e.g. 'Item' or 'Logger')"),
			"root":   strProp("Project root (defaults to current directory)"),
		}, []string{"symbol"}),
	},
	{
		Name:        "kern_context",
		Description: "Return the minimal relevant source slice for a symbol: its definition source, its callers, and what it calls. Use instead of reading an entire file.",
		InputSchema: schema(map[string]any{
			"symbol": strProp("Symbol name (e.g. 'greet')"),
			"root":   strProp("Project root (defaults to current directory)"),
			"lines":  strProp("Lines of source context around the definition (default 12)"),
		}, []string{"symbol"}),
	},
	{
		Name:        "kern_changes",
		Description: "Line-aware change-impact analysis for a diff: scopes each changed file to the symbols its added lines actually touch (from git diff hunks), then computes blast radius (transitive callers), risk scores, and test gaps. Use to review what a PR could break before reading files.",
		InputSchema: schema(map[string]any{
			"root":  strProp("Project root (defaults to current directory)"),
			"range": strProp("Git range like 'HEAD~2..HEAD'. Empty = working-tree changes"),
			"file":  strProp("Optional comma-separated explicit file list, overrides git range"),
		}, nil),
	},
	{
		Name:        "kern_review",
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
		Description: "Architectural hotspots: the most depended-on symbols (hubs) and cross-package bridges where a change in one subsystem can break another.",
		InputSchema: schema(map[string]any{
			"root":  strProp("Project root (defaults to current directory)"),
			"limit": strProp("Max hubs to return (default 10)"),
		}, nil),
	},
	{
		Name:        "kern_test_gaps",
		Description: "Test-coverage analysis from the call graph: what percent of callable symbols are exercised by tests, plus untested hotspots (called by many, covered by none).",
		InputSchema: schema(map[string]any{
			"root":  strProp("Project root (defaults to current directory)"),
			"limit": strProp("Max hotspots to list (default 10)"),
		}, nil),
	},
	{
		Name:        "kern_path",
		Description: "Shortest call path between two symbols, following in-project call edges in either direction. Traces how two things connect without reading files.",
		InputSchema: schema(map[string]any{
			"root": strProp("Project root (defaults to current directory)"),
			"from": strProp("Source symbol (simple name or Type.Method)"),
			"to":   strProp("Target symbol (simple name or Type.Method)"),
		}, []string{"from", "to"}),
	},
	{
		Name:        "kern_dead",
		Description: "Dead-code detection: symbols nothing in the project calls. Private names are dead for certain; public names may be external API. Sorted by size so the biggest cleanup wins show first.",
		InputSchema: schema(map[string]any{
			"root":  strProp("Project root (defaults to current directory)"),
			"limit": strProp("Max entries (default all)"),
		}, nil),
	},
	{
		Name:        "kern_larges",
		Description: "Find the largest function/method declarations by source lines. Use to locate god functions that beg for refactoring.",
		InputSchema: schema(map[string]any{
			"root":      strProp("Project root (defaults to current directory)"),
			"min_lines": strProp("Size threshold in source lines (default 60)"),
			"limit":     strProp("Max results (default all)"),
		}, nil),
	},
	{
		Name:        "kern_arch",
		Description: "Architecture overview from call-graph communities: subsystems with their hubs/packages, plus coupling warnings ranking the cross-community call bundles that make changes ripple.",
		InputSchema: schema(map[string]any{
			"root": strProp("Project root (defaults to current directory)"),
		}, nil),
	},
	{
		Name:        "kern_churn",
		Description: "Change-frequency risk: which files were touched by the most commits in a range, whether they are being edited right now, and how risky they are in the call graph.",
		InputSchema: schema(map[string]any{
			"root":  strProp("Project root (defaults to current directory)"),
			"range": strProp("Git range like 'HEAD~10..HEAD' (default last 30 commits)"),
		}, nil),
	},
	{
		Name:        "kern_near",
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
		Description: "Query-driven micro-context router: given a task (bug report, prompt, error text), extract the symbol names it mentions, resolve them against the index, and return a budget-capped bundle of definitions, callers, callees and tests. The graph is the retrieval index, never the payload.",
		InputSchema: schema(map[string]any{
			"task":       strProp("Natural-language task, bug report or error text mentioning symbols"),
			"root":       strProp("Project root (defaults to current directory)"),
			"max_tokens": strProp("Token budget for the bundle (default 4000)"),
		}, []string{"task"}),
	},
	{
		Name:        "kern_trace",
		Description: "Runtime-impact overlay: parse a pprof -top dump, a crash stack trace, or a plain list of function names and map the hot symbols onto the call graph — file:line, blast radius, test coverage and risk. Use to see what a hot path touches at runtime.",
		InputSchema: schema(map[string]any{
			"trace": strProp("The trace text (pprof -top, stack trace, or symbol list)"),
			"root":  strProp("Project root (defaults to current directory)"),
			"limit": strProp("Max hot symbols to return (default all)"),
		}, []string{"trace"}),
	},
	{
		Name:        "kern_lock",
		Description: "Acquire an advisory workspace lock on a scope (flock-based). Held by this server until kern_unlock. Lets concurrent agents coordinate before touching shared files. Errors when the scope is already held.",
		InputSchema: schema(map[string]any{
			"scope": strProp("Lock scope, e.g. 'db-models' or 'checkout'"),
			"root":  strProp("Project root (defaults to current directory)"),
		}, []string{"scope"}),
	},
	{
		Name:        "kern_unlock",
		Description: "Release a workspace lock previously acquired via kern_lock.",
		InputSchema: schema(map[string]any{
			"scope": strProp("Lock scope to release"),
		}, []string{"scope"}),
	},
	{
		Name:        "kern_lock_status",
		Description: "List workspace locks with whether each is held and by which PID. Use to see what other agents are working on.",
		InputSchema: schema(map[string]any{
			"root": strProp("Project root (defaults to current directory)"),
		}, nil),
	},
	{
		Name:        "kern_guard_check",
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
		Name:        "kern_rename",
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
		Description: "Run code in an isolated local runtime and return ONLY stdout — the 'Think in Code' surface. Language is selected by --lang or a shebang line; runtimes are resolved from PATH (python3, node, go, bash, perl, ruby, php, lua, julia, R, bun, deno, rust, ...). The script runs in a fresh temp dir with a hard timeout (default 10s, override timeout=N), a stdout byte cap (default 16KiB, override max=N), and a sanitized environment (HOME/XDG pointed into the sandbox, secrets stripped). When unprivileged user namespaces are available the script also runs in a private network namespace, so network egress is blocked; otherwise it degrades to env isolation only. stderr is never mixed into stdout and is only surfaced on failure. Use it to compute things (math, data munging, JSON transforms) without polluting context.",
		InputSchema: schema(map[string]any{
			"code":       strProp("The script body (required)"),
			"lang":       strProp("Language override (e.g. python3, node, bash, go); otherwise detected from the shebang"),
			"timeout":    strProp("Timeout in seconds (default 10)"),
			"max":        strProp("Max stdout bytes to return (default 16384)"),
			"stdin":      strProp("Input piped to the script's stdin"),
			"list":       strProp("If true, return the installed runtimes and supported languages and do nothing else"),
			"no_isolate": strProp("If true, inherit the caller's environment and full network access (default false)"),
		}, []string{"code"}),
	},
	{
		Name:        "kern_explore",
		Description: "Single-call explore (#2): return a symbol's verbatim source, direct call flow (callers + callees) and transitive blast radius (with affected files) in one shot. The primitive that replaces three separate calls (graph/near/path) for 'what touches this and how'. Pass depth=N to cap the blast radius to N hops and max=N to cap node count.",
		InputSchema: schema(map[string]any{
			"symbol": strProp("Symbol name (e.g. 'greet' or 'User.Login')"),
			"root":   strProp("Project root (defaults to current directory)"),
			"depth":  strProp("Cap blast radius to N hops from the symbol (default 0 = unlimited)"),
			"max":    strProp("Maximum blast-radius symbols to return (default 0 = unlimited)"),
		}, []string{"symbol"}),
	},
	{
		Name:        "kern_graph",
		Description: "One-call graph context: token-budgeted names-only adjacency for a symbol — callers first (the direction that matters for impact), then callees, every edge tagged EXTRACTED/INFERRED/AMBIGUOUS, plus community membership. Calls to interface methods carry dispatch hints listing the concrete implementations they can reach. Parity with code-review-graph's minimal_context: the minimal caller-first answer sized to the context window, no source text.",
		InputSchema: schema(map[string]any{
			"symbol":     strProp("Symbol name (simple name or Type.Method)"),
			"root":       strProp("Project root (defaults to current directory)"),
			"max_tokens": strProp("Token budget for the names-only adjacency (default 400)"),
		}, []string{"symbol"}),
	},
	{
		Name:        "kern_fts_search",
		Description: "FTS5 full-text search (#3) over the SQLite symbol index. Supports MATCH syntax ('greet', 'func AND greet', `file:\"main.go\"`). Requires a build with -tags sqlite and a persisted index. Falls back to a clear error on the default build.",
		InputSchema: schema(map[string]any{
			"query": strProp("FTS5 MATCH query over symbols"),
			"root":  strProp("Project root (defaults to current directory)"),
			"limit": strProp("Max results (default 20)"),
		}, []string{"query"}),
	},
	{
		Name:        "kern_bridges",
		Description: "Bridge detection (#4): symbols called from two or more distinct packages/directories — the coupling points where a change in one subsystem can break another. Ranks bridges by number of calling packages then caller count.",
		InputSchema: schema(map[string]any{
			"root":  strProp("Project root (defaults to current directory)"),
			"limit": strProp("Max bridges to return (default 15)"),
		}, nil),
	},
	{
		Name:        "kern_cochange",
		Description: "Co-change mode (#6): which files are actually changed together in the same commits (from git history), independent of the call graph. Grades change risk by co-change frequency: files that co-change with the current edits are the ones most likely to break next. Use before a commit to see what else must change in lockstep.",
		InputSchema: schema(map[string]any{
			"root":  strProp("Project root (defaults to current directory)"),
			"range": strProp("Git range like 'HEAD~10..HEAD' (default last 30 commits)"),
			"limit": strProp("Max co-change pairs to return (default 20)"),
		}, nil),
	},
	{
		Name:        "kern_usage_guide",
		Description: "Categorized usage guide for every kern MCP tool with performance tiers (fast/moderate/expensive), recommended workflows, and pitfalls. Consult this first when deciding which tool fits a task.",
		InputSchema: schema(map[string]any{}, nil),
	},
}

// Server handles MCP requests over a stdio stream or HTTP.
type Server struct {
	in        *bufio.Scanner
	out       io.Writer
	mu        sync.Mutex
	toolsMu   sync.Mutex
	filtered  []Tool // cached KERN_TOOLS-filtered tool list (nil = not computed)
	locks     map[string]*lock.Lock
	inflight  map[string]context.CancelFunc
	sessions  map[string]*project.Session
	transport string // "stdio" (default) or "http"
	// roots are the workspace roots every tool root/dir argument is confined
	// to (W2-29): KERN_ROOTS when set, else the server's startup directory.
	roots []string
	// lastIndex is the symbol index loaded during the current tool call, used
	// to stamp provenance (symbols/edges/packages/freshness) onto the response.
	lastIndex *index.Index
	// commits caches the short HEAD commit per project root so git is spawned
	// at most once per root per server lifetime.
	commits map[string]string
}

// NewServer returns a server wired to the given reader/writer.
func NewServer(in io.Reader, out io.Writer) *Server {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 4<<20), 4<<20)
	return &Server{in: sc, out: out, locks: map[string]*lock.Lock{}, inflight: map[string]context.CancelFunc{}, sessions: map[string]*project.Session{}, transport: "stdio", roots: defaultWorkspaceRoots(), commits: map[string]string{}}
}

// defaultWorkspaceRoots returns the roots tools may target: the KERN_ROOTS
// list (colon- or comma-separated) when set, else the directory the server was
// started in. Every tool root/dir is confined to these so a compromised or
// prompt-injected client cannot point write/exec tools at arbitrary
// directories (W2-29).
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
// tools stop promptly and the workspace is left unlocked.
func (s *Server) cancelAll() {
	s.mu.Lock()
	for _, cancel := range s.inflight {
		cancel()
	}
	s.inflight = map[string]context.CancelFunc{}
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
func (s *Server) Serve() error {
	var wg sync.WaitGroup
	defer wg.Wait() // drain in-flight tool calls before returning on EOF
	for s.in.Scan() {
		line := s.in.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			// A malformed line is a protocol error, not a silent drop (#21).
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
			wg.Add(1)
			go func(req rpcRequest) {
				defer wg.Done()
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
	return s.in.Err()
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
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResponse(id, -32602, "invalid params")
	}
	key := idKey(id)
	ctx, cancel := context.WithCancel(context.Background())
	s.registerInflight(key, cancel)
	defer func() {
		cancel()
		s.unregisterInflight(key)
	}()
	s.mu.Lock()
	s.lastIndex = nil
	s.mu.Unlock()

	text, err := func() (out string, runErr error) {
		defer func() {
			if rec := recover(); rec != nil {
				runErr = fmt.Errorf("panic in tool %s: %v", p.Name, rec)
				fmt.Fprintf(os.Stderr, "kern-mcp: panic running %s: %v\n%s\n", p.Name, rec, debug.Stack())
			}
		}()
		return s.runTool(ctx, key, p.Name, p.Arguments)
	}()
	// MCP-layer output sandbox (P1-7): no tool response may exceed the output
	// budget. This is the chokepoint every tool result flows through, so even a
	// huge kern_project_map / kern_walk / kern_doc_search can never flood the
	// agent's context. The budget is per-call overridable with an extra
	// max_output=N argument (bytes; N=0 disables) and configurable globally via
	// KERN_MCP_MAX_OUTPUT.
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
	if err != nil {
		result["content"] = []any{map[string]any{"type": "text", "text": err.Error()}}
		result["isError"] = true
	} else if prov := s.provenance(); prov != "" {
		result["content"] = []any{map[string]any{"type": "text", "text": text + "\n" + prov}}
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
	return text[:budget] + fmt.Sprintf("\n\n… [MCP output sandbox: %d → %d chars (%d → %d tokens). %s Pass max_output=N to this tool for more, or narrow the request.]",
		len(text), budget, tokenize.Count(text), tokenize.Count(text[:budget]), recoveryHint(tool))
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
// escapes); rootless calls keep the legacy behavior of reading any path, since
// the caller — a loopback MCP client — is the trusted principal.
func rootedPath(root, p string) (string, error) {
	if root == "" {
		if filepath.IsAbs(p) {
			return p, nil
		}
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(cwd, p), nil
	}
	return withinRoot(root, p)
}

func (s *Server) runTool(ctx context.Context, id string, name string, args map[string]any) (string, error) {
	if !toolAllowed(s.filteredTools(), name) {
		return "", fmt.Errorf("tool %q is not allowed (KERN_TOOLS allowlist)", name)
	}
	if err := s.checkRootArg(args); err != nil {
		return "", err
	}
	switch name {
	case "kern_optimize_prompt":
		prompt := argString(args, "prompt")
		if prompt == "" {
			return "", fmt.Errorf("prompt is required")
		}
		mask := argString(args, "mask") == "true" || argString(args, "mask") == "1"
		var names []string
		for _, n := range strings.Split(argString(args, "mask_names"), ",") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
		cacheOn := true
		if v := argString(args, "cache"); v != "" {
			cacheOn = v == "true" || v == "1"
		}
		res, err := optimize.Prompt(prompt, argString(args, "attached_log"), optimize.Options{
			Session:   argString(args, "session"),
			Model:     argString(args, "model"),
			Mask:      mask,
			MaskNames: names,
			Cache:     cacheOn,
			FewShot:   argString(args, "few_shot") == "true" || argString(args, "few_shot") == "1",
			Root:      argString(args, "root"),
		})
		if err != nil {
			return "", err
		}
		out := renderOptimize("optimized prompt", res)
		if res.FromCache {
			if res.SemanticHit {
				out += fmt.Sprintf("\n[kern] served from semantic cache (similarity %.2f, matched: %q)\n", res.Similarity, clipForMarker(res.MatchedInput))
			} else {
				out += "\n[kern] served from exact cache\n"
			}
		}
		return out, nil

	case "kern_memory_add":
		lesson := argString(args, "lesson")
		if lesson == "" {
			return "", fmt.Errorf("lesson is required")
		}
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		if err := memory.Add(root, lesson); err != nil {
			return "", err
		}
		return "remembered.", nil

	case "kern_memory_list":
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		var b strings.Builder
		for _, e := range memory.List(root) {
			fmt.Fprintf(&b, "%s  %s\n", e.Time.UTC().Format("2006-01-02 15:04"), e.Text)
		}
		return strings.TrimSuffix(b.String(), "\n"), nil

	case "kern_memory_recall":
		prompt := argString(args, "prompt")
		if prompt == "" {
			return "", fmt.Errorf("prompt is required")
		}
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		k := 5
		if v := argString(args, "k"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return "", fmt.Errorf("k: invalid integer %q", v)
			}
			if n > 0 {
				k = n
			}
		}
		var b strings.Builder
		for _, e := range memory.Recall(root, prompt, k) {
			fmt.Fprintf(&b, "%s  %s\n", e.Time.UTC().Format("2006-01-02 15:04"), e.Text)
		}
		return strings.TrimSuffix(b.String(), "\n"), nil

	case "kern_mask_pii":
		text := argString(args, "text")
		if text == "" {
			return "", fmt.Errorf("text is required")
		}
		var names []string
		for _, n := range strings.Split(argString(args, "mask_names"), ",") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
		res := pii.MaskNames(text, names)
		var parts []string
		for k, v := range res.ByLabel {
			parts = append(parts, fmt.Sprintf("%s %d", k, v))
		}
		summary := "masked " + itoa(res.Replaced) + " secrets"
		if len(parts) > 0 {
			summary += ": " + strings.Join(parts, ", ")
		}
		return res.Text + "\n[kern] " + summary + "\n", nil

	case "kern_security":
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		var allow []string
		if s := argString(args, "severity"); s != "" {
			allow = strings.Split(s, ",")
		}
		max := 100
		if v := argString(args, "max"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return "", fmt.Errorf("max: invalid integer %q", v)
			}
			if n > 0 {
				max = n
			}
		}
		findings, serr := sec.Scan(root)
		if serr != nil {
			return "", fmt.Errorf("security scan failed: %w", serr)
		}
		findings = sec.FilterBySeverity(findings, allow)
		if argString(args, "format") == "json" {
			var b strings.Builder
			if err := json.NewEncoder(&b).Encode(findings); err != nil {
				return "", fmt.Errorf("encode findings: %w", err)
			}
			return b.String(), nil
		}
		if len(findings) == 0 {
			return "no security findings", nil
		}
		out := sec.Render(findings, max)
		counts := sec.Counts(findings)
		out += fmt.Sprintf("[kern] %d findings: %d error, %d warning, %d info\n",
			len(findings), counts["error"], counts["warning"], counts["info"])
		return out, nil

	case "kern_safe_delete":
		sym := argString(args, "symbol")
		if sym == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := s.loadIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		r := intel.DeleteCheck(ix, sym)
		if argString(args, "format") == "json" {
			data, _ := json.Marshal(r)
			return string(data), nil
		}
		return intel.RenderDelete(r), nil

	case "kern_doc_search":
		query := argString(args, "query")
		if query == "" {
			return "", fmt.Errorf("query is required")
		}
		root := argString(args, "root")
		ix := docsearch.Load(root)
		if ix == nil {
			var err error
			ix, err = docsearch.IndexDir(root)
			if err != nil {
				return "", err
			}
			_ = ix.Save()
		}
		k := 5
		if v := argString(args, "k"); v != "" {
			n, err := atoiArg(v, k)
			if err != nil {
				return "", err
			}
			k = n
		}
		// If the persisted index carries dense vectors, re-attach the local
		// embedder so queries fuse the semantic signal too.
		hasDense := false
		for _, d := range ix.Docs {
			if len(d.Semantic) > 0 {
				hasDense = true
				break
			}
		}
		if hasDense {
			client := llm.New("")
			if client.HasEmbeddingModel() {
				docsearch.SemanticEmbedder = client
			}
		}
		results := ix.Search(query, k)
		if len(results) == 0 {
			return "no matching document fragments", nil
		}
		var b strings.Builder
		for i, r := range results {
			fmt.Fprintf(&b, "#%d score=%.3f %s:%d\n", i+1, r.Sim, r.Doc.Chunk.File, r.Doc.Chunk.Start)
			b.WriteString(r.Doc.Chunk.Text)
			if i < len(results)-1 {
				b.WriteString("\n\n")
			}
		}
		return b.String(), nil

	case "kern_doc_index":
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		var ix *docsearch.Index
		var err error
		if argString(args, "semantic") == "true" || argString(args, "semantic") == "1" {
			client := llm.New("")
			if !client.Available() {
				return "", fmt.Errorf("ollama not reachable (semantic index requires a local Ollama); run kern_doc_index without semantic for deterministic indexing")
			}
			if !client.HasEmbeddingModel() {
				return "", fmt.Errorf("embedding model %q not installed (run: ollama pull %s)", llm.EmbedModel(), llm.EmbedModel())
			}
			docsearch.SemanticEmbedder = client
			ix, err = docsearch.IndexDirSemantic(root, client)
		} else {
			ix, err = docsearch.IndexDir(root)
		}
		if err != nil {
			return "", err
		}
		_ = ix.Save()
		return "indexed " + itoa(len(ix.Docs)) + " chunks from " + root, nil
	case "kern_doc_fetch":
		rawURL := argString(args, "url")
		if rawURL == "" {
			return "", fmt.Errorf("url is required")
		}
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		name := argString(args, "name")
		res, err := fetch.Fetch(rawURL, 0)
		if err != nil {
			return "", err
		}
		if name == "" {
			name = docSearchSlug(rawURL)
		} else if name, err = sanitizeDocName(name); err != nil {
			return "", err
		}
		if err := os.MkdirAll(cache.Path("data", "docs-fetch"), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(cache.Path("data", "docs-fetch", name+".md"), []byte(res.Text), 0o600); err != nil {
			return "", err
		}
		added, err := docsearch.MergeFetched(root, name, res.Text)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "fetched %s -> fetch/%s.md (%d chars, %d chunks indexed into %s)\n\n", rawURL, name, len(res.Text), added, root)
		if res.Title != "" {
			fmt.Fprintf(&b, "# %s\n\n", res.Title)
		}
		if argString(args, "semantic") == "true" {
			client := llm.New("")
			if !client.HasEmbeddingModel() {
				fmt.Fprintf(&b, "note: semantic embeddings skipped (%s not installed)\n\n", llm.EmbedModel())
			} else {
				embedded, eerr := docsearch.ReembedFetch(root, name, client)
				if eerr != nil {
					return "", eerr
				}
				if embedded > 0 {
					fmt.Fprintf(&b, "semantic embeddings attached to %d fetched chunks\n\n", embedded)
				}
			}
		}
		b.WriteString(clip(res.Text, 800))
		return b.String(), nil

	case "kern_commitmsg":
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		var out []byte
		var err error
		staged := argString(args, "staged")
		if staged == "true" || staged == "1" {
			out, err = exec.Command("git", "-C", root, "diff", "--cached").Output()
		} else if rng := argString(args, "range"); rng != "" {
			out, err = exec.Command("git", "-C", root, "diff", "--unified=0", rng).Output()
		} else {
			out, err = exec.Command("git", "-C", root, "diff", "HEAD").Output()
			if err != nil {
				out, err = exec.Command("git", "-C", root, "diff").Output()
			}
		}
		if err != nil {
			return "", fmt.Errorf("git diff failed: %w", err)
		}
		return commitmsg.Generate(string(out)).String(), nil

	case "kern_precache":
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		rep := precache.Warm(root)
		if rep.SourceMiss {
			return "no project at " + root, nil
		}
		return fmt.Sprintf("pre-cached %d summaries (%d hits), %d doc chunks (docs saved=%v) in %s",
			rep.Warmed, rep.CacheHits, rep.DocChunks, rep.DocsSaved, rep.Dur.Round(time.Millisecond)), nil

	case "kern_swap":
		text := argString(args, "text")
		if text == "" {
			return "", fmt.Errorf("text is required")
		}
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		mode := argString(args, "mode")
		switch mode {
		case "summary":
			return swap.SummaryMode(text, root), nil
		case "expand":
			return swap.ExpandMode(text, root), nil
		default:
			maxTok := 0
			if s := argString(args, "max_tokens"); s != "" {
				n, err := strconv.Atoi(s)
				if err != nil {
					return "", fmt.Errorf("max_tokens: invalid integer %q", s)
				}
				if n > 0 {
					maxTok = n
				}
			}
			out, fits := swap.Fit(text, root, maxTok)
			if !fits {
				out += "\n[kern] warning: still over budget after summarization\n"
			}
			return out, nil
		}

	case "kern_sandbox":
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		cmdLine := argString(args, "command")
		if cmdLine == "" {
			return "", fmt.Errorf("command is required")
		}
		stop := s.startProgress(ctx, id, "kern_sandbox")
		defer stop()
		parts := splitShellLine(cmdLine)
		if len(parts) == 0 {
			return "", fmt.Errorf("command is empty")
		}
		timeout := 120 * time.Second
		if s := argString(args, "timeout"); s != "" {
			sec, err := strconv.Atoi(s)
			if err != nil {
				return "", fmt.Errorf("timeout: invalid integer %q", s)
			}
			if sec > 0 {
				timeout = time.Duration(sec) * time.Second
			}
		}
		res := sandbox.Run(ctx, root, parts[0], parts[1:], timeout)
		var b strings.Builder
		if res.OK {
			fmt.Fprintf(&b, "status: PASS (%s), changes kept\n", res.Duration.Round(time.Millisecond))
		} else if res.Restored {
			fmt.Fprintf(&b, "status: FAIL (exit %d, %s), tree restored to snapshot (%d files)\n", res.ExitCode, res.Duration.Round(time.Millisecond), res.Snapshots)
		} else {
			fmt.Fprintf(&b, "status: FAIL (exit %d, %s)\n", res.ExitCode, res.Duration.Round(time.Millisecond))
		}
		if res.Err != nil {
			fmt.Fprintf(&b, "error: %v\n", res.Err)
		}
		out := res.Output
		if len(out) > 4000 {
			out = out[:4000] + "\n... (truncated)"
		}
		if out != "" {
			fmt.Fprintf(&b, "output:\n%s\n", out)
		}
		return b.String(), nil

	case "kern_diff_files":
		a := argString(args, "a")
		b := argString(args, "b")
		if a == "" || b == "" {
			return "", fmt.Errorf("a and b are required")
		}
		root := argString(args, "root")
		ap, err := rootedPath(root, a)
		if err != nil {
			return "", err
		}
		bp, err := rootedPath(root, b)
		if err != nil {
			return "", err
		}
		ab, err := os.ReadFile(ap)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", a, err)
		}
		bb, err := os.ReadFile(bp)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", b, err)
		}
		u := diff.Unified(a, b, splitLines(string(ab)), splitLines(string(bb)))
		if u == "" {
			return "files identical", nil
		}
		return u, nil

	case "kern_heal":
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		task := argString(args, "task")
		if task == "" {
			task = "Fix the failing build/test/syntax errors in this project."
		}
		model := argString(args, "model")
		rounds := 3
		if s := argString(args, "max_rounds"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil {
				return "", fmt.Errorf("max_rounds: invalid integer %q", s)
			}
			if n > 0 {
				rounds = n
			}
		}
		timeout := 120 * time.Second
		if s := argString(args, "timeout"); s != "" {
			sec, err := strconv.Atoi(s)
			if err != nil {
				return "", fmt.Errorf("timeout: invalid integer %q", s)
			}
			if sec > 0 {
				timeout = time.Duration(sec) * time.Second
			}
		}
		stop := s.startProgress(ctx, id, "kern_heal")
		defer stop()
		res := heal.Run(ctx, root, task, model, rounds, timeout)
		var b strings.Builder
		if res.Validated {
			fmt.Fprintf(&b, "status: healed OK after %d round(s)\n", res.Iterations)
			for _, c := range res.Changes {
				fmt.Fprintf(&b, "changed: %s\n", c)
			}
			if res.Diff != "" {
				fmt.Fprintf(&b, "diff:\n%s\n", res.Diff)
			}
			return b.String(), nil
		}
		fmt.Fprintf(&b, "status: still failing after %d round(s)\n", res.Iterations)
		if res.Err != nil {
			fmt.Fprintf(&b, "error: %v\n", res.Err)
		}
		if res.LastOutput != "" {
			fmt.Fprintf(&b, "output:\n%s\n", truncateMCP(res.LastOutput, 3000))
		}
		return b.String(), nil
	case "kern_validate":
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		timeout := 120 * time.Second
		if s := argString(args, "timeout"); s != "" {
			sec, err := strconv.Atoi(s)
			if err != nil {
				return "", fmt.Errorf("timeout: invalid integer %q", s)
			}
			if sec > 0 {
				timeout = time.Duration(sec) * time.Second
			}
		}
		var c *validate.Command
		if cmd := argString(args, "command"); cmd != "" {
			parts := strings.Fields(cmd)
			if len(parts) == 0 {
				return "", fmt.Errorf("command is empty")
			}
			c = &validate.Command{Name: parts[0], Cmd: parts[0], Args: parts[1:]}
		} else {
			var err error
			c, err = validate.Detect(root)
			if err != nil {
				return "", err
			}
		}
		res := validate.Run(ctx, root, c, timeout)
		var b strings.Builder
		fmt.Fprintf(&b, "command: %s %s\n", c.Cmd, strings.Join(c.Args, " "))
		fmt.Fprintf(&b, "status: %s\n", map[bool]string{true: "PASS", false: "FAIL"}[res.OK])
		fmt.Fprintf(&b, "exit: %d\n", res.ExitCode)
		fmt.Fprintf(&b, "duration: %s\n", res.Dur.Round(time.Millisecond))
		out := res.Output
		if len(out) > 4000 {
			out = out[:4000] + "\n... (truncated)"
		}
		if out != "" {
			fmt.Fprintf(&b, "output:\n%s\n", out)
		}
		if res.Err != nil {
			fmt.Fprintf(&b, "error: %v\n", res.Err)
		}
		return b.String(), nil

	case "kern_schema_validate":
		data := argString(args, "data")
		sc := argString(args, "schema")
		if data == "" || sc == "" {
			return "", fmt.Errorf("data and schema are required")
		}
		s, err := jsonschema.Parse(sc)
		if err != nil {
			return "", err
		}
		vs := s.Validate([]byte(data))
		if len(vs) == 0 {
			return "schema OK: output conforms", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "schema violations (%d):\n", len(vs))
		for _, v := range vs {
			fmt.Fprintln(&b, "  - "+v)
		}
		return b.String(), nil

	case "kern_verify_output":
		text := argString(args, "text")
		if text == "" {
			return "", fmt.Errorf("text is required")
		}
		root := argString(args, "root")
		ix, err := s.loadIndex(root)
		if err != nil {
			// Without a usable index the reference checks cannot run; surface
			// the error instead of emitting a false-positive MISS report.
			return "", fmt.Errorf("cannot verify: index unavailable for %q: %w", root, err)
		}
		rep := verify.Sorted(verify.Verify(ix, root, text))
		return verify.Render(rep), nil

	case "kern_compact_file":
		path := argString(args, "path")
		if path == "" {
			return "", fmt.Errorf("path is required")
		}
		root := argString(args, "root")
		abs, err := rootedPath(root, path)
		if err != nil {
			return "", err
		}
		content, err := code.ReadFile(abs)
		if err != nil {
			return "", fmt.Errorf("cannot read %s: %w", path, err)
		}
		sum := code.Summarize(abs, content, 200)
		return sum.Render(), nil

	case "kern_project_map":
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		maxFiles := 500
		if v := argString(args, "max_files"); v != "" {
			n, err := atoiArg(v, maxFiles)
			if err != nil {
				return "", err
			}
			maxFiles = n
		}
		p, err := code.BuildProject(root, maxFiles, 200)
		if err != nil {
			return "", err
		}
		return p.Render(), nil

	case "kern_pack":
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		opts := pack.Options{}
		if v := argString(args, "max_tokens"); v != "" {
			n, err := atoiArg(v, opts.MaxTokens)
			if err != nil {
				return "", err
			}
			opts.MaxTokens = n
		} else {
			opts.MaxTokens = 8000
		}
		if v := argString(args, "instructions"); v != "" {
			opts.SkipInstructions = v == "false"
		}
		b, err := pack.Build(root, opts)
		if err != nil {
			return "", err
		}
		if argString(args, "format") == "json" {
			return b.JSON()
		}
		return b.Render(), nil

	case "kern_run_build":
		cmd := argString(args, "command")
		if cmd == "" {
			return "", fmt.Errorf("command is required")
		}
		stop := s.startProgress(ctx, id, "kern_run_build")
		defer stop()
		// Bound the build so a hanging command cannot hold the server: builds
		// and tests can legitimately take minutes, but never forever.
		bctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		res, err := optimize.RunBuild(bctx, cmd, argString(args, "dir"), optimize.Options{})
		if err != nil {
			return "", err
		}
		return res.Output, nil

	case "kern_optimize_log":
		log := argString(args, "log")
		if log == "" {
			return "", fmt.Errorf("log is required")
		}
		cacheOn := true
		if v := argString(args, "cache"); v != "" {
			cacheOn = v == "true" || v == "1"
		}
		res, err := optimize.Log(log, optimize.Options{Cache: cacheOn})
		if err != nil {
			return "", err
		}
		out := renderOptimize("optimized log", res)
		if res.FromCache {
			if res.SemanticHit {
				out += fmt.Sprintf("\n[kern] served from semantic cache (similarity %.2f, matched: %q)\n", res.Similarity, clipForMarker(res.MatchedInput))
			} else {
				out += "\n[kern] served from exact cache\n"
			}
		}
		return out, nil

	case "kern_optimize_output":
		text := argString(args, "text")
		if text == "" {
			return "", fmt.Errorf("text is required")
		}
		out, dropped := terse.Compress(text)
		before := tokenize.Count(text)
		after := tokenize.Count(out)
		return fmt.Sprintf("%d -> %d tokens (saved %d, %.1f%%, %d filler lines dropped)\n\n%s",
			before, after, before-after, pct(before, after), dropped, out), nil

	case "kern_stats":
		return renderStats(argString(args, "days"), argString(args, "session"))

	case "kern_semcache":
		switch argString(args, "action") {
		case "clear":
			ns := argString(args, "namespace")
			if err := semcache.Clear(ns); err != nil {
				return "", err
			}
			if ns == "" {
				return "semcache: cleared all namespaces", nil
			}
			return "semcache: cleared " + ns, nil
		case "list":
			ns := argString(args, "namespace")
			if ns == "" {
				return "", fmt.Errorf("namespace is required for list")
			}
			entries, err := semcache.Entries(ns)
			if err != nil {
				return "", err
			}
			if len(entries) == 0 {
				return fmt.Sprintf("semcache %q: empty", ns), nil
			}
			var b strings.Builder
			fmt.Fprintf(&b, "semcache %q: %d entries\n", ns, len(entries))
			for i, in := range entries {
				fmt.Fprintf(&b, "  %d. %s\n", i+1, truncateMCP(in, 100))
			}
			return strings.TrimSuffix(b.String(), "\n"), nil
		case "similarity":
			a, b := argString(args, "a"), argString(args, "b")
			if a == "" || b == "" {
				return "", fmt.Errorf("a and b are required for similarity")
			}
			return fmt.Sprintf("similarity: %.3f", semcache.Similarity(a, b)), nil
		default: // stats
			st, err := semcache.Stats()
			if err != nil {
				return "", err
			}
			if len(st) == 0 {
				return "semcache: empty", nil
			}
			var b strings.Builder
			b.WriteString("semcache entries by namespace:\n")
			for ns, n := range st {
				fmt.Fprintf(&b, "  %-8s %d\n", ns, n)
			}
			return strings.TrimSuffix(b.String(), "\n"), nil
		}

	case "kern_context_budget":
		text := argString(args, "text")
		if text == "" {
			return "", fmt.Errorf("text is required")
		}
		maxTokens := 4000
		if v := argString(args, "max_tokens"); v != "" {
			n, err := atoiArg(v, maxTokens)
			if err != nil {
				return "", err
			}
			maxTokens = n
		}
		out := budget.Fit(text, maxTokens)
		before := tokenize.Count(text)
		after := tokenize.Count(out)
		return fmt.Sprintf("%d -> %d tokens (saved %d, %.1f%%)\n\n%s", before, after, before-after, pct(before, after), out), nil

	case "kern_ast_search":
		pattern := argString(args, "pattern")
		if pattern == "" {
			return "", fmt.Errorf("pattern is required")
		}
		ix, err := s.loadIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 50
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		matches := ix.Search(pattern, limit)
		if len(matches) == 0 {
			return "no symbols matched: " + pattern, nil
		}
		var b strings.Builder
		for _, m := range matches {
			b.WriteString(m.Kind)
			b.WriteString(" ")
			b.WriteString(m.FullName())
			b.WriteString(" ")
			b.WriteString(m.File)
			b.WriteString(":")
			b.WriteString(itoa(m.Line))
			b.WriteString("\n")
		}
		return strings.TrimSuffix(b.String(), "\n"), nil

	case "kern_frameworks":
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		det, err := fw.Detect(root)
		if err != nil {
			return "", err
		}
		return fw.Render(det), nil

	case "kern_entry_points":
		root := argString(args, "root")
		ix, err := s.loadIndex(root)
		if err != nil {
			return "", err
		}
		limit := 50
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		var b strings.Builder
		n := 0
		for _, s := range ix.Symbols {
			if !s.Entry || s.Framework == "" {
				continue
			}
			if p := argString(args, "pattern"); p != "" {
				re, err := regexp.Compile("^" + strings.ReplaceAll(regexp.QuoteMeta(p), `\*`, `.*`) + "$")
				if err != nil {
					return "", fmt.Errorf("bad pattern %q: %w", p, err)
				}
				if !re.MatchString(s.Name) && (s.Route == "" || !re.MatchString(s.Route)) {
					continue
				}
			}
			fmt.Fprintf(&b, "%s %s %s %s:%d\n", s.Framework, s.FullName(), s.Route, s.File, s.Line)
			n++
			if n >= limit {
				break
			}
		}
		if n == 0 {
			return "no framework entry points in index (run kern build/index to populate)", nil
		}
		return strings.TrimSuffix(b.String(), "\n"), nil

	case "kern_search":
		query := argString(args, "query")
		if query == "" {
			return "", fmt.Errorf("query is required")
		}
		ix, err := s.loadIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 20
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		var matches []index.Symbol
		sem := argString(args, "semantic")
		if sem == "true" || sem == "1" {
			client := llm.New("")
			if !client.HasEmbeddingModel() {
				return "", fmt.Errorf("embedding model %q not installed (run: ollama pull %s)", llm.EmbedModel(), llm.EmbedModel())
			}
			matches = intel.SemanticSearch(ix, query, limit, client)
		} else {
			matches = intel.RankedSearch(ix, query, limit)
		}
		if len(matches) == 0 {
			return "no symbols matched: " + query, nil
		}
		var b strings.Builder
		for _, m := range matches {
			b.WriteString(m.Kind)
			b.WriteString(" ")
			b.WriteString(m.FullName())
			b.WriteString(" ")
			b.WriteString(m.File)
			b.WriteString(":")
			b.WriteString(itoa(m.Line))
			if ix.IsGenerated(m.File) {
				b.WriteString(" (generated)")
			}
			b.WriteString("\n")
		}
		return strings.TrimSuffix(b.String(), "\n"), nil

	case "kern_repo_search":
		query := argString(args, "query")
		if query == "" {
			return "", fmt.Errorf("query is required")
		}
		limit := 20
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		var hits []intel.RepoHit
		sem := argString(args, "semantic")
		if sem == "true" || sem == "1" {
			client := llm.New("")
			if !client.HasEmbeddingModel() {
				return "", fmt.Errorf("embedding model %q not installed (run: ollama pull %s)", llm.EmbedModel(), llm.EmbedModel())
			}
			hits = intel.SemanticSearchRepos(query, limit, client)
		} else {
			hits = intel.SearchRepos(query, limit)
		}
		if len(hits) == 0 {
			return "no symbols matched across registered repos: " + query, nil
		}
		return intel.FormatRepoHits(hits), nil

	case "kern_why":
		symbol := argString(args, "symbol")
		if symbol == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := s.loadIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		info, ok := intel.Why(ix, symbol)
		if !ok {
			return "no symbol found: " + symbol, nil
		}
		return intel.FormatWhy(info), nil

	case "kern_code_graph":
		symbol := argString(args, "symbol")
		if symbol == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := s.loadIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		return ix.Graph(symbol), nil

	case "kern_inherits":
		symbol := argString(args, "symbol")
		if symbol == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := s.loadIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		sym, ok := ix.FindSymbol(symbol)
		if !ok {
			return "no symbol found: " + symbol, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s (%s)\n", sym.FullName(), sym.Kind)
		sup := ix.SupertypesOf(sym)
		sub := ix.SubtypesOf(sym)
		if len(sup) == 0 && len(sub) == 0 {
			b.WriteString("  no inheritance edges\n")
		}
		for _, s := range sup {
			fmt.Fprintf(&b, "  supertype: %s\n", s)
		}
		for _, s := range sub {
			fmt.Fprintf(&b, "  subtype:   %s\n", s)
		}
		return strings.TrimRight(b.String(), "\n"), nil

	case "kern_context":
		symbol := argString(args, "symbol")
		if symbol == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := s.loadIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		lines := 12
		if v := argString(args, "lines"); v != "" {
			n, err := atoiArg(v, lines)
			if err != nil {
				return "", err
			}
			lines = n
		}
		ctx := ix.Context(symbol, lines)
		if ctx == "" {
			return "no symbol found: " + symbol, nil
		}
		return ctx, nil

	case "kern_changes":
		changes, ix, err := s.changedContext(args)
		if err != nil {
			return "", err
		}
		return intel.RenderChanges(intel.AnalyzeChangesRanged(ix, changes)), nil

	case "kern_review":
		changes, ix, err := s.changedContext(args)
		if err != nil {
			return "", err
		}
		maxTokens := 8000
		if v := argString(args, "max_tokens"); v != "" {
			n, err := atoiArg(v, maxTokens)
			if err != nil {
				return "", err
			}
			maxTokens = n
		}
		return intel.ReviewRanged(ix, changes, maxTokens), nil

	case "kern_hubs":
		ix, err := s.loadIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 10
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		var b strings.Builder
		b.WriteString(intel.RenderHubs(intel.Hubs(ix, limit)))
		b.WriteString("\n\n")
		b.WriteString(intel.RenderBridges(intel.Bridges(ix, 15)))
		return b.String(), nil

	case "kern_test_gaps":
		ix, err := s.loadIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 10
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		c := intel.AnalyzeCoverage(ix)
		c.HotGaps = intel.TestGaps(ix, limit)
		return c.Render(), nil

	case "kern_path":
		from := argString(args, "from")
		to := argString(args, "to")
		if from == "" || to == "" {
			return "", fmt.Errorf("from and to are required")
		}
		ix, err := s.loadIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		from, okFrom := intel.Resolve(ix, from)
		to, okTo := intel.Resolve(ix, to)
		if !okFrom {
			return "", fmt.Errorf("unknown symbol: %s", from)
		}
		if !okTo {
			return "", fmt.Errorf("unknown symbol: %s", to)
		}
		return intel.RenderPath(ix, intel.ShortestPath(ix, from, to)), nil

	case "kern_dead":
		ix, err := s.loadIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		dead := intel.DeadCode(ix)
		limit := 0
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		if limit > 0 && len(dead) > limit {
			dead = dead[:limit]
		}
		return intel.RenderDead(dead), nil

	case "kern_larges":
		ix, err := s.loadIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		minLines := 60
		if v := argString(args, "min_lines"); v != "" {
			n, err := atoiArg(v, minLines)
			if err != nil {
				return "", err
			}
			minLines = n
		}
		large := intel.LargeFunctions(ix, minLines)
		limit := 0
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		if limit > 0 && len(large) > limit {
			large = large[:limit]
		}
		return intel.RenderLarge(large), nil

	case "kern_arch":
		ix, err := s.loadIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		return intel.RenderArch(intel.AnalyzeArchitecture(ix)), nil

	case "kern_churn":
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		from, to := "", ""
		if r := argString(args, "range"); r != "" {
			if p := strings.SplitN(r, "..", 2); len(p) == 2 {
				from, to = p[0], p[1]
			} else {
				from = r
			}
		}
		report, err := intel.Churn(root, from, to)
		if err != nil {
			return "", err
		}
		return intel.RenderChurn(report), nil

	case "kern_near", "kern_walk":
		symbol := argString(args, "symbol")
		if symbol == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := s.loadIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		depth := 2
		if v := argString(args, "depth"); v != "" {
			n, err := atoiArg(v, depth)
			if err != nil {
				return "", err
			}
			depth = n
		}
		maxNodes := 100
		if v := argString(args, "max"); v != "" {
			n, err := atoiArg(v, maxNodes)
			if err != nil {
				return "", err
			}
			maxNodes = n
		}
		nodes, err := intel.Near(ix, symbol, depth, maxNodes)
		if err != nil {
			return "", err
		}
		return intel.RenderNear(ix, nodes), nil

	case "kern_graph":
		symbol := argString(args, "symbol")
		if symbol == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := s.loadIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		maxTokens := intel.GraphCtxDefaultTokens
		if v := argString(args, "max_tokens"); v != "" {
			n, err := atoiArg(v, maxTokens)
			if err != nil {
				return "", err
			}
			maxTokens = n
		}
		return intel.GraphCtx(ix, symbol, maxTokens)

	case "kern_explore":
		symbol := argString(args, "symbol")
		if symbol == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := s.loadIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		depth := 0
		if v := argString(args, "depth"); v != "" {
			n, err := atoiArg(v, depth)
			if err != nil {
				return "", err
			}
			depth = n
		}
		maxNodes := 0
		if v := argString(args, "max"); v != "" {
			n, err := atoiArg(v, maxNodes)
			if err != nil {
				return "", err
			}
			maxNodes = n
		}
		rep, err := intel.Explore(ix, symbol, depth, maxNodes)
		if err != nil {
			return "", err
		}
		return intel.RenderExplore(rep), nil

	case "kern_fts_search":
		query := argString(args, "query")
		if query == "" {
			return "", fmt.Errorf("query is required")
		}
		root := resolveRoot(argString(args, "root"))
		if !index.SQLiteEnabled() {
			return "", fmt.Errorf("FTS5 requires a build with -tags sqlite (rebuild kern with 'go build -tags sqlite')")
		}
		limit := 20
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		// Ensure a persisted index exists before searching.
		if _, err := index.LoadSQLite(root); err != nil {
			return "", err
		}
		matches, err := index.FTS5Search(root, query, limit)
		if err != nil {
			return "", err
		}
		if len(matches) == 0 {
			return "no full-text matches: " + query, nil
		}
		var b strings.Builder
		for _, m := range matches {
			b.WriteString(m.Kind)
			b.WriteString(" ")
			b.WriteString(m.FullName())
			b.WriteString(" ")
			b.WriteString(m.File)
			b.WriteString(":")
			b.WriteString(itoa(m.Line))
			b.WriteString("\n")
		}
		return strings.TrimSuffix(b.String(), "\n"), nil

	case "kern_bridges":
		ix, err := s.loadIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 15
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		return intel.RenderBridges(intel.Bridges(ix, limit)), nil

	case "kern_cochange":
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		from, to := "", ""
		if r := argString(args, "range"); r != "" {
			if p := strings.SplitN(r, "..", 2); len(p) == 2 {
				from, to = p[0], p[1]
			} else {
				from = r
			}
		}
		limit := 20
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		report, err := intel.CoChange(root, from, to)
		if err != nil {
			return "", err
		}
		return intel.RenderCoChange(report, limit), nil

	case "kern_probe":
		task := argString(args, "task")
		if task == "" {
			return "", fmt.Errorf("task is required")
		}
		ix, err := s.loadIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		maxTokens := 4000
		if v := argString(args, "max_tokens"); v != "" {
			n, err := atoiArg(v, maxTokens)
			if err != nil {
				return "", err
			}
			maxTokens = n
		}
		report := intel.Probe(ix, task, maxTokens)
		text := intel.RenderProbe(report)
		if report.Truncated {
			text = intel.FitProbe(text, maxTokens)
		}
		return text, nil

	case "kern_trace":
		src := argString(args, "trace")
		if src == "" {
			return "", fmt.Errorf("trace is required")
		}
		ix, err := s.loadIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 0
		if v := argString(args, "limit"); v != "" {
			n, err := atoiArg(v, limit)
			if err != nil {
				return "", err
			}
			limit = n
		}
		return intel.RenderTrace(intel.Trace(ix, src, "trace", limit)), nil

	case "kern_lock":
		scope := argString(args, "scope")
		if scope == "" {
			return "", fmt.Errorf("scope is required")
		}
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		lk, err := lock.Acquire(root, scope)
		if err != nil {
			// Only a genuine contention is "held (pid N)"; every other
			// Acquire failure (bad scope, unwritable root) is reported as
			// what it is (W2-33).
			if errors.Is(err, lock.ErrLocked) {
				_, pid, _ := lock.Held(root, scope)
				return "", fmt.Errorf("lock %q is held (pid %d)", scope, pid)
			}
			return "", err
		}
		s.mu.Lock()
		if s.locks == nil {
			s.locks = map[string]*lock.Lock{}
		}
		if prev := s.locks[scope]; prev != nil {
			_ = prev.Release()
		}
		s.locks[scope] = lk
		s.mu.Unlock()
		return fmt.Sprintf("lock acquired: %s (pid %d)", scope, os.Getpid()), nil

	case "kern_unlock":
		scope := argString(args, "scope")
		if scope == "" {
			return "", fmt.Errorf("scope is required")
		}
		s.mu.Lock()
		lk := s.locks[scope]
		delete(s.locks, scope)
		s.mu.Unlock()
		if lk != nil {
			if err := lk.Release(); err != nil {
				return "", err
			}
			return "lock released: " + scope, nil
		}
		return "", fmt.Errorf("lock %q is not held by this server", scope)

	case "kern_lock_status":
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		sts, err := lock.List(root)
		if err != nil {
			return "", err
		}
		if len(sts) == 0 {
			return "no locks in workspace", nil
		}
		var b strings.Builder
		for _, s := range sts {
			state := "free"
			if s.Held {
				state = "HELD"
			}
			holder := ""
			if s.PID > 0 {
				holder = fmt.Sprintf(" (pid %d)", s.PID)
			}
			fmt.Fprintf(&b, "%s %s%s\n", s.Scope, state, holder)
		}
		return strings.TrimSuffix(b.String(), "\n"), nil

	case "kern_usage_guide":
		return Guide(), nil

	case "kern_guard_check":
		changes, ix, err := s.changedContext(args)
		if err != nil {
			return "", err
		}
		if len(changes) == 0 {
			return "no changed files (use file= or range=, or make edits)", nil
		}
		files := make([]string, 0, len(changes))
		for _, c := range changes {
			files = append(files, c.File)
		}
		root := resolveRoot(argString(args, "root"))
		b, err := intel.LoadBoundaries(root)
		if err != nil {
			return "", err
		}
		violations := intel.CheckBoundaries(ix, b, files)
		threshold := 0
		if v := argString(args, "threshold"); v != "" {
			n, err := atoiArg(v, threshold)
			if err != nil {
				return "", err
			}
			threshold = n
		}
		// threshold=-1 means "never reject" (audit only).
		if threshold >= 0 && len(violations) > threshold {
			return "", fmt.Errorf("REJECT: %d boundary violations exceed threshold %d", len(violations), threshold)
		}
		if argString(args, "format") == "sarif" {
			return intel.RenderViolationsSARIF(violations, serverVersion), nil
		}
		return intel.RenderViolations(violations), nil

	case "kern_rename":
		root := resolveRoot(argString(args, "root"))
		ix, err := s.loadIndex(root)
		if err != nil {
			return "", err
		}
		oldName := argString(args, "symbol")
		newName := argString(args, "new_name")
		rep, err := rename.Rename(ix, oldName, newName)
		if err != nil {
			return "", err
		}
		if argString(args, "apply") == "true" || argString(args, "apply") == "1" {
			if _, err := rename.Apply(root, rep); err != nil {
				return "", fmt.Errorf("apply failed (files restored): %w", err)
			}
		}
		return rename.Render(rep), nil

	case "kern_exec":
		if argString(args, "list") == "true" || argString(args, "list") == "1" {
			return fmt.Sprintf("installed runtimes: %s\nsupported languages: %s",
				strings.Join(script.Available(), ", "), strings.Join(script.Languages(), ", ")), nil
		}
		run := script.Run{
			Lang:      argString(args, "lang"),
			Code:      argString(args, "code"),
			Stdin:     argString(args, "stdin"),
			NoIsolate: argString(args, "no_isolate") == "true" || argString(args, "no_isolate") == "1",
		}
		if v := argString(args, "timeout"); v != "" {
			sec, err := strconv.Atoi(v)
			if err != nil {
				return "", fmt.Errorf("timeout: invalid integer %q", v)
			}
			run.Timeout = time.Duration(sec) * time.Second
		}
		if v := argString(args, "max"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return "", fmt.Errorf("max: invalid integer %q", v)
			}
			if n > 0 {
				run.MaxOut = n
			}
		}
		res := script.RunScript(run)
		if res.Err != nil {
			return "", fmt.Errorf("kern_exec: %s", res.Err)
		}
		return res.Stdout, nil

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// changedContext resolves the changed files for a tool call: an explicit
// comma-separated file list wins; otherwise the git range (empty = working
// tree). Returns line-aware FileChanges so blast radius can be scoped to the
// changed hunks.
func (s *Server) changedContext(args map[string]any) ([]intel.FileChange, *index.Index, error) {
	root := resolveRoot(argString(args, "root"))
	ix, err := s.loadIndex(root)
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
// requires the result to stay inside root, rejecting `..` escapes and absolute
// paths that point outside the project boundary. It returns the resolved
// absolute path.
func withinRoot(root, file string) (string, error) {
	var abs string
	if filepath.IsAbs(file) {
		abs = filepath.Clean(file)
	} else {
		abs = filepath.Join(root, file)
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

// loadIndex returns the session's symbol index, reused while fresh and rebuilt
// when stale or missing (see project.Session.Index). The returned index is
// recorded so the tool response can be stamped with provenance.
func (s *Server) loadIndex(root string) (*index.Index, error) {
	ix, err := s.sessionFor(root).Index()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.lastIndex = ix
	s.mu.Unlock()
	return ix, nil
}

// provenance returns a one-line stamp describing the index the current tool
// call used, or "" when the call did not load an index (e.g. prompt or memory
// tools). The index is guaranteed fresh by project.Session.Index.
func (s *Server) provenance() string {
	s.mu.Lock()
	ix := s.lastIndex
	s.mu.Unlock()
	if ix == nil {
		return ""
	}
	var edges int
	for _, callees := range ix.Calls {
		edges += len(callees)
	}
	age := time.Since(ix.UpdatedAt)
	if age < 0 {
		age = 0
	}
	age = age.Round(time.Second)
	return fmt.Sprintf("[kern] index: %d symbols, %d call edges, %d packages · built %s ago · fresh · commit %s",
		len(ix.Symbols), edges, len(ix.Pkgs), age, s.commit(ix.Root))
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
