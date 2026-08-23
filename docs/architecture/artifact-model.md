# Artifact Model — internal/domain, internal/app, internal/evidence

## Purpose
Artifacts are the named, traced outputs of every engineering workflow stage.
They form an auditable, immutable chain that can be reconstructed from stored
state without re-running the model. The model spans three packages:
`internal/domain` (types), `internal/app/artifact_store.go` (persistence +
replay + compare), and `internal/evidence` (claims backing artifacts).

## ArtifactKind taxonomy
`ArtifactKind` constants (`internal/domain/entities.go:101-124`) cover every
workflow output:

- `context_packet` · `analysis_report` · `impact_report` · `risk_report` ·
  `plan` · `code_patch` · `diff`
- `test_report` · `security_report` · `architecture_report` ·
  `verification_report`
- `incident_report` · `root_cause_report` · `evidence_report`
- `pull_request` · `deployment` · `deployment_report` · `rollback_report`
- `memory_entry`

## Artifact fields
`Artifact` (`entities.go:127-143`):

- `ID` — stable, unique id: `kind-taskID-shorthash8` (`NewArtifact`,
  `entities.go:149-170`).
- `Kind` (canonical `ArtifactKind`), `Type` (string form of kind).
- `TaskID` — originating task; `CreatedBy` — producing agent id.
- `CreatedAt`, `Version` (0 = initial), `Status` (`"draft"`/`"final"`/
  `"superseded"`).
- `Scope` — what the artifact applies to; `Provenance` — how it was produced.
- `URI` — location; `Digest` — SHA-256 content hash.
- `ParentArtifactID` — parent in the traceable chain.
- `RelatedEntities` — related entity ids; `Links` — typed directional links.

`NewArtifact(kind, taskID, uri)` computes a deterministic content digest
(SHA-256 over `kind|task|uri`) and a URI-derived short hash so distinct URIs for
the same kind+task never collide (`entities.go:149-157`).

## Typed links (P3.4)
`ArtifactLinkKind` (`entities.go:175-187`): `derived_from` (To is the source
From was produced from), `supports` (To corroborates From), `contradicts` (To
conflicts with From). `ArtifactLink` (`entities.go:194`) is directional
(`FromID` → `ToID`), making the chain walkable and auditable in both directions.

## Persistence — ArtifactStore
- `NewArtifactStore(root)` (`artifact_store.go:29`) persists JSON to
  `<cache>/artifacts/<project_hash>.json` via `cache.Path`.
- `Save(a)` (`artifact.go:107`) is insert-or-replace by `ID`, written
  atomically (temp file + rename, `0o600`). It enforces **Invariant 8**
  (immutability): a `Save` that would overwrite an existing `"final"` artifact
  returns an error instead of replacing it; drafts/superseded may be replaced.
- `Get(id)`, `GetByTask(taskID)` (sorted by `CreatedAt`), `List()`.
- `Replay(taskID)` (`artifact.go:175`) reconstructs the chain by following
  `ParentArtifactID` links from the root (an artifact with no parent in the
  task's set), returning chain order for full analysis reconstruction without
  re-running the model.
- `Compare(taskID1, taskID2)` (`artifact.go:242`) returns an
  `ArtifactComparison` — `OnlyIn1`, `OnlyIn2`, `InBoth`, and per-kind
  `DigestDiff` — for run-to-run comparison.
- `TaskService.recordArtifact` (`internal/app/task.go:2066`) sets
  `ParentArtifactID` automatically to link artifacts into the chain.

## Evidence layer — internal/evidence
Claims give artifacts their factual backing:
- `ClaimType` (`internal/domain/domain.go:97-103`): `FACT` (verified
  deterministic), `INFERENCE` (derived from facts), `HYPOTHESIS` (unverified
  proposition), `RECOMMENDATION` (suggested action).
- `Builder` (`internal/evidence/builder.go`) is a fluent builder
  (`NewBuilder(type, statement)` → `WithSource`/`WithProvenance`/`WithScope`/
  `WithConfidence`/`WithEvidence` → `Build()`).
- `RequireEvidence`/`BuildChecked` (`builder.go:70-87`) enforce the
  evidence-free-critical guard: a `RECOMMENDATION` at `>= ConfidenceHigh` must
  carry at least one `Evidence` entry or `BuildChecked` errors.
- `Digest(content)` (`internal/evidence/digest.go:10`) returns a stable SHA-256
  hex digest for integrity checks.
- Confidence constants (`internal/evidence/evidence.go:11-15`): certain = 1.0
  (deterministic), high = 0.9 (inference from facts), moderate = 0.8 (recalled
  memory).

## Storage
- JSON files under `<cache>/artifacts/`, keyed by project hash, one artifact
  list per project. Deterministic, restart-surviving.

## Security
- Final artifacts immutable (Invariant 8). Writes are `0o600`, temp-file +
  rename for atomicity. Task scope gating of artifact access flows through
  `TaskService.authorizeResource` (see tool-gateway.md).

## Failure modes
- Corrupt store file surfaces an unmarshal error on load; absent file returns an
  empty list. Overwriting a final artifact errors (never silently mutates).

## Tests
- `artifact_store_test.go`, `artifact_replay_test.go`,
  `internal/domain/artifact_test.go`, `artifact_coverage_test.go`
  (`TestSafeChangeProducesAllArtifacts`), and `internal/evidence/builder_test.go`
  + `factories_test.go`.

## Performance / trade-offs
- Whole-file JSON rewrite on each `Save` (simple, atomic, but not incremental);
  fine for per-project artifact chains. Chain replay and compare are O(n)
  in-memory walks. Immutability of final artifacts trades edit-ability for
  auditability.