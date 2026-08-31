# PHASE 6 — INTENT ENGINE + KERN RUN — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 6.1–6.10.
Go: 1.23, stdlib-only default build (tree-sitter + sqlite behind build tags).

## Scope

Let the external agent call one high-level entry point: an intent compiler,
intent taxonomy, workflow selector, policy precheck, kern_run, capability
registry/planner, tool-decision trace, discovery, and tool fallback (exit
gate: "a broad engineering request can be passed to kern_run() without the
external agent manually sequencing Kern internals").

## Work completed (micro-phases)

### 6.1 — Intent compiler (P0)

Verified `CompileIntent` (`internal/app/intent.go`) produces exactly the spec
JSON shape: intent_type, objective, target, scope, environment,
desired_outcome (+ raw_text) on `domain.CompiledIntent`. Keyword-driven,
deterministic. Tests: `TestCompileIntentClassification`,
`TestCompileIntentFields`, `TestCompileIntentEnvironmentDerived`,
`TestCompileIntentProductionNotUnconditionalIncident`.

### 6.2 — Intent taxonomy (P0)

Verified all 10 intent types (`domain.IntentType`): UNDERSTAND, CODE_CHANGE,
REVIEW, WHAT_IF, INCIDENT, MODERNIZATION, SECURITY, TEST, DEPLOY, AUDIT.

### 6.3 — Workflow selector (P0)

Verified `SelectWorkflow(it)` maps each intent type to one of the 5 primary
user workflows (A-E), plus the task-type-driven `agents.SelectWorkflow(kind)`
for specialist pipelines (both preserving the human approval gate). Tests:
`TestSelectWorkflow`, `TestSelectWorkflowPreservesApprovalGate`.

### 6.4 — Policy precheck (P0)

Verified `TaskService.PolicyPrecheck` unifies the five gates (identity via
firewall, scope/path, permission, environment, preliminary risk) into a single
ALLOW/DENY `PrecheckResult` with `DenyReason`. Tests:
`TestPolicyPrecheckPass`, `TestPolicyPrecheckDenyPermission`,
`TestPolicyPrecheckDenyEnv`, `TestRunSurfacesPrecheck`,
`TestRunNextActionReflectsDeniedPrecheck`.

- **Closed a gap**: the precheck's identity gate failed for the DEFAULT
  control-plane operator. `kern run` / kern_run use TaskService's default
  agent ID "kern", which was never registered on the Platform firewall — so
  every kern_run precheck returned "denied: unknown agent". Registered the
  "kern" operator identity on the Platform firewall (repository/source read+
  write, repository scan/audit, security scan, tests/config/documentation
  write, context read/write). DEPLOY stays gated by KERN_ALLOW_DEPLOY + the
  deploy firewall rules — not granted to the operator. Live-verified all 10
  intent types now clear the precheck ("next: execute workflow"). New test:
  `TestKernRunOperatorClearsPrecheck` (all 10 intent types through a real
  Platform).

### 6.5 — kern_run (P0)

Verified `TaskService.Run` returns all 8 spec outputs on `domain.RunResult`:
Task (TaskID), workflow, capability plan (Capabilities), context plan
(ContextPlan), risk, agent plan (Agents), approval state (ApprovalState), next
action (NextAction) + Precheck. CLI `kern run` (cmd_review.go:603) and MCP
kern_run both delegate to it. Live-verified.

### 6.6 — Capability registry (P1)

Verified `CapabilityRegistry` + `allCapabilities()`: each `domain.Capability`
carries name, purpose, inputs, dependencies, tools, permissions, risk,
outputs, artifacts (all 9 spec fields). `NewCapabilityRegistry` seeded with
the full catalog; `Get`/`All`/`Tools`. Tests: `TestCapabilityRegistryGet`,
`TestCapabilityRegistryDiscovery`.

### 6.7 — Capability planner (P1)

Verified `DefaultCapabilities(intentType)` selects only the capabilities the
intent needs (analyze/plan/impact/execute/verify/pr for CODE_CHANGE, etc.),
plus `DeterministicPlan` (rule-based plan when no LLM provider) and the
internal/planner LLM path. Tests: `TestDefaultCapabilities`,
`TestDeterministicPlan`, `TestCapabilitiesToTools`, `TestCapabilitiesToAgents`.

### 6.8 — Tool decision trace (P1)

`ToolDecisionTraceRecorder` + `domain.ToolDecisionTrace` (tool, why_selected,
expected_output, actual_output, cost, latency) existed but had ZERO production
callers.

- **Closed the gap**: wired the recorder into `TaskService.RunWorkflow` — every
  workflow step records a trace (tool from `toolForAction`, why selected, input,
  expected/actual output, latency). `WithTraceRecorder` attaches it. New test:
  `TestRunWorkflowRecordsToolDecisionTraces`.

### 6.9 — Capability discovery (P2)

Verified `Registry.Discover` (semantic/lexical ranked matching →
`CapabilityMatch` with relevance score + matched fields) + `Search` helper.
Tests: `TestCapabilityDiscoverRanked`, `TestCapabilitySearchHelper`,
`TestDiscoverMatchesSurface`.

### 6.10 — Tool fallback (P2)

`FallbackFor` (kern_what_if→kern_impact, kern_validate→kern_verify,
kern_loop→kern_plan, kern_heal→kern_validate) existed but was dead code.

- **Closed the gap**: wired it into the MCP tool gate (`runTool` in
  server.go): when KERN_TOOLS blocks a tool, a call reroutes to its
  policy-approved alternative when that alternative is allowed; fails closed
  when the alternative is also blocked. The dispatch-switch default branch
  was deliberately NOT used (every catalog tool has a case; the allowlist gate
  is the realistic restricted-deployment path). New tests:
  `TestToolFallbackAllowlistReroutes` (blocked kern_what_if → reroutes to
  kern_impact, IMPACT output), `TestToolFallbackFailsClosedWhenAlternativeBlocked`.

## Tests

- `go vet ./...` — PASS; `go build ./...` — PASS; `-tags treesitter`,
  `-tags sqlite` — PASS
- `go test ./internal/app/` — PASS (100s; incl. new
  `TestKernRunOperatorClearsPrecheck`, `TestRunWorkflowRecordsToolDecisionTraces`)
- `go test ./internal/mcp/` — PASS (37s; incl. new tool-fallback tests)
- Remaining 88 packages — PASS, exit 0
- `go test -race ./internal/app/ ./internal/mcp/` — PASS

## Exit gate

> "A broad engineering request can be passed to kern_run() without the external
> agent manually sequencing Kern internals." — MET and live-verified:
> `kern run "Add tenant-aware caching to UserService"` returns task +
> workflow + caps + tools + agents + risk + approval + next action through one
> entry point, with the precheck cleared for the default operator and the
> tool-selection trail recorded.

## Notes / non-changes

- Registered the "kern" operator with explicit permissions rather than a
  wildcard, so governance stays meaningful: DEPLOY is still separately gated,
  and an unknown agent still fails closed
  (`TestRunNextActionReflectsDeniedPrecheck`).
- `kern_validate` turned out to be a registered tool (catalog parity), so the
  switch-default fallback can never fire in the current catalog — the
  allowlist gate is the correct P6.10 wiring point.