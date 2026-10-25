# Changelog

> **Historical record.** Tool names, counts, and defaults in this document reflect the version at the time it was written. See [Tool Selection Guide](docs/tool-selection.md) for the current catalog and default surface.


All notable changes to kern are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Summary

## [0.9.12] — 2026-10-25
- **Tool Surface Expansion & Unified Change Application**: Added native `kern apply` CLI and `kern_apply` MCP tool, expanding the default advertised surface to 32 tools with full catalog parity across CLI and MCP layers.
- **Smart Guard Steering & Interactive Ask Protocol**: Transitioned from fail-closed command denials to 3-tier read serving, path-aware in-tree filesystem mutations, and an interactive human Ask Protocol (`kern guard hook`, `kern guard grant/pause`).
- **MCP Envelope Strictness & Pre-Indexing**: Hardened JSON-RPC protocol compliance (pre-initialize rejection, path normalization, token metadata), introduced `--preindex` cold-start warmup and background index preloading.
- **Index Engine & Language Precision**: Added exact-name search for digit-suffixed identifiers, fixed nested method/class receiver resolution across Java and Python heuristics, and extracted `internal/tokenize` under architectural LOC constraints.
- **Governance & Execution Hardening**: Serialized single-use allow-once tokens across processes, sandboxed network and execution boundaries, human-gated rejection workflows, and func-scoped security suppressions.
- **CI/CD Composite Deduplication**: Consolidated 19 redundant checkout/setup pairs into `.github/actions/go-env` and `.github/actions/release-assets`, trimming over 230 net lines of workflow boilerplate.

## [0.9.11] — 2026-10-21
- **Platform Refinement & Architecture Consolidation**: Completed core context-engine focus, retiring auxiliary server planes and consolidating internal packages into modular, focused subsystems (`internal/topology`, `internal/twinindex`, `internal/verifycmd`).
- **Core Governance & Verification**: Streamlined unified verification engine (`kern verify`) for security and architecture invariants; enhanced AST boundary guards, sandbox isolation, and deterministic verification workflows.
- **MCP Tool Surface & Anti-Stall Economy**: Stabilized the 31-tool catalog surface (29 advertised default tools + full-only specialized tools), per-session dedup ledger, advisory loop detection, and token-budget enforcement.
- **Telemetry, Health & Benchmarking**: Added regression benchmark gate (`kern bench --check`), validated offline doctor diagnostics, and verified optional-by-default local LLM (Ollama) integrations.
- **CI/CD & Release Pipeline Modernization**: Cleaned release and test matrices across Linux, macOS, and Windows runners, adding automated release artifact verification smoke suites.

- **[0.9.10.3]** — 2026-10-07: Streamlined default advertised MCP tool surface 22→6 (`kern_meta`, `kern_explore`, `kern_impact`, `kern_search`, `kern_verify`, `kern_buddy`) with full dynamic routing via `kern_meta` or `KERN_MCP_FULL=1`; fast/full tiers in `kern_verify`; test fixture demotion in fuzzy symbol search; nested sandbox execution and sandbox isolation hardening; single-sourced agent rules generation from `assets/AGENTS.md`; `kern debt` report; strict per-command flag validation; ETag conditional fetch protocol support.

- **[0.9.10.2]** — 2026-10-01: MCP surface economy (catalog 139→117 via the `kern_memory` + 8-family action-arg consolidations, default advertised surface 11→22), `kern_do` autonomous preset, and release-hardening fixes found by full-sweep verification (kern-server version footgun, meta `memories` routing, worktree-safe loop verify, typescript checker false-fails, `staleSnapshot` data race).

- **[0.9.10.1]** — 2026-09-30: Default tree-sitter indexer (14 languages), agent-adoption campaign (kern-first enforcement, meta-router coverage), compiled-intent task typing, global skill sync parity, session-aware LLM chain, and the 2026-09-29 blind-audit / dogfooding fixes.
- **[0.9.10]** — 2026-09-26: Semver release channel (Stages A–C), org governance P13 stages 1–3, SDK passthrough, governance hardening (RBAC default-deny + persistence), semcache compounding, `kern bench` + docs site, per-tool token attribution, and the audit campaigns' security fixes.
- **[0.9.9.1]** — 2026-09-19: Adaptive context windowing (`-A`/`-B`), configurable compaction profiles via `.kern/kern.yaml`, MCP server modularized into 44 subpackages, skill-playbook CI parity, and subprocess/hook security hardening.
- **[0.9.9]** — 2026-09-18: Repo-wide dead-code & duplication cleanup, standalone binaries removed, architecture guards enforced by default, major hot-path performance cuts, and ~40 correctness fixes to exit codes, MCP contracts, and boundary inference.
- **[0.9.8.2]** — 2026-09-14: Dynamic architecture guardrails fallback, multi-repo aggregate freshness in `kern doctor`, and fixes to `fit-context` budgets, MCP index hot-reload/watcher, and security hardening.

## Detailed changes

## [0.9.12] - 2026-10-25

### Added
- **`kern apply` & `kern_apply`**: Integrated atomic unified diff patch engine across CLI (`kern apply`) and MCP (`kern_apply`) layers, bringing the default advertised surface to 32 tools with full catalog parity.
- **Pure-Go PreToolUse Hook & Ask Protocol (`internal/guardhook`, `internal/guardrule`)**: Added `kern guard hook` for shell-independent hook execution. Interactive Ask Protocol allows human operators to grant one-time or prefix allowances (`kern guard grant`, `kern guard pause`) without full environment bypass.
- **Cold-Start Warmup & Index Preloading (`--preindex`)**: Introduced `--preindex` flag to warm and persist project indexes before serving MCP requests, along with automatic background preloading on MCP server start.
- **Architectural Tokenizer Leaf (`internal/tokenize`)**: Extracted identifier-segmentation and tokenization functions (`SplitIdentifierSegments`, `SplitCamel`, `WordRuns`, `NormalizeProseWord`) into `internal/tokenize`, preserving module size caps under strict architectural ledgers.
- **CI Composite Actions**: Reusable `.github/actions/go-env` and `.github/actions/release-assets` composites deduplicating Go environment setup and multi-target release packaging across GitHub Actions workflows.

### Changed
- **Guard Steer & 3-Tier Read Serving**: Content-intent commands (`cat`, `head`, `sed`, `awk`, `less`) are served transparently via the index, read-head stream, or isolated readonly sandbox rather than denied outright. In-tree file operations (`touch`, `mkdir`, `rm`, `mv`, `cp`, `sed -i`) are permitted and audited.
- **MCP Envelope Strictness & Error Handling**: Rejected pre-initialize requests with standard code `-32002`, rejected invalid path inputs (such as bare `~`), distinguished protocol-version headers, and aligned tools/list payload limits.
- **Index Engine & Overload Discrimination**: Digit-suffixed symbol search parity improvements, formal parameter tracking for method overloads, and container-aware receiver attribution for nested classes and methods in Java and Python.
- **Governance & Rejection Lifecycle**: Rejection workflow (`kern reject`) is human-gated with interactive TTY requirements matching approvals; allow-once registry transitions to cross-process file-locked serialization.

### Fixed
- **Language Detection & Parser Parity**: Addressed duplicate symbol collisions when extracting nested classes and method helpers in Python and Java.
- **Subprocess & HTTP Timeouts**: Increased MCP HTTP write timeouts past 30 minutes to prevent premature termination of long-running tool runs; threaded cancellation context through execution engines.
- **Secret Masking & Suppression Pinning**: Masked secrets in security scanning outputs before reporting; updated suppression line anchors and introduced function-scoped suppression matching to prevent fragile scan drift.

