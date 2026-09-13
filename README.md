<div align="center">

# kern

Already installed? Run `kern doctor` to verify everything is wired.

### The local, deterministic code-intelligence engine for AI agents

**Index · graph · guard · audit · context optimization — one self-contained CLI binary, zero network, no telemetry**

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Language: Go](https://img.shields.io/badge/Language-Go_1.25+-blue.svg)](https://go.dev/)
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

**Phase-aware MCP routing (11 high-level tools by default, 131 in full mode) · 90+ CLI commands · 17 indexed languages (see [Supported Languages](#supported-languages)) · 100% local**

</div>

## Contents

- [Get Started](#get-started)
- [Why kern?](#why-kern)
- [Built for determinism — the Go kernel](#built-for-determinism--the-go-kernel)
- [Key Features](#key-features)
- [Framework-aware Entry Points](#framework-aware-entry-points)
- [How It Works](#how-it-works)
- [Agent Skills & Runbooks](#agent-skills--runbooks)
- [CLI Reference](#cli-reference)
- [MCP Tools](#mcp-tools)
- [Telemetry & Privacy](#telemetry--privacy)
- [Configuration](#configuration)
- [Supported Platforms](#supported-platforms)
- [Supported Agents](#supported-agents)
- [GitHub Action](#github-action)
- [Supported Languages](#supported-languages)
- [Docs](#docs)
- [Troubleshooting & FAQ](#troubleshooting--faq)
- [Uninstall](#uninstall)
- [Roadmap](#roadmap)
- [License](#license)

## Get Started

### 1. Install the CLI

**No runtime required** — prebuilt static binaries, one command per platform:

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/JayveerPrajapati/kern/main/install.sh | sh

# Windows (PowerShell)
powershell -ExecutionPolicy Bypass -c "irm https://raw.githubusercontent.com/JayveerPrajapati/kern/main/install.ps1 | iex"
```

<details>
<summary><b>Other install methods — homebrew, go install, source</b></summary>

| Method | Command | Notes |
|---|---|---|
| **Homebrew** | `brew install --build-from-source ./homebrew/kern.rb` | A formula ships in this repo; `scripts/publish-tap.sh` publishes your own tap (the release workflow also attaches a ready-to-use `kern.rb` to every GitHub Release) |
| **go install** | `go install github.com/JayveerPrajapati/kern/cmd/kern@latest && go install github.com/JayveerPrajapati/kern/cmd/kern-mcp@latest && go install github.com/JayveerPrajapati/kern/cmd/kern-server@latest` | All three binaries to `$(go env GOPATH)/bin` |
| **from source** | `make build` → `bin/kern`, `bin/kern-mcp`, `bin/kern-server` | Requires Go 1.25+ |

<sub>`install.sh` (macOS/Linux) and `install.ps1` (Windows) honor `KERN_VERSION`
(pin a release) and `KERN_INSTALL_DIR` (default `~/.local/bin`); they fall back
to `go install` when no prebuilt asset matches your platform. Verify any
install with `kern version`.</sub>

</details>

#### Binaries

The `kern` CLI is one self-contained static binary (stdlib-only by default).
The repo also builds two companion binaries, installed by `install.sh` /
`make build` alongside it:

- **`kern-mcp`** — the MCP server entry point. Agent configs point at this
  binary (the same server also runs as the `kern mcp` subcommand).
- **`kern-server`** — the REST API + HTML dashboard server (`kern serve`).

Two more binaries exist in the repo but are secondary: **`kernops`** (the
terminal cockpit / governed-autonomy surface behind `kern ops`) and
**`blueprint`** / **`blueprint-mcp`** (the change-governance engine behind
`kern check` / `kern fix` / `kern ci`; the standalone binaries are legacy
surface — use `kern` directly).

### 2. Wire up your agent(s)

Open a **new terminal** (or re-source your shell profile) so the
`~/.local/bin` PATH entry from a default install takes effect, then connect
kern to every agent on the machine:

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

Auto-sync is on by default: `kern watch` uses native OS file events
(inotifywait/fswatch when present, a polling fallback otherwise), debounces,
and re-indexes exactly what changed. Sessions inside opencode run their own
file-event watcher, so the index is never stale while your agent edits code.

### 4. See it work

The index is live — ask kern about this very codebase:

```bash
kern explore treeOID    # definition, callers, callees, blast radius
```

Actual output (run in this repo):

```text
symbol: treeOID (func internal/index/identity.go:182)

== callers (8) ==
FreshnessProofStrict [EXTRACTED]
TestTreeOID_CleanTreeFastPath [EXTRACTED]
TestTreeOID_DirtyTreeSlowPath [EXTRACTED]
TestTreeOID_SlowPathWithGitignoredKern [EXTRACTED]
TreeOIDProbe [EXTRACTED]
VerifySnapshot [EXTRACTED]
finishFreshness [EXTRACTED]
startIdentityGit [EXTRACTED]
== callees (15) ==
Background [EXTRACTED]
Close [INFERRED]
CommandContext [EXTRACTED]
CreateTemp [EXTRACTED]
Environ [EXTRACTED]
Name [INFERRED]
Output [INFERRED]
Remove [EXTRACTED]
Run [INFERRED]
TrimSpace [EXTRACTED]
WithTimeout [EXTRACTED]
append [EXTRACTED]
cancel [EXTRACTED]
string [EXTRACTED]
treeOIDFast [EXTRACTED]
== blast radius (2557 symbols, 571 files) ==
Add
AddAuto
Agent.Code
Agent.Plan
AgentCheck
Analyzer.Analyze
App.ArchitectureReport
App.buildArchitecture
App.buildDashboard
App.buildGraph
App.buildOverview
App.buildSystemMap
App.buildTaskDetailData
App.freshGraph
App.handleApprovalApprove
App.handleArchitecture
App.handleArchitecturePage
… (blast-radius list continues)
```

`kern why <symbol>` adds the rationale — who depends on it and why. Actual
output for the same symbol:

```text
func treeOID  internal/index/identity.go:182

doc: (none)

incoming edges: 8, outgoing calls: 15

who depends on it and why:
  Index.FreshnessProofStrict   internal/index/identity.go:284 [EXTRACTED]  FreshnessProofStrict always recomputes the content root (a full re-hash of
  Index.TreeOIDProbe           internal/index/ensure.go:20 [EXTRACTED]  TreeOIDProbe is the tri-state git tree-OID freshness probe: a git
  Index.finishFreshness        internal/index/identity.go:380 [EXTRACTED]  finishFreshness resolves the verdict by recomputing the content root from
  TestTreeOID_CleanTreeFastPath internal/index/identity_test.go:157 [EXTRACTED]  TestTreeOID_CleanTreeFastPath pins the cheap path: on a clean worktree the
  TestTreeOID_DirtyTreeSlowPath internal/index/identity_test.go:194 [EXTRACTED]  TestTreeOID_DirtyTreeSlowPath: any real change (staged or unstaged, inside
  TestTreeOID_SlowPathWithGitignoredKern internal/index/identity_test.go:261 [EXTRACTED]  TestTreeOID_SlowPathWithGitignoredKern reproduces the live defect: a repo
  VerifySnapshot               internal/index/snapshot.go:119 [EXTRACTED]  VerifySnapshot checks whether the tree at root still matches what snap was
  startIdentityGit             internal/index/identity.go:78 [EXTRACTED]  startIdentityGit begins observing root's git identity concurrently. Call
```

Now point your agent at kern. In any agent, ask:

> What breaks if I change `treeOID`? Who depends on it, and why?

— kern answers from the index instead of the agent re-reading the tree.

The success criterion for the whole install is `kern doctor`:

```text
# kern doctor — .

[ok] AGENTS.md rules        kern entry present
[ok] antigravity            kern entry present
[ok] antigravity (detected) kern-first policy present
[ok] binary                 /Users/jayveer.prajapati/.local/bin/kern
[ok] binary-exec            /Users/jayveer.prajapati/.local/bin/kern-mcp runs (Usage of /Users/jayveer.prajapati/.local/bin/kern-mcp:)
[ok] blueprint-mcp          WARN: cannot read version from /Users/jayveer.prajapati/.local/bin/blueprint-mcp; refresh it from the same release as kern
[ok] cache                  2 files under /Users/jayveer.prajapati/.cache/kern
[ok] capabilities           sqlite: on (persistent index + FTS5) · treesitter: on (13 grammars)
[ok] claude                 kern MCP registered (project or user scope)
[ok] claude (detected)      kern-first policy present
[ok] claude hooks           kern entry present
[ok] codex                  kern MCP registered; codex_hooks feature: on
[ok] codex hooks            kern entry present
[ok] config                 no .kern/config.json (defaults)
[ok] continue               kern entry present
[ok] copilot                kern entry present
[ok] copilot                kern entry present
[ok] copilot (detected)     kern-first policy present
[ok] cursor                 kern entry present
[ok] cursor (detected)      kern-first policy present
[ok] cursor hooks           kern entry present
[ok] cursor rule            kern entry present
[ok] env                    KERN_ALLOW_EXEC=1
[ok] freshness              index is fresh (12926 symbols, 1373 files)
[ok] gemini                 kern entry present
[ok] gemini (detected)      kern-first policy present
[ok] gemini hooks           kern entry present
[ok] git global ignore      kern entry present
[ok] git local exclude      kern entry present
[ok] gitignore (generated block) kern entry present
[ok] index                  12926 symbols, 1373 files, 0 cached projects
[ok] kern-mcp               /Users/jayveer.prajapati/.local/bin/kern-mcp
[ok] kiro                   kern entry present
[ok] mcp (project .mcp.json) kern entry present
[ok] opencode (global config) kern entry present
[ok] opencode (project)     kern entry present
[ok] opencode plugin        kern entry present
[ok] opencode plugin (global) kern entry present
[ok] opencode plugin (global) kern entry present
[ok] opencode-plugin-sync   2 installed plugin copy(ies) match the embedded asset
[ok] precision              all 11 languages at AST-or-better precision (tree-sitter build; 2 resolved)
[ok] qoder                  kern entry present
[ok] qoder hooks            kern entry present
[ok] qwen                   kern entry present
[ok] skills (project)       kern skills present
[ok] skills-antigravity (global) global kern skills present
[ok] skills-claude (global) global kern skills present
[ok] skills-codex (global)  global kern skills present
[ok] skills-copilot (global) global kern skills present
[ok] skills-cursor (global) global kern rules present
[ok] skills-opencode (global) global kern skills present
[ok] skills-qoder (global)  global kern skills present
[ok] skills-qwen (global)   global kern skills present
[ok] stats                  3720 ops, 831436 tokens saved (19.9%)
[ok] vscode                 kern entry present
[ok] windsurf               kern entry present
[ok] zed                    kern entry present
[warn] copilot hooks          not present
[warn] network-isolation      network isolation: unavailable (darwin) — scripts fail closed unless KERN_ALLOW_UNISOLATED=1 (or KERN_ALLOW_NET=1) is set
[warn] ollama                 http://localhost:11434 not reachable (optional; deterministic compression still works)
[warn] parity                 unstamped binary (version=dev): build with -ldflags "-X github.com/JayveerPrajapati/kern/internal/version.Version=$(git rev-parse HEAD)" to enable build-vs-repo parity
[warn] runtime                no runtime source; set KERN_PROMETHEUS_URL / KERN_OTEL_URL / KERN_K8S_API, or provide .kern/runtime.json
[warn] version                kern dev · go1.27.0 · darwin/arm64

verdict: warnings — mostly optional; run `kern setup` and `kern index .`
```

<sub>The doctor output above is this machine's actual run — paths and numbers
vary per install. The verdict line tells you exactly what to run next.</sub>

---

## Why kern?

kern gives AI coding agents a **local, deterministic model of your
codebase** — symbols, call graphs, architecture boundaries, and a
tamper-evident audit trail — so agents work from evidence instead of
guessing. It is a code-intelligence and governance engine that runs entirely
on your machine: one self-contained CLI binary, zero network calls, no
telemetry.

What's delivered is concrete: a symbol index across 17 languages (see
[Supported Languages](#supported-languages)), call-graph and blast-radius
analysis, architecture guards (`kern guard`), an approvals/governance
firewall, and a tamper-evident audit chain (`kern audit`). On top of that
foundation sits context optimization:

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

**Retrieval recall (docs index): 3/3 (100%)** at recall@5 — 3/3 in the
current bench harness (`go run ./evaluate/bench`).

Your own savings are recorded locally and visible with `kern stats` — they are
per-install numbers, so they are not published here.

---

## Built for determinism — the Go kernel

kern's engine is a **single static Go binary** — the `kern` CLI is one
self-contained binary (the repo also builds companion `kern-mcp` and
`kern-server` binaries; see [Binaries](#binaries)). The default build is
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
| **One command wires every agent** | `kern setup` wires 12 JSON-config adapters plus opencode, Claude Code, Codex, Gemini, Cursor and any MCP client via `.mcp.json` — 17 MCP write targets total — plus custom adapters via `.kern/agents.json`, in one shot |
| **Savings you can measure** | Every run is tracked: `kern stats` / `kern diff` report before/after tokens and cost saved |
| **Real code intelligence** | AST index + dependency-free analysis: change impact, blast radius, hotspots, dead code, call paths, architecture guards, communities, coverage gaps |
| **Framework-aware** | 74-framework detection catalog + route extraction linking URL patterns to handlers |
| **Always fresh** | File-event watcher (inotifywait/fswatch + poll fallback) with debounced auto-sync; stale-index guard on every read |
| **Surgical context** | `kern context` / `kern explore` / `kern probe` hand the agent exactly the source it needs — no file-by-file crawling |
| **Safety tooling** | PII masking, secret scanning, hallucination verification (`file:line` claims), snapshot sandbox, self-healing test fixes, JSON-schema validation |
| **Multi-repo search** | `kern repos add` registers repos; `kern search --repos` / `--semantic` searches across all of them |
| **Zero-dependency single binary** | The `kern` CLI is one static binary: Go stdlib only by default; opt-in tree-sitter (14 grammars) and SQLite WAL + FTS5 via build tags |
| **Governed Autonomy (KernOps)** | 5-stage lifecycle ($L_0$–$L_5$) in ephemeral git worktree sandboxes, 30-gate immune system ($G_0$–$G_{29}$), machine-actionable repair contracts, Auto-SRE incident triage, and tamper-evident SLSA/in-toto v0.2 + SARIF 2.1.0 attestations |

<details>
<summary><strong>How auto-syncing works — why the index is never stale</strong></summary>

Three layers keep the index in step with your code:

1. **File watcher with debounced auto-sync.** `kern watch` and in-agent
   sessions use native OS file events (inotifywait on Linux, fswatch on macOS)
   when available — near-real-time with a short debounce — and fall back to
   polling.
2. **Stale-index guard on every read.** Every code-intelligence command goes
   through `ReadIndex`, which compares the on-disk file set against the
   index's content-hash manifest and rebuilds automatically when a source file
   is added, removed, or edited. Analyses never serve a stale call graph, even
   with no watcher running.
3. **Per-session invalidation.** Sessions in opencode invalidate the cached
   index on file events, bypassing the cooldown so burst tool calls still see
   fresh code.

```text
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

```text
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
   tools by default, 131 in full mode) read the same index:
   call graphs, blast radius, change impact, hotspots, dead code, path
   finding, architecture communities, coverage gaps — all dependency-free,
   all deterministic.
4. **Auto-sync** — file-event watchers (inotifywait/fswatch + polling
   fallback) rebuild the index on change; every read re-validates staleness
   against the manifest so analyses never go stale.

---

## Agent Skills & Runbooks

kern ships bundled agent skills adhering to the open Agent Skills standard (`SKILL.md` + executable helper scripts). These runbooks guide coding agents through high-impact workflows without guesswork:

| Skill | Focus & Capabilities | Helper Script |
|---|---|---|
| **`kern-investigate`** | Codebase exploration, symbol discovery, call-graph hierarchy, and blast-radius simulation using kern's prebuilt index instead of slow file reads and greps. | `./scripts/inspect.sh <symbol>` |
| **`kern-safe-change`** | Predictive pre-edit safety checks, change-firewall validation ($G_0$–$G_{29}$), sandboxed auto-repair loop, and cryptographic CI receipts. | `./scripts/pre_check.sh <target>` |
| **`kern-incident-triage`** | Auto-SRE production crash & error triage: deduplicates & compresses logs, correlates stack traces to AST symbols, writes reproduction unit tests in sandbox, and drives auto-repair. | `./scripts/triage.sh <path_to_log>` |

### Managing Skills

```bash
kern skills                # list all bundled agent skills
kern skills show <name>    # display detailed markdown runbook for a skill
kern skills install        # install/sync skills into .agents/skills and global agent directories
```

---

## CLI Reference

The full command reference (90+ commands, including the `kern exec`
sandboxed-runtime details) lives in
[`docs/cli-reference.md`](docs/cli-reference.md). Highlights:

```bash
kern explore <symbol>     # definition + callers + callees + blast radius
kern why <symbol>         # rationale: doc comment + dependents
kern graph <symbol>       # call-graph neighbourhood; exports JSON/GraphML/HTML
kern context <symbol>     # minimal source slice
kern setup --check        # per-agent wiring status
kern doctor               # diagnostics report
```

`kern exec` runs code in a fresh temp dir with a sanitized environment and
**fails closed** when network isolation is unavailable — full details and the
escape hatches (`KERN_ALLOW_EXEC`, `KERN_ALLOW_UNISOLATED`/`KERN_ALLOW_NET`)
are in the reference.

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
  tools; the full 131-tool catalog stays behind `KERN_MCP_FULL=1`.
  Most MCP servers expose capability with no policy layer.

When running as an MCP server (`kern-mcp`), kern exposes an **11-tool
high-level surface by default** (routed through `kern_meta`), with the full
**toolset (131 tools)** behind `KERN_MCP_FULL=1` for advanced use — and
phase-aware routing (`KERN_MCP_PHASE=explore|plan|edit|verify`) as the
default way to keep the advertised list focused. The same catalog is exposed
as `kern_*` MCP tools — 11 high-level tools by default, 131 in full mode —
mapped 1:1 to the CLI commands, so opencode, Claude Code, Codex, Cursor and
every other wired agent get the engine over MCP. The complete, generated
catalog (every `kern_*` tool with its phase, cost tier and description) lives
in [`docs/tool-catalog.md`](docs/tool-catalog.md) — regenerate it with
`kern gen-catalog` after any MCP tool change.

<sub>Tools are available both over stdio (any MCP client) and the Streamable
HTTP transport with an Origin allow-list (loopback only; empty origins are
accepted for non-browser clients).</sub>

**Optional TLS for the HTTP transport.** `kern-mcp --http` serves plain HTTP
on loopback by default. To serve HTTPS instead, pass `--tls-cert` and
`--tls-key` (PEM files), or set `KERN_MCP_TLS_CERT` and `KERN_MCP_TLS_KEY`
(flags win, env fills the gap). TLS is optional and backward compatible: with
no cert/key configured the server falls back to plain HTTP. A config with
only one of the two files set is rejected — it never silently downgrades to
plaintext. Example:

```sh
kern-mcp --http :8080 --tls-cert /path/to/cert.pem --tls-key /path/to/key.pem
# or
KERN_MCP_TLS_CERT=/path/to/cert.pem KERN_MCP_TLS_KEY=/path/to/key.pem kern-mcp --http :8080
```

The listener stays loopback-only even with TLS enabled; the loopback Origin
check still applies. TLS here protects against loopback sniffing and is the
building block for exposing the transport through a local TLS-terminating
proxy.

### High-level analysis & workflow tools

The **high-level tools** (`kern_analyze`, `kern_plan`, `kern_impact`,
`kern_what_if`, `kern_verify`, `kern_incident`, `kern_agents`, `kern_loop`,
`kern_correlate`, `kern_learn`, `kern_modernize`)
build higher-level workflows on the index and graph: analyzing a proposed
change against the whole system, planning the change, estimating blast
radius, verifying claims, and recovering from incidents — deterministic and
local, like everything else in kern.

**Caller/edge counts are projections, and they are consistent by design.**
All tools count *distinct* direct callers of a symbol (a symbol called twice
from the same caller counts once), but each surface projects over a
slightly different lens: `kern hubs`/`kern explore` count distinct caller
symbols from the intel index (`prodCallers` excludes test-only callers);
`kern impact`/`kern_impact` count distinct caller nodes in the
package-scoped intelligence graph, and `--precision strict` drops
inferred/ambiguous call edges (so a strict impact can report fewer callers
than `kern explore` on the same symbol). `kern buddy` and `kern onboard`
both report **total directed call edges** (each caller→callee pair), not the
number of callers. Treat counts as relative signal, and prefer
`kern explore`/`kern graph --html` when a single number decides a review.

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
| **MCP HTTP** (`kern-mcp --http`) | **loopback-only** - a non-loopback bind is refused (kern-mcp exposes RCE-capable tools) | run behind your own authenticated proxy if remote access is required; optional **TLS** via `--tls-cert/--tls-key` or `KERN_MCP_TLS_CERT`/`KERN_MCP_TLS_KEY` |
| **MCP exec tools** (`kern_exec`/`kern_sandbox`/`kern_execute`) | **denied** unless allowlisted | `KERN_ALLOW_EXEC=1` or `KERN_TOOLS` allowlist |
| **Script/sandbox isolation** | isolated by default; `no_isolate` ignored, network blocked | `KERN_ALLOW_NO_ISOLATE=1`, `KERN_ALLOW_NET=1` |
| **kern-server (local mode)** | **no auth** — `kern serve` binds all interfaces `:8090` by default (intended for local/loopback use); the standalone `kern-server` binary defaults to loopback `127.0.0.1:8090` | restrict with `-addr 127.0.0.1:8090`; put a proxy in front for remote access |
| **kern-server (enterprise mode)** | **fail-closed**: refuses to serve (503) without `KERN_AUTH_TOKEN`; per-project isolation | `-enterprise` + `KERN_ENTERPRISE_PROJECTS` |
| **Deploy** (`ShellDeployer`) | **fail-closed**: refuses to run unless explicitly enabled | `KERN_ALLOW_DEPLOY=1` (+ optional `KERN_DEPLOY_COMMAND`) |
| **Remote LLM providers** | never contacted by default | `--llm` + `KERN_LLM_PROVIDER=anthropic|openai|google`; prompts masked first |

Every gate is documented in code next to its check (e.g.
`internal/governance/exec.go`, `internal/deployment/deployment.go`,
`internal/mcp/gate.go`), so the fail-closed behavior is auditable, not a claim.

---

## Configuration

kern is **zero-config by default** — nothing to write or keep in sync to get
started. Language support is automatic from file extensions. The full catalog
of knobs (`.kern/boundaries.json`, `.kern/docs/`, `.kern/agents.json`, the
event relay, the env-var list, and the `.kern/config.json` reference) lives in
[`docs/configuration.md`](docs/configuration.md). The short version:

- **`.kern/config.json`** (optional) — one config path for operator knobs,
  resolved as **env var > `.kern/config.json` > built-in default**;
  `kern config` prints every effective value and its source.
- **Environment variables** — secrets and safety toggles stay env-only and are
  never read from the file (`KERN_AUTH_TOKEN`, `KERN_ALLOW_*`, `KERN_TOOLS`,
  `KERN_MCP_FULL/PHASE/...`, …); see `docs/configuration.md` for the full list.

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

`kern setup` wires kern into every agent it finds — **17 MCP write targets**
(the universal `.mcp.json` + opencode + Claude Code + Codex + 12 JSON-config
adapters + the global pre-wire) plus native hooks for agents whose hook APIs
allow it:

| Agent | MCP | Auto-interception & session memory |
|---|---|---|
| **Any MCP client** | project `<root>/.mcp.json` (auto-discovered by Claude Code, Cursor, Windsurf, most MCP hosts) | — |
| **opencode** | `opencode.json` + global config | `.opencode/plugins/kern.ts` — plugin API: compresses oversized tool output in place, captures edits/failures/prompts into project memory |
| **Claude Code** | `claude mcp add kern -- <abs path to kern-mcp>` | `.claude/settings.json` hooks — `PostToolUse` compresses large Bash/Read/Grep results (via `updatedToolOutput`) and records edits + failures; `UserPromptSubmit` captures prompts (`kern hook claude-post/…`) |
| **Gemini** | `.gemini/settings.json` (MCP entry) | `.gemini/settings.json` hooks — `AfterTool` compresses oversized shell/read/grep results (exit-2 stderr substitution) and records edits + failures; `BeforeAgent` captures prompts (`kern hook gemini-after/…`) |
| **Cursor** | `.cursor/mcp.json` | `.cursor/rules/kern-hooks.mdc` — instruction rule (Cursor cannot execute shell hooks; the rule steers the model to kern's MCP tools) |
| **Codex** | `[mcp_servers.kern]` in `~/.codex/config.toml` | `~/.codex/hooks.json` — `PreToolUse` hook blocks Bash and suggests kern's MCP equivalents via the kern-guard script, gated by `[features] codex_hooks = true` in `~/.codex/config.toml` |
| **JSON adapters** | 12 configs: `continue`, `windsurf`, `zed`, `vscode`, `antigravity`, `qwen`, `qoder`, `kiro`, `copilot` (VS Code `.vscode/mcp.json` + CLI `~/.copilot/mcp-config.json`), plus `cursor`/`gemini` (rows above) | — (no hook API) |

All agents receive the same MCP surface (11 high-level tools by default, 131
in full mode, phase-filtered via `KERN_MCP_PHASE`) and the same `AGENTS.md` rules. Output
compression + session memory run natively where the platform's hook API allows
in-place output replacement (opencode, Claude Code, Gemini, Codex); agents
without such an API keep full MCP parity but no automatic interception.
Generated wiring files carry machine-specific binary paths, so `kern setup`
adds them to `.gitignore` and the index never scans agent config directories.

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

---

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

## Docs

Deep-dives and references live alongside this README:

- [`docs/cli-reference.md`](docs/cli-reference.md) — the full CLI reference (90+ commands).
- [`docs/tool-catalog.md`](docs/tool-catalog.md) — the complete generated MCP tool catalog (131 tools).
- [`docs/configuration.md`](docs/configuration.md) — the configuration env-var and file catalog.
- [`docs/privacy.md`](docs/privacy.md) — telemetry & privacy deep-dive.
- [`docs/authorized-context.md`](docs/authorized-context.md) — the authorized-context primitive (agent identity + task scope).
- [`docs/mcp-client.md`](docs/mcp-client.md) — wiring MCP clients.
- [`docs/skills.md`](docs/skills.md) — bundled agent skills & runbooks.
- [`docs/review-lenses.md`](docs/review-lenses.md) · [`docs/review-pack.md`](docs/review-pack.md) — code-review lenses and review packs.
- [`docs/memory-governance.md`](docs/memory-governance.md) — engineering-memory governance.
- [`docs/consensus-divergence.md`](docs/consensus-divergence.md) — consensus/divergence semantics.
- [`docs/context-envelope.md`](docs/context-envelope.md) · [`docs/evidence-bundle-schema.md`](docs/evidence-bundle-schema.md) — context packets and evidence bundles.
- [`docs/diff-gate.md`](docs/diff-gate.md) — the change-firewall diff gate.
- [`docs/adr/`](docs/adr/) — architecture decision records (ADR-0001…0007).
- [`docs/security/`](docs/security/) — security deep-dives: [`threat-model.md`](docs/security/threat-model.md), [`signed-releases.md`](docs/security/signed-releases.md).
- [`docs/benchmarks/`](docs/benchmarks/) — the benchmark harness, corpora and fixtures behind the numbers above.
- [`docs/mcp/`](docs/mcp/) — MCP protocol, tool contracts and versioning.
- [`docs/examples/`](docs/examples/) — example configs (e.g. `kern-gate.yml`).
- [`docs/notes/`](docs/notes/) — engineering notes and design journals.
- [SECURITY.md](SECURITY.md) — security policy.

---

## Troubleshooting & FAQ

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

### FAQ

#### How do I optimize a prompt?
Run `kern optimize <prompt>` or call `kern_optimize_prompt`. It strips conversational filler and normalizes whitespace deterministically without external LLM dependencies.

#### How do I compress a log?
Run `kern log <file>` or call `kern_optimize_log`. It folds repetitive external stack frames and compresses log output deterministically.

---

## Uninstall

Nothing to uninstall — the `kern` CLI is a single static binary in your PATH.
Remove it and the `.mcp.json`/agent-config entries `kern setup` wrote, and
you're done.

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