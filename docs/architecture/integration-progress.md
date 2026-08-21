# Kern 2.0 Integration Transformation — Progress Tracker

_Source: `support_docs/KERN_2_0_INTEGRATION_TRANSFORMATION_PLAN.md` (2026-08-21)_
_Audit baseline: HEAD `8ad1089` (pre-integration)_

## Executive Summary

The Integration Transformation Plan's thesis: **stop adding features, vertically integrate existing subsystems around a Task-centered lifecycle with a shared application-services layer.** The audit confirmed the diagnosis — the subsystems all existed but the integrating spine did not. This tracker records the phase-by-phase closure of that gap.

## Phase Status

| Phase | Name | Status | Commit | Summary |
|-------|------|--------|--------|---------|
| 0 | Current-State Audit | ✅ DONE | — | 4-lane parallel explorer audit; gap analysis produced |
| 1 | Task-Centered Application Layer | ✅ DONE | `c9151e2` | `internal/app.Platform` — shared facade, eliminates 3× duplicated orchestration |
| 2 | Make Task Authoritative | ✅ DONE | `fadc88f` | `TaskService` + lifecycle fields + `kern task <id>` |
| 3 | Unify Artifacts | ✅ DONE | `31b2e95` | `ArtifactStore` + linked chain + `kern artifacts` + REST |
| 4 | Standardize Events | ✅ DONE | `9e928a9` | 8 orphan event categories wired to real emitters |
| 5 | Control-Plane Analyze Workflow | ✅ DONE | `090d5ec` | REST /v1/analyze routed through TaskService (was bypass) |
| 6 | Control-Plane Plan Workflow | ✅ DONE | `090d5ec` | domain.Plan (12 spec fields) + TaskService.Plan; CLI/MCP/REST distinct |
| 7 | Control-Plane Impact | ✅ DONE | `090d5ec` | domain.ImpactReport (11 questions) + TaskService.Impact; CLI/MCP/REST |
| 8 | Control-Plane What-If | ✅ DONE | `090d5ec` | REST /v1/what-if routed through TaskService (was bypass) |
| 9 | Governance Everywhere | ✅ DONE | `d251d43` | Mandatory gateway on all paths; fail-closed; approval binding |
| 10 | Agent Runtime | ✅ DONE | `10a34d8` | TaskService.RunWorkflow — workflow engine wired through Task lifecycle + artifacts |
| 11 | Sandboxed Execution | ✅ DONE | `10a34d8` | TaskService.Execute — governance-gated worktree execution + diff artifact |
| 12 | Verification | ✅ DONE | `10a34d8` | TaskService.VerifyTask — verify worktree diff, transition READY_FOR_PR |
| 13 | PR Creation | ✅ DONE | `10a34d8` | TaskService.CreatePR — PR body from artifacts, transition PR_CREATED |
| 14 | Runtime ↔ Code ↔ Agent Correlation | ✅ DONE | `5d728ee` | TaskService.Correlate — deep evidence chain through Task lifecycle |
| 15 | Incident Workflow | ✅ DONE | `5d728ee` | TaskService.InvestigateIncident — wraps incident.Engine through Task |
| 16 | Production Learning | ✅ DONE | `5d728ee` | TaskService.Learn — pattern extraction + evidence-based promotion |
| 17 | Legacy Modernization | ✅ DONE | `5d728ee` | TaskService.Modernize — modernization analyzer through Task lifecycle |
| 18 | MCP | ✅ DONE | `951f4d3` | kern_execute/kern_incident through TaskService; NEW kern_correlate/kern_learn/kern_modernize |
| 19 | CLI | ✅ DONE | `951f4d3` | NEW kern correlate/learn/modernize; all workflows task-native |
| 20 | REST API | ✅ DONE | `951f4d3` | NEW /v1/correlate, /v1/learn, /v1/modernize; all workflows task-native |
| 21 | SDK | ✅ DONE | `c6c5602` | Client methods for Execute/Correlate/Learn/Modernize/Artifacts/Audit |
| 22 | Web Control Console | ✅ DONE | `c6c5602` | /v1/execute + /v1/audit/{task_id}; all task-native workflows exposed |
| 23 | Enterprise Control Plane | ✅ DONE | `7d44bd2` | Shared memory, shared task visibility, multi-repo graph, agent governance |

