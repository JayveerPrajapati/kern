# Kern 2.0 — Domain Model (Phase 0.5)

Canonical entities. Current fields live in `internal/domain/*.go`; gaps annotated.

## Task (`domain.Task`, `agent.Task`)
task_id, intent, state, workflow, agent_refs, context_ref, impact_ref, risk_ref, plan_ref, artifact_refs, verification_ref, pr_ref.
**Gap (P1.1):** add project, repository, scope, requester; convert inline objects → `*_ref` IDs (memory_ref, policy_ref, approval_ref, deployment_ref, outcome_ref).

## State machine (17 states) — CURRENT (P1.2)
CREATED ANALYZING PLANNING WAITING_FOR_APPROVAL APPROVED EXECUTING VERIFYING READY_FOR_PR PR_CREATED DEPLOYING OBSERVING COMPLETED | FAILED BLOCKED REJECTED CANCELLED ROLLED_BACK. Explicit `validTransitions` map.

## Artifact (P3.1) — CURRENT
id, kind, type, task_id, created_by, created_at, version, status, scope, provenance, URI, digest, parent_artifact_id, related_entities. Immutable when final (P3.5). Replay by ParentArtifactID (P3.6).
**Gap (P3.4):** relation types derived_from / supports / contradicts.

## Evidence / Claim (P3.2) — CURRENT
Claim types FACT / INFERENCE / HYPOTHESIS / RECOMMENDATION. Evidence sources graph/test/build/git/runtime/memory/policy.

## CompiledIntent (P6.1) + IntentType taxonomy (P6.2) — CURRENT
UNDERSTAND, CODE_CHANGE, REVIEW, WHAT_IF, INCIDENT, MODERNIZATION, SECURITY, TEST, DEPLOY, AUDIT.

## RunResult (P6.5) — CURRENT
Task, workflow, capabilities, tools, agents, risk, approval_state, next_action.

## Capability (P6.6) — PARTIAL
name, inputs, dependencies, tools, permissions, risk, outputs. **Gap:** add `purpose`.

## TaskBoundary (P7.2) + SafetyBudget (P7.4) — CURRENT
Allowed/denied paths; MaxFiles/MaxServices/MaxRisk/MaxTokens/MaxCost/MaxRuntime → PAUSE.

## Constitution (P8) — CURRENT
ConstraintType MUST/MUST_NOT/SHOULD/SHOULD_NOT; PlanViolation.Provenance field exists but unpopulated (P8.4).

## Consistency (P14) — PARTIAL
ConsistencyConflict{ClaimA,ClaimB,SourceA,SourceB}; **gap:** ConflictResult enum NO_CONFLICT/CONFLICT/STALE/UNKNOWN (P14.2) + explanation field (P14.4).

## Freshness (P15) — CURRENT
FreshnessRecord{CreatedAt,ObservedAt,SourceVersion,State}; FreshnessState FRESH/STALE/UNKNOWN.

## Context (P5) — CURRENT partial
ContextClass (15) + ContextState (5: ACTIVE/WARM/COLD/ARCHIVED/DROPPED). **Gaps:** authorization, paging, leases, replay.

## Correlation (P13.2) — MISSING
**Gap:** contract with source/relationship(FACTUAL/INFERRED/UNKNOWN)/confidence/timestamp/provenance. Change fingerprint (P13.4).

## Result contract (P9.3) — CURRENT
task_id, agent, status, result, evidence, risks, confidence, artifacts, recommended_action.

## Event (P4.1) — CURRENT
id, kind, occurred_at, source, service, project_id, repository_id, task_id, agent_id, provenance, event_version, entity_refs, payload. **Gaps:** idempotency (P4.3), retry/DLQ (P4.4), persisted replay (P4.5).

## Failure modes
- Invalid task transition: moving a task between the 17 states must be checked against the explicit `validTransitions` map; an illegal jump (e.g. APPROVED → PLANNING) corrupts the workflow. Mitigation: reject invalid transitions with an error; only the map-driven paths are allowed.
- Final artifact immutability: writing to a finalized artifact (status final) must not silently mutate the record. Mitigation: enforce immutability at the artifact layer — overwriting a final artifact errors, preserving provenance and digest for audit.
- Memory supersession: a newer memory/context version can supersede an older one; if supersession is applied inconsistently, planning reads stale guidance. Mitigation: apply supersession + freshness (STALE) rules so superseded memory is excluded from scoring and context.
- Digest collision / mismatch: a changed artifact whose digest no longer matches the recorded one can go unnoticed and poison replay/compare. Mitigation: recompute and verify digest on read/replay; mismatch flags the artifact and blocks blind replay.
- Capability registration drift: a Capability with a missing required tool or unpopulated `purpose` can be invoked with incomplete inputs. Mitigation: validate dependencies/tools at registration and populate `purpose` so planning routes correctly.
- Unpopulated provenance: PlanViolation.Provenance and other gap fields are defined but empty, so a claim can lack its evidence trail. Mitigation: populate provenance at write time so decisions are auditable and evidence-backed.