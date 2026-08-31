# PHASE 19 — REST / SDK / ENTERPRISE — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 19.1–19.4.
Go: 1.23, stdlib-only default build.

## Scope

MCP, CLI, REST and SDK all use the same control-plane application services.

## Micro-phase audit

- **19.1 (P0) REST** — 25 `/v1` routes covering all 12 required categories:
  tasks (submit/fetch/deploy), analyze, plan, impact, what-if, approve/reject,
  execute, verify, memory, incidents (investigate/correlate), artifacts, audit —
  plus context/risk/agents/loop/learn/modernize/graph. All handlers delegate to
  the shared TaskService/Platform.
- **19.2 (P1) Go SDK** — NEW `sdk/go/kernsdk` (below).
- **19.3 (P1) Enterprise** — `/org/...` endpoints: memory, tasks (multi-project
  aggregation), agents (+ registration, teams), teams, audit, policies, search
  (multi-repo), architecture; LRU project eviction + per-project memory
  isolation (verified by the enterprise test suite).
- **19.4 (P2) Other SDKs** — Python `kern_sdk` (28 methods, 16 tests PASS) and
  TypeScript `@kern/sdk` (12 tests PASS) both verified against the same REST
  surface.

## Gap found and closed

### G1 — No Go SDK (19.2)

`sdk/` shipped Python and TypeScript SDKs but no Go SDK, so Go consumers had
no first-class client for the control plane.

**Fix (`sdk/go/kernsdk/`):** a thin, stdlib-only (net/http) client mirroring
the 28 Python methods — Analyze, Plan, WhatIf, Impact, Verify,
InvestigateIncident, MemoryList/Add, Graph, Context, Risk, Task, Agents, Loop,
TaskSubmit, Execute, Correlate, Learn, Modernize, ArtifactsList/Get, Audit,
Approve, Reject, Deploy, Health — with typed error surfacing (`kernsdk.Err`).

## Exit gate

> "MCP, CLI, REST and SDK all use the same control-plane application services."
> — **MET and LOCKED**: `TestGoSDKAgainstControlPlane` boots a REAL
> kern-server (`internal/web` over a fixture — the same handlers the Python/TS
> SDKs and the CLI/MCP share) and drives Analyze/Plan/WhatIf/Memory/Task/Agents
> through the Go SDK, proving one control plane across every surface.

## Verification

- `go vet ./...`, `go build ./...`, `-tags treesitter`, `-tags sqlite` — PASS
- **90/90 Go packages pass, exit 0**
- Python SDK: 16 tests PASS · TypeScript SDK: 12 tests PASS (after `npm
  install && npx tsc` — `dist/` is gitignored and not prebuilt)
- Enterprise suite PASS