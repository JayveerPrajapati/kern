# kern

**Kern your context — less tokens, same meaning.**

`kern` is a **100% local, free, offline** context optimizer for AI agents. It pre-processes prompts, logs, code, and build output before they reach an LLM — cutting tokens and cost without losing meaning.

Everything runs on your machine. Nothing is uploaded. Nothing is stored in your workspace (state lives in `~/.cache/kern/`).

## What it does

| Problem | kern's answer |
|---|---|
| Noisy logs pasted into prompts | `kern_optimize_log` — keeps errors, stack traces, build failures; strips timestamps/chatter/dedupes |
| Agent re-reading the whole codebase | `kern_project_map` / `kern_compact_file` — symbolic summaries (functions, types, lines), cached by content hash |
| Huge build/test output flooding context | `kern_run_build` — runs the command, returns only pass/fail + errors |
| Verbose prompts | `kern_optimize_prompt` — cleans and compresses before sending |
| "How much did I save?" | `kern_stats` / `kern diff` — before/after tokens, cost saved |

## Install

### Pick your channel

| Method | Command | Notes |
|---|---|---|
| **curl \| sh** | `curl -fsSL https://raw.githubusercontent.com/JayveerPrajapati/kern/main/install.sh \| sh` | Prebuilt binary from GitHub Releases → `~/.local/bin` |
| **Homebrew** | `brew tap JayveerPrajapati/tap && brew install kern` | Formula in `homebrew/kern.rb` (builds from source) |
| **pip** | `pip install kern-context` | Thin shim that fetches the prebuilt binary on first use |
| **go install** | `go install github.com/JayveerPrajapati/kern/cmd/kern@latest` | Also installs `kern-mcp` (`cmd/kern-mcp`) |
| **from source** | `make build` → `bin/kern`, `bin/kern-mcp` | Requires Go 1.22+ |

`install.sh` honors `KERN_VERSION` (pin a release), `KERN_INSTALL_DIR` (default `~/.local/bin`) and falls back to `go install` when no prebuilt asset matches your platform. Verify any install with `kern version`.

### First run

```sh
kern setup        # wire kern into your agents (opencode, Claude, any MCP client)
kern setup --check
kern doctor       # health report: wiring, index, Ollama
```

## CLI usage

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
kern graph    Prompt                         # call graph: def + callers + calls
kern context  symbolRegex                    # minimal source slice for a symbol

kern stats    --days 7 --json                # savings report
kern tokens   "fix the login bug."           # token count (estimator)
kern tokens   --bpe "fix the login bug."     # token count (exact BPE)
kern diff     --session abc                  # recent before/after entries
kern export   --csv                          # full history

kern setup    --check                        # show agent wiring status
kern setup                                   # wire kern into agents (idempotent)
kern buddy                                   # session onboarding digest for any agent
kern prompt   fix-bug --file src/x.go --task "crashes on empty input"
kern doctor                                  # diagnostics: binary, wiring, index, ollama
kern remember "always use tabs here"         # record a lesson in project memory
kern memory                                  # show cross-session project memory
kern budget   "$(cat build.log)" --max 4000  # fit text into a token budget
kern hook     install                        # post-commit: commit diff -> project memory
kern hook     diff                           # compressed git diff
kern ast      "func *extract*" --all         # search across every cached project
kern graph    Prompt --mermaid               # call graph as Mermaid flowchart
kern mcp                                     # run MCP server on stdio
kern path                                    # show cache dir
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

`kern buddy` prints a session briefing — project map, language mix, symbol
kinds, most-called hub symbols, entry points and recent kern savings — the
best starting context to paste into any fresh agent session. `kern prompt
<template>` emits fine-tuned, token-efficient prompts (`code-review`,
`fix-bug`, `write-tests`, `explain`, `onboard`, `debug`) with project map and
file context pre-filled.

## Agent powerups

Things that make kern a better long-lived companion for any agent:

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
  as a `flowchart LR` you can paste straight into a Markdown doc.
