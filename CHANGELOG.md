# Changelog

All notable changes to kern are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.0] - 2026-09-14

### Added
- **Storage & IPC Scalability (Domain 1)**:
  - SQLite WAL persistent store with 256MB memory-mapped I/O (`PRAGMA mmap_size=268435456`) and sub-millisecond B-Tree point queries: `LookupSymbol`, `LookupCallers`, `LookupCalls`, `LookupFileSymbols`, `LookupInherits`, `LookupInheritedBy`.
  - Unix Domain Sockets (UDS) & Named Pipes IPC transport (`unix:///path/to/sock`), accelerating agent-daemon throughput by 3×–5×.
- **Universal Language & Framework Intelligence (Domain 3)**:
  - Zero-Weight LSP Client Bridge (`kern_lsp_bridge` / `kern lsp-bridge`): connects to local language servers (`gopls`, `vtsls`, `pyright`, `rust-analyzer`, `clangd`) over stdio for exact compiler types without bundled compiler bloat.
  - Deep Framework DI & Route Tracing (`kern_fw_trace` / `kern fw-trace`): maps end-to-end framework routes, handler DTOs, and dependency injection services (Spring Boot, FastAPI, NestJS, Next.js, Express, Gin).
- **AI Agent Token Economy (Domain 4)**:
  - Context-Window Adaptive Token Compressor (`kern_fit_context` / `kern fit-context`): multi-tier source folding (Full Source $\rightarrow$ Signatures + Docstrings $\rightarrow$ Symbol Adjacency Graph) guaranteed to fit any token budget (8k, 32k, 128k, 1M).
  - Dynamic Phase-Filtered MCP Tool Discovery (`KERN_MCP_PHASE=explore|plan|edit|verify`) and Single-Tool Zero-Overhead Mode (`KERN_MCP_SINGLE_TOOL=1`), slashing agent prompt token overhead by 60%–80%.
- **Safe Multi-File Mutation & Auto-Repair (Domain 5)**:
  - Multi-File Transactional AST Refactoring Engine (`kern_refactor_transaction`): evaluates batch multi-file modifications in an isolated sandbox worktree with automated compilation and firewall gates (G0–G39), with atomic rollback on failure.
  - Structural AST 3-Way Merge (`internal/merge3`): AST node-level merging preventing conflict markers when parallel agents edit independent functions.
  - Compiler-Error-to-AST Auto-Repair Engine (`kern_repair_diagnostics`): deterministic surgical AST fixes for trivial compiler diagnostics in $< 1\text{ms}$.
- **Quality & Defect Memory (Domain 6)**:
  - Lightweight Mutation Testing for Test Gaps (`kern_mutation_test` / `kern mutate`): inverts AST conditions and zero-value returns to verify regression sensitivity.
  - Causal Defect & Fragility Hotspot Memory (`kern_fragility_hotspots` / `kern fragility`): correlates git fix history with symbol graph hubs to warn agents before modifying regression-prone code.
- **Human Ergonomics & Streamlined Help Interface**:
  - Overhauled `kern --help` to cleanly present the 5 Core Verbs (`explore`, `search`, `plan`, `mutate`/`refactor`, `verify`) and context optimization tools on a single screen without terminal scrolling fatigue.
  - Added `kern --all` / `kern help --all` for the complete 140+ command catalog reference.
- **Test Suite Acceleration (Domain 7)**:
  - Fast-path git worktree checks bypassing redundant `git` subprocess spawns on temp fixture directories, accelerating unit tests up to **300×**.

### Changed
- Universal MCP tool catalog synchronized to **143 tools** with 100% parity across `internal/mcp/tools.go`, `.opencode/plugins/kern.ts`, `README.md`, `AGENTS.md`, and all `docs/mcp/*.md` specifications.
- Verified and documented all 40 Blueprint Firewall Gates (**G0 through G39**) in `docs/gates.md`.

## [0.9.8] - 2026-09-10

- **Review Packs, Council & Diff Gate (silent-orchestrator P2)**:
  - `kern review-pack` (KERN-P2-001): immutable deterministic review packet — commit + dirty-state hash, task, planner-selected evidence with reasons, relevant symbols with call paths, changed code, tests, project constraints, observed claims, unverified assumptions, and exact per-section token counts. Pack sealed with a content hash; identical builds over identical state produce byte-identical JSON. `internal/reviewpack`.
  - `kern review-consensus` (KERN-P2-002): normalizes review packs into a consensus/divergence report — consensus, divergence, minority positions, supporting evidence, unsupported claims, assumptions, decision drivers, and next verification — without treating majority vote as truth. `internal/council`.
  - `kern diff-gate` (KERN-P2-003): deterministic diff gate over the working-tree diff — 8 checks (formatting, vulnerabilities, secrets via the blueprint G3 adapter, tests via G8, schema drift, unsafe execution additions, missing changelog, MCP catalog↔plugin drift). Structured JSON verdicts; new blueprint gates G30–G35.
