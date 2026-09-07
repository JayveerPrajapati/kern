# Changelog

All notable changes to kern are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