## [0.9.11] - 2026-10-21

### Breaking
- **Platform pivot (cluster cuts 1–6)**: the non-core planes are cut to refocus kern as a code-context engine: `internal/cockpit` + `kern ops`/`kernops` commands (cut 1), the web console + `kern-server` binary (cut 2), the enterprise org plane (cut 3), fragility/council/agenteval (cut 4a), dead CLI-backend packages `lsp`/`reviewpack`/`remove` (cut 6), and the remaining `kern-server` leftovers. All cut code is preserved in the private `kern_server` repo. Net: ~61k LOC out.
- **P4: the blueprint family is deleted** (19.7k LOC, 300+ files: `internal/blueprint`, `bpcli`, `bppolicy`, `bpreceipt`, `gates`, `resilience`, `scanners`, `mcp/blueprint`, `mcp/repair`). The change-governance suite lives on in the standalone blueprint repo; the drift gates survive as `internal/diffgate` + `internal/gitrun`; the global pre-commit hook now runs `kern verify --staged security,architecture`. The `kern check`/`kern fix`/`kern ci` commands died with the family — migrate to `kern verify --types security,architecture`. Thin blueprint bridge commands (`kern guard`/`sec`/`fingerprint`/`audit`) are restored as native kern implementations (P7).
- **P5 surface slim**: MCP catalog 114→31 tools (default advertised surface 29 + full-only `kern_arch`/`kern_graph` via `KERN_MCP_FULL=1`), CLI command table 194→60 (core surface only). `KERN_TOOLS` allowlists naming removed tools must migrate.

### Changed
- **Extraction campaign (2026-10-11)**: agent wiring moved out of `internal/setup` into a dedicated `internal/agentwire` leaf package; the graph/index/search CLI family moved out of `cmd/kern` into `internal/graphcli` (flags/etag/helper surface included); language parsing and AST types moved out of `internal/index` into `internal/langparse` + `internal/asttypes` (154 files, ~8.6k insertions) while preserving declaration identity and `nosqlite` builds; the new `internal/configstrip` package becomes the single shared JSON/TOML strip home for `setup --remove` and `agentwire` config. ARCHITECTURE.md ledger parity updated for the whole campaign.
- **Default MCP advertised surface 6→7**: `kern_exec` joins the default surface (D5); thin AGENTS.md rule text, plugin/shadow assets, and the shipped plugin hash registry updated to the 7-tool claim.
- **More de-monolithization**: `internal/mcp/catalog` → top-level `mcpcatalog`, `internal/mcp/meta` → `mcpmeta`, the approval/governance CLI family → `internal/approvalcli`, and duplicated CLI flag helpers → `internal/cliflags`; `cmd/kern` and `internal/mcp` size ratchets lowered (19600 / 19000).
- **Project `AGENTS.md` thin by default**: when the host global slot is kern-managed, project rule files install thin and single-source the full rules from the global slot (this repo dogfoods it).
- **Perf**: verification engine checks now run concurrently; repeated staleness checks on dirty trees are stat-gated.
- **Perf (post-pivot fix wave)**: index build streams bounded-channel jobs, tree-sitter parser pooling, search-regex compilation outside the cache lock, single-lstat walk, background Platform warm, shared `internal/filecache`, arch-cache reuse + single-flighted staleness probes, memoized LogStore legacy-shadow.
- **Anti-stall machinery**: per-session already-served dedup for read tools (`internal/servedledger`), advisory loop-suspect detection (`internal/mcp/loopdetect.go`), `kern_plan` context-sufficiency proof + deterministic first slice, per-phase tool-call budgets with explore-phase enforcement, in-band next-step hints (`internal/mcphints`).
- **Stats**: per-project tool-usage diagnostics (`kern stats --by-root`, `kern stats --by-tool --root`), buddy savings scoped by project, persisted cross-process semcache counters.
- **Perfection campaign 2026-10-20**: `kern bench --check` regression gate + `make bench-gate` (versioned thresholds); runtime telemetry sources validated (URL gate, env-only bearer tokens `KERN_PROMETHEUS_TOKEN`/`KERN_OTEL_TOKEN`, fetch-error surfacing, offline doctor); Ollama is optional-by-default with a provider-state doctor report; doctor classifies parent-index state (fresh/stale/empty/unreadable) in the index-shadow warning; ADR-0013 fixes the audit chain as append-only with a documented archival policy; MCP surface trio (31/29/full-only) pinned by test; `scripts/release-smoke.sh` post-packaging artifact verification wired into the release workflow + `make release-smoke`; `make help` target; pre-existing embedded-plugin-hash registry drift fixed.
- **CI repair**: every GitHub workflow referencing packages deleted in the pivots is fixed — `internal/blueprint`, `internal/bpcli/cli`, `internal/web`, and the `kern-server` binary are removed from ci.yml, test.yml, nightly-full.yml, release.yml, and the Homebrew formula; the release workflow gains the artifact smoke step.
- **Generated MCP documentation and surface parity:** `kern gen-docs --doc selection` now renders the tool-selection guide from the live catalog/default surface, Cursor skill rules gained deterministic parity coverage, historical records carry explicit notices, and current runtime/doc references to removed tools were cleaned.
- **Signed agent identities for org mode (`internal/govtoken`)**: org-mode
  agents can be enrolled with signed bearer tokens, so `agent_id` becomes a
  verifiable principal instead of a client-asserted label (minimal surface;
  RBAC default-deny pairing unchanged).
- **Governance: `kern approval clear` + bounded rejection stickiness**:
  operators can clear stale approval records, and a rejected record's
  byte-identical retry block is now bounded rather than permanent.
- **Setup: backup-then-refresh for customized plugin copies**: a global
  plugin copy that differs from the embedded asset is backed up before it is
  refreshed, so hand-edits are never silently clobbered.
- **Index fixes**: anonymous-arrow functions no longer produce phantom
  symbols; CSS re-selection (multi-selector rule) duplicates are deduped;
  the call-resolution metric now splits external (stdlib/vendor) from
  in-repo unresolved callees.
- **Guard**: the regex-escape advice is self-consistent across every deny
  message.
- **Dogfood-close batch (F1–F16)**: plugin CLI mirrors fixed (memory list,
  fit-context flags), extractor scoping for nested defs, audit-chain
  degradation discrimination (ChainTiers surfaced by `kern audit`), evidence
  output cap.
- **Perf**: cut fixed per-call MCP serve-path overhead; shared path-keyed
  `internal/filecache` for repeated file reads (doc index decode, arch
  cache); staleness probes single-flighted; background Platform warm after
  index build; LogStore legacy-shadow absence memoized.
- **Security**: `kern sec` applies the suppression triage end-to-end;
  refactor symlink-escape closed; exec retention bounded; cache-search
  mirrors deduped.
- **MCP protocol**: oversize HTTP bodies rejected with 413 (was a misleading
  parse error); string-args violations pinned as tool-error results, not
  `-32602`; `kern semcache stats` now surfaces persisted cross-process
  hit/miss/saved/eviction counters (a fresh process no longer reports zeros).
- **Benchmarks**: measured multi-repo latency report published
  (`docs/benchmarks/multi-repo-latency.md`).