- **Enterprise & Org Admin**:
  - `kern org` CLI and `kern_org_*` MCP tools: organization-level multi-repo indexing, project registry, aggregated metrics, and team dashboard (`internal/enterprise`).
- **Runtime Drift Detection & Governance**:
  - `kern runtime` CLI and `kern_runtime` MCP tool: route drift detection comparing observed runtime routes with static AST code declarations (`internal/runtime`).
  - Safety-budget gateway, audit task transitions, GC pinning, and context clearance (`internal/governance`).
  - Security hardening: deny-by-default network egress gate with `KERN_ALLOW_*` verification and macOS fail-closed execution safety.
- **Index & Code Intelligence**:
  - Parser confidence scores (schema v13): every parsed symbol, call edge, and import relationship carries a HIGH/MEDIUM/LOW confidence rating.


- **Index & Code Intelligence**:
  - Incremental re-index: `index.Update` re-parses only changed files while reusing the prior build's per-file results, with a `Update`-specific merge that keeps every derived map consistent (identity, freshness, precision, communities).
  - Resource-adaptive tuning: index builds now size worker pools, file caps and batch sizes from detected CPU count and RAM, with `KERN_INDEX_*` overrides; large-memory machines index faster without OOM risk.
  - **Dead-code lens trust (two fixes)**:
    - Constructor-inferred receiver edges (`x := New(); x.M()` recorded as `New.M`) no longer hide live callers: `kern dead` / `kern delete` resolve them via same-file return-type inference, multi-value assigns, merge-time callee rewriting, and alias-merged delete checks. All 8 symbols a previous audit flagged "safe to delete" were confirmed live.
    - Field-access receiver chains (`a.taskSvc.Deploy(...)`): the index now records struct field types (`Pkg.StructFields`, schema v12) and resolves receiver-field calls to the field's type at merge time — `kern dead` no longer flags live methods as "uncertain", while foreign/undeclared field types are never forged into canonical callers.
  - Blast radius links import-qualified cross-package callees (e.g. `db.Do` → local symbol) in impact/what-if/context analysis, closing the cross-package under-reporting gap.
  - O(n²) fileMap rebuilds eliminated in Bridges/Architecture/Coverage/Communities (hoisted per query).
  - Bounded reorder buffer in parallel builds (memory capped at 1024 pending results, byte-identical output) and asynchronous index saves (persisted copy always complete at process exit).
- **Autonomous Loop & Verification**:
  - The coder is now grounded: `kern do` assembles plan-named files + blast-radius context into the coder prompt, applies per-file search/replace edits, and feeds apply failures back with the actual file head.
  - Polyglot verification: test/lint/build commands resolve from detected frameworks (Go/Node/Rust/Python) with `.kern/config.json` overrides, replacing the hard-coded `go build`/`go test`.
  - `kern_do` MCP tool (catalog 105): the closed loop is now reachable from MCP (default L2, sandboxed).
- **Governance & Safety (fail-closed hardening)**:
  - Swallowed approval/audit write errors surfaced in web handlers, governance store and audit log; approval/audit stores fail closed on corrupt files (never silently-empty).
  - CI: new full (non-short) E2E job for internal/{app,cicd,mcp} + blueprint gate; blueprint gates now FAIL (not skip) when the kern binary is absent (`KERN_REQUIRE_BINARY=1`).
  - `kern onboard` exits non-zero on register/index failure; simulated loop stage outcomes are labeled `simulated:`; library panic paths on tool routes return errors instead; lock/unlock errors surface; ignored `ix.Save()` results handled loudly; audit log gains a 5000-entry retention cap with O(1) governance metrics.
  - `GET /v1/incidents` list route added (the Python SDK's `incidents()` previously 404'd).
- **MCP & CLI**:
  - `kern_risk` MCP tool added — the missing impact/what-if sibling (catalog 106).
  - Flight recorder reader: `kern flight list` / `kern flight show <task-id>` replay every stage of an autonomous run; `kern_flight` MCP tool (catalog 107).
  - HTTP daemon fix: the `kern mcp --http` path now registers the default agent (governed tools were denied); daemon E2E proves flat-memory multi-client operation with shared-index leadership election (flock, per-root).
  - `kern doctor` gains 4 checks: config-file validity, cache corruption (zero-byte JSON), binary version, and full `KERN_*` env echo with validation.
  - Niche MCP tools (`kern_evidence_anchor`, `kern_stream`) compact by default with JSON behind `format=json`; all remaining bare `fatal("%v")` calls labeled with their command.
