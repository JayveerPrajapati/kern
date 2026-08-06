# kern

Local-first token optimizer for AI agents.

`kern` is an offline CLI + MCP server that pre-processes prompts, logs, code, and
build output before they reach an LLM, shrinking token usage. Everything runs on
your machine — no network calls, no telemetry, no paid APIs. Compression is
deterministic (regex/heuristic rules; token counts are estimated or exact
byte-level BPE), so identical input always produces identical output.

## Why kern

The selling points, in priority order:

1. **Private & offline by default** — the runtime makes no network calls and
   reports no telemetry. Optional local Ollama rewriting is opt-in (`--llm`) and
   silently falls back to the deterministic path when unavailable.
2. **Deterministic, reproducible output** — identical input always produces
   identical output; token counts are exact (byte-level BPE) or consistently
   estimated, so before/after savings are always honest.
3. **Instant value** — a 30-second quick start: one command compresses a noisy
   log, one command maps a whole project.
4. **One command wires every agent** — `kern setup` configures opencode, Claude
   Code, Codex, Cursor, Windsurf and 8 more MCP adapters in one shot.
5. **Savings you can measure** — every run is tracked; `kern stats` / `kern diff`
   report before/after tokens and cost saved.
6. **A real code-intelligence engine** — AST index plus dependency-free
   analysis: change impact, review context, hotspots, dead code, call paths,
   architecture guards, free-text search.
7. **Dependency-free static binaries** — Go stdlib only, single binary, no
   server to run, no modules to install.

## Quick start (30 seconds)

```sh
# compress a noisy log before pasting it into your agent
tail -n 500 server.log | kern optimize "server log" --attach -

# a project map an agent can read instead of the whole codebase
kern project . > /tmp/project_map.txt

# see the savings
kern stats
```

## Install

| Method | Command | Notes |
|---|---|---|
| **curl \| sh** | `curl -fsSL https://raw.githubusercontent.com/JayveerPrajapati/kern/main/install.sh \| sh` | Prebuilt binary from GitHub Releases → `~/.local/bin` |
| **Homebrew** | `brew tap JayveerPrajapati/tap && brew install kern` | Formula in `homebrew/kern.rb` (builds from source) |
| **pip** | `pip install kern-context` | Thin shim that fetches the prebuilt binary on first use |
| **go install** | `go install github.com/JayveerPrajapati/kern/cmd/kern@latest && go install github.com/JayveerPrajapati/kern/cmd/kern-mcp@latest` | Both binaries to `$(go env GOPATH)/bin` |
| **from source** | `make build` → `bin/kern`, `bin/kern-mcp` | Requires Go 1.22+ |

`install.sh` honors `KERN_VERSION` (pin a release), `KERN_INSTALL_DIR` (default
`~/.local/bin`) and falls back to `go install` when no prebuilt asset matches
your platform. Verify any install with `kern version`.

After installing, run:

```sh
kern setup        # wire kern into your agents (opencode, Claude, any MCP client)
kern setup --check
kern doctor       # health report: wiring, index, Ollama
```

## Agent wiring — one command

`kern setup` makes kern available to every agent on the machine. It is
idempotent — run it any time:

```sh
kern setup             # wire everything below
kern setup --check     # show current wiring status
kern setup --agents claude    # only wire specific agents
```

What it configures:

| Target | Mechanism |
|---|---|
| **Any MCP agent** | writes `<root>/.mcp.json` — auto-discovered by Claude Code, Cursor, Windsurf and most MCP clients |
| **opencode** | `opencode.json` (project MCP) + `.opencode/plugins/kern.ts` (first-class tools + auto-interception) + `AGENTS.md` rules + global config MCP entry |
| **Claude Code** | `claude mcp add kern -- <abs path to kern-mcp>` |
| **Codex** | appends `[mcp_servers.kern]` to `~/.codex/config.toml` |
| **JSON adapters** | `continue`, `windsurf`, `zed`, `vscode`, `cursor`, `gemini`, `antigravity`, `qwen`, `qoder`, `kiro`, `copilot`, `copilot-cli` — writes their MCP config (project- or home-based), creating parent dirs as needed |

