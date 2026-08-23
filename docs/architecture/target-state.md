# Kern 2.0 — Target-State (Phase 0.5)

The target is a **control plane for AI software engineering**, per KERN 2.0 CANONICAL END-TO-END BUILD SPEC V3. This document maps the target architecture onto the existing repo (integrate/refactor, never greenfield).

## Target loop (from spec §0)
USER INTENT → KERN RUN → INTENT COMPILER → POLICY PRECHECK → WORKFLOW SELECTION → CAPABILITY PLANNING → CONTEXT PLANNING → MINIMUM SUFFICIENT CONTEXT → TASK → ANALYZE → IMPACT → RISK → PLAN → CONSTRAINT VALIDATION → APPROVAL → AGENT ROUTING → SANDBOX → EXECUTE → VERIFY → ARTIFACTS → PR → AUDIT → DEPLOY → OBSERVE → CORRELATE → LEARN → MEMORY ↺

## Target layer stack
```
Interfaces (CLI / MCP / REST / SDK / Web / Events / IDE / Git / CI-CD / K8s / Webhooks)
    ↓  (all call the SAME application layer)
Application / Control Plane  (TaskService + discrete service contracts)
    ↓
Domain  (entities: Task/Artifact/Evidence/Intent/Capability/Risk/Policy/...)
    ↓
Existing Kern Engines  (index, intelligence, context, governance, agent, loop,
                        verification, sandbox, incident, runtime, twin, memory, ...)
    ↓
Infrastructure  (eventbus, storage, cache, llm providers)
```

## 15 platform capabilities (spec §5)
Digital Twin · Code Intelligence · Knowledge Graph · Engineering Memory · Evidence System · Impact/Blast Radius · What-If/Scenario · Intent+Capability Engine · AI Change Firewall/Policy · Multi-Agent Runtime · Sandbox/Execution · Verification Engine · Production Intelligence/Correlation · Incident+Modernization Engineering · AI Governance/Continuous Learning.

## Target gaps vs current
- **Service contracts**: introduce discrete `*Service` interfaces (Phase 2.1) over the `Platform` facade so all interfaces call identical application behavior.
- **Event backbone**: idempotency, retry/dead-letter, persisted replay (Phase 4).
- **Context runtime**: per-item authorization, minimum-sufficient selector, GC completeness, paging, leases, replay, freshness policy (Phase 5).
- **Intent/capability**: registry (with purpose), planner, tool-decision trace, discovery, tool fallback (Phase 6).
- **Tool gateway**: dry-run, explain-deny, unified task scoping (Phase 7).
- **Constitution**: populate provenance, rule suggestions (Phase 8).
- **Orchestration**: richer routing (risk/language/historical), model A/B, agent eval with duration+production outcome (Phase 9).
- **Correlation**: contract (FACTUAL/INFERRED/UNKNOWN), change fingerprint, shared service, canonical chain (Phase 13).
- **Consistency**: conflict-result enum, stale path, explanation (Phase 14).
- **Freshness**: invalidation marker, memory supersession, freshness-in-scoring (Phase 15).
- **Audit/resume/replay**: full reconstruction, replay metadata, run-compare (Phase 16).
- **Benchmarking**: context-quality + task-outcome reports, `kern efficiency` (Phase 17).
- **Web console**: Tasks/Approvals/Risks/Artifacts/Audit pages, engineering views, efficiency/evaluation (Phase 18).
- **Enterprise**: full org/team model (Phase 19.3).
- **Autonomy**: multi-dimension score, full budget dims, all pause triggers, evidence-based learning (Phase 20).

## Non-negotiable invariants (spec §11) — all preserved
Interfaces don't own orchestration · governance mandatory · deterministic facts from LLM guesses · auditable actions · evidence-backed claims · central permission enforcement · production mutation disabled by default · immutable final artifacts · idempotent event consumers · replaceable LLM providers · preserve existing capabilities · local-first · STORE != SEND · OLD != IRRELEVANT · smaller-context != better unless sufficient · authorization-aware context · no cross-task leakage · fail-closed high-risk · shared application layer · gated phase advancement · no future-phase leakage · deterministic test paths.

## Failure modes
- LLM provider unavailability: an ollama/openai/anthropic/google endpoint that is down or rate-limited mid-loop can stall the task. Mitigation: replaceable LLM providers with fallback routing and timeouts; agent degradation fails the task and publishes an event rather than hanging.
- Unparseable governance policy: a malformed `.kern/constitution.yaml` or gateway rule must not be silently skipped. Mitigation: fail-closed on load/parse errors — deny the operation and record the denial in the audit log.
- Event consumer retry/dead-letter exhaustion: a poisoned consumer can wedge the bus if retries are unbounded. Mitigation: idempotent consumers with bounded retry and dead-lettering so a bad message never crashes the bus or blocks the queue.
- Cross-engine inconsistency: index, intelligence, and governance can disagree on a fact (e.g. the graph still shows a file governance has blocked). Mitigation: consistency conflict-result enum + explanation, resolved before approval/execution.
- Stale/unknown freshness: a context or memory item served from cache can be STALE relative to its source version. Mitigation: freshness policy, invalidation markers, and memory supersession keep stale data out of scoring and planning.
- Mutation of a final artifact: writing to a finalized artifact after approval must be rejected. Mitigation: immutable final artifacts enforced at the artifact layer; a write errors and is audited.