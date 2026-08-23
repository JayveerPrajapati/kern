# Kern 2.0 — Gap Analysis (Phase 0.4)

Classification: CURRENT / PARTIAL / MISSING. Baseline frozen at `894fb10`.

## Fully CURRENT (no work needed)
1.2 Task state machine (17 states + explicit transitions) · 1.3 Persistence · 1.6 Human takeover · 1.7 Task snapshot
2.2 MCP→services · 2.3 CLI→services · 2.4 REST→services
3.1 Artifact contract · 3.2 Evidence contract · 3.3 Persistence · 3.5 Immutability · 3.6 Replay
4.1 Event envelope · 4.2 Event names (62 kinds)
5.1 Context taxonomy · 5.2 Lifecycle states · 5.6 Tool-output normalization · 5.7 Context snapshots
6.1 Intent compiler · 6.2 Intent taxonomy · 6.3 Workflow selector · 6.5 kern_run
7.1 Tool gateway · 7.2 Task boundary · 7.4 Safety budget
8.1 Constraint model · 8.2 Constitution YAML · 8.3 Plan validator
9.1 Agent lifecycle · 9.2 Specialist roles (7) · 9.3 Result contract · 9.6 Shared memory (authorized)
10.3 Full 20-step vertical slice
11.1 N+1 incident · 11.2 Correlation · 11.3 Root cause
14.1 Consistency model (7 sources)
15.1 Freshness metadata · 15.2 Freshness state
16.1 Flight recorder
17.1 Benchmark repos · 17.2 Benchmark task classes
19.1 REST · 19.2 Go SDK · 19.4 Py/TS SDK
20.2 Autonomy levels L0-L5
Test matrix A-J (all 10)
Build/vet/tests clean; MCP 77 tools
All 16 former MISSING items (4.3, 4.4, 5.4, 5.10-5.12, 6.9-6.10, 7.5-7.6, 8.5, 13.2, 13.4, 14.2, 15.4, 17.6) — verified CURRENT, see table below

## PARTIAL (exists, tighten)
1.1 Task aggregate (no project/repo/scope/requester, refs are inline objects not *_ref IDs)
1.4 No dedicated Pause (only BLOCKED/takeover)
1.5 Retry no attempt counter/reason/prev-new result
2.1 No 15 discrete service interfaces (Platform facade instead)
2.5 No MCP↔CLI↔REST equivalence test
3.4 Artifact links: parent only (no derived_from/supports/contradicts)
4.5 Event history in-memory cap 100, no persisted replay
5.3 Min-sufficient selector (no explicit memory/freshness) · 5.5 GC missing last_used/dependency_distance/task_relation · 5.8 dedup only in GC · 5.9 freshness not full policy
6.4 Policy precheck (no identity/scope/env) · 6.6 registry no purpose field · 6.7 planner static · 6.8 tool-decision-trace unwired
7.3 Unified task scoping (context/runtime/artifacts not unified)
8.4 Constitution provenance not populated
9.4 Routing by kind only (no risk/language/historical) · 9.5 model routing no language/historical · 9.7 eval duration no-op · 9.8 agent A/B no model A/B
10.1 fixture not on disk (inline consts) · 10.2 flagship drives "NewServer" not exact request · 10.4 RiskReport/Diff/PR/Audit artifacts not asserted · 10.5 failures scattered not consolidated · 10.6 metrics in stats/bench not per-lifecycle
11.4 Fix pipeline lacks risk step · 11.5 learning not wired to incident
12.1 what-if output omits arch/memory/confidence/limitations · 12.2 modernization lacks ownership/deps · 12.3 one task not phase-tasks · 12.4 no candidate/migration visualization
13.1 Correlation chain lacks Trace/Event link · 13.3 not a single shared service
14.3 no stale-detection path · 14.4 no explanation/resolution field
15.3 no invalidation marker · 15.5 freshness only in GC scoring
16.2 resume not full reconstruction · 16.3 replay missing repo-version/model/config · 16.4 run-compare absent
17.3 only token reduction measured · 17.4 no ratio/sufficiency report · 17.5 no outcome report
19.3 enterprise org/team partial (now CURRENT — see Phase 19 roadmap ✅)
20.1 autonomy score = policy-risk only · 20.3 budget no tool-count/env · 20.4 only budget pause trigger · 20.5 learning not autonomy-level (all now CURRENT — see Phase 20 roadmap ✅)

## MISSING — none (all 16 items implemented; verified 2026-08-23)
| ID | Item | Impl (location) |
|----|------|------|
| 4.3 | Event idempotency (dedup on Publish) | ✅ `eventbus.EnableIdempotency` (eventbus.go:186) |
| 4.4 | Event retry / dead-letter queue | ✅ `eventbus.SetRetryPolicy`+`SubscribeDeadLetter` (eventbus.go:467/483) |
| 5.4 | Context authorization (per-item) | ✅ `context.AuthorizeItems` (phase5.go:41) |
| 5.10 | Context paging | ✅ `context.PageItems` (phase5.go:81) |
| 5.11 | Context leases | ✅ `context.LeaseManager` (phase5.go:119) |
| 5.12 | Context replay engine | ✅ `context.Replay` (phase5.go:187) |
| 6.9 | Capability discovery | ✅ `app.CapabilityRegistry` (capability.go) |
| 6.10 | Tool fallback | ✅ `app.FallbackFor` (capability.go:18) |
| 7.5 | Gateway dry run | ✅ `governance.ToolGateway.DryRun` (gateway.go:124) |
| 7.6 | Explain-deny object | ✅ `domain.DenyReason` (boundary.go:206) |
| 8.5 | Rule suggestions (non-activating) | ✅ `governance.SuggestRules` (constitution.go:194) |
| 13.2 | Correlation contract (FACTUAL/INFERRED/UNKNOWN) | ✅ `runtime.Correlation.Contract` (correlation_contract.go:44) |
| 13.4 | Change fingerprint | ✅ `runtime.Fingerprint`/`ChangeFingerprint` (correlation_contract.go:128) |
| 14.2 | Conflict-result enum (NO_CONFLICT/CONFLICT/STALE/UNKNOWN) | ✅ `domain.ConflictResult` (consistency.go:17) |
| 15.4 | Memory supersession (current/historical/superseded) | ✅ `memory.Supersede` (store.go:132) |
| 17.6 | `kern efficiency <task-id>` | ✅ `kern task efficiency <id>` (cmd_task.go:51) |

## Docs (all MISSING, now being created)
docs/architecture/{current-state,target-state,gap-analysis,capability-inventory,domain-model,workflow-model,event-model,artifact-model,context-runtime,intent-engine,tool-gateway,agent-orchestration,runtime-correlation,testing-model}.md + docs/roadmap.md + docs/adr/0001-kern-control-plane.md

## Test matrix A-J — all CURRENT
A normal change · B high-risk approval · C what-if split · D incident→PR · E resume · F policy DENY · G pruning · H sufficiency · I isolation · J agent routing.

## Recommended execution order (spec: strict phase order)
Follow Phase 1→20 micro-phase order; the MISSING/priority items above are the concrete work. Docs first (this phase).