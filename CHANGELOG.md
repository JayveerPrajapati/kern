# Changelog

All notable changes to kern are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.9.14] — 2026-11-23

### Added
- **Concise Read & Query Modes**: added opt-in `--mode concise|evidence|full` across read paths (`kern why`, `kern refs`, `kern probe`, and symbol exploration) with fast-path summary projection.
- **Governed Code-Mode Orchestration**: end-to-end task pipeline orchestration via `kern run` with task, workflow, risk classification, approval hooks, and capability resolution.
- **Enhanced MCP Surface**: direct core MCP tool surface (32 tools advertised) including `kern_apply`, `kern_arch`, `kern_evidence`, `kern_plan_context`, `kern_path`, `kern_heal`, and `kern_project_map`.
- **Fast Full-Surface Verification & Load Gates**: end-to-end reliability and chaos load test suites, race-safe recorders, and warm daemon reuse gates.
- **Clean Tool Management**: full two-phase `kern uninstall` (repo/wiring cleanup followed by prefix-scoped binary/PATH retirement) with `--prefix-only` and `--project-only` flags.

### Changed
- **Index & Graph Performance**: parallelized freshness index walks, persisted derived graph and export catalogs as sidecars, memoized catalog owner resolution, and zero-allocation edge sorting.
- **FTS5 Compaction**: automated SQLite full-text search compaction (`indexdb.OptimizeFTS`) and churn-budgeted delta compaction.
- **Default Posture Alignment**: updated exec posture defaults to ask/risk-assessment with resolvable approval tokens and single-command daemon sweeps.
- **Architecture Ratchet**: bidirectional ledger enforcement covering duplication, MCP layers, and evaluated agentic bounds; refined LOC baselines across core packages.

### Fixed
- **Call-Binding & Identity Resolution**: restored primary catalog declaration binding for Python/FastAPI unsourced call edges; disambiguated Rust `impl` identities and qualified cross-package call targets.
- **Audit & Governance Hygiene**: fixed audit chain replay to exclude zero-identity head stamps, deduped identical approval requests, and added stale-approval pruning.
- **Read & Guard Serving**: hardened read serving pipelines against un-governed command execution while running allow-verdict probes directly.
- **Doc & Tool Drift**: reconciled MCP schemas, unified configuration escape hatches, and updated CLI documentation parity across all commands.