`kern buddy` prints a session briefing — project map, language mix, symbol
kinds, most-called hub symbols, entry points and recent kern savings — a
starting context you can paste into a fresh agent session. `kern prompt
<template>` emits prompts for the templates `code-review`, `fix-bug`,
`write-tests`, `explain`, `onboard`, `debug`, with project map and file context
pre-filled.

## What it does

| Problem | kern's answer |
|---|---|
| Noisy logs pasted into prompts | `kern_optimize_log` — keeps errors, stack traces, build failures; strips timestamps/chatter/dedupes |
| Agent re-reading the whole codebase | `kern_project_map` / `kern_compact_file` — symbolic summaries (functions, types, lines), cached by content hash |
| Huge build/test output flooding context | `kern_run_build` — runs the command, returns only pass/fail + errors |
| Verbose prompts | `kern_optimize_prompt` — cleans and compresses before sending |
| "How much did I save?" | `kern_stats` / `kern diff` — before/after tokens, cost saved |

## Code intelligence

`kern index` builds a real AST-level index of a Go project (via `go/ast` — no
external dependencies). It tracks symbols, imports, call edges and reverse
callers. `kern watch` runs a background daemon that detects file changes by
content hash and re-indexes automatically. Indexes persist in
`~/.cache/kern/index/` per project.

Every code-intelligence command (`kern changes`, `near`, `probe`, `trace`,
`guard`, …) goes through `ReadIndex`, which compares the on-disk file set
against the index's content-hash manifest and rebuilds automatically when a
source file is added, removed, or edited — so analyses never serve a stale call
graph and never require a manual `kern index` first.

**Multi-language** (dependency-free heuristics): Go is parsed with `go/ast`;
other languages use a built-in extractor (`internal/index/foreign.go`) that
strips comments/strings, detects the language by extension or shebang, and
applies per-language declaration rules (methods via `Type::name` / `name(self)`
/ indent-based `def`, types via a brace-depth stack). Supported: Go, Rust,
C/C++, TypeScript, JavaScript (incl. JSX/TSX), Python, Ruby, Java, PHP, shell,
CSS (classes, ids, `@keyframes`, custom properties), HTML (`id` anchors), and
Vue/Svelte single-file components (the `<script>` block is extracted and
indexed as JS/TS, incl. `<script lang="ts">`). `kern index`
prints the language mix; symbols carry a `Lang` field. `kern ast` understands
kind prefixes `class`, `enum`, `trait`, `module`, `union`, `impl`, `prop`
alongside the Go ones.