## Vertical Slices (Proof Points)

| Slice | Description | Status |
|-------|-------------|--------|
| First | "Add caching to UserService" → 20-step lifecycle → PR_CREATED | ✅ PASS (analyze→plan→impact→whatif→verify→createPR tested end-to-end) |
| Second | Incident → correlation → root cause → fix → PR | ✅ PASS (correlate→investigate→learn→modernize tested end-to-end) |
| Third | What-if "split PaymentService" → bounded contexts + impact | ✅ PASS (SplitService + ChangeDependency tested) |

## Architecture Invariants

| # | Invariant | Status | Evidence |
|---|-----------|--------|----------|
| 1 | Interfaces do not contain core business orchestration | ✅ MET | Phase 1: Platform facade; MCP/CLI/REST delegate |
| 2 | All execution passes through governance | ✅ MET | Phase 9: deploy path gated by KERN_ALLOW_DEPLOY; approval workflow wired; Web approve unblocks firewall |
| 3 | Deterministic facts do not originate from LLM guesses | ✅ MET | Graph/impact/risk are deterministic |
| 4 | Every important task action is auditable | ✅ MET | AuditEntry has TaskID; FilterByTask; /v1/audit returns entries+approvals; web approve/reject records identity |
| 5 | Important claims have evidence or are explicitly uncertain | ✅ MET | Claim types FACT/INFERENCE/HYPOTHESIS/RECOMMENDATION |
| 6 | Agent permissions are centrally enforced | ✅ MET | Web approve/reject records approver identity + TaskID in audit log; firewall enforces centrally |
| 7 | Production mutation is disabled by default | ✅ MET | Phase 9: KERN_ALLOW_DEPLOY gate; loop skips deploy without it |
| 8 | Finalized artifacts are immutable | ✅ MET | ArtifactStore.Save rejects overwrite of Status=final artifacts |
| 9 | Event consumers are idempotent | ✅ MET | webhook.Client.Deliver deduplicates by eventID+URL |
| 10 | LLM providers are replaceable | ✅ MET | internal/llm provider abstraction |
| 11 | Existing low-level Kern capabilities remain usable | ✅ MET | All v1 tools preserved |
| 12 | Local-first usage remains available | ✅ MET | cmd/kern + cmd/kern-mcp zero-network wedge |

## Audit Gap Summary (from Phase 0)

### P0 — Integrating spine was absent (CLOSED by Phases 1-2)
- ~~No application-services layer~~ → Platform (Phase 1)
- ~~Task not created for user intents~~ → TaskService (Phase 2)
- ~~Three disjoint orchestration engines~~ → Platform unifies analyze/what-if/verify

### P1 — Governance not a gateway (Phase 9 target)
- Governance bypassed on CLI rename/memory, REST /v1/loop deploy, Web approve
- Approval decorrelated from risk (binds only task+agent+action)
- Fail-open risk default (unmatched → LOW, should be → DENY)
- Production mutation enabled by default
- `Risk.ApprovalRequired`/`Blocked` fields are dead (defined, never assigned)

### P2 — Contracts existed on paper, now wired (Phases 3-4 CLOSED)
- ~~Artifact chain unwired (ParentArtifactID never set)~~ → ArtifactStore + chain (Phase 3)
- ~~~60% of event kinds orphan constants~~ → 8 categories wired (Phase 4)
- Webhook delivery still non-idempotent (Invariant 9 unmet)
- No `GET /v1/audit/{task_id}` route

## Package Map (Post-Phase 4)

```
internal/app/          ← NEW (Phase 1-4): Platform, TaskService, ArtifactStore
  platform.go          # shared facade over engines
  task.go              # TaskService: create/analyze/what-if/verify + artifact chain
  artifact_store.go    # JSON-backed artifact store
  render.go            # CLI output parity helpers
  platform_test.go     # Phase 1 contract test

internal/agent/        # Task type + state machine + workflow engine (Phase 2: lifecycle fields)
internal/verification/ # verification engine (Phase 4: security.finding + architecture.violation events)
internal/loop/         # closed loop (Phase 4: deployment.failed event)
internal/agents/       # specialist pipeline (Phase 4: agent.completed/failed events)
internal/eventbus/     # 55 Kind constants (Phase 4: 8 orphan categories wired)
```
