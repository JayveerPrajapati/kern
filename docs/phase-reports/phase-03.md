# PHASE 3 — ARTIFACT + EVIDENCE — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 3.1–3.6.
Go: 1.23, stdlib-only default build (tree-sitter + sqlite behind build tags).

## Scope

Make all meaningful workflow outputs persistent and auditable: a typed
artifact contract, an evidence-claims model, persistence across restart,
typed links, finalized immutability, and replay (exit gate: "no important
workflow output exists only as an in-memory return value").

## Work completed (micro-phases)

### 3.1 — Artifact contract (P0)

- Verified all 18 spec types exist as typed structs in `internal/domain`
  (`artifact_reports.go` + pre-existing types): ContextPacket, AnalysisReport,
  ImpactReport, RiskReport, Plan, CodePatch, Diff, TestReport, SecurityReport,
  ArchitectureReport, VerificationReport, IncidentReport, RootCauseReport,
  EvidenceReport, PullRequest, DeploymentReport, RollbackReport, MemoryEntry
  (plus Audit).
- Verified the 13 required envelope fields on `domain.Artifact`
  (`entities.go:128`): id, type, task_id, created_by, created_at, version,
  status, scope, provenance, digest, parent_artifact_id, related_entities,
  location (URI). `NewArtifact` builds a stable id + deterministic SHA-256
  digest.
- Tests: `TestNewArtifact`, `TestNewArtifactDeterministicDigest`,
  `TestArtifactExtendedFields`, `TestNewArtifactWorksWithExtendedStruct`,
  `TestNewArtifactKindsCovered` (all kinds), `TestReportStructsJSON`.

### 3.2 — Evidence contract (P0)

- Verified all 4 claim types (`ClaimFact/Inference/Hypothesis/Recommendation` =
  FACT/INFERENCE/HYPOTHESIS/RECOMMENDATION) and all 7 evidence sources
  (`EvidenceGraph/Test/Build/Git/Runtime/Memory/Policy`) on `domain.Claim` /
  `domain.Evidence` (`domain.go:120,138`), each claim carrying statement,
  evidence list, source, provenance, timestamp, scope, confidence.
- **Closed a gap**: `agent.Task.Evidence []domain.Claim` was declared and
  rendered/efficiency-counted but never populated — the context engine's
  claims lived only in `ContextPacket.Facts`, so the Task itself carried no
  evidence trail. `TaskService.Analyze` now appends `pkt.Facts` to
  `t.Evidence`, so claims are persisted with the Task (auditable from the
  task alone) and survive the store round trip.
- New test: `TestAnalyzeAttachesEvidenceClaims` (claims attached, valid types,
  persisted across store round trip).

### 3.3 — Persistence (P0)

- `internal/app/artifact_store.go` persists artifacts to the per-root JSON
  store with the cross-process file lock (Phase 0); artifacts survive restart.
  `agent.Task` (with all typed payloads: ContextPacket, ImpactReport, Impact,
  Plan, Evidence, Artifacts refs) persists via TaskStore.
- Tests: `TestArtifactStoreGetMissing`, restart coverage via the task-store
  restart tests.

### 3.4 — Artifact links (P1)

- Verified all 5 link kinds: parent (ParentArtifactID), child (chain
  traversal), derived_from, supports, contradicts (`domain.ArtifactLink`,
  `NewArtifactLink` validates self-link + empty ids). The full workflow links
  each artifact to its predecessor (`recordArtifact` parent linkage, e.g.
  AnalysisReport → parent ContextPacket; RootCauseReport → parent
  IncidentReport).
- Tests: `TestNewArtifactLinkValidKinds`, `TestNewArtifactLinkRejectsInvalid`,
  `TestArtifactLinksRoundTrip`, and chain-parent assertions in
  `TestAnalyzeRecordsAnalysisReport`.

### 3.5 — Finalized immutability (P1)

- Verified finalized artifacts cannot be silently overwritten
  (`ArtifactStore.Save` rejects final-status overwrite; `NewVersion` creates a
  superseding version instead; drafts stay replaceable).
- Tests: `TestFinalArtifactImmutableAcrossStatusChange`,
  `TestNewVersionSupersedesFinal`, `TestNewVersionNoExistingFallsBackToSave`,
  `TestArtifactDraftReplaceable`.

### 3.6 — Replayable artifacts (P2)

- Verified `ArtifactStore.Replay(taskID)` reconstructs the artifact chain by
  walking ParentArtifactID from the root; `Compare(taskID1, taskID2)` diffs two
  runs. Every workflow stage records its artifact (analyze/plan/impact/risk/
  what-if/verify/test/security/architecture/execute/diff/pr/deploy/observe/
  audit/incident/root-cause/modernization), and the Task carries the typed
  payloads, so an analysis is reconstructable from stored artifacts alone.
- Tests: `TestArtifactReplay`, `TestArtifactReplayEmpty`, `TestArtifactCompare`,
  `TestSafeChangeProducesAllArtifacts` (all 12 required kinds produced with a
  traceable chain).

## Tests

- `go vet ./...` — PASS; `go build ./...` — PASS; `-tags treesitter`,
  `-tags sqlite` — PASS
- `go test ./internal/app/` — PASS (92.6s; incl. new
  `TestAnalyzeAttachesEvidenceClaims` and all artifact/replay/immutability
  tests)
- Remaining 89 packages — PASS, exit 0
- `go test -race ./internal/app/ -run 'Evidence|Replay|Immutable'` — PASS

## Exit gate

> "No important workflow output exists only as an in-memory return value." —
> MET. Audit confirms every workflow stage records a typed artifact
> (TaskService.recordArtifact calls for analyze/analysis/impact/risk/plan/
> verify/test/security/architecture/diff/pr/deploy/observe/audit/incident/
> root-cause/modernization), every artifact persists with links + digest +
> provenance, finalized artifacts are immutable, and the Task carries the
> typed payloads + evidence claims so the full analysis reconstructs from
> stored artifacts.

## Notes / non-changes

- The one gap found (task-level evidence claims) was additive: claims were
  already produced by the context engine and persisted inside the
  ContextPacket; attaching them to `t.Evidence` makes the Task itself carry
  its evidence trail without duplicating or changing the engine.
- No interface changes were needed: artifacts/evidence are recorded inside the
  shared TaskService, so MCP/CLI/REST all get persistence + auditability for
  free.