| Command | Purpose |
|---|---|
| `kern ast "func *Err*"` | AST symbol search; prefixes `func`, `method`, `struct`, `interface`, `type`, `const`, `var`; `*` wildcards |
| `kern search "load index"` | ranked free-text symbol search — multi-word, name/path matching, best match first; `--repos` searches every registered repo |
| `kern graph <sym>` | definition + callers + what it calls; `--mermaid` flowchart, `--json`/`--graphml`/`--html` exports (edges carry confidence tiers) |
| `kern context <sym>` | minimal source slice (definition + callers + calls) |
| `kern why <sym>` | rationale: doc comment + who depends on it and why (each caller's own doc line) |
| `kern wiki` | export a markdown wiki — one page per package, symbols with docs, locations, callers |
| `kern repos add/list/remove` | multi-repo registry powering cross-repo `kern search --repos` |

<details>
<summary>Code intelligence — all 15 analysis commands</summary>

**Code intelligence** — kern turns the same AST index into a dependency-free
analysis engine (`internal/intel`, pure Go, no databases or servers). All of
these operate on the persisted index and take `--json` for machine-readable
output:

| Command | Purpose | Notes |
|---|---|---|
| `kern changes` | change-impact report: changed symbols → blast radius (transitive callers), risk scores, test gaps, cross-package flags | risk = blast radius × churn × untested |
| `kern review` | token-optimised review context for a diff, sized to a budget | budget via `--max` |
| `kern hubs` / `kern testgaps` | architectural hotspots + bridges; coverage % + untested hotspots | hubs ranked by in-project callers |
| `kern flows` | execution flows from entry points (depth, reach, longest path) | |
| `kern communities` | call-graph clustering via label propagation (deterministic, no deps) | |
| `kern path <a> <b>` | shortest call path between two symbols (bidirectional traversal) | |
| `kern dead` | dead code: functions/methods nothing in-project calls, sorted by size | public symbols flagged as potential API |
| `kern larges` | largest declarations by source lines — god-function finder | |
| `kern arch` | architecture overview: subsystems + coupling warnings (cross-community call bundles) | |
| `kern churn` | change-frequency risk: most-churned files, flagged if edited NOW, with graph risk | |
| `kern near <sym>` | N-hop semantic neighborhood tree, both directions, budget-capped — "everything two hops away from this model" | `kern walk` is an alias; depth 0 = symbol only, default 2 |
| `kern probe "<text>"` | task-driven micro-context bundle: candidate symbols → definitions + callers + callees + tests + inter-anchor paths, budget-capped | anchors via regex; caps 12 anchors / 12 callers+callees |
| `kern trace` | telemetry overlay: hot symbols, their blast radius, risk, test coverage | accepts pprof `-top`, crash stacks, or symbol lists; stdin via `-` |
| `kern guard` | deterministic architectural guardrails from `.kern/boundaries.json`; `REJECT` + exit 2 on violation | import-level and call-level crossings; allow rules override forbids; missing `--file` → git working-tree diff |
| `kern lock` / `unlock` / `status` | advisory workspace locks so concurrent agents coordinate (flock on Unix, exclusive-create on Windows) | locks auto-release on process exit; stale files removable |

Test files are indexed too, so coverage analysis sees what the tests exercise.
Only project-local call edges feed flows/communities/hubs/arch — stdlib calls
are never treated as nodes. Package-qualified callees (`pkg.Fn`) are normalised
to the canonical symbol so traversals never dead-end.

</details>

## Agent powerups

Additional capabilities for long-running agent sessions:

- **`kern doctor`** — one-shot health report: binary, kern-mcp path, agent
  wiring, index freshness, Ollama reachability and savings stats, each with
  `[ok]` / `[warn]` / `[fail]`. Run it after `kern setup` or when something
  feels off.
- **Cross-session memory** — `kern remember "<lesson>"` stores a lesson for the
  project; `kern memory` shows them (newest first). `kern buddy` automatically
  injects the last few into its briefing, so a fresh agent session inherits
  what earlier sessions learned.
- **Context budget** — `kern budget "<text>" --max N` (or pipe stdin) dedupes
  lines, keeps the head plus important lines (errors, stack frames), then
  trims to N tokens. The same logic backs the `kern_context_budget` MCP tool.
- **Git hooks** — `kern hook install` adds a `post-commit` hook that compresses
  each commit's diff (file headers + hunks + added/removed lines, 200-line
  cap) and stores it in project memory, so agents always know the latest
  change. `kern hook diff`/`store` work standalone too.
- **Mermaid call graphs** — `kern graph <sym> --mermaid` renders the call graph
  as a `flowchart LR` you can paste straight into a Markdown doc. For
  visualisation tools, `--json`, `--graphml` (yEd/Gephi/Cytoscape) and
  `--html` (self-contained interactive SVG: hover to trace edges, click for
  details) export the same graph; edges carry **confidence tiers** — high when
  both endpoints resolve to real definitions, dashed/red when an endpoint is an
  unresolved name.
