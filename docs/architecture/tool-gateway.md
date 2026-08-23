# Tool Gateway — internal/governance, internal/domain, internal/app

## Purpose
Every governed tool call passes through one unified gateway that evaluates
identity → task → resource → action → permission → risk → policy → approval →
budget before allowing or denying. The gateway is fail-closed, logs every
decision to the audit trail, and exposes a dry-run path and an explain-deny
object so callers never parse error strings.

## Unified gateway — `internal/governance/gateway.go`
- `ToolGateway` (`gateway.go:17`) wraps a `firewall.Firewall` and an optional
  audit callback. `NewToolGateway(fw)` constructs it.
- `Evaluate(agentID, taskID, resource, action, boundary, budget)`
  (`gateway.go:33`) checks: (1) task boundary (`boundary.CheckPath`), (2)
  firewall policy (`firewall.Check` → allowed/risk/approval), (3) safety budget
  (`budget.Exceeded()`). It returns `allowed, risk, approval, err` and logs
  `ALLOW`/`DENY`/`PAUSE` to the audit callback.
- `EvaluateScoped(agentID, taskID, resource, action, env, scope, budget)`
  (`gateway.go:69`, P7.3) takes a unified `TaskScope` (paths + envs + services)
  and runs the same pipeline, returning a structured `GatewayResult` with a
  `DenyReason` object (P7.6) carrying the failing `Stage`
  (`"env"`/`"boundary"`/`"firewall"`/`"budget"`). A budget breach returns
  `DecisionPaused`; env/path/firewall denials return `DecisionDenied`.
- `DryRun(...)` (`gateway.go:124`, P7.5) evaluates on a cloned budget so dry
  runs never advance live usage, letting callers predict the decision before
  running.

## Boundaries & scope — `internal/domain/boundary.go`
- `TaskBoundary` (`boundary.go:10`): `AllowedPaths`, `DeniedPaths`,
  `AllowedEnvs`. `CheckPath(path)` (`boundary.go:21`) allows a path iff it
  matches an `AllowedPaths` prefix AND no `DeniedPaths` prefix; empty allowlist
  = allow-all (except denied).
- `TaskScope` (used by `EvaluateScoped`) additionally gates environments via
  `CheckEnv`.
- `SafetyBudget` (`boundary.go:52`): `MaxFiles`, `MaxServices`, `MaxRisk`,
  `MaxToolCalls`, `MaxToolCallsByKind`, `MaxExternalCalls`, `MaxTokens`,
  `MaxCost`, `MaxRuntime`, `AllowedEnvs`, `CurrentEnv`. `DefaultSafetyBudget()`
  (`boundary.go:83`) returns a conservative default. `Exceeded()`
  (`boundary.go:152`) reports the first breached limit (including per-kind tool
  caps and env allow-list); exceeding a limit causes the gateway to **PAUSE**
  rather than proceed.

## Exec gating (fail-closed) — `internal/governance/exec/exec.go`
- `CheckExec(toolName...)` (`exec.go:46`) is the single governance gate the
  host-command tools (`kern_exec`, `kern_sandbox`, `kern_execute`) pass through;
  it fails closed. Three gates apply:
  1. An empty `KERN_TOOLS` allowlist means "all tools allowed", so exec is
     refused unless `KERN_ALLOW_EXEC=1` opts in (`execAllowlistGate`,
     `exec.go:71`).
  2. When `KERN_TOOLS` is set, the named exec tool must appear in it.
  3. The change firewall authorizes `command.execute`; a HIGH/CRITICAL risk
     fails closed with an approval-required error.
- `RequestExecApproval(wf, toolName...)` (`exec.go:116`) drives the human-
  approval workflow, returning a pending `Approval` for HIGH/CRITICAL commands
  (fails closed if no workflow is configured). `ResumeExecApproval(wf, id)`
  (`exec.go:147`) only proceeds when the approval is `"approved"`.
- Risk severity of `command.execute` is operator-configurable via `KERN_EXEC_RISK`
  (default `MEDIUM`; `HIGH`/`CRITICAL` makes it approval-gated).

## Unified task policy — `internal/app/task.go`
- `authorizeResource(ctx, taskID, resourceKind, action, value)` (`task.go:526`,
  P7.3) is the single policy checkpoint for every resource access — context,
  memory, artifact, or runtime — applying the **same** `TaskScope` uniformly
  (`TaskScope`/`SetTaskScope`, `task.go:496-514`). A value outside the task's
  path/environment scope is denied across all resource kinds: exactly one
  boundary, not four. `actionEnv` (`task.go:542`) extracts an env from an
  action like `"read:production"` so the env gate applies on any resource kind.
- `TaskService.PolicyPrecheck` (`task.go:356`) is the read-only pre-execution
  gate (see intent-engine.md).

## Dependencies
- `internal/governance/firewall` (policy/risk/approval), `internal/governance/
  identity`, `internal/governance/risk`, `internal/governance/approval`,
  `internal/governance/audit`, `internal/domain` (boundaries, scopes).

## Storage / security
- Every evaluation is logged to the audit log (`logAudit`, `gateway.go:135`);
  decisions are `ALLOW`/`DENY`/`PAUSE`. Fail-closed: any error/denial refuses
  execution, and HIGH/CRITICAL commands require human approval.

## Failure modes
- Out-of-scope resource → denied with a structured `DenyReason` at the
  `"boundary"`/`"env"` stage.
- Budget breach → `PAUSE` (not proceed).
- Exec without allowlist/allow-flag → refused.
- Approval required but no workflow → fail closed (never runs).

## Tests
- `gateway_test.go`, `exec_test.go`, `firewall_test.go`, `approval_test.go`,
  `audit_test.go`, `internal/domain/boundary_test.go`,
  `internal/app/task_policy_scope_test.go`
  (`TestUnifiedTaskPolicyScope`).

## Performance / trade-offs
- Every governed call pays an O(1) path + policy + budget check; cost is
  negligible. Unified scoping reduces the surface area from four boundaries to
  one but requires the task scope be set once up front (`SetTaskScope`).