### Added
- **Native CGO release matrix**: releases ship tree-sitter `-full` assets across the native target matrix, with installers preferring `-full` and falling back to pure-Go; UPX compression and `version` smoke now cover kern-mcp in addition to kern; the `release` job is gated to tag pushes (`workflow_dispatch` dry-runs the matrix without publishing); the Homebrew formula retargets `${{ github.repository }}` with accurate tap-publish wording.
- **Machine-wide install, visual setup, `kern setup --remove`**: installers gain machine-wide install; `kern setup` gains a visual TUI and `--remove`; `kern doctor` reports machine-wide install health; CI install-smoke covers linux/macos/windows. (`kern try` — added in this window, then cut by the platform pivot — does NOT ship in this release.)
- **Agent-level evals**: `internal/agenteval` phase 1 (deterministic with/without-kern suite) and phase 2 (real-LLM A/B harness with model pinning for published runs), plus new rename-scoped/add-param agentic tasks with reference solutions.
- **Search upgrades**: path-fragment ranking and file-prefix boost, 16KiB per-tool search budget, FTS on default builds, a read-only verify allowlist, and `kern stats --net`.
- **Callee-filtering extraction (`internal/calleefilter`)**: builtin and builder-chain callees filtered, endpoint routes populated, test symbols excluded from symbol surfaces.
- **Guard & hooks**: fail-open guard mode, global pre-push protection and chained repo hooks, harmless-git inspection passthrough, uniform fail-closed content-intent bash deny on opencode, operator allow-once for guarded subjects, and a network-fetch danger class gating plain `curl`/`wget`/`fetch`.
- **Index hardening**: refusal of broad auto-index roots with bounded first-index waits, stat-gated staleness checks, and index pre-warming when the opencode plugin loads.
- **Plugin UX**: specific guard deny messages naming the command and passthrough set, plugin captures routed via `kern remember --auto` (with dedup), `chat.message` noise filter, `--no-memory` on `kern_buddy`, and `kern memory prune --auto`; `@opencode-ai/plugin` bumped to 1.18.35.
- **Audit transparency**: dropped audit entries are surfaced (counter + sidecar + `kern doctor` check).
- **`kern_exec` output contract**: kern_verify's output contract for `kern_exec` plus post-hoc dump steering.
- **Docs/dev-experience**: architecture tour + scoped test target + good-first-issues (contributing), a per-language index accuracy page, doc tool counts derived from the catalog, and TypeScript SDK npm publish metadata (`files`/license/repository/keywords/`access: public`).

### Fixed
- **Semcache LLM-pinning flake**: semcache tests pin the LLM off — the root cause of the recurring TempDir cleanup flakes.
- **`go` telemetry writer race**: the sandbox confinement test's TempDir cleanup no longer races the telemetry writer.
- **Audit-chain integrity**: the audit-chain format downgrade path is closed.
- **Index identity fixes**: nested same-named defs in different methods no longer collapse into one identity and block the whole index; Java overload/anonymous/nested-class identities distinguished in the duplicate guard; index format v14 bump + merge-boundary noise filter purges stale derived data.
- **Memory store fixes**: cross-process file locks serialize store mutations; explicit writes are signal-driven genuine (never prefix-classified); deduped `kern remember` writes ack truthfully across CLI/MCP surfaces.
- **Callee-filter correctness**: language-aware chain rule + file-only test classification + Go 1.21 builtins; `testing.T`-method callees excluded as view noise.
- **`kern verify --command`**: the resolved executable is classified explicitly (closing the `-d`/`make`/`pip` channels); refusals list allowed commands rather than detected toolchains; governance refusals exit 3 (denial contract).
- **`kern brief`**: dominant-language endpoints ranked first, cap disclosed.
- **Plugin output fidelity**: kern read-only view outputs are never lossy-compressed.
- **`kern stats`**: per-session rules ledger + loud JSONL scanner errors.
- **twin endpoint extraction**: test files and comment lines excluded; Go 1.22 method-path routes parsed into proper api entities.
- **MCP server shutdown**: `Close` drains a running background preload build instead of abandoning it mid-index.
- **CLI/usage fixes**: `kern ast --all` searches per-project `.kern/` indexes read-only; `verify --skill` fails loudly on skill-less dirs; `tools/call` with an empty name errors cleanly; `provenance.symbols` capped at 128 entries; verify command-mode/request-approval flags documented in usage; the managed global-rules block is content-verified (not just markers); the audit repair subcommand is documented.
- **Doc-drift catch-up**: stale README dependency/tree-sitter/benchmark claims corrected; the 6→7 tool-surface drift closed across README/tool-selection/protocol; `KERN_MCP_FULL=1` documented as a single unfiltered `tools/list` response (retain/cursor paginates tool *output*, not the list itself); the four post-D5/D4 pin drifts closed.

### Security
- **HTTP MCP bearer auth documented**: `KERN_MCP_AUTH_TOKEN` coverage added to SECURITY.md (and the stale release-build tree-sitter claim corrected).
- **Guard posture**: uniform fail-closed content-intent bash deny on opencode (operator directive) and the network-fetch danger class ship as part of the default guard posture.

## [0.9.10.3] - 2026-10-07

### Changed
- **Default MCP Advertised Surface 22→6**: Reduced default tool list to 6 high-leverage entry points (`kern_meta`, `kern_explore`, `kern_impact`, `kern_search`, `kern_verify`, `kern_buddy`), routing all 117 tools dynamically through `kern_meta` or opt-in via `KERN_MCP_FULL=1` / `HIGH_LEVEL_ONLY=36`.
- **Single-Source Agent Rules**: Agent rules and setup blocks are single-sourced from `assets/AGENTS.md` and derived dynamically, ensuring byte-level parity across agent installations.
- **Strict CLI Flag Validation**: Unknown flags now exit with code 2 across all subcommands, providing actionable help and did-you-mean suggestions.

### Added
- **Fast / Full Verification Tiers**: `kern_verify` supports changed-scope fast tier (running only affected packages) or complete-suite opt-in, sharing `verification.ChangedTestPackages` with the CLI.
- **`kern debt` Command**: Unified technical debt reporting composed across lint, test coverage, dead code, and duplication metrics.
- **ETag Conditional Fetch**: `kern_explore`, `kern_context`, `kern_compact_file`, and `kern_retrieve` support conditional fetch via ETags (`unchanged (etag ...)`).
- **Opt-in `kern-minimal-change` Skill**: Procedural minimal-change ladder and restraint overlay for changes.

### Fixed
- **Symbol Resolution Precision**: `ResolveFuzzy` and ranked search demote test/testdata fixtures as fallback, preventing test symbols from hijacking bare-name lookups.
- **Caller Attribution Parity**: Fixed 9 impact-to-explore caller attribution gaps via raw-edge graph traversal.
- **Interface Dispatch Detection**: `kern whatif` surfaces interface dispatch blind spots on method targets.
- **Nested Sandbox Inheritance**: Inherited runs inside sandboxes skip duplicate isolation wraps, ensuring deterministic test execution.
- **Governance Audit Lock**: Added bounded timeout on governance audit locks, preventing hung approval waits.

## [0.9.10.2] - 2026-10-01

