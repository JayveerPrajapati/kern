# The 10-Minute Architecture Tour

You are a new contributor. This walks you from the binary's entry point to a
tool call hitting the index, in the order execution actually flows. Paths only,
no line numbers (they rot); every claim here is checkable with `kern` itself.

## 0. The shape (30 seconds)

One Go module, three binaries:

| Binary | Entry | What it is |
|---|---|---|
| `kern` | `cmd/kern` | The CLI — 424 commands |
| `kern-mcp` | `cmd/kern-mcp` | The MCP server agents talk to |

All logic lives in `internal/` (200 packages). `cmd/kern` is deliberately thin:
command-table entries dispatch to `run*` functions that parse flags and call
`internal/*` packages.

## 1. A CLI call, end to end (2 minutes)

`kern explore someFunc`:

1. `cmd/kern/main.go` → `cmd/kern/dispatch.go` reads the subcommand and looks it
   up in `commandTable` (`cmd/kern/dispatch_table.go` — name, category, help,
   usage, and the `run` function for every command).
2. The `run*` function (e.g. in `cmd/kern/cmd_context.go`) resolves flags and
   delegates to an `internal/` package.
3. Anything that needs code facts loads the index:
   `internal/index.Load(root)` — a prebuilt symbol graph cached in `.kern/`
   (JSON + SQLite), never rebuilt per request.
4. Output is rendered and the process exits with the repo-wide exit-code
   contract: `0` success, `1` findings/failure, `2` usage, `3` denied/blocked
   (pinned by `exit_contract_test.go`).

## 2. The index — where the value is (3 minutes)

`internal/index` is the core: symbol extraction, the call graph, and the
persistence layer.

- **Go** is extracted with the native `go/ast` (tier `resolved`) — no fallback.
- **13 other languages** (Java, Python, TypeScript, Rust, ...) go through
  tree-sitter grammars (`treesitter.go`, CGO) in the default build, or the
  regex fallback (`-tags notreesitter`) — a documented lower-fidelity
  *subset* of tree-sitter's facts.
- The parity contract between the two extractors is **enforced by tests**
  (`parity_test.go`, `parity_report_test.go`): regex may be less certain,
  never more certain, and never drop a fact tree-sitter is confident in.
  The measured per-language numbers are published in
  [`docs/benchmarks/accuracy.md`](../benchmarks/accuracy.md) — and gated:
  the doc must contain the live-measured rows.
- `internal/intel` runs the graph queries on top (blast radius, impact,
  hotspots, dead code). Quirk to know: cross-package call edges carry
  qualified callee names — code traversing edges must use the RAW edge
  endpoints, not resolved node IDs.

## 3. The MCP surface (2 minutes)

`internal/mcp` is the server; `internal/mcpcatalog/tools.go` is the single
source of truth for the 32-tool catalog (name, phase, risk, schema).

- The full 32-tool catalog is advertised by default (`internal/mcp/toolsurface`
  — the original `kern_meta`, `kern_explore`, `kern_impact`, `kern_search`,
  `kern_verify`, `kern_buddy`, `kern_exec` plus the usage-curated additions);
  standing mandate 2026-10-23. The
  advertisement gate never blocks execution: `kern_meta`'s natural-language
  router reaches every tool.
- Every tool call passes `precheckTool` → string validation → the governance
  gate. `kern_exec` additionally goes through
  `internal/governance.CheckExec` — fail-closed, approval-bound, audited.
- Audit entries go to a tamper-evident chain (`.kern/audit/`); entries lost
  to a store failure are counted and surfaced by `kern doctor`
  (`audit-dropped` check).
- The opencode plugin (`.opencode/plugins/kern.ts`) mirrors the catalog and
  the default surface — it must stay byte-identical to
  `internal/setup/assets/plugin/kern.ts`, with its sha256 registered in
  `internal/setup/plugin_hashes.go`. Change one, sync all four copies.

## 4. The self-checks that keep this honest (2 minutes)

kern dogfoods itself, and the gates bite contributors:

- **`ARCHITECTURE.md` is a contract** — `go test ./internal/architecture/`
  parses the subsystem ledger (LOC caps + allowed deps) and fails on
  divergence. New internal import? Update the ledger FIRST.
- **Doc counts are derived** — `TestDocToolCountsDerived`
  (`internal/setup`) fails if any doc states a tool count that is not the
  live catalog/default/legacy number.
- **`kern doctor`** is the user-facing health surface — index freshness,
  precision tiers, plugin sync, dropped audit entries.
- The benchmarks under `docs/benchmarks/` are protocols + measured numbers,
  each with its own gate test.

## Where to start

- Run `make build && ./bin/kern doctor` in the repo — everything should be
  green; read the check names.
- Pick a small subsystem from `ARCHITECTURE.md` (e.g. `internal/strutil`,
  59 LOC) and read it whole.
- The iteration loop is `make test-touched` — it runs only the packages your
  diff touches, plus the gate packages that pin cross-package files.
  `make test-short` (the full suite) is the pre-merge ritual.

Welcome in. The rule of the house: if a claim is checkable, it is checked by a
test — including the claims in this tour.
