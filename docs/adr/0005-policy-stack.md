# ADR-0005: internal/governance is the long-term policy core; blueprint's stack is frozen

- Status: Accepted (2026-09-08)
- Deciders: maintainer (ADR review with orchestrator)
- Scope: policy, approval, audit stacks in internal/governance (+fw) vs
  internal/blueprint (34k LOC)

## Context

kern carries two parallel policy/approval/audit stacks (gap F4,
2026-09-08 audit):

- internal/governance (+ internal/fw): the runtime control plane —
  firewall, approval workflow, tamper-evident audit log, exec gating,
  authorize-context.
- internal/blueprint: the CI-side change firewall — checks, gates
  (g2/g3/g5/g12), verdict cache, receipt verification.

Both implement policy evaluation, approvals, and audit trails with
different models. Extending both doubles every feature; folding one
into the other is a large migration.

## Decision

**internal/governance is the long-term core. blueprint's policy stack is
FROZEN at its current scope.**

1. New governance capabilities (policy evaluation, approval flows, audit
   semantics) land in internal/governance only.
2. blueprint keeps its CI-side checks and gates as the consumer of the
   governance core: where blueprint needs approval/audit semantics it
   delegates to internal/governance (as its adapters/kern package already
   shells out to the kern binary) instead of growing its own.
3. No new features on blueprint's own policy/approval/audit machinery;
   bug fixes and gate correctness work continue as normal.
4. Any eventual consolidation is one-directional: blueprint's checks
   outlive as CHECKS (they are good), blueprint's policy/approval/audit
   machinery does not grow and may be reduced in favor of governance's.

## Consequences

- One place to reason about approvals, denials, and audit truth — the
  governance chain (which the web console, CLI approve/audit, MCP, and
  enterprise already read).
- blueprint stays valuable as the change-firewall CI runner; its gate
  tests and receipt verification keep running (and now fail-not-skip in
  CI per commit 0286475).
- A future full merge remains possible but is NOT scheduled by this
  ADR; freezing removes the pressure to decide it now.