- **Cross-project search** — `kern ast "<pattern>" --all` searches every cached
  project index at once (great for "where have I solved this before?"), and
  `kern search "query" --repos` does ranked free-text lookup across repos you
  register with `kern repos add`.
- **Rationale & docs** — `kern why <sym>` shows a symbol's doc comment plus each
  caller with its own doc line ("who depends on it and why"); `kern wiki`
  exports a markdown wiki with one page per package for community reference.

## MCP server (works with any MCP agent)

Start it: `kern-mcp` (or `kern mcp`). It speaks MCP over stdio and exposes 29
`kern_*` tools plus workflow prompts — any MCP agent can consume it over stdio
or over HTTP (`kern-mcp --http :8080` speaks the Streamable HTTP transport:
`POST /mcp` with JSON-RPC bodies, batch requests supported, no SSE streaming,
advertised via `streamableHttpCapabilities`).

<details>
<summary>MCP tools — all 29 `kern_*` tools</summary>

- `kern_optimize_prompt(prompt, attached_log?, session?, model?)`
- `kern_compact_file(path)`
- `kern_project_map(root, max_files?)`
- `kern_run_build(command, dir?)`
- `kern_optimize_log(log)`
- `kern_stats(days?, session?)`
- `kern_ast_search(pattern, root?, limit?)`
- `kern_search(query, root?, limit?)`
- `kern_repo_search(query, limit?)`
- `kern_code_graph(symbol, root?)`
- `kern_context(symbol, root?, lines?)`
- `kern_context_budget(text, max_tokens?)`
- `kern_why(symbol, root?)`
- `kern_changes(root?, range?, file?)`
- `kern_review(root?, range?, file?, max_tokens?)`
- `kern_hubs(root?, limit?)`
- `kern_test_gaps(root?, limit?)`
- `kern_path(root?, from, to)`
- `kern_dead(root?, limit?)`
- `kern_larges(root?, min_lines?, limit?)`
- `kern_arch(root?)`
- `kern_churn(root?, range?)`
- `kern_near(symbol, depth?, max?, root?)`
- `kern_probe(task, root?, max_tokens?)`
- `kern_trace(trace, root?, limit?)`
- `kern_lock(scope, root?)`
- `kern_unlock(scope)`
- `kern_lock_status(root?)`
- `kern_guard_check(root?, file?, range?)`

</details>

It also exposes **workflow prompts** (`prompts/list`, `prompts/get`) that
string a sequence of `kern_*` calls into a single step-by-step instruction for
the host model: `review_changes`, `architecture_map`, `debug_issue`,
`onboard_developer`, `pre_merge_check`.

### opencode

**MCP server** (all 29 `kern_*` tools available in the TUI):

```jsonc
// opencode.json
{
  "mcp": {
    "kern": { "type": "local", "command": ["kern-mcp"], "enabled": true }
  }
}
```

**Plugin** (`.opencode/plugins/kern.ts`, auto-discovered — no config entry needed):

- Registers thirty first-class tools (`kern_optimize_prompt`,
  `kern_compact_file`, `kern_project_map`, `kern_run_build`, `kern_optimize_log`,
  `kern_stats`, `kern_changes`, `kern_review`, `kern_hubs`, `kern_test_gaps`,
  `kern_path`, `kern_dead`, `kern_larges`, `kern_arch`, `kern_churn`,
  `kern_near`, `kern_walk`, `kern_ast_search`, `kern_search`, `kern_repo_search`,
  `kern_code_graph`, `kern_context`, `kern_context_budget`, `kern_why`,
  `kern_probe`, `kern_trace`, `kern_lock`, `kern_unlock`, `kern_lock_status`,
  `kern_guard_check`) that shell out to the local `bin/kern` binary.
- Auto-intercepts `bash`/`read`/`grep` results over 4 KB and compresses them
  before they enter context (`kern log` under the hood).
- Binary resolution: `$KERN_BIN` → `<project>/bin/kern` → `PATH`.

