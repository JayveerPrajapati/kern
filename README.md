<div align="center">

# kern

Already installed? Run `kern doctor` to verify everything is wired.

### The deterministic, 100% local context optimizer for AI agents

**Fewer tokens · honest numbers · zero network calls · one binary**

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Language: Go](https://img.shields.io/badge/Language-Go_1.23+-blue.svg)](https://go.dev/)
[![Telemetry: None](https://img.shields.io/badge/Telemetry-None-brightgreen.svg)](#telemetry--privacy)
[![Network: none by default](https://img.shields.io/badge/Network-none_by_default-brightgreen.svg)](#telemetry--privacy)
[![Zero deps](https://img.shields.io/badge/Dependencies-zero_(stdlib_only)-brightgreen.svg)](#how-it-works)

[![Linux](https://img.shields.io/badge/Linux-supported-blue.svg)](#supported-platforms)
[![macOS](https://img.shields.io/badge/macOS-supported-blue.svg)](#supported-platforms)
[![Windows](https://img.shields.io/badge/Windows-supported-blue.svg)](#supported-platforms)

[![opencode](https://img.shields.io/badge/opencode-supported-blueviolet.svg)](#supported-agents)
[![Claude Code](https://img.shields.io/badge/Claude_Code-supported-blueviolet.svg)](#supported-agents)
[![Cursor](https://img.shields.io/badge/Cursor-supported-blueviolet.svg)](#supported-agents)
[![Codex](https://img.shields.io/badge/Codex-supported-blueviolet.svg)](#supported-agents)
[![Gemini](https://img.shields.io/badge/Gemini-supported-blueviolet.svg)](#supported-agents)
[![+ 12 more](https://img.shields.io/badge/%2B12_more_surfaces-blueviolet.svg)](#supported-agents)

<br>

**74 `kern_*` MCP tools · 50+ CLI commands · 74 detected frameworks · 18 indexed languages (+ Vue/Svelte/Astro SFC)**

</div>

## Contents

- [Get Started](#get-started)
- [Why kern?](#why-kern)
- [Built for determinism — the Go kernel](#built-for-determinism--the-go-kernel)
- [Key Features](#key-features)
- [Framework-aware Entry Points](#framework-aware-entry-points)
- [Quick Start](#quick-start)
- [How It Works](#how-it-works)
- [CLI Reference](#cli-reference)
- [MCP Tools](#mcp-tools)
- [Telemetry & Privacy](#telemetry--privacy)
- [Configuration](#configuration)
- [Supported Platforms](#supported-platforms)
- [Supported Agents](#supported-agents)
- [Supported Languages](#supported-languages)
- [Troubleshooting](#troubleshooting)
- [License](#license)

## Get Started

### 1. Install the CLI

**No runtime required** — prebuilt static binaries, one command per platform:

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/JayveerPrajapati/kern/main/install.sh | sh

# Windows
#   the install.sh script is macOS/Linux only — download kern-windows-amd64.zip
#   from GitHub Releases and extract kern.exe
```

<details>
<summary><b>Other install methods — go install, source</b></summary>

| Method | Command | Notes |
|---|---|---|
| **go install** | `go install github.com/JayveerPrajapati/kern/cmd/kern@latest && go install github.com/JayveerPrajapati/kern/cmd/kern-mcp@latest` | Both binaries to `$(go env GOPATH)/bin` |
| **from source** | `make build` → `bin/kern`, `bin/kern-mcp` | Requires Go 1.23+ |

<sub>`install.sh` honors `KERN_VERSION` (pin a release) and `KERN_INSTALL_DIR`
(default `~/.local/bin`); it falls back to `go install` when no prebuilt asset
matches your platform. Verify any install with `kern version`.</sub>

</details>

### 2. Wire up your agent(s)

In a **new terminal**, connect kern to every agent on the machine:

```bash
kern setup
```

<sub>Detects and auto-configures opencode (project + global + plugin), Claude
Code, Codex CLI, Cursor, Windsurf, Zed, VS Code, Gemini CLI, Antigravity, Qwen,
Qoder, Kiro, GitHub Copilot (VS Code + CLI), Continue, and any MCP client via a
project `.mcp.json`. It is idempotent — run it any time. Check the wiring with
`kern setup --check`.</sub>

### 3. Initialize each project

```bash
cd your-project
kern index .        # one-shot: build the symbol index
kern watch .        # daemon: keep it fresh automatically
```

<sub>`kern index` builds a real AST index (symbols, call edges, inheritance,
routes) from `go/ast` plus dependency-free heuristics for 18 languages.
Every read path re-validates the index against a content-hash manifest, so
analyses are never served stale — `kern index` is optional in practice and only
speeds up first use.</sub>

### 4. No more syncing!

Auto-sync is on by default. `kern watch` uses native OS file events
(inotifywait/fswatch when present, a polling fallback otherwise), debounces,
and re-indexes exactly what changed. Sessions inside opencode run their own
file-event watcher, so the index is never stale while your agent edits code.

### Uninstall

Nothing to uninstall — a single static binary in your PATH. Remove it
and the `.mcp.json`/agent-config entries `kern setup` wrote, and you're done.

---

## Why kern?

When an AI agent works, almost every token it consumes passes through the same
few expensive shapes: raw logs, whole files, prompts padded with boilerplate,
build output, verbose model replies, repeated file searches. That's context
burned before the real task starts — and it makes every session slower and
every bill bigger.

**kern intercepts exactly those costs, locally and deterministically.** Logs
are compressed before they're pasted (keeping errors and stack frames).
A 10,000-line codebase becomes a one-call symbolic map. A giant build log
becomes "pass/fail + errors". Secrets get masked, filler gets stripped, and
token counts are exact (byte-level BPE) — so the before/after numbers are
always honest.

> **A note on honesty:** kern measures token reduction **vs. the raw input it
> is given** — never a LOC→LLM-input ratio. Compression is rule-based and
> deterministic: identical input always produces identical output, so every
> reported saving is reproducible. Benchmark numbers from other tools are only
> comparable when normalized the same way.

### Benchmark Results

Reproducible on any machine — `go run ./evaluate/bench` (or `make bench`),
fixed inline corpora, no network. Hard gates run inside `go test ./...` so a
compression regression fails CI:

| Operation | Before | After | Reduction | Note |
|---|---|---|---|---|
| **optimize prompt** | 213 | 142 | **33.3%** | deterministic, keeps paths/code/fences |
| **optimize log** | 176 | 69 | **60.8%** | keeps errors + stack frames |
| **optimize output (terse)** | 208 | 193 | 7.2% | strips filler/hedging, keeps code |
| **budget fit (40 tok)** | 176 | 32 | **81.8%** | head + key lines |

**Retrieval recall (docs index): 3/3 (100%)** at recall@5.

Your own savings are recorded locally and visible with `kern stats` — they are
per-install numbers, so they are not published here.

---

## Built for determinism — the Go kernel

kern's engine is a **single static Go binary** — the default build is
`stdlib only`: no databases, no runtimes, no modules to install, nothing to
serve. That's what makes the guarantees real:

- **Deterministic by construction** — compression is regex/rule-based, token
  counts are exact (byte-level BPE) or consistently estimated, and identical
  input always produces identical output. No model, no randomness, no drift.
- **Offline by default** — the runtime makes zero network calls and reports
  zero telemetry. The one explicit exception is `kern docs fetch`, invoked
  deliberately to pull a public docs page into the local index.
- **Scales down to a VPS** — no worker daemons, no RAM-hungry caches; index
  builds take seconds and every analysis reads from the same persisted
  content-hash-verified index.
- **Optional precision upgrades, still no infra** — `-tags treesitter` adds
  tree-sitter extraction for 13 languages (call/inheritance edges, precise
  parsing); `-tags sqlite` swaps the JSON hash cache for a **SQLite WAL +
  FTS5** persistent store with full-text search. Both are build tags — never
  runtime dependencies.
- **Quality is CI-enforced, not anecdotal** — the benchmark harness
  (`go run ./evaluate/bench`) ships deterministic corpora and hard
  compression gates; `TestGatesAreMet` pins them on every `go test ./...`,
  so a regression in any compression surface fails CI instead of silently
  shipping weaker numbers.

---

## Key Features

| | |
|---|---|
| **Instant value** | One command compresses a noisy log; one command maps a whole project; a 30-second quick start |
| **Deterministic output** | Rule-based, byte-identical on identical input; exact BPE token counting |
| **100% private** | No telemetry, no network by default, no paid APIs; optional local Ollama rewriting that silently falls back when absent |
| **One command wires every agent** | `kern setup` configures 17+ agent surfaces (MCP-based) in one shot |
| **Savings you can measure** | Every run is tracked: `kern stats` / `kern diff` report before/after tokens and cost saved |
| **Real code intelligence** | AST index + dependency-free analysis: change impact, blast radius, hotspots, dead code, call paths, architecture guards, communities, coverage gaps |
| **Framework-aware** | 74-framework detection catalog + route extraction linking URL patterns to handlers |
| **Always fresh** | File-event watcher (inotifywait/fswatch + poll fallback) with debounced auto-sync; stale-index guard on every read |
| **Surgical context** | `kern context` / `kern explore` / `kern probe` hand the agent exactly the source it needs — no file-by-file crawling |
| **Safety tooling** | PII masking, secret scanning, hallucination verification (`file:line` claims), snapshot sandbox, self-healing test fixes, JSON-schema validation |
| **Multi-repo search** | `kern repos add` registers repos; `kern search --repos` / `--semantic` searches across all of them |
| **Zero-dependency single binary** | Go stdlib only by default; opt-in tree-sitter (13 grammars) and SQLite WAL + FTS5 via build tags |

<details>
<summary><strong>How auto-syncing works — why the index is never stale</strong></summary>

Three layers keep the index in step with your code:

1. **File watcher with debounced auto-sync.** `kern watch` and in-agent
   sessions use native OS file events (inotifywait on Linux, fswatch on macOS)
   when available — near-real-time with a short debounce — and fall back to
   content-hash polling otherwise.
2. **Stale-index guard on every read.** Every code-intelligence command goes
   through `ReadIndex`, which compares the on-disk file set against the
   index's content-hash manifest and rebuilds automatically when a source file
   is added, removed, or edited. Analyses never serve a stale call graph, even
   with no watcher running.
3. **Per-session invalidation.** Sessions in opencode invalidate the cached
   index on file events, bypassing the cooldown so burst tool calls still see
   fresh code.

```
agent saves src/engine.go
  → watcher fires (<100ms)
  → debounce (150–300ms)
  → rebuild; engine.go is in the index
  → next query sees it
```

</details>

---

## Framework-aware Entry Points

kern detects web-framework routing and links URL patterns to their handlers —
`kern entries` lists them, `kern search` matches routes, and callers of a
handler surface the URL that binds it:

| Framework | Shapes recognized |
|---|---|
| **Spring Boot (Java)** | `@RestController` / `@GetMapping` / `@PostMapping` / `@RequestMapping` |
| **Django (Python)** | `path()`, `re_path()`, `url()`, `include()` in `urls.py` (CBV `.as_view()`, dotted paths) |
| **Flask (Python)** | `@app.route('/path', methods=[...])`, blueprint routes |
| **FastAPI (Python)** | `@app.get(...)`, `@router.post(...)`, all standard methods |
| **Express (Node)** | `app.get(...)`, `router.post(...)` with middleware chains |
| **NestJS (Node)** | `@Controller` + `@Get/@Post/...` |
| **Rails (Ruby)** | `get '/x', to: 'users#index'`, hash-rocket syntax |
| **Laravel (PHP)** | `Route::get()`, `Route::resource()`, `Controller@action` |
| **Go** | `http.HandleFunc(...)`, `r.GET(...)` (gin), verb routers |

Beyond routes, `kern fw` detects **74 frameworks** from imports, config files
and code markers (`kern fw --catalog` lists them all) — so an unknown
codebase's stack is answered in one call.

---

## How It Works

```
┌───────────────────────────────────────────────────────────────────┐
│                        Your AI agent                              │
│                                                                   │
│   "compress this log" · "map this repo" · "who calls Load?"       │
│                                 │                                 │
└─────────────────────────────────┬─────────────────────────────────┘
                                  │
                                  ▼
┌───────────────────────────────────────────────────────────────────┐
│                  kern (CLI) / kern-mcp (MCP server)               │
│                                                                   │
│  74 kern_* tools → optimize · map · graph · review · verify ...   │
│                                 │                                 │
│                                 ▼                                 │
│                  persisted symbol index (JSON hash cache,         │
│                  opt-in SQLite WAL + FTS5)                        │
│          symbols · call edges · inheritance · routes · hashes     │
└───────────────────────────────────────────────────────────────────┘
```

1. **Extraction** — `go/ast` parses Go precisely; a dependency-free extractor
   (comment/string stripping + per-language declaration rules) covers 17 more
   languages; `-tags treesitter` upgrades 13 languages to tree-sitter grammar
   parsing (call/inheritance edges included).

2. **Storage** — everything persists to a content-hash-verified index under
   `~/.cache/kern/` (per project). `-tags sqlite` switches to a SQLite store
   with WAL journaling and FTS5 full-text search for concurrent access.

3. **Analysis** — 50+ commands and 74 MCP tools read the same index:
   call graphs, blast radius, change impact, hotspots, dead code, path
   finding, architecture communities, coverage gaps — all dependency-free,
   all deterministic.

4. **Auto-sync** — file-event watchers (inotifywait/fswatch + polling
   fallback) rebuild the index on change; every read re-validates staleness
   against the manifest so analyses never go stale.

---

## CLI Reference

```bash
kern optimize <prompt> [--attach FILE] [--session ID] [--model NAME] [--llm MODEL]
kern preview  <prompt> [--attach FILE]          (dry-run, no stats recorded)
kern compact <file>                             symbolic summary of a file
kern project [root]                             compact project map
kern pack [root] [--max-tokens N] [--out FILE]  one paste-ready bundle: tree + instructions + contents
kern build "<command>" [--dir DIR]              run build, compact output
kern log <file>                                 compress a log file
kern index [root]                               build/refresh the AST index
kern watch [root]                               daemon: auto re-index on change
kern ast <pattern> [--all]                      AST symbol search (wildcards, kind prefixes)
kern search <query> [--limit N] [--repos] [--json] [--semantic]
                                ranked free-text symbol search
kern repos (list|add <path> [name]|remove <name>)   multi-repo registry
kern graph <symbol> [--mermaid] [--json] [--graphml] [--html] [--out FILE] [--limit N]
                                 definition + callers + what it calls; graph exports;
                                 --html with no symbol renders a whole-repo explorer (community bands + search)
kern inherits <symbol> [root] [--json]           supertypes + subtypes
kern context <symbolRegex> [--lines N]           minimal source slice
kern why <symbol> [--json]                       rationale: doc comment + dependents
kern wiki [root] [--out DIR]                     export a markdown wiki, one page per package
kern stats [--days N] [--session ID] [--json]    token/cost savings
kern semcache [stats|clear [NS]|list <NS>|sim <A> <B>]   semantic cache inspection
kern diff [--session ID]                         recent before/after entries
kern export --csv                                export stats to CSV
kern tokens [--bpe] "<text>"                     token count (estimator or exact BPE)
kern setup [--root DIR] [--agents mcp,opencode,claude]   wire kern into agents
kern setup --check                               show wiring status
kern buddy [root]                                session onboarding digest
kern prompt <template> [--file PATH] [--task TEXT]   fine-tuned prompt templates
kern prompt list                                 list templates
kern remember "<lesson>" / kern memory / kern recall "<prompt>"   project memory
kern budget "<text>" --max N                     fit text to a token budget
kern terse "<text>"|-                            compress an LLM's output
kern exec "<code>" [--lang LANG] [--timeout s] [--max bytes] [--stdin file|-]
                                                isolated local runtime, stdout only
kern doctor [root]                              diagnostics report
kern mask [file|-] [--names a,b,c]              mask secrets/PII locally
kern sec [root] [--severity ...] [--max N] [--json]   security scan (exit 1 on errors)
kern delete <symbol> [root] [--json]            safe-delete check (exit 1 when unsafe)
kern rename <old> <new> [root] [--apply] [--json]    structural rename (AST-scoped)
kern guide                                          categorized tool usage guide (performance tiers)
kern udiff <file-a> <file-b> [--out patch]          unified line diff between two files (pure Go)
kern hook install / hook diff [range] / hook store [range]
                                post-commit diff → project memory
kern lock <scope> [root] / unlock / status          workspace locks for concurrent agents
kern precache [root] [--interval s] [--once]        watch daemon: pre-warm code/doc caches
kern changes / review / hubs / testgaps / entries / flows / communities / path / dead
       / larges / arch / churn / cochange / near / walk / probe / trace / explore
                                change impact and code-intelligence analyses
kern fts "<query>" [root] [--limit N]               full-text search over the SQLite index
                                (requires -tags sqlite)
kern guard init [root]                              scaffold .kern/boundaries.json
kern guard check [root] [--file F] [--range a..b] [--json|--sarif] [--threshold N]
                                reject boundary violations (exit 2 when count > N)
kern fw [root] [--catalog]                       framework detection
kern verify "<text>"                             hallucination check: file:line claims
kern validate [root]                             run the project's build/test, compact
kern heal "<task>" [--model MODEL] [--rounds N]  snapshot-based LLM auto-fix
kern sandbox "<command>"                         run with filesystem snapshot + rollback
kern schema ...                                  JSON-schema validation
kern docs index/search/fetch                     local docs index for doc search
kern version                                     print the installed version
```

### `kern exec` — think in code

```bash
kern exec "print(sum(range(101)))" --lang python3   # 5050 (stdout only)
kern exec './script.py'                             # shebang picks the runtime
kern exec 'core::panic!("x")' --lang rust           # compiles + runs
kern exec --list                                    # runtimes installed here
```

Runtimes resolve from PATH (python3/python, node/bun/deno, bash/sh, perl,
ruby, php, lua, julia, R, go, rust). Runs in a fresh temp dir with a hard
timeout (10s default) and a stdout byte cap — only stdout is returned.

---

## MCP Tools

When running as an MCP server (`kern-mcp`), kern exposes **74 `kern_*`
tools**. They map 1:1 to the CLI commands, so opencode, Claude Code, Codex,
Cursor and 12 more agents get the full engine over MCP:

| Group | Tools |
|---|---|
| **Context optimization** | `kern_optimize_prompt`, `kern_optimize_log`, `kern_optimize_output`, `kern_context_budget`, `kern_pack`, `kern_swap`, `kern_compact_file`, `kern_project_map` |
| **Code graph** | `kern_ast_search`, `kern_fts_search`, `kern_search`, `kern_repo_search`, `kern_code_graph`, `kern_graph`, `kern_inherits`, `kern_context`, `kern_near`, `kern_walk`, `kern_path`, `kern_probe`, `kern_explore`, `kern_why`, `kern_frameworks`, `kern_entry_points`, `kern_communities` |
| **Change & review** | `kern_changes`, `kern_review`, `kern_churn`, `kern_trace`, `kern_hubs`, `kern_bridges`, `kern_arch`, `kern_dead`, `kern_larges`, `kern_test_gaps`, `kern_cochange` |
| **Safety** | `kern_mask_pii`, `kern_security`, `kern_safe_delete`, `kern_verify_output`, `kern_guard_check`, `kern_schema_validate`, `kern_sandbox` |
| **Automation** | `kern_run_build`, `kern_validate`, `kern_heal`, `kern_exec`, `kern_execute`, `kern_rename`, `kern_diff_files`, `kern_commitmsg`, `kern_doc_fetch`, `kern_doc_index`, `kern_doc_search`, `kern_precache`, `kern_memory_add`, `kern_memory_list`, `kern_memory_recall`, `kern_memory`, `kern_lock`, `kern_unlock`, `kern_lock_status`, `kern_semcache`, `kern_stats`, `kern_usage_guide`, `kern_buddy` |
| **Kern 2.0 high-level** | `kern_analyze`, `kern_plan`, `kern_impact`, `kern_what_if`, `kern_verify`, `kern_incident`, `kern_agents`, `kern_loop` |

<sub>Tools are available both over stdio (any MCP client) and the Streamable
HTTP transport with an Origin allow-list (loopback only; empty origins are
accepted for non-browser clients).</sub>

### Kern 2.0 — AI Software Engineering Control Plane

The **Kern 2.0 high-level tools** (`kern_analyze`, `kern_plan`, `kern_impact`,
`kern_what_if`, `kern_verify`, `kern_incident`, `kern_agents`, `kern_loop`)
extend kern from a context optimizer into an **AI Software Engineering
Operating System** — a continuously-updated software digital twin with
engineering memory, a reasoning engine, a policy firewall, a multi-agent
runtime, and a verification system.

The core loop: **UNDERSTAND → REMEMBER → REASON → PLAN → ACT → VERIFY →
PROTECT → OBSERVE → LEARN ↺**. Four non-negotiable principles:

1. **Kern is the platform; MCP is one interface** among many (CLI, REST, SDK,
   IDE, Git, CI/CD, Web UI).
2. **Don't throw away existing kern** — inspect, reuse, refactor incrementally.
3. **Deterministic things stay deterministic** — AST, graph, hashes, policy,
   and tests are never turned into LLM guesses. LLMs are used only for
   planning, reasoning, and summarization.
4. **Every important AI claim is typed** — FACT, INFERENCE, HYPOTHESIS, or
   RECOMMENDATION — with source, provenance, timestamp, scope, and confidence.

Autonomy levels L0–L5 gate which stages run: L0 is read-only; L2 enables
code generation in a sandbox worktree; L4 adds deploy with human approval;
L5 is full closed-loop autonomy. The default is L0 — no code is ever written
or deployed without explicit configuration.

---

## Telemetry & Privacy

**kern has no telemetry.** It collects nothing, sends nothing, and reports
nothing — not usage numbers, not paths, not queries. The binary makes no
network calls by default at all.

The complete list of network touchpoints, all explicit and optional:

| Path | When | What |
|---|---|---|
| `kern_doc_fetch` (MCP tool) | you ask | pulls one public docs page into the local index |
| `kern --llm` / semantic search | you pass a flag | talks to your **local** Ollama instance for rewriting/embeddings |

Everything else — compression, indexing, analysis, search, masking, token
counting — runs entirely on your machine. If you're reading a prompt that
contains secrets, `kern mask` scrubs them **before** any optional LLM call.

---

## Configuration

Next to none — kern is **zero-config by default**, with nothing to write or
keep in sync to get started. Language support is automatic from file
extensions; there's nothing to wire per language. What exists:

- **`.kern/boundaries.json`** (optional) — architecture guardrails. Declare
  forbidden dependency crossings (e.g. a frontend importing a backend DB
  model); `kern guard` (or `kern_guard_check`) rejects a diff before it
  touches the filesystem. `kern guard init` writes a starter file.
- **`.kern/docs/`** (optional) — local doc index. `kern docs index` (or
  `kern_doc_index(semantic=true)`) embeds project docs with a local Ollama
  model for real-meaning `kern_doc_search`.
- **Environment variables** — `OLLAMA_HOST` (default `http://localhost:11434`,
  used only when you opt in), `KERN_EMBED_MODEL` (default
  `nomic-embed-text`), `KERN_VERSION`/`KERN_INSTALL_DIR` (installer).

What it skips out of the box: dependency/build/cache directories
(`node_modules`, `vendor`, `dist`, `target`, `.venv`, …), anything in
`.gitignore` (root and nested), generated files (path conventions or a
"Code generated" banner), and files over a size budget — so the index is
your code, not third-party noise.

---

## Supported Platforms

| Platform | Architectures | Artifact |
|---|---|---|
| **Linux** | amd64 · arm64 | `kern-linux-{amd64,arm64}.tar.gz` |
| **macOS** | amd64 · arm64 | `kern-darwin-{amd64,arm64}.tar.gz` |
| **Windows** | amd64 | `kern-windows-amd64.zip` |

Any other platform: `go install` (or build from source — stdlib only, no
CGO required for the default build).

---

## Supported Agents

`kern setup` wires kern into every agent it finds — 17 MCP surfaces plus
native hooks for agents whose hook APIs allow it:

| Agent | MCP | Auto-interception & session memory |
|---|---|---|
| **Any MCP client** | project `<root>/.mcp.json` (auto-discovered by Claude Code, Cursor, Windsurf, most MCP hosts) | — |
| **opencode** | `opencode.json` + global config | `.opencode/plugins/kern.ts` — plugin API: compresses oversized tool output in place, captures edits/failures/prompts into project memory |
| **Claude Code** | `claude mcp add kern -- <abs path to kern-mcp>` | `.claude/settings.json` hooks — `PostToolUse` compresses large Bash/Read/Grep results (via `updatedToolOutput`) and records edits + failures; `UserPromptSubmit` captures prompts (`kern hook claude-post/…`) |
| **Gemini** | `.gemini/settings.json` (MCP entry) | `.gemini/settings.json` hooks — `AfterTool` compresses oversized shell/read/grep results (exit-2 stderr substitution) and records edits + failures; `BeforeAgent` captures prompts (`kern hook gemini-after/…`) |
| **Cursor** | `.cursor/mcp.json` | `.cursor/rules/kern-hooks.mdc` — instruction rule (Cursor cannot execute shell hooks; the rule steers the model to kern's MCP tools) |
| **Codex** | `[mcp_servers.kern]` in `~/.codex/config.toml` | — (no output-rewrite hook API) |
| **JSON adapters** | `continue`, `windsurf`, `zed`, `vscode`, `antigravity`, `qwen`, `qoder`, `kiro`, `copilot` (VS Code), `copilot-cli` | — (no hook API) |

All agents receive the same 74 MCP tools and the same `AGENTS.md` rules. Output
compression + session memory run natively where the platform's hook API allows
in-place output replacement (opencode, Claude Code, Gemini); agents without
such an API keep full MCP parity but no automatic interception. Generated
wiring files carry machine-specific binary paths, so `kern setup` adds them to
`.gitignore` and the index never scans agent config directories.

---

## Supported Languages

Updated by one content-hash manifest; language is detected by extension or
shebang. **18 languages indexed by default** (dependency-free heuristics):

Go · Python · JavaScript (JSX) · TypeScript (TSX) · Rust · C · C++ · C# ·
Java · Ruby · PHP · Shell · CSS/SCSS/Less · HTML · Markdown · JSON · YAML · Dart

<sub>Vue/Svelte single-file components and Astro pages extract their
`<script>`/frontmatter blocks and index them as JS/TS (`<script lang="ts">`
included) — so the same 17-language pipeline covers frontend SFCs too. Go is
parsed precisely with `go/ast`.</sub>

**13 languages upgraded with tree-sitter** (`-tags treesitter` build):
Go · Python · JavaScript · TypeScript (+ TSX) · Bash/Shell · C · C++ · CSS ·
Java · PHP · Ruby · Rust · Dart — inheritance edges and precise calls included.

---

## Troubleshooting

```bash
kern doctor          # one report: binary, PATH, agent configs, index, Ollama
kern setup --check   # per-agent wiring status
kern version         # installed version
go run ./evaluate/bench   # verify compression gates still pass
```

Common situations:

- **"kern-mcp not found"** in an agent — re-run `kern setup` after
  installing, or add `$(go env GOPATH)/bin` to PATH (go install target).
- **"index missing"** — run `kern index .`; `kern doctor` reports freshness.
- **Semantic features inactive** — kern needs a local Ollama at
  `OLLAMA_HOST` (default `localhost:11434`); everything else still works.
- **Agent produces stale answers after edits** — the index auto-rebuilds via
  staleness checks and watchers; `kern watch .` makes it event-driven.

If `kern doctor` reports warnings it's usually optional wiring — the verdict
lines say exactly what to run next.

---

## License

[MIT](LICENSE) © 2026 Jayveer Prajapati