- **Performance**:
  - Session index rebuilds are stale-while-revalidate: one caller rebuilds, concurrent callers are served the previous index immediately (single-flight, -race clean).
  - Enterprise mode: per-project single-flight builds off the org-wide mutex; `serveOrgArchitecture` answers from cached apps and async-warms up to 2 builds concurrently (no cold-start OOM).
- **Evidence & Trust**:
  - `kern evidence explain` renders plain-language "what this proves" summaries; `kern evidence verify --url` verifies bundles without cloning (sealed fetch + optional local chain replay).
  - CI action exports an evidence bundle and appends a tamper-sealed Evidence section to PR comments.
- **SDKs & Docs**:
  - TypeScript and Go SDKs reach parity with Python: `approvalsPending()`, `incidents()`, `incident(id)`, `eventsStream()`.
  - ADR-0002 through ADR-0005 locked in: per-repo `.kern/` storage identity, name-qualified symbol identity, external-agent-first coder contract, `internal/governance` as the long-term policy core.
- **Tests & Tooling**:
  - Blueprint CLI coverage 0 → 6 tests; real cross-process flock tests (PID-liveness for election); twin/data + twin/infra coverage (incl. a Helm chart dispatch-order bug fix); test-first contracts pinned for `parseFlags` and `dispatchCommand`.

## [0.9.7] - 2026-09-07

### Added

- **Multi-Agent Skills Subsystem & CLI (`internal/skills`, `kern skills`)**:
  - Added native support for the open Agent Skills standard (`SKILL.md` + executable automation scripts).
  - Bundled 3 production runbooks: `kern-investigate` (AST symbol, call-graph, and blast radius exploration), `kern-safe-change` (pre-edit risk prediction, $G_0$–$G_{29}$ firewall verification, auto-repair, and cryptographic CI receipts), and `kern-incident-triage` (Auto-SRE log compaction, AST stack-trace mapping, sandbox repro synthesis, and auto-repair).
  - CLI management: `kern skills` lists bundled skills, `kern skills show <name>` displays markdown runbooks, and `kern skills install` installs and synchronizes skills across project and global agent environments.
- **AI Agent Intelligence & Token Optimization Suite (15 new MCP tools, expanding catalog to 104 tools)**:
  - **Phase 1 (Observability & Orchestration)**:
    - `kern_health`: Real-time MCP server and index health diagnostics snapshot (status, freshness, metrics, cache hit rate, audit depth) eliminating blind retries. Also wired to HTTP `GET /health`.
    - `kern_compose`: Executes ordered deterministic multi-tool pipelines with `$var` variable interpolation and configurable error policies in 1 RPC, eliminating round-trip latency and token overhead.
    - `kern_pre_edit`: Pre-edit blast radius and structural safety checks evaluating callers, transitive impacts, untested hotspots, and risk ratings before files are modified.
    - `kern_prompt_fill`: Dynamic standardized prompt template compilation with auto-injected project architecture maps, token budgets, and lessons from project memory.
  - **Phase 2 (Context Intelligence & Safety)**:
    - `kern_semantic_diff`: AST-level functional symbol diff highlighting modified functions, changed signatures, and newly impacted callers instead of raw line diffs.
    - `kern_evidence_anchor`: Zero-hallucination citation verification confirming file:line and symbol existence, self-healing line drift, and issuing cryptographic SHA-256 evidence certificates.
    - `kern_context_watch`: Proactive rolling conversation token audit detecting code/log bloat and recommending concrete deterministic compaction commands.
    - `kern_agent_fingerprint`: Tool-call pattern hashing and audit analysis detecting agent infinite loops, tool polarization, and behavioral drift.
  - **Phase 3 (Architecture, Multi-Repo & Governance)**:
    - `kern_explain`: Graph-backed architectural narrator synthesizing declaration details, callers, outbound dependencies, and test posture in a single call.
    - `kern_cross_repo_impact`: Multi-repository blast-radius analyzer detecting breaking contract changes and external call sites across linked repositories.
    - `kern_memory_ranked`: Decay-weighted and keyword-overlap memory retrieval using exponential time decay ($e^{-\lambda \Delta t}$), ensuring fresh lessons take precedence over stale patterns.
    - `kern_policy_dsl`: Declarative Policy-as-Code evaluation engine checking git diffs, changed files, and imported libraries against declarative rules.
    - `kern_agent_coordination`: Workspace coordination protocol for multi-agent teams providing structured task handoffs, exclusive resource locking with TTL, and agent inboxes.
    - `kern_agent_role_rbac`: Identity-based role authorization matrix (`junior_dev`, `reviewer`, `auditor`, `developer`, `architect`, `admin`) preventing unauthorized execution or unreviewed deletions.
    - `kern_stream`: Streaming & chunking transport scaffold with response partitioning and progress token notifications.
  - **Phase 4 (AST Mutation & Test Synthesis)**:
    - `kern_ast_transform`: Deterministic AST-level struct field additions and interface implementation scaffolding.
    - `kern_semantic_merge`: AST-aware 3-way semantic merge and structural conflict detection.
    - `kern_synthesize_test`: Automatic synthesis of table-driven unit test scaffolding from function signatures.
