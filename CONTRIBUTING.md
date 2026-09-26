# Contributing to kern

kern is a local, deterministic code-intelligence engine for AI agents —
one self-contained Go binary, no telemetry, 100% offline. This file is the
contributor front door; the [README](README.md) covers the user story.

## Quick start

```bash
git clone https://github.com/JayveerPrajapati/kern
cd kern
go build ./...          # Go 1.25+
go test ./...           # full suite (~2-4 min); targeted: go test ./internal/sec/
go vet ./...
```

No codegen bootstrap: everything is `go` + the repo itself (a thin 159-line
`Makefile` wraps the common workflows — see `make help`).
`kern` is self-hosting — run `go run ./cmd/kern doctor` to check your
worktree the way users' installs are checked.

## Repo layout

| Path | What it is |
|---|---|
| `cmd/kern` | The CLI. One `run*` function per command; the command table lives in `dispatch_table.go` (215 entries: name, category, help, usage). |
| `cmd/kern-mcp` | The MCP server binary (delegates to `internal/mcp`). |
| `cmd/kern-server` | Web console + enterprise control plane (`internal/web`). |
| `internal/` | All core logic — 184 packages (190 total incl. `cmd/`, `evaluate/`, `sdk`). `internal/mcp` is the MCP surface; `internal/app` is the platform glue. |
| `docs/` | Generated + curated docs. `docs/tool-catalog.md` is GENERATED — never hand-edit. |
| `.kern/sandboxes/loop/` | A SEPARATE go module (vendored snapshot for sandbox-loop tests). Never rewrite its imports when refactoring `internal/` — `go build ./...` excludes it, and rewriting breaks it. |

## House rules (the ones tests enforce)

**ARCHITECTURE.md is a contract.** `go test ./internal/architecture/`
parses the subsystem ledger in [ARCHITECTURE.md](ARCHITECTURE.md) and
fails on divergence: every `internal/*` dir has a LOC cap and an
allowed-deps set. Adding a new internal import to a package means updating
the table FIRST — that is the drift gate. Caps are ~1.5x baseline.

**MCP catalog parity.** `internal/mcp/catalog/tools.go` is the single
source of truth for tool names/descriptions. After changing it:

```bash
go run ./cmd/kern gen-catalog              # regenerates docs/tool-catalog.md
go test ./internal/setup/ -run TestPluginMatchesMCPCatalog
```

The two plugin copies (`.opencode/plugins/kern.ts` and
`internal/setup/assets/plugin/kern.ts`) must stay **byte-identical**.

**Sandbox SkipDirs.** `internal/sandbox` skips large dirs when snapshotting
(100 MiB cap). If you add a big generated/artifact directory to the repo,
add it to SkipDirs or the closed loop (`kern loop`, `kern do`) regresses at
the verify stage.

**Tags.** SQLite is default-ON (`-tags nosqlite` opts out); tree-sitter is
opt-in (`-tags treesitter`). CI-relevant changes should build both ways.

**Determinism is a product principle.** AST/graph/hashes/policy stay
deterministic; LLMs are only for planning/reasoning/summarization — never
turn a deterministic fact into an LLM guess. Nothing phones home.

## Adding a CLI command

1. Write `runYourThing(rest []string)` in `cmd/kern/` (see any `cmd_*.go`).
2. Register it in `dispatch_table.go` (category, run, help, usage strings).
3. Flags: the generic flag set lives in `flags.go` (`parseFlags`); add the
   field + `case "--your-flag":` there, not ad-hoc.
4. Add a test (`cmd/kern/yourthing_test.go` — see `captureStdout` /
   `newRoot` helpers) and update `docs/cli-reference.md` if it changes UX.
   `go test ./cmd/kern/ -run TestCLIReferenceDocCoversCommandTable` gates the
   doc against the dispatch table: every command key must appear in the doc.

## Commit style

Conventional commits, scope = package or surface:
`feat(cache): …`, `fix(sec): …`, `docs(mcp): …`, `refactor(blueprint): …`.
Keep commits local-first and self-verifying: the message states what
changed, why, and how it was verified (which tests ran).

## Verification bar

- Targeted package tests while iterating (`go test ./internal/<pkg>/`).
- Full `go test ./...` + `go vet ./...` before handing off.
- `go test ./internal/architecture/` after touching `internal/` structure.
- For MCP-surface changes: regenerate the catalog doc + run the parity
  tests above, then smoke the server:
  `printf '…jsonrpc initialize…' | go run ./cmd/kern-mcp`.