### Changed
- **MCP family consolidation (BREAKING)**: 26 narrow tools merged into 8 action-arg tools — `kern_optimize` (action: prompt|log|output), `kern_doc` (search|fetch|index), `kern_agent` (message|interrupt|fingerprint|coordination|rbac), `kern_org` (entity: projects|agents|teams|memory|tasks|search|audit|user), `kern_lock` (acquire|release|status — absorbs `kern_unlock`/`kern_lock_status`), `kern_evidence` (+anchor action), `kern_repair` (guidance|diagnostics), `kern_semantic` (diff|merge). Catalog 137→117; default surface stays 22 (`kern_optimize_prompt`→`kern_optimize`, `kern_repair_diagnostics`→`kern_repair` renames). `KERN_TOOLS` allowlists naming removed tools must migrate. Deliberate hard removal following the `kern_memory` precedent; breaking changes documented in `docs/mcp/versioning.md`.
- **MCP memory tools consolidated (BREAKING)**: `kern_memory_add` / `kern_memory_list` / `kern_memory_recall` / `kern_memory_ranked` are removed and replaced by a single `kern_memory` tool taking an `action` argument (`add|list|recall|ranked|remove|clear`), mirroring the CLI's `kern memory` subcommands. Catalog 139→137 tools. `KERN_TOOLS` allowlists naming the old tools must migrate to `kern_memory` with `action`. Deliberate hard removal documented in `docs/mcp/versioning.md`; generated docs (tool-catalog, tool-contracts, tool-schemas.json baseline) regenerated.
- **Default MCP advertised surface 11→22**: the default `tools/list` now includes the evidence-backed top savers and the previously missing edit-phase tools — `kern_compact_file`, `kern_project_map`, `kern_probe`, `kern_memory`, `kern_buddy`, `kern_heal`, `kern_repair`, `kern_commitmsg`, `kern_synthesize_test`, `kern_retrieve`, `kern_fit_context` join the 11 incumbents. Unchanged: `KERN_MCP_FULL=1` (all 117), `KERN_MCP_PHASE`, `KERN_TOOLS` resizing.
- **RBAC**: the consolidated `kern_memory` is excluded from the reviewer role's allow-list (it can write via `add`/`remove`/`clear`); the architect glob `kern_memory*` covers the new name unchanged.
- **Closed-loop verify scoped to changed packages**: the loop's test leg now runs only the Go packages its own diff touched (derived from the worktree diff) — a one-file task pays seconds instead of the whole-module suite (~10 min on this repo). Read-only loops keep the full informational advisory; dependent-package regressions remain CI's full tier's job. `verification.Engine.WithTestPackages` is the new scoping API; the `KERN_VERIFY_TEST` override still wins verbatim.
- **`kern mutate` never touches the real tree**: mutants are evaluated inside a worktree copy (both the CLI and `kern_mutation_test`); the real tree is no longer transiently modified during each mutant's test run. Falls back to the journaled in-place mode with a loud warning if the snapshot cannot be built. `internal/mutation` gained its first internal dep (`internal/execution`) — ledger updated.