- **MCP Dispatch Parity**: Full 1:1 parity enforced between `internal/mcp/tools.go` and `internal/mcp/dispatch.go`, covered by `TestDispatchParityWithRegistration`.
- **Automated `.kern` Protection & Test Environment Isolation**:
  - Isolated test git repositories (`g11Repo`, `g4GitRepo`) by configuring `core.excludesfile` to `/dev/null` and force-tracking `.kern/boundaries.json`, preventing host global git ignore settings from interfering with sandboxed CI validation.
  - Hardened machine-wide and local exclusions for generated `.kern` cache and metadata files.

## [0.9.6] - 2026-09-06

### Added

- **Unified Change Governance Engine (`internal/blueprint`)**: Merged sibling `blueprint` engine natively into `kern`, integrating all 21 packages and 30 phase gates ($G_0$–$G_{29}$) without breaking contracts.
- **First-Class Subcommands**: Wired `kern check`, `kern fix`, `kern ci`, and `kern verify-receipt` directly into `cmd/kern` CLI.
- **Broader PII/secret masking** (`kern mask`, `kern_mask_pii`): added the `sk-live-…`/`sk-test-…` dash-form Stripe keys (`STRIPE_DASH`), scheme-less DSN/userinfo credentials (`root:pass@host`, `user:pass@tcp(host:port)`), and generic 32+-char hex tokens (`HEX`). Previously an `sk-live-9f8a…` key and `root:supersecret@tcp(…)` leaked in plaintext while email/phone/IP were masked.
- **Legacy Shims (Blueprint standdown)**: Deleted the ~9,200-line standalone Blueprint CLI (byte-for-byte duplicate of `internal/blueprint/cli`) and reduced `cmd/blueprint` to a thin `main.go` shim forwarding to the shared `internal/blueprint/cli` implementation (parity by construction). `cmd/blueprint`/`cmd/blueprint-mcp` remain only as backward-compatibility entry points for existing scripts/hooks; CI, the Makefile, and `kern setup` no longer build or install them.
- **KernOps Governed Autonomous Platform Plan**: Added `docs/architecture/kernops-plan.md` specifying the architecture, Option 2 standalone repository layout (`/workspace/kernops`), self-healing repair contracts, and implementation roadmap.
- **Auto-captured memory is labeled and recall-safe** (`kern memory`, `kern_memory_list`, `kern buddy`): the opencode plugin and native Claude/Gemini hooks auto-record raw user prompts (`User: …`) and tool outcomes (`Edited …`, `Command failed: …`, sanitized change snapshots) into project memory. These are now tagged `Source: "auto"` — shown as `[auto]` in lists, excluded from `kern recall`/`kern_memory_recall` and from the `kern buddy` session briefing (so raw prompts, which may carry PII, never leak back into an LLM context), and backfilled for legacy stores written before the field existed.
- **`kern trace` accepts plain `file:line symbol` lines and inline text**: `kern trace "path/file.py:24 funcname"` (or `path/file.py:24 funcname` piped text) now resolves the trailing symbol from the line, and an unresolvable trace prints the accepted formats (pprof -top dump, crash stack, plain function list, or `file:line symbol`) instead of dying with a bare exit 1. This also documents the ambiguity when a path is actually inline trace content.
- **`kern_verify` and `kern verify` surface FAIL verdicts instead of a bare error**: a failed verification (e.g. a security scan finding criticals) now returns the typed `verdict: FAIL` plus per-check status — `build: OK (141ms)`, `security: FAIL findings=42 critical=35 …`, `tests: passed=…`, `architecture: violations=…` — instead of collapsing into `Failed with exit code 1` / `verification failed: …`. Exit code still reflects the failure; the message now says what failed.
- **`kern_churn`/`kern_cochange`/`kern_commitmsg` exclude vendor noise and stay bounded**: the git-history tools now drop `vendor/`, `dist/`, `build/` and other ignored paths (the same ignore set the index crawler uses) from reports and commit-message bodies, so deep vendor-heavy ranges no longer blow up the risk pass (capped at 300 files) or hang. `kern_cochange` also gained a context-cancellable form (`CoChangeContext`) used by the CLI.
- **`kern_exec` isolation behavior documented (A12)**: README and the `kern_exec` MCP description now state that on platforms without private network namespaces (macOS, some containers) execution fails closed instead of silently degrading to full network egress, with the explicit opt-in (`KERN_ALLOW_UNISOLATED=1` / `KERN_ALLOW_NET=1`). Regression test added for the `kern commitmsg`/`readStdin` non-TTY fix (A15): piped content still reads, empty regular files read empty, and a character device (`/dev/null`) returns immediately with no blocking.
- **`kern_meta`/`kern impact` tune symbol extraction (A8)**: prose queries no longer land on a lead verb ("what breaks if I remove the translate function from cmaas_controller?" now targets `translate`, not `breaks`). `whatif.ExtractSymbols` gained inflected change-verb stopwords ("breaks", "removes", "changed", …), and `Platform.resolveSymbol` verifies extracted candidates against the index — the first candidate that resolves wins, and when none resolves it fails with a hint ("no symbol named X was found … pass a concrete exported name") instead of reporting a misleading 0-caller impact. New `Graph.Resolvable`.
- **Flow questions route to a graph answer (A9)**: `kern_meta` now classifies "how does the bundle upload flow work end to end?"-style queries to `kern_walk` (when a symbol is named, depth 4) or `kern_entry_points` (otherwise) instead of the flat `kern_search` fallback.
- **Caller/edge counts are consistent and documented (A10)**: `kern buddy` now reports the same metric as `kern onboard` — total directed call edges (each caller→callee pair) — instead of the distinct-caller count that understated edge volume (e.g. 122 rows vs 910 directed edges on the same repo). README documents the intentional projection differences between `kern hubs`/`kern explore` (distinct caller symbols, production-only for hubs), `kern impact --precision strict` (inferred edges dropped), and `kern buddy`/`kern onboard` (directed edge sum).