- **Cross-project search** — `kern ast "<pattern>" --all` searches every cached
  project index at once (great for "where have I solved this before?").

## Index engine

`kern index` builds a real AST-level index of a Go project (via `go/ast` —
no dependencies). It tracks symbols, imports, call edges and reverse callers.
`kern watch` runs a background daemon that detects file changes by content hash
and **re-indexes automatically — no manual intervention**. Indexes persist in
`~/.cache/kern/index/` per project.

**Multi-language** (dependency-free heuristics): Go is parsed with `go/ast`;
other languages use a built-in extractor (`internal/index/foreign.go`) that
strips comments/strings, detects the language by extension or shebang, and
applies per-language declaration rules (methods via `Type::name` / `name(self)`
/ indent-based `def`, types via a brace-depth stack). Supported: Go, Rust,
C/C++, TypeScript, JavaScript, Python, Ruby, Java, PHP, shell. `kern index`
prints the language mix; symbols carry a `Lang` field. `kern ast` understands
kind prefixes `class`, `enum`, `trait`, `module`, `union`, `impl`, `prop`
alongside the Go ones.

| Command | Purpose |
|---|---|
| `kern ast "func *Err*"` | AST symbol search; prefixes `func`, `method`, `struct`, `interface`, `type`, `const`, `var`; `*` wildcards |
| `kern graph <sym>` | definition + callers + what it calls |
| `kern context <sym>` | minimal source slice (definition + callers + calls) — the token-saver for agents |

## MCP server (works with any MCP agent)

Start it: `kern-mcp` (or `kern mcp`). It speaks MCP over stdio and exposes:

- `kern_optimize_prompt(prompt, attached_log?, session?, model?)`
- `kern_compact_file(path)`
- `kern_project_map(root, max_files?)`
- `kern_run_build(command, dir?)`
- `kern_optimize_log(log)`
- `kern_stats(days?, session?)`
- `kern_ast_search(pattern, root?, limit?)`
- `kern_code_graph(symbol, root?)`
- `kern_context(symbol, root?, lines?)`
- `kern_context_budget(text, max_tokens?)`

### opencode

**MCP server** (all 9 `kern_*` tools available in the TUI):

```jsonc
// opencode.json
{
  "mcp": {
    "kern": { "type": "local", "command": ["kern-mcp"], "enabled": true }
  }
}
```

**Plugin** (`.opencode/plugins/kern.ts`, auto-discovered — no config entry needed):

- Registers six first-class tools (`kern_optimize_prompt`, `kern_compact_file`,
  `kern_project_map`, `kern_run_build`, `kern_optimize_log`, `kern_stats`) that
  shell out to the local `bin/kern` binary.
- Auto-intercepts `bash`/`read`/`grep` results over 4 KB and compresses them
  before they enter context (`kern log` under the hood).
- Binary resolution: `$KERN_BIN` → `<project>/bin/kern` → `PATH`.

Global install (available in every project):

```sh
make install            # cp kern kern-mcp -> ~/.local/bin
make hooks              # global MCP config + plugin + AGENTS.md
```

Then restart opencode so the config and plugin load.

## Local LLM compression (optional)

