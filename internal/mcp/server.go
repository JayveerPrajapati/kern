// Package mcp implements a minimal Model Context Protocol server over stdio.
// It is deliberately dependency-free so the binary stays offline and static.
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/diff"
	"github.com/JayveerPrajapati/kern/internal/docsearch"
	"github.com/JayveerPrajapati/kern/internal/fw"
	"github.com/JayveerPrajapati/kern/internal/heal"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/lock"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/precache"
	"github.com/JayveerPrajapati/kern/internal/sandbox"
	jsonschema "github.com/JayveerPrajapati/kern/internal/schema"
	"github.com/JayveerPrajapati/kern/internal/stats"
	"github.com/JayveerPrajapati/kern/internal/swap"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
	"github.com/JayveerPrajapati/kern/internal/validate"
	"github.com/JayveerPrajapati/kern/internal/verify"
)

const (
	protocolVersion = "2025-06-18"
	serverName      = "kern"
	serverVersion   = "0.1.0"
)

// Tool is an MCP tool definition.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
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
		Description: "Compress and clean a raw prompt before sending it to an LLM. Returns the optimized prompt plus token savings. Use this to reduce context cost for large or noisy prompts.",
		InputSchema: schema(map[string]any{
			"prompt":       strProp("The raw prompt text to optimize"),
			"attached_log": strProp("Optional noisy log output to compress and attach"),
			"session":      strProp("Optional session identifier for stats tracking"),
			"model":        strProp("Optional model name for cost estimation"),
			"mask":         strProp("If true, strip secrets/PII before processing and restore placeholders in the output (default false)"),
			"mask_names":   strProp("Comma-separated client/project names to mask as [MASKED_NAME_N]"),
			"cache":        strProp("If true, serve identical requests from the local response cache (default false)"),
			"few_shot":     strProp("If true, inject top recalled lessons from project memory as baseline examples (default false)"),
			"root":         strProp("Project root used for few-shot memory (defaults to current directory)"),
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
		Description: "Pre-index a project's documents for kern_doc_search. Run once after documents change; searches auto-index on first use.",
		InputSchema: schema(map[string]any{
			"root": strProp("Project root (defaults to current directory)"),
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
			"a": strProp("Path to the old/base file"),
			"b": strProp("Path to the new/changed file"),
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
		Description: "Ranked free-text symbol search: returns symbols matching a query by name or file, best matches first. Forgiving lookup for humans — e.g. 'load index' or 'login handler'.",
		InputSchema: schema(map[string]any{
			"query": strProp("Free-text query (symbol name, path fragment, or partial name)"),
			"root":  strProp("Project root (defaults to current directory)"),
			"limit": strProp("Max results (default 20)"),
		}, []string{"query"}),
	},
	{
		Name:        "kern_repo_search",
		Description: "Ranked free-text symbol search across every repo in the kern multi-repo registry (kern repos add). Returns matches tagged with their repo name, best hits first.",
		InputSchema: schema(map[string]any{
			"query": strProp("Free-text query (symbol name, path fragment, or partial name)"),
			"limit": strProp("Max results (default 20)"),
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
		Description: "Deterministic architectural guardrails: validate changed files against .kern/boundaries.json rules and return every forbidden dependency crossing (e.g. a frontend importing a backend DB model) with file evidence. Rejects a proposal before it touches the filesystem.",
		InputSchema: schema(map[string]any{
			"root":  strProp("Project root (defaults to current directory)"),
			"file":  strProp("Optional comma-separated explicit file list, overrides git range"),
			"range": strProp("Git range like 'HEAD~2..HEAD'. Empty = working-tree changes"),
		}, nil),
	},
}

// Server handles MCP requests over a stdio stream or HTTP.
type Server struct {
	in        *bufio.Scanner
	out       io.Writer
	locks     map[string]*lock.Lock
	transport string // "stdio" (default) or "http"
}

// NewServer returns a server wired to the given reader/writer.
func NewServer(in io.Reader, out io.Writer) *Server {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 4<<20), 4<<20)
	return &Server{in: sc, out: out, locks: map[string]*lock.Lock{}, transport: "stdio"}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (s *Server) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.out.Write(append(data, '\n'))
	return err
}

// Serve runs until the stream ends.
func (s *Server) Serve() error {
	for s.in.Scan() {
		line := s.in.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if resp := s.dispatch(req); resp != nil {
			if err := s.write(resp); err != nil {
				return err
			}
		}
	}
	return s.in.Err()
}

// dispatch computes the JSON-RPC response for a request. A nil return means
// the request needs no response (e.g. a notification). The response is a
// transport-neutral object so both stdio and HTTP can send it.
func (s *Server) dispatch(req rpcRequest) any {
	switch req.Method {
	case "initialize":
		caps := map[string]any{
			"tools":   map[string]any{"listChanged": false},
			"prompts": map[string]any{"listChanged": false},
		}
		if s.transport == "http" {
			caps["streamableHttpCapabilities"] = map[string]any{"sse": false}
		}
		return map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    caps,
				"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
			},
		}
	case "notifications/initialized":
		return nil
	case "ping":
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}}
	case "tools/list":
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": tools}}
	case "tools/call":
		return s.toolCallResponse(req.ID, req.Params)
	case "prompts/list":
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"prompts": prompts}}
	case "prompts/get":
		return s.promptGetResponse(req.ID, req.Params)
	default:
		if req.Method == "" {
			return nil
		}
		return errorResponse(req.ID, -32601, "method not found: "+req.Method)
	}
}

