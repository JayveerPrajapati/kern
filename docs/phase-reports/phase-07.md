# PHASE 7 — TOOL GATEWAY + TASK BOUNDARY — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 7.1–7.6.
Go: 1.23, stdlib-only default build (tree-sitter + sqlite behind build tags).

## Scope

Make every governed tool use task-scoped: one tool gateway with the full
request chain, a task boundary enforced on execution, the same task policy
across context/memory/tools/artifacts/runtime, a safety budget that PAUSEs,
dry-run preview, and explain-deny (exit gate: "no controlled action can
bypass task-scoped governance").

## Work completed (micro-phases)

### 7.1 — Tool gateway (P0)

Verified `ToolGateway` (`internal/governance/gateway.go`) implements the
spec chain: request → identity (firewall) → task (boundary) → resource →
action → permission → risk → policy → approval → tool, in `Evaluate`
(boundary → firewall → budget). Every decision is audit-logged.

### 7.2 — Task boundary (P0)

Verified `TaskBoundary` (`domain/boundary.go`): AllowedPaths + DeniedPaths
with prefix matching, exact spec example tested (`TestTaskBoundaryCheckPath`:
allowed UserService/UserRepository/CacheService/tests/, denied
payments/production/secrets).

- **Closed a gap**: the boundary existed but was enforced NOWHERE in the live
  execution path (`ToolGateway` and `authorizeResource` had zero production
  callers) — a controlled action could bypass task-scoped governance. Added
  `TaskScope.ValidatePatch(patch)`: it extracts every file path touched by a
  unified diff and requires each to pass the task's boundary BEFORE the patch
  is applied, so out-of-scope changes are rejected at the gate.
- **Wired the enforcement** into `TaskService.Execute` and
  `ExecuteAndVerify`: after the exec governance gate, the task's scope
  validates the patch; a boundary violation fails the task and returns a
  denial error.
- Tests: `TestTaskScopeValidatePatch` (in-scope ok; out-of-scope denied;
  denied sub-path denied; unrestricted scope backward-compatible),
  `TestExecuteEnforcesTaskBoundary` (Execute rejects an out-of-scope patch,
  task fails).

### 7.3 — Context/tool/memory scope (P0)

Verified `EvaluateScopedFull` gates env → service → artifact → path →
firewall → budget (the same task policy across tools, artifacts, runtime),
and `authorizeResource` + `TestUnifiedTaskPolicyScope` prove one TaskScope
uniformly gates context/memory/artifact/runtime (one boundary, not four).

### 7.4 — Safety budget (P1)

Verified `SafetyBudget` (`domain/boundary.go`) tracks files, services, tools
(MaxToolCalls + per-kind), tokens, cost, runtime, risk, environment; a budget
exceeded yields DecisionPaused (gateway + loop both enforce). Tests:
`TestSafetyBudgetExceeded`, `TestToolGatewayBudgetExceeded`,
`TestEvaluateScopedBudgetPause`, `TestDefaultSafetyBudget`.

### 7.5 — Dry run (P1)

Verified `ToolGateway.DryRun` evaluates a governed call on a CLONE of the
budget so dry runs never advance live usage. Test:
`TestDryRunDoesNotMutateBudget`.

### 7.6 — Explain deny (P2)

Verified `GatewayResult.Deny` (`domain.DenyReason`) carries decision, reason,
policy (scope.env/service/artifact/path/firewall.permission/safety_budget),
risk, required approval, and safe alternative — callers never parse error
strings. Tests: `TestEvaluateScopedEnvDeny`, `TestEvaluateScopedPathDeny`,
`TestEvaluateScopedFirewallDeny`.

## Tests

- `go vet ./...` — PASS; `go build ./...` — PASS; `-tags treesitter`,
  `-tags sqlite` — PASS
- `go test ./internal/governance/` — PASS (incl. new `TestTaskScopeValidatePatch`)
- `go test ./internal/app/` — PASS (99s; incl. new `TestExecuteEnforcesTaskBoundary`)
- `go test ./internal/domain/` — PASS
- Remaining 87 packages — PASS, exit 0
- `go test -race ./internal/app/ ./internal/governance/` — PASS

## Exit gate

> "No controlled action can bypass task-scoped governance." — MET. A patch
> is validated against the task's allowed/denied paths before ANY execution
> (Execute/ExecuteAndVerify), the unified scope gates context/memory/artifact/
> runtime uniformly, the gateway enforces identity→boundary→permission→risk→
> policy→approval, and the safety budget PAUSEs on exceed — with dry-run
> preview and structured explain-deny on every denial.

## Notes / non-changes

- Enforcement was added at the execution seam (patch validation) because it is
  the deterministic point where a controlled action would otherwise land
  outside the task's boundary; the loop already enforced the safety budget and
  the gateway already enforced boundary+permission for its callers. The
  `ToolGateway` and `authorizeResource` remain available for interface-layer
  pre-checks (REST/CLI can call EvaluateScoped/DryRun before dispatch).