Global install (available in every project):

```sh
make install            # cp kern kern-mcp -> ~/.local/bin
make hooks              # global MCP config + plugin + AGENTS.md
```

Then restart opencode so the config and plugin load.

### Claude Code

```sh
claude mcp add kern -- kern-mcp
```

### Cursor / other MCP clients

Add a stdio server named `kern` with command `kern-mcp`.

## Local LLM compression (optional)

Deterministic compression is the default and always works offline. If you run
a local [Ollama](https://ollama.dev), `kern optimize --llm <model>` sends the
prompt to the local server for a smarter rewrite:

- `--llm` flag on `kern optimize`; env `KERN_MODEL` and `OLLAMA_HOST`
  (default `http://localhost:11434`, model `llama3.2`).
- If Ollama is unreachable, errors, or the response is empty, kern silently
  falls back to the deterministic path — it never blocks a run.

## Design

- **Local only** — the runtime makes no network calls and no telemetry. The
  installers fetch a prebuilt binary once; `kern optimize --llm` talks only to a
  local Ollama server when you opt in.
- **No paid APIs** — the deterministic path is pure rule-based compression
  (regex/AST heuristics). Optional local-model compression via Ollama is opt-in
  (`--llm`).
- **State in the cache dir** — analysis state lives in `~/.cache/kern/`
  (honours `XDG_CACHE_HOME`); only explicit wiring (`kern setup`), guard rules
  and lock files are written into the workspace.
- **Dependency-free** — single static binaries, Go stdlib only, no external
  modules.
- **Exact BPE counting** — `tokenize.Counter` has two implementations: the
  default estimator (cheap, consistent before/after) and `BPECounter` — a real
  byte-level BPE (GPT-2 style) that trains a merge table once from a bundled
  corpus, so counts are exact and reproducible offline.

```
core (parse -> compress -> cache -> token-count)
  ├── kern        CLI + MCP on stdio
  ├── kern-mcp    MCP server (stdio; --http ADDR for HTTP)
  └── stats       JSONL before/after tracking
```

## Reference

<details>
<summary>CLI usage — full command reference</summary>

```sh
kern optimize "Fix the login bug, check error handling." --attach build.log
kern optimize --llm llama3.2 "a very long prompt…"   # local Ollama step (opt-in)
kern preview  "..." --attach log.txt        # dry-run, no stats recorded
kern compact  src/main.go                    # file summary
kern project  .                              # compact project map
kern build    "go test ./..."                # compact build output
kern log      server.log

 kern index    .                              # build AST index
 kern watch    .                              # daemon: auto re-index on change
 kern ast      "func *rompt*"                 # AST search (wildcards, kind prefixes)
 kern search   "load index" [--repos]         # ranked free-text symbol search (--repos: across registered repos)
 kern graph    Prompt                         # call graph: def + callers + calls
 kern context  symbolRegex                    # minimal source slice for a symbol
 kern why      Prompt                         # rationale: doc comment + who depends on it and why
 kern wiki     [root]                         # export a markdown wiki (one page per package)

# Code intelligence (built on the same AST index)
kern changes  [root] [--range a..b] [--file f] [--json]   # change impact: blast radius, risk, test gaps
kern review   [root] [--range a..b] [--max N]             # token-optimised review context for a diff
kern hubs     [root] [--limit N] [--json]                 # most depended-on symbols + cross-package bridges
kern testgaps [root] [--limit N] [--json]                 # test coverage % + untested hotspots
kern flows    [root] [--limit N] [--json]                 # execution flows from entry points
kern communities [root] [--json]                          # call-graph communities (label propagation)
kern arch    [root] [--json]                         # architecture overview: communities + coupling
kern dead    [root] [--limit N] [--json]             # dead code (no in-project callers)
kern churn   [root] [--range a..b] [--json]          # change churn over git history
kern larges  [root] [--lines N] [--limit N] [--json] # largest declarations by source lines

kern near     <sym> [root] [--depth N] [--max N]       # N-hop neighborhood tree (callers + callees)
kern walk     <sym> [root] [--depth N] [--max N]       # alias of `kern near` (default depth 2)
kern probe    "<task text>" [root] [--max N]           # task -> budget-capped micro-context bundle
kern trace    <file|- for stdin> [root] [--limit N]    # overlay pprof/stack trace on the call graph
kern guard    init [root]                              # write starter .kern/boundaries.json
kern guard    check [root] [--file f] [--range a..b]   # enforce boundaries; exit 2 on violations
kern lock     <scope> [root]                           # acquire advisory lock, block until interrupt
kern unlock   <scope> [root]                           # release a held lock
kern status   [root] [--json]                          # show held locks

kern stats    --days 7 --json                # savings report
kern tokens   "fix the login bug."           # token count (estimator)
kern tokens   --bpe "fix the login bug."     # token count (exact BPE)
kern diff     --session abc                  # recent before/after entries
kern export   --csv                          # full history

kern setup    --check                        # show agent wiring status
kern setup                                   # wire kern into agents (idempotent)
kern buddy                                   # session onboarding digest for any agent
kern prompt   fix-bug --file src/x.go --task "crashes on empty input"
kern prompt   list                           # list available templates
kern doctor                                  # diagnostics: binary, wiring, index, ollama
kern remember "always use tabs here"         # record a lesson in project memory
kern memory                                  # show cross-session project memory
kern memory   --clear                        # wipe project memory
kern budget   "$(cat build.log)" --max 4000  # fit text into a token budget
kern hook     install                        # post-commit: commit diff -> project memory
kern hook     diff                           # compressed git diff
kern hook     store [range]                  # store compressed diff in project memory
 kern ast      "func *extract*" --all         # search across every cached project
 kern graph    Prompt --mermaid               # call graph as Mermaid flowchart
 kern graph    Prompt --html --out g.html     # interactive HTML visualisation
 kern graph    Prompt --graphml --out g.xml   # export to yEd/Gephi/Cytoscape
 kern search   "login" --repos                # cross-repo search (kern repos add <path>)
 kern why      Prompt                         # doc comment + who depends on it and why
 kern wiki     --out docs/wiki                # export a markdown wiki
 kern mcp                                     # run MCP server on stdio
 kern-mcp --http :8080                        # serve MCP over HTTP (Streamable HTTP, POST /mcp)
 kern path                                    # show cache dir
 kern version                                 # show version
```

</details>

## Project layout

```
cmd/kern        CLI
cmd/kern-mcp    MCP server entry
internal/tokenize   offline token counting (estimator + exact BPE)
internal/compress   log/prompt noise stripping
internal/code       project map + symbol summaries
internal/cache      ~/.cache/kern persistence
internal/optimize   orchestrator
internal/llm        optional local Ollama compression client
internal/stats      JSONL savings tracking
internal/mcp        dependency-free MCP server: stdio + HTTP (Streamable HTTP, --http ADDR); tools, prompts
internal/index      AST index (go/ast + dependency-free multi-language extractor): symbols, imports, call graph, watcher, mermaid, JSON/GraphML/HTML graph exports
internal/intel      code intelligence: changes, review, hubs, flows, near, probe, trace, guard, path, dead, larges, arch, churn, search, why, wiki, repos
internal/lock       advisory workspace locks: flock on Unix, exclusive-create on Windows
internal/setup      one-command agent wiring (opencode / claude / codex / continue / windsurf / zed / vscode / cursor / gemini / antigravity / qwen / qoder / kiro / copilot / copilot-cli)
internal/brief      session onboarding digest ("buddy")
internal/prompt     fine-tuned prompt templates
internal/memory     cross-session per-project lessons
internal/budget     token-budget fitting (dedup + head + important lines)
internal/doctor     diagnostics report
internal/hooks      git post-commit hook (diff -> memory)
```
