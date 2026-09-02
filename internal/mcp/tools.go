// Tool registration table — the single source of truth for the kern MCP
// catalog (86 tools at HEAD). Kept in its own file so the registration table
// does not bury the server core (dispatch, transports, filtering) in
// server.go; the G-11 "expensive tier" plan keeps per-domain extraction as a
// follow-up. The catalog parity invariants (plugin <-> MCP, docs <-> MCP)
// read this table via ToolNames().

package mcp

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
		Name:        "kern_check_draft",
		Phase:       "verify",
		Description: "Validate an agent's draft code against the project index (lighter than LSP, deterministic): Go parse errors, relative imports that do not resolve under root, calls to symbols that are neither declared in the draft nor indexed, and method calls on package aliases not found in the indexed package. Non-Go languages are skipped conservatively.",
		InputSchema: schema(map[string]any{
			"code": strProp("The draft code to validate"),
			"root": strProp("Project root (defaults to current directory)"),
			"lang": strProp("Code language: \"go\" or empty for Go; any other language is skipped conservatively"),
		}, []string{"code"}),
	},
	{
		Name:        "kern_taint",
		Phase:       "verify",
		Description: "Taint-lite analysis: flag security sinks (SQL injection, command injection, unsafe deserialization, Python eval/exec/subprocess/pickle/yaml sinks) whose containing function is transitively called by a framework entry point (Symbol.Entry) or whose file contains source expressions (request params, bodies, CLI args). With generate=true, emits a deterministic test scaffold per tainted sink (go test for Go sinks, pytest for Python sinks, G-4) for LLM-assisted fill. The optional range argument scopes findings to files changed in a 'from..to' git range ('..' = working tree). Deterministic, bounded BFS.",
		InputSchema: schema(map[string]any{
			"root":     strProp("Project root to scan (defaults to current directory)"),
			"file":     strProp("Optional filter: only findings in this file path"),
			"range":    strProp("Optional git range 'from..to' to scope findings to changed files; '..' means the working tree"),
			"generate": map[string]any{"type": "boolean", "description": "When true, emit a test scaffold per tainted sink (default false)"},
		}, nil),
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
