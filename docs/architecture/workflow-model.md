# Kern 2.0 — Workflow Model (Phase 0.5)

Five primary user workflows (spec §4) mapped onto the current implementation.

## 4.1 UNDERSTAND ("Explain this service")
- **Intent:** UNDERSTAND
- **Current path:** `kern search/graph/explore/probe` (intelligence) + `kern context` + `kern memory recall` + `kern buddy`.
- **Gaps:** runtime relevance + incident history not folded into a single "explain" response; no unified evidence/confidence surface (needs correlation contract P13.2).

## 4.2 SAFELY CHANGE ("Implement this feature")
- **Intent:** CODE_CHANGE
- **Current path:** `kern run` → `TaskService.Run` → Intent → Workflow → Context → Memory → Impact → Risk → Policy → Plan → Approval → Agent (pipeline) → Sandbox → Execute → Verify → Artifacts → PR → Audit → (Deploy → Observe).
- **Status:** 20-step vertical slice (vertical_slice_test.go) + test matrix A,B,D,F,I,J pass. Gap: RiskReport artifact, full fixture, consolidated failure drill (P10).

## 4.3 PREDICT / WHAT-IF ("What happens if I change this?")
- **Intent:** WHAT_IF
- **Current path:** `kern what-if` → `whatif.Impact` (facts/files/architecture/memory/risk/confidence/summary) + `intelligence` blast radius.
- **Gap (P12.1):** SimulateRender omits architecture/memory/confidence/limitations; Task/Artifact/Evidence-awareness (P12 exit gate).

## 4.4 OPERATE / INCIDENT ("Why is production failing?")
- **Intent:** INCIDENT
- **Current path:** `kern incident` → runtime correlate → incident engine root-cause (hypothesis/evidence/confidence) → ApplyAndVerifyFix → approval → PR.
- **Gaps (P11):** fix pipeline lacks explicit risk step; learning not wired to incident; needs correlation contract.

## 4.5 GOVERN AI ENGINEERING ("What did AI change and why?")
- **Intent:** AUDIT
- **Current path:** `kern audit` (flight recorder + audit log) + `kern task <id>` + artifact replay.
- **Gaps (P16):** resume/replay not full reconstruction; no run-compare in CLI/web.

## Workflow selection (P6.3) — CURRENT
`app.SelectWorkflow` maps intent→workflow (A–E). Dynamic pipeline variants via `agents.SelectPipeline`/`SelectWorkflow` (CODE_CHANGE/DOCUMENTATION/INCIDENT/MODERNIZATION/DEFAULT), each governance-preserving (approval gate kept).

## Exit-gate mapping (spec per-phase gates)
- P1: task lifecycle works without MCP/CLI-specific state — CURRENT.
- P2: no core workflow exists only in one interface — CURRENT (all route through services); P2.5 equivalence test to add.
- P3: no important output exists only in-memory — CURRENT (ArtifactStore persists).
- P4: task lifecycle event-observable + retry-safe — PARTIAL (idempotency/DLQ missing).
- P5: same task demonstrates less irrelevant context, no unauthorized context — PARTIAL (authorization missing).
- P6: broad request → `kern_run()` without manual sequencing — CURRENT.
- P7: no controlled action bypasses task-scoped governance — CURRENT.
- P8: mandatory rules block plan pre-execution — CURRENT.
- P9: Kern selects/coordinates agent team — CURRENT.
- P10: entire workflow passes with no governance bypass — PARTIAL (fixture + PR assertions).
- P11: controlled incident → verified remediation PR — PARTIAL (risk step).
- P12: what-if + modernization are Task/Artifact/Evidence-aware — PARTIAL.
- P13: deployment traced back to Task/PR/commit/symbol — PARTIAL (contract missing).
- P14: conflicting knowledge never silently collapsed — PARTIAL (enum missing).
- P15: stale knowledge not presented as current — PARTIAL (supersession missing).
- P16: tasks survive restart + resume — PARTIAL (full reconstruction).
- P17: reproducible benchmark suite — PARTIAL (efficiency report missing).
- P18: human inspects/approves Task through UI — PARTIAL (task detail page yes, approvals page JSON-only).
- P19: MCP/CLI/REST/SDK use same services — CURRENT.
- P20: autonomy passes failure/security/rollback/budget/policy-bypass tests — PARTIAL.