### Added
- **`kern_do` MCP tool**: the autonomous closed-loop preset (`kern do`'s contract) is back on the MCP surface — a thin delegate over `kern_loop` with mode fixed to `autonomous` (L2 default, level overridable), risk high, Slow-flagged so it emits progress notifications.
- **`kern_memory` remove/clear actions**: targeted lesson deletion by 1-based list index or text prefix, plus whole-store clear — previously CLI-only (`kern memory remove` / `--clear`).
- **Stage-aware MCP progress for loop-family tools**: `kern_loop`/`kern_do` now report each stage as it starts ("stage: verify", with cumulative pct across the 9-stage chain) on top of the generic 5s keep-alive — a 10-minute run no longer looks identical to a hung one. The reporter travels on the request context, so no intermediate signature changed.

### Fixed
- **`kern-server version` footgun**: `kern-server version` (positional) and `-v` now print the version and exit 0 instead of silently starting the HTTP server — parity with the kern-mcp fix (76031df).
- **kern_meta classifier**: plural `memories` phrasings ("what memories do we have about X") now route to `kern_memory` recall — previously fell through to `kern_search`.
- **Closed-loop verify was worktree-unsafe**: the loop's verify stage runs `go test` in sandbox worktrees that exclude `.git` by design; `TestCheckParityReleaseTagAccepted` (internal/doctor) hard-assumed a git checkout, making every L2+ loop run report `test: FAIL` (and burning heal repair rounds) on healthy repos. The test now skips outside git checkouts (verified: full short suite passes in a git-less worktree).
- **Typescript syntax checker false-fail**: `node --check` parses `.ts` as CommonJS JavaScript (the `--check` path bypasses type stripping), so every file using real TypeScript syntax failed validation — feeding false failures to `kern validate`, heal, and the loop's verify. `.ts`/`.tsx` now validate as "unable to validate" per the checker-less convention instead of false-failing healthy code.
- **`staleSnapshot` data race (internal/index)**: `web.freshGraph` deliberately runs staleness walks off the app lock, so concurrent requests could race `retainStaleSnapshot`'s pointer write — caught by the CI race tier (`TestFreshGraphSingleFlight`). The retained observation is now guarded by a dedicated mutex (retain/clear/read); published snapshots stay immutable.
- **kern_meta router keyword audit**: 17 routing gaps of the `memories` bug class (plurals/synonyms/phrase variants) closed and pinned by a 33-case table test — including two DEFAULT-SURFACE tools that had no keyword arm at all (`kern_heal`, `kern_synthesize_test` — reachable only by literal name before), the `compile`/`compilation`/`build error` variants the repair arm missed, and intent-before-generic ordering so "repair the build" no longer routes to the plain validate re-check.

## [0.9.10.1] - 2026-09-30

### Changed
- **Tree-sitter is now the DEFAULT build** (requires CGO): the symbol index compiles 13 tree-sitter grammars in by default for higher-fidelity AST extraction (14 languages, typescript covering `.ts`+`.tsx`). The pure-Go regex extractor remains as the opt-out — build with `-tags notreesitter` (or `-tags sqlite,notreesitter` for cross-compiles). Earlier changelog entries describing tree-sitter as "opt-in via `-tags treesitter`" describe those releases' state and are superseded from here on.
- **Agent-adoption campaign (kern-first enforcement)**: thin wiring-only `AGENTS.md` as the default mode, full `kern_meta` router coverage across all 139 catalog tools, operator-gated bypass (`KERN_BYPASS=1`), slimmed plugin tool descriptions under a token-budget gate, and byte-identical kern skill copies across 5+ agent locations.
- **Task-type derivation from compiled intent**: workflow, incident, and ops tasks now persist their intent-derived type (`code`/`incident`/`modernize`) at creation (persisted store + bus event), instead of a hardcoded `analyze`.
- **Global skill sync now covers `~/.opencode/skills` and `~/.agents/skills`** (opencode's actual load dirs) — previously unmanaged, stale copies there silently shadowed fresh ones at agent init; a new `TestGlobalSkillCopiesParity` gate catches drift.
### Added
- **Session/model-aware LLM chain**: host sampler (MCP sampling ack) first — the connected session's own model answers without spawning a new session — then agent CLIs (`claude` → `opencode` → `codex`) before ollama; per-leg probe budgets (15s host / 45s CLI / 10s ollama) via the shared `ProbeReachable`; `kern do` prints which provider answered.
- **`KERN_PROBE_TIMEOUT`** env (Go duration, default 60s) overrides the `kern agents --probe` per-provider bound.
- **`kern verify <unknown-positional>`** did-you-mean hint naming valid check types.
### Fixed
- `kern health` disk-index block reported `stale:true/verdict:unknown` on bare invocation (root defaulted via `projectRoot`).
- Copied/moved repos served a sticky stale index ("will rebuild" never fired) — `Load` re-points `ix.Root` (rebuild now triggers).
- `kern execute` dumped ~1,100 `.git`-internals lines when run from inside a repo — git-aside placed in the absolute parent dir (now ~15 lines).
- `kern optimize` silently no-op'd without `--llm` — LLM stage defaults to the auto chain; masking via canonical `MaskRequired`.
- `synthesize-test --apply` refusal exited 0 — now exits 3.
- `kern exec` undetectable-language exited 1 — now exits 2 (usage error).
- `kern_compact_file` returned empty "success" for non-indexed files — explicit not-indexable note; `kern_search` numeric `root` rejected at MCP validation.
- `kern index --status --json` zeroed every `time.Time` timestamp (`clipJSONValue` now passes `json.Marshaler` values through).
- `kern bench` rendered sub-ms latencies as `0.00 ms` — now µs/ns tiers.
- `kern team` reported `tasks: 0` (in-memory registry only) — merges the persisted store; `kern task` showed `type: analyze` for workflow tasks — derived from compiled intent at creation.
- `kern agents --probe` help text and bound updated to 60s + env override.

## [0.9.10] - 2026-09-26

### Added
- **Semver release channel (Stages A–C)**: downgrade-proof `kern update` with a dry-run decision preview (`kern update --dry-run`), and `KERN_CHANNEL` release selection — `stable` = newest 3-component tag, `latest` = newest tag incl. hotfixes, any other value = regex over release tags; an explicit `--pin`/`KERN_VERSION` always wins. Dev builds keep commit-hash versions until tagged.
- **Org governance (P13 stages 1–3)**: central policy distribution (`KERN_ORG_ROOT`), org-scope RBAC with org-wins resolution, and org-wide single-use approvals (`/org/approvals`; deploy gates consume them). Enterprise mode now REQUIRES `KERN_RBAC_DEFAULT_DENY=1` to start (pairing gate): `agent_id` is a client-asserted label, not proof of identity, so org mode without default-deny falls back to the legacy permit-all trust.
- **SDK passthrough**: the full MCP tool catalog through one REST route — `POST /v1/tools/{name}` delegates to the same governed in-process dispatch path MCP clients hit (KERN_TOOLS allowlist, root confinement, RBAC), returning `{"output": <raw tool text>}`; `internal/sdk` gains an in-process full-catalog `Call` + discovery helpers, and every web App gets a root-bound per-project tool-server factory.
- **Governance hardening**: RBAC opt-in default-deny (`KERN_RBAC_DEFAULT_DENY=1`), persisted RBAC assignments via the atomic identity-store pattern, audit aliasing/ID fixes, firewall race fix, permission hardening — and the reviewer role now gets the full read-only default MCP surface.
- **Semcache compounding**: hit/miss accounting, warm-on-build + compress integration, LRU-touch eviction, stale sweep, and profile-scoped (slugified) namespaces.
- **Benchmarks + docs site**: `kern bench` (deterministic, zero-network, writes `.kern/bench.json`), a web console `/benchmarks` page rendering the same schema, and the `docs/index.md` site index.
- **Per-tool token attribution**: `kern stats` records per-tool/operation token usage.
- **Semcache prompt-namespace coverage**: `kern optimize`/`preview` `--cache` now defaults ON (masked previews + results compound locally unless `--cache=false`), and the LLM path uses model-scoped `prompt.llm.<model>` semantic namespaces with a stricter 0.8 threshold — a fuzzy hit can never ship a different model's answer.
- **Cost-model unification**: the two divergent cost tables merged on one canonical resolver (longest-prefix + `KERN_MODEL_COSTS`/`cost_per_token` overrides + labeled flat assumed default); versioned model names now price correctly and per-entry savings respect operator overrides.
- **Org-tool RBAC**: every `kern_org_*` tool now enforces per-action authorization (mutations → org-admin, reads → org-member), extending the `kern_org_user` precedent to projects/agents/teams/memory/tasks/search/audit; new actions fail closed until explicitly granted.
- **Cold-start scale benchmarks**: `docs/benchmarks/cold-start.md` — same-tree protocol across a real-repo ladder (83→7,819 files) answering the "slower than grep" question honestly (grep wins below ~200 files; kern cold-load wins 3–7× above it).
- **SQLite-primary index persistence**: three storage formats merged into one (`.kern` footprint halved; SQLite WAL + FTS5 is the default build).
- **Sandbox filesystem read confinement** (macOS stage 1) plus an exec mask-order fix.
- **P2 batches**: UDS transport default, deterministic test waits, review-orchestration extraction, perf caches, coverage + fuzz gates, doc-drift gates, test parallelism, eventbus retention, root grammar, web console UX.

### Fixed
- **8-repo real-world validation campaign**: ~40 F-series fixes — exit-code contract pinning for every findings-producing command, snake_case JSON contracts (`doctor`, `impact`, `context`, `stats`, web governance metrics), exact-name search ranking, verb-first positional/verb-shaped-symbol resolution, heal fail-fast on dead LLM providers, `mutate --min-score` CI gate, diff-gate untracked-file inclusion + `[BLOCK]` summary, setup plugin-copy clobber, sandbox network-isolation enforcement, corrupt-index self-heal, review empty-path JSON, commitmsg subject accuracy, prose compression fillers, near/retrieve flag validation.
- **RBAC multi-root**: assign no longer swaps in-memory roles across roots; persisted roles activate in-process and the store reloads under a mutex.
- **Audit-campaign security fixes**: semcache profile-slug path-escape sanitization, the org-mode RBAC pairing gate, root-bound tool servers (per-App factories), typed REST error sentinels (`domain.ErrToolDenied`/`ErrToolUnknown`), web console auth + discovery, and ERE docs for the new environment variables.
- **Cost-table accuracy**: `o3-mini` no longer prefix-matches `o3` at $10/1M (correct $1.10); unknown/empty-model entries accrue at the labeled flat assumed rate instead of silently $0.
- **Staleness note wording**: the inline STALE note no longer promises an immediate retry is fresh — it says "retry in a moment… (the rebuild is asynchronous)".

### Removed
- **The governed decision-record ("kern note") system**: `docs/notes/` tree, the `kern note` CLI, the `kern_note` MCP tool, the `internal/note` package, gates G37 (note:format) + G38 (note:missing), and the diffgate notegate check — deleted whole. The MCP catalog is now 139 tools; no decision notes are created for repo changes going forward.

## [0.9.9.1] - 2026-09-19

### Added
- **Adaptive Context Windowing**: Added grep-like `-A` / `-B` (`--context-before` / `--context-after`) contextual line preservation to CLI `kern optimize`, MCP tools (`kern_optimize_prompt`, `kern_optimize_log`), and Python SDK (`local_compress`), retaining essential context surrounding matched log traces and code snippets.
- **Dynamic Configuration & Soft-Truncation DSL (`internal/kernconfig`)**: Introduced declarative `.kern/kern.yaml` (and `.kern.yaml`) project profiles for configurable file extensions, custom compaction thresholds, and non-destructive soft-truncation markers without binary recompilation.
- **Agent Skill Playbook CI Parity (`internal/skills/parity_test.go`)**: Added automated CI regression suites and pre-commit checks enforcing 100% byte-for-byte synchronization across all 16 `SKILL.md` playbooks and helper scripts in `.agents/`, `.github/`, `.opencode/`, and `internal/skills/assets/`.
- **Full MCP Leaf Subpackage Modularization**: Decomposed monolithic MCP server into 44 independent subpackages under `internal/mcp/*` with 100% backward compatibility across all 140 tools and zero architectural ledger drift.
- **Enterprise Telemetry & Multi-Repo Graph Contracts**: Enhanced `internal/mcp/crossrepo` with structured JSON output and expanded `internal/mcp/runtime` for production incident crash correlation against AST symbols.
- **Worktree Acceleration & Non-Interactive Safety**: Accelerated change validation in `internal/execution/worktree.go` via direct `git -C diff HEAD` fast-paths and added `--non-interactive` automation flags to skill triage runbooks.

### Fixed
- **Subprocess Security Hardening**: Remediated Python SDK subprocess execution by enforcing explicit `shell=False` and upgraded AST security scanner (`internal/sec/python.go`) to eliminate false-positive command-injection flags on array invocations.
- **Multi-Scope Agent Hook Detection**: Fixed `internal/setup` hook resolution to detect both workspace-local and user-home global configurations across Gemini, Claude, Cursor, and OpenCode environments.
- **MCP Token Formatting**: Aligned `internal/mcp/optimize` token savings calculation formatting with standardized MCP response schemas.

## [0.9.9] - 2026-09-18
### Added
- **Repo-wide dead-code & duplication cleanup**: removed three packages with zero live importers — `internal/merge3` (~1000 LOC, orphaned superset fork of `internal/diff`'s merge; `kern semantic-merge` routes to `diff.SemanticMerge3Way`), `internal/cicd` (180 LOC, never imported by any Go file), and `internal/blueprint/watcher` + the orphaned blueprint `watch` command chain (`RunWatch`/`runWatch`/watch.go lost its last dispatcher when `cmd/blueprint` was removed; G10 "Watcher robustness" gate retired, registry shape now 39 gates with G10 as the single documented gap). `internal/ci` was audited as dead but is LIVE (`internal/verification/engine.go` imports it) — kept. Also removed 30 verified-dead symbols (~410 LOC) across ~20 packages — `TaskService.VerifyTask`, dead `internal/service` methods (`svc.Index`/`svc.Graph` never consumed), `WorkflowEngine.RejectApproval`, `SQLiteStore.LookupInheritedBy`, `Toolchain`, `AgentResult.MarshalJSON`, `AuditLog.FilterByTask`, `MerkleTree.Leaves`, `DocumentedByEdges`, `Session.BridgesList`/`HubsList`, `Tier.String`, `ComputeFreshness`, `SafetyBudget.TrackFile`/`TrackTokens`, `Map.OwnersByFile`, `Pool.CloseAll`, `RepoNames`, `SemanticSearchRepos`, `ReviewWithRuntime`, `ValidateDiffWithIndex`, `FirewallAdapter.Validator`, `GetPriorities`, `Recorder.WhatTested`/`WhoApproved`, `RoutingContext.RouteTeam`, `ServeHTTPWithTLS`. Option-builder `With*` APIs and documented setters (`SetCostPerToken`) kept intentionally.
- **Duplication extractions**: new `internal/fsutil.WriteFileAtomic(path, data, perm)` consolidates 11 near-identical atomic-save sites (agent, app, memory, incident, calibrate, sandbox, blueprint metrics/duplication/adapters); `resolveRoot` helper in cmd/kern collapses the 9× repeated root+index preamble in cmd_graph.go; `stats`/`diff`/`export` dispatch closures deduped into `runStatsEntry`; blueprint CLI staged-changes discovery shared between `check.go` and `diffgate.go`; `buildValidationService` extracted in blueprint/mcp handlers; shared POST path in `internal/llm`; missing one-line help texts added for `build`/`prompt`/`team`/`testgaps` and the 14 MCP-mirror command pairs now document the canonical underscore key. CI drops the `./internal/cicd/...` test pattern.
- **Standalone `blueprint`/`blueprint-mcp` binaries removed**: `cmd/blueprint` and `cmd/blueprint-mcp` (legacy shims forwarding to `internal/blueprint/cli` / the merged MCP tools) are deleted. Their 23-file gate test suite moved into `internal/blueprint/cli` (black-box binary tests now drive the merged `kern` binary or an in-process `mcp.NewServer`), the gate registry `TestFile` paths were updated, and CI no longer builds or tests the standalone entry points. The change firewall remains fully available as `kern check/fix/ci/sec` and the `kern_*` MCP tools.
- **Standalone `kernops` binary removed**: `cmd/kernops` (a thin `RunOpsCLI` wrapper) is deleted — the ops CLI remains fully available as `kern ops` (alias `kern kernops`) backed by `internal/cockpit`. Its binary smoke test moved into `internal/cockpit` as an in-process `RunOpsCLI` test, and CI no longer builds the standalone binary.
- **Inferred Boundaries Enforced in `kern verify`**: the architecture check now falls back to `intel.InferBoundaries` when `.kern/boundaries.json` is missing (matching `kern guard`/`kern impact`), so the guard is enforced by default instead of skipping 1,436 files; the inference is surfaced as an advisory warning and inferred violations fail the check.
- **Java/Maven-Aware Boundary Inference**: `InferBoundaries` no longer classifies `*-api` Maven modules (contract interfaces services implement) as a presentation layer, classifies a dir's layer by its own name rather than ancestor segments, and drops rules between nested same-module layer dirs. Dogfood finding: slice-adaptors went from 197 spurious service→api violations to 0 while cross-module and non-Java enforcement is unchanged.
- **Boundary Check Skips `testdata/` Fixtures**: `intel.CheckBoundariesPrecise` ignores files under `testdata/` (fixture/demo code, not production source), eliminating false architecture findings on fixture directories.
- **Bare-Name Call-Edge Collision Guard**: `CheckBoundariesPrecise` no longer attributes call edges of same-named package-level functions (e.g. every package's `New`) across packages — the index merges such edges under bare FullName keys, which previously fabricated cross-package boundary violations.
- **Bare-Name Callee Collision Guard (mirror)**: `resolveCallee` now resolves a bare callee to the caller's own file when that file defines the name, and drops the edge entirely when the name is ambiguous (defined in several files, none of them the caller's) instead of attributing it to an arbitrary first-seen file. Dogfood finding: TradingApp `nse_service.py -> api/stocks.py "get_quote"` was a fabricated reversal — the API actually calls the service's `get_quote`.
- **Unix Socket Path Length Guard**: `ServeHTTPContext` rejects `unix:` socket paths longer than 100 bytes with a clear error instead of the OS's silent `invalid argument` (macOS `sun_path` limit is 104 bytes).
- **Entry-Binary Smoke Tests**: `cmd/kern-mcp` (version flags, stdio initialize round-trip, HTTP /health + SIGTERM exit 0) and `cmd/kern-server` (version flag, /api/health + graceful shutdown) are now covered at the binary level.
- **ARCHITECTURE.md Ledger Completeness**: 9 previously unlisted subsystems (`fit`, `fragility`, `integration`, `lspbridge`, `merge3`, `mutation`, `refactor`, `repair`, `testfixture`) added to the parity table with measured baselines and caps.
### Fixed
- **Repo-wide modernization sweep**: ~73 production `os.IsNotExist`/`os.IsExist` call sites converted to `errors.Is(err, fs.ErrNotExist)` / `errors.Is(err, os.ErrExist)` across 37+ files (blueprint, storage, lock, cache, host, governance, mcp, mcpclient, cmd/kern, sandbox, semcache, docbudget, testfixture, metrics, note, memory, evidence, setup, ownership, architecture, loop, doctor, intel) so wrapped errors are detected correctly; 9 `fmt.Errorf` `%v` error chains switched to `%w`; 5 untyped-atomic sites converted to `atomic.Int64`/`atomic.Uint64` (index work-queue claim, audit sequence CAS, intel guard events, eventbus IDs), removing the load/add race-window footgun in parallel index sharding; ~37 hand-rolled helper sites replaced with stdlib idioms (`slices.Contains`, `slices.Compact`, `slices.Sorted(maps.Keys)`, builtin `min`/`max`); 88 `interface{}` → `any` sites. Order-preserving dedupe helpers with documented "keep first occurrence" contracts (twin, jscpd) intentionally left as-is.
- **Regex compilation hoisted off hot paths**: 17 per-call/per-symbol `regexp.Compile` sites hoisted to package-level vars (verify/draft.go ×7, index/sqlite_store FTS escape ×3, mcp handlers_graph entry-point pattern, whatif/parse ×5, cockpit triage, index/entries identifier); `pii.maskCustom` now caches compiled per-name patterns in a shared `sync.Map` (new `TestMaskCustomNameCache`).
- **Search hot-path allocation cuts**: `intel.RankedSearchScored` precomputes lowercased name/full/file once per call instead of 3× `strings.ToLower` + `FullName()` concat per symbol per query (persisted index format untouched); `SemanticSearch` pre-filters already-cached descriptors before spawning embed goroutines and caps embed concurrency at 4 (was 8); `symbolEmbedCache` eviction is FIFO instead of random single-key delete (`TestSemanticSearchDescriptCacheBounded` updated to actually exercise eviction).
- **Task-failure transition no longer misreported**: `TaskService.fail` was swallowing `t.Fail`'s transition error and then persisting/publishing `task.failed` as if the task went FAILED; a failed transition is now logged loudly and does not publish the misleading event or persist FAILED (`TestTaskServiceFailPublishesEvent`, `TestTaskServiceFailNoEventWhenTransitionFails`).
- **Sandbox byte-size overflow guard**: `parseByteSize` could wrap silently for huge `KERN_SANDBOX_MAX_SNAPSHOT_BYTES` values (MaxInt64 × 1GiB), yielding a bogus snapshot cap; it now errors loudly (`TestParseByteSizeOverflow`).
- **Code-standards cleanup**: gofmt on 7 files (CI fmt gate green again), builtin shadow renames (`cap`/`max` in llm, mcp, cmd, compress, diff, memory, calibrate), vestigial `_ = id` removed (governance/approval), golint ST1005 error strings lowered (mcp handlers_security/handlers_graph), 16 exported decls gained doc comments (intel, mcp, index), trailing whitespace stripped from 5 test files, `kern memory` now honors `--root`; `internal/blueprint/checks/duplication/fixtures.go` moved to `fixtures_test.go` so test-only code no longer ships in the production package.
- **Stopword-Colliding Symbol Resolution**: symbols whose names collide with English/change verbs (`Add`, `Get`, `Run`, `Set`, `Fix`, …) were invisible to natural-language extraction — `kern probe "Add returns wrong result"` returned 0 anchors and `kern plan`/`kern orchestrate`/`kern what-if` failed with `symbol "fix the Add function" not found in graph`. The index is now consulted FIRST (case-sensitive exact resolution) before stopword filtering, so real symbols always win; neither stoplist was pruned and index-less extraction behavior is unchanged (`internal/intel/probe.go`, `internal/whatif/parse.go`, `internal/context/engine.go`, `internal/app/platform.go`).
- **`kern commit --all --dry-run` no longer mutates the git index**: the dry-run preview previously ran `git add -A` before checking the flag; it now renders the would-be commit read-only (staged diff, or `git diff HEAD` + per-untracked-file no-index diff for `--all`).
- **`kern synthesize-test` generated non-compiling tests** for functions with a `name`/`want*` parameter (duplicate struct fields and literal keys); colliding columns are renamed to `arg<Name>`.
- **`kern entries` missed root Go packages** whose package name was clobbered by a fallback-named package from non-Go files in the same directory; the Go package clause now wins during merge.
- **`kern mcp-client call` sent no `MCP-Protocol-Version` header** on streamable-http/unix transports, getting HTTP 412 from spec-compliant servers (including kern itself); both transports now send `MCP-Protocol-Version: 2025-03-26`.
- **Blueprint MCP tools `kern_validate_proposed`/`kern_explain_finding`/`kern_repair_guidance` were uncallable**: the catalog declares `files`/`finding` as JSON-encoded strings but the handlers unmarshaled typed structs; the bridge now decodes JSON-string payloads (loud, self-describing error on malformed JSON).
- **MCP error responses dropped the handler payload**: `toolCallResponse` replaced the text with `err.Error()` on error, so `kern_authorize_context`'s auditable denial proof never reached the client; errors now deliver `err + "\n" + payload` when a payload exists.
- **Refactor path confinement (MCP + engine)**: empty `edits[].path` surfaced as a confusing "write .: is a directory" error; `../` escapes and absolute paths outside the project root could write outside it. Both the MCP handler and `internal/refactor` transaction engine now validate (fail-closed) and normalize absolute-inside-root paths.
- **`kern_retrieve` L2/L3 results never rendered their registry handles**, making `kern_resolve` undrivable for non-L1 results; an 8-char handle line is now appended.
- **`kern verify-receipt` accepts the full `<id>.json` filename** (was "Receipt not found"; only the bare id resolved).
- **`kern diff-gate`/`fix` printed "0 findings" for a check carrying an Error**; the per-check printer now shows the actual error.
- **`kern register-host-sampler <cmd> --key K` silently dropped flags after the positional**; args are re-parsed after the command.
- **README dependency claim corrected**: the default build links SQLite (WAL+FTS5) and yaml by default, so the "Dependencies: Zero (stdlib_only)" badge/tagline was false; now "Dependencies: Minimal (stdlib+sqlite+yaml)" with an accurate storage description (tree-sitter remains opt-in via `-tags treesitter`).
- **Orphan concept docs removed** (`docs/review-lenses.md`, `docs/consensus-divergence.md` — zero references in code/CI/README; only listed in the doc budget, updated accordingly).
- **`kern workflow` task-state failures now exit non-zero**: an invalid task transition (e.g. resuming a COMPLETED or pre-approval WAITING_FOR_APPROVAL task) printed "error: …" in the text but exited 0; the CLI now renders the state trace and exits 1. The approval-required pause stays exit 0 (`cmd/kern/helpers.go`, `cmd/kern/cmd_agent.go`).
- **`kern correlate` accepts an alert-JSON file path** as well as inline JSON, mirroring `kern incident` — a literal path previously failed with a cryptic `invalid character '/'`.
- **`kern explain-finding` omits hollow fields**: a minimal finding (rule id only) no longer renders empty `Severity: / File: :0 / Message: ` lines; absent data renders as absent (`internal/blueprint/mcp/handlers.go`).
- **`kern diff-gate` doc gates scoped to repos with generated docs**: `catalog:doc` (G36) and `contracts:doc` now guard DRIFT of an existing `docs/tool-catalog.md` / `docs/mcp/tool-contracts.md` instead of BLOCKing every non-kern repo for their absence; kern's own repo behavior is unchanged.
- **MCP catalog freeze gate** (`TestMCPToolCatalogSizeCap`): the tool catalog must stay within a deliberate size budget (floor 140 / ceiling 160) — no silent growth; bumping the cap requires a stated reason in the diff.
- **First de-monolithization slice of `internal/mcp`** (Rec P0-2): the tool catalog is extracted into a pure-data leaf package `internal/mcp/catalog` — the `Tool` type, phase/risk/schema-version constants, the 145-entry registration table (`tools.go`), schema helpers, `ToolNames()`, and the diff-gate catalog provider (~2,100 LOC moved; monolith 16,083 → 14,075). Zero behavioral change: `mcp.Tool`/`tools`/`ToolNames()`/consts remain as aliases for pre-extraction callers (handlers, transports, tests, setup parity). `dispatch.go` stays in the monolith by design. ARCHITECTURE.md gains the `internal/mcp/catalog` row; architecture parity + plugin-catalog parity + dispatch parity + size-cap tests all green.
- **Binary index snapshot cache** (Rec P0-3, adapted): `index.Save` now writes a gob snapshot (`.kern/index.json.snap`) alongside the canonical JSON, and `Load`/`LoadFile` prefer it when provably fresh (header vs JSON stat: size + nanosecond mtime). Halves the index-load cost on the default build (isolated: JSON decode 54.6ms → snapshot 27.8ms on the kern repo) with zero new dependencies (encoding/gob is stdlib). JSON remains canonical — any miss/stale/corrupt snapshot falls back to the JSON path; SQLite store (now default-on via `!nosqlite`) is untouched. The remaining CLI floor is process startup + the git-based freshness check + search work, not the index parse.
- **`secret:scan` content-hash incremental cache** (Rec P1-4): the per-file cache keys on stat (size+mtime) AND a content hash, so a touch that preserves bytes replays instead of rescanning; a genuinely modified file is re-scanned ALONE from a temp dir (partial miss) instead of forcing a whole-repo `kern sec` run. Measured on the kern repo: `secret:scan` 6008ms cold → 0ms warm (diff-gate total 7.7s → 279ms). Cache format bumped to v2; `TestSecretCheckMtimeTouchReplays` + `TestSecretCheckPartialDirtyScansOnlyDirty` cover the new paths.
- **Perf SLI gate** (Rec P1-5): diff-gate/blueprint checks now carry wall-clock budgets (`internal/blueprint/service/perfsli.go` — default 5s, `secret:scan` 15s for cold whole-repo scans). A check that blows its budget gets an advisory WARN `perf:sli` finding and its status bumps PASS → WARN — visible in the status line and summary, never a BLOCK (timing gates are flaky by nature; this is an order-of-magnitude regression detector). Applied after policy evaluation so the finding survives policy-rewritten results; verified 0 false positives on the kern repo.
- **MCP progress notifications centralized + timeout contract documented** (Rec P1-6): slow tools (sandbox, heal, run_build, execute, verify, validate, doc_index) now emit `notifications/progress` (0% start, 5s keep-alive, 100% stop) via a CENTRAL `runTool` wrap instead of three scattered handler calls — a future slow tool just joins the `slowTools` set. LLM-class tools (analyze/plan) stay out: cross-interface tests capture their responses byte-exact. `kern guide` documents the timeout contract (30-min ceiling, exec-family 120s + `timeout=N`, run_build 5-min hard bound, stdio-only progress, `max_output=N`).
- **`kern_meta` semantic escalation fallback** (Rec P1-7): when the deterministic keyword classifier misses, a dependency-free semantic fallback (token-overlap cosine over tool name+description, ≥2-token overlap, threshold 0.16) routes novel phrasing to the right tool — e.g. "show me the implementation plan" now reaches `kern_plan` instead of `kern_search`. Explanation questions ("how does X work") keep their code-intent semantics (candidate set restricted to the symbol/search family — a subsystem listing tool can never capture a code question). No LLM, no network: the escalation stays deterministic, and the response marks it — `[kern] classified as: kern_plan (semantic fallback)`.
- **CLI exit-code + `--json` contract fixes** (Rec P2-8): an audit of all 210 CLI command entries found 20 surface stragglers + 3 latent wiring defects, all fixed. (1) Eleven mcp-tool mirror commands swallowed `fs.Parse` errors — an unknown flag printed usage but exited 0; now exit 2 (evidence-anchor, stream, compose, prompt-fill, context-watch, agent-fingerprint, memory-ranked, agent-coordination, semantic-merge, register-host-sampler). (2) `kern brief`/`buddy`/`pack` rendered empty output with exit 0 for a nonexistent root; now exit 1 via an `os.Stat` guard (matching `kern project map`). (3) `kern org agents register` reported "registered agent" with exit 0 when persistence failed; now fatal. (4) `kern verify --verify-silent/--skill/--verify-pipeline` printed FAIL/INVALID/envelope-invalid verdicts but exited 0; now exit 1 in both text and `--json` modes. (5) `prose`/`review-pack`/`review-consensus` dispatch wrappers discarded their run function's int; now returned. Verified live: `brief /nope`→1, `evidence-anchor --nope`→2; 13 new regression tests.
- **Tool cost metadata** (Rec P2-9): `kern_meta` classifications now carry a deterministic cost hint — `[kern] classified as: kern_plan (semantic fallback) · est 800ms · 4000 out tokens` — so agents can budget context from the highest-traffic tool. Tiers: slow (~800ms/~4k tok; exec/verify + LLM-class tools), medium (~150ms/~1200 tok; index-backed search/graph/context family), fast (~10ms/~150 tok; the rest, default). Offline + deterministic (`internal/mcp/cost.go` — no LLM, no network, no catalog-shape change); `kern guide` documents the tiers; per-tool latency actuals remain in metrics.
- **Enterprise auth hardening for remote exposure** (Rec P2-10): the single-project web console now refuses to start when bound to a non-loopback address without `KERN_AUTH_TOKEN` — `kern-server -addr 0.0.0.0:8090` exits 1 with a loud message (verified live), matching the fail-closed posture of enterprise mode and `kern-mcp --http`. When the token IS set, every console request must carry `Authorization: Bearer <token>` (401 otherwise, constant-time compare — the same gate enterprise mode uses), covering state-mutating endpoints (`/v1/memory`, `/api/approvals/*`) and LLM work. Loopback default (`127.0.0.1:8090`) is unchanged: no token required. Tests: `TestWebAuthGate` (+unset variant), `TestIsLoopbackAddr`.

## [0.9.8.2] - 2026-09-14

### Added
- **Dynamic Architecture Guardrails Fallback**: `kern guard` and `kern impact` automatically infer default layered architecture boundaries (`intel.InferBoundaries`) from AST module hierarchies (Controller/Router $\rightarrow$ Service $\rightarrow$ Repository/DB, banning reverse dependencies) when `.kern/boundaries.json` is unconfigured, falling back cleanly without hard errors while publishing `architecture.warning` events.
- **Multi-Repo Aggregate Freshness in `kern doctor`**: `internal/doctor` automatically discovers subprojects in multi-repo workspaces and evaluates aggregate index freshness and symbol counts across all child projects.

### Fixed
- **Sub-Repository Scoping in `kern modernize`**: added `WithPathPrefix` filter to `modernization.Analyzer` ensuring bounded context extraction and migration planning are scoped strictly to the target subfolder rather than including sibling repositories in a parent workspace.
- **MCP Server In-Memory Index Hot-Reload**: added filesystem modtime tracking for SQLite and JSON stores so external `kern index --force` runs immediately update active MCP server sessions without requiring a process restart.
- **`kern fit-context` Positional Arguments & `--budget` Alias**: added fallback positional argument handling (`args[0]` as symbol or file path) and `--budget` flag alias for `--max-tokens` to match `kern --help` interface.
- **Total Token Budget Compaction in `fit-context`**: enforced global aggregate token limits (`renderTierBudgeted`) across multi-file search results, preventing individual file slices from exceeding the requested token budget.
- **MCP Server Background Watcher**: eliminated concurrency data race during server shutdown by serializing watch termination state and preventing asynchronous rebuild scheduling against `sync.WaitGroup`.
- **Test Fixtures**: disabled background Git maintenance/gc in template repository initialization and added resilient handling for transient lock files during fixture directory copies.
- **Security & Code Quality Hardening**:
  - Bound memory allocations and integer multiplication in line diff DP table calculations (`internal/diff`).
  - Added Unicode rune codepoint validation before decoding PII runes (`internal/pii`).
  - Clamped capacity hints in WhatIf symbol parsing to prevent integer overflow (`internal/whatif`).
  - Enforced path cleaning on root validation within MCP server requests (`internal/mcp`).

---

*For historical changes and releases prior to v0.9.8.2, refer to the respective git release tags and commits.*