func errorResponse(id json.RawMessage, code int, msg string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": code, "message": msg},
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
	text, err := s.runTool(p.Name, p.Arguments)
	if err != nil {
		return errorResponse(id, -32000, err.Error())
	}
	result := map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
		"isError": false,
	}
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
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
		return errorResponse(id, -32002, "prompt not found: "+p.Name)
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

func (s *Server) runTool(name string, args map[string]any) (string, error) {
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
		res, err := optimize.Prompt(prompt, argString(args, "attached_log"), optimize.Options{
			Session:   argString(args, "session"),
			Model:     argString(args, "model"),
			Mask:      mask,
			MaskNames: names,
			Cache:     argString(args, "cache") == "true" || argString(args, "cache") == "1",
			FewShot:   argString(args, "few_shot") == "true" || argString(args, "few_shot") == "1",
			Root:      argString(args, "root"),
		})
		if err != nil {
			return "", err
		}
		out := renderOptimize("optimized prompt", res)
		if res.FromCache {
			out += "\n[kern] served from cache\n"
		}
		return out, nil

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
			fmt.Sscanf(v, "%d", &k)
		}
		results := ix.Search(query, k)
		if len(results) == 0 {
			return "no matching document fragments", nil
		}
		var b strings.Builder
		for i, r := range results {
			fmt.Fprintf(&b, "#%d sim=%.3f %s:%d\n", i+1, r.Sim, r.Doc.Chunk.File, r.Doc.Chunk.Start)
			b.WriteString(r.Doc.Chunk.Text)
			if i < len(results)-1 {
				b.WriteString("\n\n")
			}
		}
		return b.String(), nil

	case "kern_doc_index":
		root := argString(args, "root")
		ix, err := docsearch.IndexDir(root)
		if err != nil {
			return "", err
		}
		_ = ix.Save()
		return "indexed " + itoa(len(ix.Docs)) + " chunks from " + root, nil

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
				if n, err := strconv.Atoi(s); err == nil && n > 0 {
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
		parts := strings.Fields(cmdLine)
		timeout := 120 * time.Second
		if s := argString(args, "timeout"); s != "" {
			if sec, err := strconv.Atoi(s); err == nil && sec > 0 {
				timeout = time.Duration(sec) * time.Second
			}
		}
		res := sandbox.Run(root, parts[0], parts[1:], timeout)
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
		ab, err := os.ReadFile(a)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", a, err)
		}
		bb, err := os.ReadFile(b)
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
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				rounds = n
			}
		}
		timeout := 120 * time.Second
		if s := argString(args, "timeout"); s != "" {
			if sec, err := strconv.Atoi(s); err == nil && sec > 0 {
				timeout = time.Duration(sec) * time.Second
			}
		}
		res := heal.Run(root, task, model, rounds, timeout)
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
			if sec, err := strconv.Atoi(s); err == nil && sec > 0 {
				timeout = time.Duration(sec) * time.Second
			}
		}
		var c *validate.Command
		if cmd := argString(args, "command"); cmd != "" {
			parts := strings.Fields(cmd)
			c = &validate.Command{Name: parts[0], Cmd: parts[0], Args: parts[1:]}
		} else {
			var err error
			c, err = validate.Detect(root)
			if err != nil {
				return "", err
			}
		}
		res := validate.Run(root, c, timeout)
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
		ix, err := loadOrBuildIndex(root)
		if err != nil {
			ix = nil
		}
		rep := verify.Sorted(verify.Verify(ix, root, text))
		return verify.Render(rep), nil

	case "kern_compact_file":
		path := argString(args, "path")
		if path == "" {
			return "", fmt.Errorf("path is required")
		}
		abs := path
		if !strings.HasPrefix(abs, "/") {
			cwd, _ := os.Getwd()
			abs = cwd + "/" + abs
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
		p, err := code.BuildProject(root, 500, 200)
		if err != nil {
			return "", err
		}
		return p.Render(), nil

	case "kern_run_build":
		cmd := argString(args, "command")
		if cmd == "" {
			return "", fmt.Errorf("command is required")
		}
		res, err := optimize.RunBuild(cmd, argString(args, "dir"), optimize.Options{})
		if err != nil {
			return "", err
		}
		return res.Output, nil

	case "kern_optimize_log":
		log := argString(args, "log")
		if log == "" {
			return "", fmt.Errorf("log is required")
		}
		res, err := optimize.Log(log, optimize.Options{})
		if err != nil {
			return "", err
		}
		return renderOptimize("optimized log", res), nil

	case "kern_stats":
		return renderStats(argString(args, "days"), argString(args, "session"))

	case "kern_context_budget":
		text := argString(args, "text")
		if text == "" {
			return "", fmt.Errorf("text is required")
		}
		maxTokens := 4000
		if v := argString(args, "max_tokens"); v != "" {
			fmt.Sscanf(v, "%d", &maxTokens)
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
		ix, err := loadOrBuildIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 50
		if v := argString(args, "limit"); v != "" {
			fmt.Sscanf(v, "%d", &limit)
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
		ix, err := loadOrBuildIndex(root)
		if err != nil {
			return "", err
		}
		limit := 50
		if v := argString(args, "limit"); v != "" {
			fmt.Sscanf(v, "%d", &limit)
		}
		var b strings.Builder
		n := 0
		for _, s := range ix.Symbols {
			if !s.Entry || s.Framework == "" {
				continue
			}
			if p := argString(args, "pattern"); p != "" {
				re, _ := regexp.Compile("^" + strings.ReplaceAll(regexp.QuoteMeta(p), `\*`, `.*`) + "$")
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
		ix, err := loadOrBuildIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 20
		if v := argString(args, "limit"); v != "" {
			fmt.Sscanf(v, "%d", &limit)
		}
		matches := intel.RankedSearch(ix, query, limit)
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
			fmt.Sscanf(v, "%d", &limit)
		}
		hits := intel.SearchRepos(query, limit)
		if len(hits) == 0 {
			return "no symbols matched across registered repos: " + query, nil
		}
		return intel.FormatRepoHits(hits), nil

	case "kern_why":
		symbol := argString(args, "symbol")
		if symbol == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := loadOrBuildIndex(argString(args, "root"))
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
		ix, err := loadOrBuildIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		return ix.Graph(symbol), nil

	case "kern_context":
		symbol := argString(args, "symbol")
		if symbol == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := loadOrBuildIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		lines := 12
		if v := argString(args, "lines"); v != "" {
			fmt.Sscanf(v, "%d", &lines)
		}
		ctx := ix.Context(symbol, lines)
		if ctx == "" {
			return "no symbol found: " + symbol, nil
		}
		return ctx, nil

	case "kern_changes":
		changes, ix, err := changedContext(args)
		if err != nil {
			return "", err
		}
		return intel.RenderChanges(intel.AnalyzeChangesRanged(ix, changes)), nil

	case "kern_review":
		changes, ix, err := changedContext(args)
		if err != nil {
			return "", err
		}
		maxTokens := 8000
		if v := argString(args, "max_tokens"); v != "" {
			fmt.Sscanf(v, "%d", &maxTokens)
		}
		return intel.ReviewRanged(ix, changes, maxTokens), nil

	case "kern_hubs":
		ix, err := loadOrBuildIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 10
		if v := argString(args, "limit"); v != "" {
			fmt.Sscanf(v, "%d", &limit)
		}
		var b strings.Builder
		b.WriteString(intel.RenderHubs(intel.Hubs(ix, limit)))
		b.WriteString("\n\n")
		b.WriteString(intel.RenderBridges(intel.Bridges(ix, 15)))
		return b.String(), nil

	case "kern_test_gaps":
		ix, err := loadOrBuildIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 10
		if v := argString(args, "limit"); v != "" {
			fmt.Sscanf(v, "%d", &limit)
		}
		return intel.AnalyzeCoverage(ix).Render(), nil

	case "kern_path":
		from := argString(args, "from")
		to := argString(args, "to")
		if from == "" || to == "" {
			return "", fmt.Errorf("from and to are required")
		}
		ix, err := loadOrBuildIndex(argString(args, "root"))
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
		ix, err := loadOrBuildIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		dead := intel.DeadCode(ix)
		limit := 0
		if v := argString(args, "limit"); v != "" {
			fmt.Sscanf(v, "%d", &limit)
		}
		if limit > 0 && len(dead) > limit {
			dead = dead[:limit]
		}
		return intel.RenderDead(dead), nil

	case "kern_larges":
		ix, err := loadOrBuildIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		minLines := 60
		if v := argString(args, "min_lines"); v != "" {
			fmt.Sscanf(v, "%d", &minLines)
		}
		large := intel.LargeFunctions(ix, minLines)
		limit := 0
		if v := argString(args, "limit"); v != "" {
			fmt.Sscanf(v, "%d", &limit)
		}
		if limit > 0 && len(large) > limit {
			large = large[:limit]
		}
		return intel.RenderLarge(large), nil

	case "kern_arch":
		ix, err := loadOrBuildIndex(argString(args, "root"))
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

	case "kern_near":
		symbol := argString(args, "symbol")
		if symbol == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := loadOrBuildIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		depth := 2
		if v := argString(args, "depth"); v != "" {
			fmt.Sscanf(v, "%d", &depth)
		}
		maxNodes := 100
		if v := argString(args, "max"); v != "" {
			fmt.Sscanf(v, "%d", &maxNodes)
		}
		nodes, err := intel.Near(ix, symbol, depth, maxNodes)
		if err != nil {
			return "", err
		}
		return intel.RenderNear(ix, nodes), nil

	case "kern_probe":
		task := argString(args, "task")
		if task == "" {
			return "", fmt.Errorf("task is required")
		}
		ix, err := loadOrBuildIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		maxTokens := 4000
		if v := argString(args, "max_tokens"); v != "" {
			fmt.Sscanf(v, "%d", &maxTokens)
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
		ix, err := loadOrBuildIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 0
		if v := argString(args, "limit"); v != "" {
			fmt.Sscanf(v, "%d", &limit)
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
			_, pid, _ := lock.Held(root, scope)
			return "", fmt.Errorf("lock %q is held (pid %d)", scope, pid)
		}
		if s.locks == nil {
			s.locks = map[string]*lock.Lock{}
		}
		if prev := s.locks[scope]; prev != nil {
			_ = prev.Release()
		}
		s.locks[scope] = lk
		return fmt.Sprintf("lock acquired: %s (pid %d)", scope, os.Getpid()), nil

	case "kern_unlock":
		scope := argString(args, "scope")
		if scope == "" {
			return "", fmt.Errorf("scope is required")
		}
		if lk := s.locks[scope]; lk != nil {
			if err := lk.Release(); err != nil {
				return "", err
			}
			delete(s.locks, scope)
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

	case "kern_guard_check":
		changes, ix, err := changedContext(args)
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
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		b, err := intel.LoadBoundaries(root)
		if err != nil {
			return "", err
		}
		return intel.RenderViolations(intel.CheckBoundaries(ix, b, files)), nil

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// changedContext resolves the changed files for a tool call: an explicit
// comma-separated file list wins; otherwise the git range (empty = working
// tree). Returns line-aware FileChanges so blast radius can be scoped to the
// changed hunks.
func changedContext(args map[string]any) ([]intel.FileChange, *index.Index, error) {
	root := argString(args, "root")
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}
	ix, err := loadOrBuildIndex(root)
	if err != nil {
		return nil, nil, err
	}
	if files := argString(args, "file"); files != "" {
		var out []intel.FileChange
		for _, p := range strings.Split(files, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, intel.FileChange{File: p})
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

// loadOrBuildIndex loads the persisted index for root, or builds + saves it.
func loadOrBuildIndex(root string) (*index.Index, error) {
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}
	if ix, err := index.Load(root); err == nil && ix != nil {
		return ix, nil
	}
	ix, err := index.Build(root)
	if err != nil {
		return nil, err
	}
	_ = ix.Save()
	return ix, nil
}

func renderOptimize(label string, res optimize.Result) string {
	return fmt.Sprintf("%s (tokens: %d -> %d, saved %d (%.1f%%)):\n%s",
		label, res.BeforeTokens, res.AfterTokens, res.SavedTokens, res.SavedPercent, res.Output)
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
