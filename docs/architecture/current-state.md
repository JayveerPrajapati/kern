# Kern 2.0 — Current-State (Phase 0.1)

Baseline frozen at `894fb10` (2026-08-23). Go 1.23, stdlib-only default build (tree-sitter + sqlite behind build tags).

## Repository topology

- `cmd/kern` — CLI, ~50+ subcommands in a main.go split across cmd_*.go (run/task/approve/audit/incident/modernize/what-if/memory/index/exec/setup/doc/graph/security/context/artifacts/agent/meta/optimize/mcp).
- `cmd/kern-mcp` — MCP server entry (~68 lines, delegates to internal/mcp; graceful shutdown implemented).
- `cmd/kern-server` — web/REST server (`-root`, `-addr`), single-project + enterprise modes, graceful shutdown.
- `internal/` — 74 packages. Core control-plane surface:
  - `domain` — Task/Artifact/Evidence/CompiledIntent/RunResult/Capability/ToolDecisionTrace/Consistency/Freshness/ContextClass/ContextState/TaskBoundary/SafetyBudget/Constitution.
  - `app` — control-plane services: `Platform` facade + `TaskService` (Run, Analyze, Plan, Impact, Risk, WhatIf, Verify, Execute, Approve, Deploy, Observe, Modernize, Retry, Cancel, Resume, HumanTakeover), `CompileIntent` (Intent Compiler), `ArtifactStore` (persistence + replay + compare).
  - `index` — multi-language indexing pipeline (17 langs stdlib, 13 tree-sitter opt-in).
  - `intelligence` — call graphs, blast radius, impact, hubs/bridges, communities, dead code, flows, paths, probe, trace, wiki, guard/boundaries, coverage, churn, cochange, rename, delete.
  - `context` — context engine, GC, snapshots, normalizer, rules/crossesBoundary, consistency check, git/freshness.
  - `eventbus` — async pub/sub, 62 Kind constants, in-memory History.
  - `governance` — firewall, gateway (identity→task→resource→action→permission→risk→policy→approval→budget), constitution (.kern/constitution.yaml + ValidatePlan), approval store, audit log, identity, risk, exec gating.
  - `agent` / `agents` — agent runtime (Task, workflow, session, registry, provider, handoff, snapshot) + specialist roles (Planner/Architect/Coder/Reviewer/Security/Tester/SRE), dynamic selection, model routing, agent eval.
  - `loop` — 9-stage loop (intent→remember→plan→code→verify→protect→deploy→observe→learn), autonomy L0-L5, budget pause.
  - `verification` — build/test/security/architecture validation engine.
  - `sandbox` — snapshot isolation (100MiB cap, SkipDirs), restore, heal.
  - `execution` — worktree execution, apply/verify.
  - `incident` — incident engine, correlation, store.
  - `runtime` — runtime sources (prometheus/otel/k8s live + local), correlate chain, store, events.
  - `deployment` — Deployer (Noop/Shell), approval-gated.
  - `modernization` — modernization analysis (communities/bridges/churn).
  - `twin` — digital-twin API/data/edges/extractors/merge.
  - `evidence` — evidence builder/digest/factories.
  - `memory` — engineering memory store + AuthorizedRecall.
  - `learning` — pattern extraction from memory.
  - `flight` — flight recorder for audit.
  - `mcp` — MCP server, 77 kern_* tools.
  - `sdk` — Go SDK client.
  - `web` — web console (dashboard.html, task_detail.html) + REST API routes.
  - `enterprise` — org/team memory isolation + LRU project cache.
  - `llm` — provider abstraction (ollama/openai/anthropic/google).
  - `whatif`, `metrics`, `stats`, `evaluate/bench`, `cicd`, `prprovider`, `planner`, `learning`.
- `sdk/python`, `sdk/typescript` — Python + TypeScript SDKs.
- `docs/` — only `examples/kern-gate.yml` + this new architecture set.
- `AGENTS.md`, `.opencode/plugins/kern.ts`, `.mcp.json` — agent wiring.

## Owners / responsibilities

All 20 spec-required packages present and owning their spec responsibilities. No critical subsystem is unknown.

## Baseline (Phase 0.3)

- `go build ./...` ✓
- `go vet ./...` ✓
- `go test ./...` ✓ (app suite 338s; core control-plane packages pass)
- Build-tag variants (`-tags treesitter`, `-tags sqlite`) verified pass at prior audit.
- MCP catalog 77/77 tools; web server + graceful shutdown verified.
- Test matrix A–J all present (acceptance_matrix_test.go).

## Known TODOs / limitations

- docs/architecture deliverables being written in this phase.
- Several spec micro-phases PARTIAL/MISSING — see gap-analysis.md.

## Failure modes
- Stale index: the graph can lag the filesystem after an edit (no live file-watch invalidation). Mitigation: rebuild before impact/blast-radius analysis; freshness is best-effort.
- Incomplete index: only 17 langs are indexed in the stdlib build, so symbols in tree-sitter-only languages are invisible until `-tags treesitter` is used. Mitigation: enable the optional tag when analyzing those languages; document coverage.
- Per-request reindex latency: indexing is triggered on demand rather than pre-built, so first impact/what-if queries pay the full pipeline cost. Mitigation: pre-warm the index for large repos before interactive analysis.
- Optional-tag build variance: `tree-sitter` and `sqlite` builds diverge from the stdlib baseline, so behavior can differ by build. Mitigation: run the test matrix under each tag variant; treat stdlib-only as the default contract.
- Cache staleness: enterprise LRU project cache and in-memory event history can serve stale state after out-of-band changes. Mitigation: invalidate/refresh before governance approval or audit reads.
- Governance gating on a stale constitution: if `.kern/constitution.yaml` changes, in-flight policy prechecks may not reflect the new rules. Mitigation: reload the constitution at task boundary; fail-closed on parse errors.