### CI & Tooling

- **`architecture:not-enforced` message corrected** (`kern check`/`kern ci`): the WARN no longer claims "kern has never been run in this repo" when the evaluated path is a detached-worktree CI sandbox whose gitignored `.kern/` index simply isn't visible — that case is now reported as "the repository has a .kern/ index but this validation sandbox cannot see it" — and the suggested command is fixed from the non-existent `kern init` to `kern guard init`.
- **Read-only loops no longer fail on repo hygiene** (`kern loop`/`kern_loop` at L0/L1): a read-only loop makes no changes, so a verification FAIL can only reflect the repository's pre-existing posture (hardcoded secrets, stale boundaries, a broken build) — never the task's surface. The verify stage now reports such findings as an informational advisory (`Result.VerifyAdvisory`, surfaced as `verify-advisory` in the CLI output) instead of aborting the run. Write levels (L2+) still hard-fail verification on the loop's changes.
- **`verify-receipt` verifies a CI artifact**: `kern ci` now stamps the sealed receipt id into the emitted artifact (`receipt_id`), and `kern verify-receipt <file>` accepts a positional JSON file — a receipt (verified directly, using its recorded repo root) or a CI artifact (correlated to its receipt, with base/head cross-checked for tampering). An artifact from a BLOCK/ERROR run — receipts are only sealed for PASS/WARN — exits 3 with an actionable explanation instead of a bare "Receipt not found."
- **Toolchain Availability in `/usr/local/bin`**:
  - Pinned and extracted official static `gitleaks` v8.30.1 directly into `/usr/local/bin/gitleaks` in GitHub Actions.
  - Globally installed `jscpd` and linked into `/usr/local/bin/jscpd`, preventing 120s `npx` network download timeouts in headless runners.
  - Pre-installed `kern` to `/usr/local/bin` ahead of test execution (legacy `blueprint`/`blueprint-mcp` are no longer built or installed).
  - Added a proactive `Verify tool availability` step in `.github/workflows/ci.yml`.
- **Test Robustness**: Enhanced `cmd/blueprint/g25_test.go` to ignore environment-level fallback warnings (`secret:incumbent-unavailable`) during file-level provenance validation.

---

*For historical changes and releases prior to v0.9.6, refer to the respective git release tags and commits.*