Deterministic compression is the default and always works offline. If you run
a local [Ollama](https://ollama.dev), `kern optimize --llm <model>` sends the
prompt to the local server for a smarter rewrite:

- `--llm` flag on `kern optimize`; env `KERN_MODEL` and `OLLAMA_HOST`
  (default `http://localhost:11434`, model `llama3.2`).
- If Ollama is unreachable, errors, or the response is empty, kern silently
  falls back to the deterministic path — it never blocks a run.

### Claude Code

```sh
claude mcp add kern -- kern-mcp
```

### Cursor / other MCP clients

Add a stdio server named `kern` with command `kern-mcp`.

## Design

- **Local only** — no network calls, no telemetry, no server.
- **Free** — pure deterministic rules (regex/AST heuristics). No paid APIs. Optional local-model compression via Ollama is opt-in (`--llm`).
- **Nothing in your workspace** — all state in `~/.cache/kern/` (honours `XDG_CACHE_HOME`).
- **Dependency-free** — single static binaries, stdlib only.
- **Exact BPE counting** — `tokenize.Counter` has two implementations: the
  default estimator (cheap, consistent before/after) and `BPECounter` — a real
  byte-level BPE (GPT-2 style) that trains a merge table once from a bundled
  corpus, so counts are exact and reproducible offline.

```
core (parse -> compress -> cache -> token-count)
  ├── kern        CLI + MCP on stdio
  ├── kern-mcp    MCP server entry
  └── stats       JSONL before/after tracking
```

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
internal/mcp        dependency-free MCP stdio server
internal/index      AST index (go/ast + dependency-free multi-language extractor): symbols, imports, call graph, watcher, mermaid
internal/setup      one-command agent wiring (opencode / claude / continue / windsurf / zed / vscode / any MCP client)
internal/brief      session onboarding digest ("buddy")
internal/prompt     fine-tuned prompt templates
internal/memory     cross-session per-project lessons
internal/budget     token-budget fitting (dedup + head + important lines)
internal/doctor     diagnostics report
internal/hooks      git post-commit hook (diff -> memory)
```

## Releasing a new version

1. **Set your repo once** (before the first release):
   ```sh
   ./scripts/retarget.sh github.com/yourname/kern   # rewrites module path + <OWNER> placeholders
   go build ./... && go test ./...
   ```
   Then `git init`, add, commit, `git remote add origin git@github.com:yourname/kern.git`, push.

2. **Tag and let CI build** — push a `v*` tag and the workflow
   `.github/workflows/release.yml` cross-compiles linux/darwin/windows assets
   and publishes them to GitHub Releases with install notes:
   ```sh
   git tag v0.1.0 && git push origin v0.1.0
   ```

3. **Homebrew tap** — the release workflow does *not* auto-publish to brew.
   After each release:
   ```sh
   ./scripts/brew-release.sh v0.1.0 > kern.rb   # fills in SHA256 from the tag tarball
   # copy kern.rb to your homebrew-tap repo: git@github.com:yourname/homebrew-tap.git
   #   Formula/kern.rb, then commit + push
   ```

4. **PyPI** — to publish the pip shim:
   ```sh
   cd python && python -m build && python -m twine upload dist/*   # requires `build` and `twine`
   ```

`make release VERSION=v0.1.0` also builds the same tarballs locally if you
prefer to attach them manually.

## Roadmap

- [x] Deterministic compression (logs, prompts, code maps, builds)
- [x] MCP stdio server with 10 tools
- [x] Token/cost savings tracking (CLI + MCP)
- [x] AST index: search, code graph, minimal-context slices
- [x] `kern watch` — automatic re-indexing on file change
- [x] Multi-language AST (dependency-free heuristic extractor for Rust, C/C++, TS/JS, Python, Ruby, Java, PHP, shell)
- [x] opencode plugin adapter (auto-interception of tool output + first-class kern tools)
- [x] Ollama-backed prompt compression (optional local LLM step, deterministic fallback)
- [x] Exact BPE tokenizer behind the `tokenize.Counter` interface (`kern tokens --bpe`)
- [x] Universal agent wiring (`kern setup` — any MCP agent, opencode, Claude Code)
- [x] Agent buddy (`kern buddy` session briefing) + fine-tuned prompt templates (`kern prompt`)
- [x] Cross-session project memory (`kern remember`/`kern memory`, injected into buddy)
- [x] Context budget management (`kern budget` + `kern_context_budget` MCP tool)
- [x] Diagnostics report (`kern doctor`)
- [x] Git integration (`kern hook` — post-commit diff -> memory)
- [x] More agent adapters (continue, windsurf, zed, vscode)
- [x] Mermaid call graphs (`kern graph --mermaid`) + cross-project AST search (`kern ast --all`)
