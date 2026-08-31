# PHASE 2 — APPLICATION CONTROL-PLANE LAYER — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 2.1–2.5.
Go: 1.23, stdlib-only default build (tree-sitter + sqlite behind build tags).

## Scope

Move business orchestration out of interfaces: every core workflow lives in a
shared application service, and MCP / CLI / REST are thin adapters over the same
service layer (exit gate: "no core business workflow exists only in one
interface").

## Work completed (micro-phases)

### 2.1 — Service contracts (P0)

- Verified the 15 service contracts (Task, Context, Analysis, Impact, WhatIf,
  Memory, Risk, Policy, Agent, Execution, Verification, Incident,
  Modernization, Deployment, Audit — plus Loop) exist as interfaces in
  `internal/app/services.go`, satisfied by `*TaskService` / `*Platform`, with
  compile-time assertions `var _ Task = (*TaskService)(nil)` etc. so a contract
  drift breaks the build.
- No engine changes were needed; the interfaces capture the exact signatures
  the concrete services already expose.

### 2.2 — MCP refactor (P0)

- Audited all 9 named tools (`kern_analyze`, `kern_plan`, `kern_impact`,
  `kern_what_if`, `kern_execute`, `kern_verify`, `kern_incident`,
  `kern_agents`, `kern_loop`): every handler routes through `TaskService`
  (`Analyze/Plan/Impact/WhatIf/Execute/Verify/InvestigateIncident/Agents/
  RunLoop`). Zero inline engines in `internal/mcp` handlers (verified by grep:
  no `incident.NewEngine`/`whatif.Simulate`/etc. in handler code).

### 2.3 — CLI refactor (P0)

- **Fixed exit-gate violation**: `kern incident` (cmd/kern/cmd_agent.go
  `runIncident`) was inlining the incident engine (`incident.NewEngine` +
  `IngestAlert`/`Correlate`/`RootCause`) — the incident workflow existed
  inline in the CLI only. Rewritten to route through the shared
  `TaskService.InvestigateIncident` exactly like MCP kern_incident (snapshot
  wired via `p.WithRuntimeSource`), emitting the same
  `[task: <id> — state: COMPLETED — incident: <id>]` trailer.
- Verified the other named CLI commands already delegate to the services
  (`runAnalyze/runPlan/runImpact/runWhatIf/runVerify/runExecute` all build
  `app.NewTaskService(...)` and call the service method).

### 2.4 — API refactor (P1)

- **Fixed exit-gate violation**: REST `handleV1Context` was calling the context
  engine directly (`a.ctx.AnalyzeChange`) instead of the shared Context
  service; now routes through `Platform.Analyze` (the service both CLI and MCP
  paths use).
- **Fixed exit-gate violation**: REST `handleV1IncidentInvestigate` was
  inlining the incident engine (`a.inc.IngestAlert` + `Correlate` +
  `RootCause`); now routes through `TaskService.InvestigateIncident`.
- Removed now-dead state: the `App.ctx *kerncontext.Engine` and
  `App.inc *incident.Engine` fields and their startup wiring (the prebuilt
  `incident.NewEngineWithGraph` was only consumed by the inline handler). The
  web App still prebuilds the verification engine and shares the platform's
  prebuilt index/graph (memory #55 constraint preserved).
- Verified remaining handlers: `handleV1Analyze/Plan/WhatIf/Impact/Verify/
  Execute/Risk/Incident/Loop/Agents/Memory` all route through `a.taskSvc`.

### 2.5 — Interface consistency tests (P1)

- Existing: `internal/mcp/interface_consistency_test.go`
  `TestCrossInterfaceAnalyzeConsistency` (real MCP kern_analyze JSON-RPC +
  real REST /v1/analyze + CLI service path, all landing in the shared
  authoritative task store) and `internal/app/interface_consistency_test.go`
  (`TestInterfaceConsistencySharedAnalysis`, `TestServiceContracts`).
- **Added** `TestCrossInterfaceIncidentConsistency`: drives real MCP
  kern_incident AND real REST /v1/incidents/investigate with the same alert
  through the shared Incident service, asserting the authoritative Task is
  queryable via a fresh TaskService rooted at the same project. Guards the P2
  gate for the incident workflow specifically (the workflow that had two
  inline violations).

## Tests

- `go vet ./...` — PASS
- `go build ./...` — PASS; `-tags treesitter`, `-tags sqlite` — PASS
- `go test ./internal/web/` — PASS (incl. TestV1ContextEndpoint,
  TestIncidentsEndpoint, TestContextEngineWiredRuntimeAndBoundary,
  TestGovernanceMetricsAvgConfidence)
- `go test ./internal/mcp/` — PASS (incl. TestCrossInterfaceAnalyzeConsistency,
  TestCrossInterfaceIncidentConsistency, TestCrossInterfaceMatchesCLIServicePath)
- Remaining 88 packages — PASS, exit 0
- Live CLI check: `kern incident '{"id":"a1",...}' --root .` → task
  t-1966 COMPLETED, incident ROOT_CAUSE_FOUND via the service path.

## Exit gate

> "No core business workflow exists only in one interface." — MET. Audit
> confirms: analyze/plan/impact/what-if/execute/verify/incident/loop/agents/
> context/risk all exist as shared services and every interface (MCP, CLI,
> REST) calls the same `TaskService` / `Platform` methods; the two inline
> workflows found (CLI incident, REST incident+context) now route through
> services, and cross-interface tests lock analyze + incident.

## Notes / non-changes

- The `App.inc` prebuilt incident engine removal does not regress the shared
  digital-twin startup (memory #55): web.New still prebuilds index/graph once
  and shares them; TaskService.InvestigateIncident builds its incident engine
  from the same platform state.
- `kern_context` / `kern_compact_file` (MCP+CLI) are symbol-lookup tools, not
  the change-analysis Context workflow — out of scope for the P2 gate.