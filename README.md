<div align="center">

# kern

Already installed? Run `kern doctor` to verify everything is wired.

### The local, deterministic code-intelligence engine for AI agents

**Index · graph · guard · audit · context optimization — one binary, zero network, no telemetry**

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

**Phase-aware MCP routing (11 high-level tools by default, 86 in full mode) · 90+ CLI commands · 17 indexed languages (Go + Java resolved; 15 at heuristic precision — skipped under `--precision strict`; build with `-tags treesitter` for AST) · 100% local**

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
- [GitHub Action](#github-action)
- [Supported Languages](#supported-languages)
- [Troubleshooting](#troubleshooting)
- [Roadmap](#roadmap)
- [License](#license)

## Get Started

### FAQ & Common Questions

#### How do I optimize a prompt
Run `kern optimize <prompt>` or call `kern_optimize_prompt`. It strips conversational filler and normalizes whitespace deterministically without external LLM dependencies.

#### How do I compress a log
Run `kern log <file>` or call `kern_optimize_log`. It folds repetitive external stack frames and compresses log output deterministically.

### 1. Install the CLI

**No runtime required** — prebuilt static binaries, one command per platform:

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/JayveerPrajapati/kern/main/install.sh | sh

# Windows (PowerShell)
powershell -ExecutionPolicy Bypass -c "irm https://raw.githubusercontent.com/JayveerPrajapati/kern/main/install.ps1 | iex"
```

<details>
<summary><b>Other install methods — go install, source</b></summary>

| Method | Command | Notes |
|---|---|---|
| **go install** | `go install github.com/JayveerPrajapati/kern/cmd/kern@latest && go install github.com/JayveerPrajapati/kern/cmd/kern-mcp@latest && go install github.com/JayveerPrajapati/kern/cmd/kern-server@latest` | All three binaries to `$(go env GOPATH)/bin` |
| **from source** | `make build` → `bin/kern`, `bin/kern-mcp`, `bin/kern-server` | Requires Go 1.23+ |

<sub>`install.sh` (macOS/Linux) and `install.ps1` (Windows) honor `KERN_VERSION`
(pin a release) and `KERN_INSTALL_DIR` (default `~/.local/bin`); they fall back
to `go install` when no prebuilt asset matches your platform. Verify any
install with `kern version`.</sub>

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

#### MCP clients (Claude Code, Cursor, VS Code)
Prefer `kern setup` — it wires every agent automatically. To connect a
specific MCP client manually, point it at the `kern` binary's `mcp`
subcommand (stdio transport):

**Claude Code:**
```sh
claude mcp add kern -- kern mcp
```

**Cursor / VS Code** — add to your project's `.mcp.json`:
```json
{
  "mcpServers": {
    "kern": { "command": "kern", "args": ["mcp"] }
  }
}
```

Optional env vars:
- `KERN_ROOTS` — workspace root paths to index (defaults to cwd).
- `KERN_BINARY` — path to the kern binary (defaults to PATH).

### 3. Initialize each project

```bash
cd your-project
kern index .        # one-shot: build the symbol index
kern watch .        # daemon: keep it fresh automatically
```

<sub>`kern index` builds a real AST index (symbols, call edges, inheritance,
routes) from `go/ast` plus dependency-free heuristics for 17 languages.
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

kern gives AI coding agents a **local, deterministic model of your
codebase** — symbols, call graphs, architecture boundaries, and a
tamper-evident audit trail — so agents work from evidence instead of
guessing. It is a code-intelligence and governance engine that runs entirely
on your machine: one binary, zero network calls, no telemetry.

What's delivered is concrete: a symbol index across 17 languages (Go and Java
at "resolved" precision), call-graph and blast-radius analysis, architecture
guards (`kern guard`), an approvals/governance firewall, and a tamper-evident
audit chain (`kern audit`). On top of that foundation sits context
optimization:

When an AI agent works, almost every token it consumes passes through the same
few expensive shapes: raw logs, whole files, prompts padded with boilerplate,
build output, verbose model replies, repeated file searches. That's context
burned before the real task starts — and it makes every session slower and
every bill bigger.

**kern intercepts exactly those costs, locally and deterministically.** Logs
are compressed before they're pasted (keeping errors and stack frames).
A 10,000-line codebase becomes a one-call symbolic map. A giant build log
becomes "pass/fail + errors". Secrets get masked, filler gets stripped, and
token counts use a deterministic heuristic estimator (chars/word density per
content kind) — so the before/after numbers are always honest. Savings stats
use that estimator; an opt-in byte-level BPE counter is available via
`kern tokens --bpe`.

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
counts use a deterministic heuristic estimator (chars/word density per content
kind; opt-in byte-level BPE counter via `kern tokens --bpe`), and identical
input always produces identical output. No model, no randomness, no drift.
- **Offline by default** — the runtime makes zero network calls and reports
zero telemetry. The only exceptions are deliberate opt-ins: `kern docs fetch`
and LLM rewriting via `--llm` / semantic features (local Ollama by default;
remote providers only if you set `KERN_LLM_PROVIDER`).
- **Scales down to a VPS** — no worker daemons, no RAM-hungry caches; index
  builds take seconds and every analysis reads from the same persisted
  content-hash-verified index.
- **Optional precision upgrades, still no infra** — `-tags treesitter` adds
  tree-sitter extraction for 14 languages (call/inheritance edges, precise
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
| **Deterministic output** | Rule-based, byte-identical on identical input; heuristic token counting (opt-in byte-level BPE counter) |
| **100% private** | No telemetry, no network by default, no paid APIs; optional LLM rewriting (local Ollama by default) that silently falls back when absent |
| **One command wires every agent** | `kern setup` configures 12 built-in agent surfaces (MCP-based) plus custom adapters via `.kern/agents.json` in one shot |
| **Savings you can measure** | Every run is tracked: `kern stats` / `kern diff` report before/after tokens and cost saved |
| **Real code intelligence** | AST index + dependency-free analysis: change impact, blast radius, hotspots, dead code, call paths, architecture guards, communities, coverage gaps |
| **Framework-aware** | 74-framework detection catalog + route extraction linking URL patterns to handlers |
| **Always fresh** | File-event watcher (inotifywait/fswatch + poll fallback) with debounced auto-sync; stale-index guard on every read |
| **Surgical context** | `kern context` / `kern explore` / `kern probe` hand the agent exactly the source it needs — no file-by-file crawling |
| **Safety tooling** | PII masking, secret scanning, hallucination verification (`file:line` claims), snapshot sandbox, self-healing test fixes, JSON-schema validation |
| **Multi-repo search** | `kern repos add` registers repos; `kern search --repos` / `--semantic` searches across all of them |
| **Zero-dependency single binary** | Go stdlib only by default; opt-in tree-sitter (14 grammars) and SQLite WAL + FTS5 via build tags |

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
│  11 high-level tools → optimize · map · graph · review · verify   │
│                                 │                                 │
│                                 ▼                                 │
│                  persisted symbol index (JSON hash cache,         │
│                  opt-in SQLite WAL + FTS5)                        │
│          symbols · call edges · inheritance · routes · hashes     │
└───────────────────────────────────────────────────────────────────┘
```

1. **Extraction** — `go/ast` parses Go precisely; a dependency-free extractor
   (comment/string stripping + per-language declaration rules) covers 16 more
   languages; `-tags treesitter` upgrades 14 languages to tree-sitter grammar
   parsing (call/inheritance edges included).

2. **Storage** — everything persists to a content-hash-verified index under
   `~/.cache/kern/` (per project). `-tags sqlite` switches to a SQLite store
   with WAL journaling and FTS5 full-text search for concurrent access.

3. **Analysis** — 90+ CLI commands and the MCP tool catalog (11 high-level
   tools by default, 86 in full mode) read the same index:
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
kern tokens [--bpe] "<text>"                     token count (estimator or byte-level BPE counter)
kern setup [--root DIR] [--agents mcp,opencode,claude]   wire kern into agents
kern setup --check                               show wiring status
kern buddy [root]                                session onboarding digest
kern onboard [root]                              register + index + wire a repo for kern (session-start)
kern artifacts [task-id]                         inspect task artifacts (ContextPacket → ImpactReport → VerificationReport chain)
kern correlate <alert-json> [--root ROOT]        correlate a production alert to evidence (alert → service → commit → symbol)
kern learn [threshold]                           extract recurring patterns from engineering memory
kern modernize [root]                            phased monolith modernization plan
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
kern taint [root] [--file F] [--range a..b] [--generate]   taint-lite scan; --range scopes to files changed in a git range; --generate emits test scaffolds (Go + pytest)
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
kern cache (dir|entries|size|maintain) [root] [--dry-run]   cache GC: gzip-archive dormant entries, TTL-evict stale ones
kern lsp [root]                                     LSP over stdio: hover/definition/references from the index
kern guard init [root]                              scaffold .kern/boundaries.json
kern guard check [root] [--file F] [--range a..b] [--json|--sarif] [--threshold N]
                                reject boundary violations (exit 2 when count > N)
kern check [--staged|--repo R] [--format F]          run change-firewall gates (secrets, boundaries, duplication)
kern fix [--file F] [--content C] [--repo R]         validate fix in isolated git worktree; auto-repair loop
kern ci --base <sha> --head <sha> [--repo R]         pre-merge CI gate; emits tamper-evident receipt
kern verify-receipt <id> [--repo R]                  verify cryptographic provenance of CI receipt
kern fw [root] [--catalog]                       framework detection
kern verify <file|-> [root]                     hallucination check: file:line claims; `kern verify <types>` (build,test,security,arch,deps) runs the verification engine
kern validate [root]                             run the project's build/test, compact
kern heal "<task>" [--model MODEL] [--rounds N]  snapshot-based LLM auto-fix
kern analyze <change> [--root ROOT]          analyze a proposed change against the whole system (ADR)
kern team [--root ROOT]                      build the standard specialist team; list roles + task states
kern risk <change> [--root ROOT]             deterministic risk report for a proposed change
kern bridges [root] [--limit N] [--json]     cross-package bridge detection (coupling points)
kern sandbox "<command>"                         run with filesystem snapshot + rollback; every run reports its network policy (posture + network-error hits from output)
kern schema ...                                  JSON-schema validation
kern docs index/search/fetch                     local docs index for doc search
kern version                                     print the installed version
kern serve [--root PATH] [--addr ADDR] [--enterprise] [--project NAME=PATH]...
                                                start the REST API + dashboard server
                                                (single-project, or --enterprise multi-project
                                                with shared org audit/memory/policies)
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

Host command execution is gated by a governance firewall (fail-closed): set
`KERN_ALLOW_EXEC=1` or allowlist tools via `KERN_TOOLS` to opt in. `kern build`
and `kern validate` share the same gate.

---

## MCP Tools

kern's MCP server is **auditable, sandboxed, and governance-aware** — not just
another code-intelligence MCP. Three properties set it apart:

- **Auditable** — every tool call lands in a tamper-evident SHA-256 hash chain
  (`kern audit`). `VerifyChain()` detects tampering. External systems append
  via `kern audit append`. Most MCP servers have no audit trail.
- **Sandboxed** — `KERN_MCP_ROOTS` confines path arguments to workspace roots;
  the `WithPreToolHook` gate denies calls before side effects; `kern_sandbox`
  runs commands in a filesystem snapshot with rollback. Most MCP servers run
  with full user privileges and no rollback.
- **Governance-aware** — an approval workflow gates risky changes; `kern guard`
  enforces architecture boundaries; phase-aware routing keeps agents focused
  instead of overwhelmed — 4 phases (explore/plan/edit/verify), each with a
  focused shortlist. Set `KERN_MCP_PHASE=explore` to filter the advertised
  tools; the full 86-tool catalog stays behind `KERN_MCP_FULL=1`.
  Most MCP servers expose capability with no policy layer.

When running as an MCP server (`kern-mcp`), kern exposes an **11-tool
high-level surface by default** (routed through `kern_meta`), with the full
**toolset (86 tools)** behind `KERN_MCP_FULL=1` for advanced use — and
phase-aware routing (`KERN_MCP_PHASE=explore|plan|edit|verify`) as the
default way to keep the advertised list focused. They map 1:1 to the CLI
commands, so opencode, Claude Code, Codex, Cursor and 8 more agents get the
engine over MCP:

| Group | Tools |
|---|---|
| **Context optimization** | `kern_optimize_prompt`, `kern_optimize_log`, `kern_optimize_output`, `kern_context_budget`, `kern_pack`, `kern_swap`, `kern_compact_file`, `kern_project_map` |
| **Code graph** | `kern_ast_search`, `kern_fts_search`, `kern_search`, `kern_repo_search`, `kern_code_graph`, `kern_graph`, `kern_inherits`, `kern_context`, `kern_near`, `kern_walk`, `kern_path`, `kern_probe`, `kern_explore`, `kern_why`, `kern_frameworks`, `kern_entry_points`, `kern_communities` |
| **Change & review** | `kern_changes`, `kern_review`, `kern_churn`, `kern_trace`, `kern_hubs`, `kern_bridges`, `kern_arch`, `kern_dead`, `kern_larges`, `kern_test_gaps`, `kern_cochange` |
| **Safety** | `kern_mask_pii`, `kern_security`, `kern_safe_delete`, `kern_verify_output`, `kern_guard_check`, `kern_schema_validate`, `kern_sandbox` |
| **Automation** | `kern_run_build`, `kern_validate`, `kern_heal`, `kern_exec`, `kern_execute`, `kern_rename`, `kern_diff_files`, `kern_commitmsg`, `kern_doc_fetch`, `kern_doc_index`, `kern_doc_search`, `kern_precache`, `kern_memory_add`, `kern_memory_list`, `kern_memory_recall`, `kern_memory`, `kern_lock`, `kern_unlock`, `kern_lock_status`, `kern_semcache`, `kern_stats`, `kern_usage_guide`, `kern_buddy` |
| **High-level workflow** | `kern_analyze`, `kern_plan`, `kern_impact`, `kern_what_if`, `kern_verify`, `kern_incident`, `kern_agents`, `kern_loop`, `kern_correlate`, `kern_learn`, `kern_modernize` |

<sub>Tools are available both over stdio (any MCP client) and the Streamable
HTTP transport with an Origin allow-list (loopback only; empty origins are
accepted for non-browser clients).</sub>

### High-level analysis & workflow tools

The **high-level tools** (`kern_analyze`, `kern_plan`, `kern_impact`,
`kern_what_if`, `kern_verify`, `kern_incident`, `kern_agents`, `kern_loop`,
`kern_correlate`, `kern_learn`, `kern_modernize`)
build higher-level workflows on the index and graph: analyzing a proposed
change against the whole system, planning the change, estimating blast
radius, verifying claims, and recovering from incidents — deterministic and
local, like everything else in kern.

The design loop: **UNDERSTAND → REMEMBER → REASON → PLAN → ACT → VERIFY →
PROTECT → OBSERVE → LEARN ↺**. The governing principles:

1. **Deterministic things stay deterministic** — AST, graph, hashes, policy,
   and tests are never turned into LLM guesses. LLMs are used only for
   planning, reasoning, and summarization.
2. **Every important AI claim is typed** — FACT, INFERENCE, HYPOTHESIS, or
   RECOMMENDATION — with source, provenance, timestamp, scope, and confidence.
3. **Governance is local-first** — approvals, policy, and the tamper-evident
   audit chain (`kern audit`) gate what an agent may do, and every governed
   action is recorded.

The broader north-star ambitions — a multi-agent runtime, additional surfaces
(REST/SDK/IDE/K8s/webhooks), deployment, and a production feedback loop — are
explicitly **future**; see [Roadmap](#roadmap).

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
| `kern --llm` + `KERN_LLM_PROVIDER` (`anthropic`/`openai`/`google`) | you pass a flag **and** set the provider env | optional remote LLM rewriting — sends your prompt to the chosen vendor's API; never invoked by default |

Everything else — compression, indexing, analysis, search, masking, token
counting — runs entirely on your machine. If you're reading a prompt that
contains secrets, `kern mask` scrubs them **before** any optional LLM call.

### Security posture at a glance

kern's surfaces are deliberately fail-closed: nothing exposes data or runs
code unless a human opt-in exists. The matrix:

| Surface | Default posture | Opt-in / escape hatch |
|---|---|---|
| **CLI** (`kern ...`) | local process, no daemon, no listening socket | - |
| **MCP stdio** (`kern mcp`) | local pipe; path args confined to cwd (**fail-closed**) | `KERN_MCP_PERMISSIVE=1` restores raw mode; `KERN_MCP_ROOTS` pins explicit roots |
| **MCP HTTP** (`kern-mcp --http`) | **loopback-only** - a non-loopback bind is refused (kern-mcp exposes RCE-capable tools) | run behind your own authenticated proxy if remote access is required |
| **MCP exec tools** (`kern_exec`/`kern_sandbox`/`kern_execute`) | **denied** unless allowlisted | `KERN_ALLOW_EXEC=1` or `KERN_TOOLS` allowlist |
| **Script/sandbox isolation** | isolated by default; `no_isolate` ignored, network blocked | `KERN_ALLOW_NO_ISOLATE=1`, `KERN_ALLOW_NET=1` |
| **kern-server (local mode)** | binds `127.0.0.1:8090` by default, **no auth** - localhost only | change `-addr` at your own risk; put a proxy in front for remote |
| **kern-server (enterprise mode)** | **fail-closed**: refuses to serve (503) without `KERN_AUTH_TOKEN`; per-project isolation | `-enterprise` + `KERN_ENTERPRISE_PROJECTS` |
| **Deploy** (`ShellDeployer`) | **fail-closed**: refuses to run unless explicitly enabled | `KERN_ALLOW_DEPLOY=1` (+ optional `KERN_DEPLOY_COMMAND`) |
| **Remote LLM providers** | never contacted by default | `--llm` + `KERN_LLM_PROVIDER=anthropic|openai|google`; prompts masked first |

Every gate is documented in code next to its check (e.g.
`internal/governance/exec.go`, `internal/deployment/deployment.go`,
`internal/mcp/gate.go`), so the fail-closed behavior is auditable, not a claim.

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
- **`.kern/agents.json`** (optional) — custom agent wiring. Declare
  forked/private agents as JSON (`name`, config `path` with `~`/`$VAR`
  expansion, servers `key`, `entry` shape `stdio`|`cmd`, `scope`
  `global`|`repo`) and `kern setup` wires them exactly like the built-in
  adapters; a name clash with a builtin overrides it. The user-scope
  counterpart is `~/.config/kern/agents.json` (project file wins on
  clash). Like the rest of `.kern/`, it is gitignored by default —
  unignore it to share a team's agent list.
- **Event relay** — `kern events serve` owns `.kern/events.sock` and fans
  the system event bus out to any number of watchers across processes;
  `kern events watch [--kind policy.evaluated,...] [--json]` streams it,
  and `kern events emit <kind> [--subject S] [--payload k=v]` publishes
  from scripts. `kern lock`/`unlock` publish lock lifecycle events
  (acquired/contended/released) through the relay, so concurrent agents
  see contention in real time. Guard check outcomes publish the same way:
violations and the not-configured warning are appended to
`.kern/events.jsonl` for replay when no relay is running, and streamed
live when one is. The socket is local-user-only; a second
  server (e.g. kern-server) detects the live owner and runs without its
  own relay. Deep workspace paths that exceed the OS socket-path limit
  transparently bind under a hashed name in the temp dir.
- **Environment variables** — `OLLAMA_HOST` (default `http://localhost:11434`,
  used only when you opt in), `KERN_EMBED_MODEL` (default
  `nomic-embed-text`), `KERN_VERSION`/`KERN_INSTALL_DIR` (installer).
`KERN_TOKENIZER` selects the token counter used across all sizing and
savings numbers: `estimator` (default — stable, heuristic, offline),
`bpe` (self-trained byte-level BPE), or exact OpenAI encodings
`cl100k` / `o200k` (official rank tables embedded in the binary; GPT-4o
and o-series models map to o200k, GPT-3.5/4 to cl100k via `KERN_MODEL`).
The estimator stays the default so historical numbers remain comparable;
`go run ./evaluate/bench` gates the exact counters against reference
counts. `KERN_CACHE_ARCHIVE_DAYS` (default 7) and `KERN_CACHE_TTL_DAYS`
(default 30) drive the cache garbage collector: entries untouched past
the archive age are gzip-compressed in place, and past the TTL they are
evicted (`kern cache maintain [--dry-run]` runs it on demand; it also
runs automatically, at most once an hour). `KERN_MCP_AUDIT_DIR` overrides
where the MCP server persists its tool-call audit chain (default
`<project>/.kern/audit`).
`KERN_INCREMENTAL=1` makes the web console rebuild its index
incrementally on staleness: unchanged files (mtime fast path, then
content-hash check) reuse the previous index's per-file parse results
instead of re-parsing. Rebuilds stay equivalent to full rebuilds; the
flag only changes how the new index is computed.
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
Release binaries are built with CGO_ENABLED=0 for portability, so the bundled lightweight build uses fast regex extraction instead of the optional tree-sitter grammars and JSON (not SQLite) caching; build locally with `-tags "sqlite treesitter"` to enable full FTS5 search and tree-sitter extraction.

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

All agents receive the same MCP surface (11 high-level tools by default, 86
in full mode, phase-filtered via `KERN_MCP_PHASE`) and the same `AGENTS.md` rules. Output
compression + session memory run natively where the platform's hook API allows
in-place output replacement (opencode, Claude Code, Gemini); agents without
such an API keep full MCP parity but no automatic interception. Generated
wiring files carry machine-specific binary paths, so `kern setup` adds them to
`.gitignore` and the index never scans agent config directories.

---

## GitHub Action

`action/action.yml` ships a reusable **composite GitHub Action** ("kern Code
Review") that runs kern's code-intelligence review on a PR — blast radius,
token savings, edge confidence, and boundary guard checks — and posts the
result as a PR comment (updating the previous `<!-- kern-review -->` comment
on re-runs). Reference it from any workflow:

```yaml
- uses: JayveerPrajapati/kern/action@v0.8.0
  with:
    install-method: download   # "download" (prebuilt binary) or "go-install"
    fail-on-risk: "false"      # "true" fails the job on boundary guard violations
    range: origin/main..HEAD   # optional; defaults to base..HEAD from the PR event
```

Inputs: `range` (git range to review, default `base..HEAD` from the PR
event), `root` (project root, default `.`), `file` (comma-separated files to
review instead of a range), `review-max` (token budget for the blast-radius
panel, default `6000`), `fail-on-risk` (fail on guard violations), `version`
(kern version to install, default `latest`), and `install-method`
(`download` or `go-install`, default `download`).

## Supported Languages

Updated by one content-hash manifest; language is detected by extension or
shebang. **17 languages indexed by default** (dependency-free heuristics):

Go · Python · JavaScript (JSX) · TypeScript (TSX) · Rust · C · C++ · C# ·
Java · Ruby · PHP · Shell · CSS/SCSS/Less · HTML · Markdown · JSON · YAML

<sub>Vue/Svelte single-file components and Astro pages extract their
`<script>`/frontmatter blocks and index them as JS/TS (`<script lang="ts">`
included) — so the same 17-language pipeline covers frontend SFCs too. Go is
parsed precisely with `go/ast`.</sub>

**14 languages upgraded with tree-sitter** (`-tags treesitter` build):
Go · Python · JavaScript · TypeScript · TSX · Bash/Shell · C · C++ · CSS ·
Java · PHP · Ruby · Rust · Dart — inheritance edges and precise calls included.

### Precision

kern indexes 17 languages. In the default (dependency-free) build, Go and
Java are at **resolved** precision (compiler-accurate call edges); the other
15 are at **heuristic** precision (regex-based, skipped under
`--precision strict`).

For AST-level precision on 14 more languages (Python, JavaScript, TypeScript,
TSX, Bash, C, C++, CSS, PHP, Ruby, Rust, Dart, and more), build with
`-tags treesitter`. This requires CGO and the tree-sitter grammar
dependencies — see `go.mod`.

Run `kern doctor` or `kern index --status` to see which languages are resolved
for your build.

This tradeoff (dependency-free default vs. AST-precision opt-in) is
intentional: kern's core value is 100% local, zero-config operation.
Tree-sitter is available for users who need deeper cross-language analysis and
can accept the CGO dependency.

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

## Roadmap

kern's delivered scope is a local code-intelligence and governance engine. The
north-star vision extends further — these are explicitly **future** and not yet
shipped:

- **ModelProvider abstraction** — pluggable LLM backends (OpenAI, Anthropic,
  Google) beyond the current Ollama-only path.
- **Additional surfaces** — IDE integrations and a Kubernetes operator are
  still future. REST API, typed Go SDK, outbound webhooks, and the HTML
  dashboard are **shipped**: start them with `kern serve` (single-project
  REST + dashboard) or `kern serve --enterprise` (multi-project, shared org
  audit/memory/policies).
- **Multi-agent runtime** — coordinated agent orchestration with shared state.
- **Production feedback loop** — runtime trace correlation and deployment
  evidence chains.
- **Enterprise/shared mode** — **shipped** via `kern serve --enterprise`:
  multiple projects behind one listener with shared org-level audit log, event
  bus, memory store, and policy set, gated by a bearer token (`KERN_AUTH_TOKEN`).
  Multi-user/centrally-hosted server deployment is still future.

What's shipped today is the foundation: index, graph, guard, audit, context
optimization, and the MCP/CLI surfaces. Everything above builds on it.

---

## License

[MIT](LICENSE) © 2026 Jayveer Prajapati