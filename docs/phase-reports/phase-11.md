# PHASE 11 — INCIDENT VERTICAL SLICE — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 11.1–11.5.
Go: 1.23, stdlib-only default build.

## Scope

Prove the production-to-engineering closed loop: a controlled incident becomes
a verified remediation PR.

## Micro-phase audit

- **11.1 (P0) Controlled incident** — incident engine has `InjectRegression`
  (synthetic deploy-commit-error source); the slice uses a fixture Go repo
  with a KNOWN N+1 regression (UserService.GetUsers doing per-user repository
  lookups) + a synthetic runtime source (deployment of commit `deadbeefcafe`
  that touched `service/user.go`, corroborated by critical error events
  referencing the same file + the `GetUsers` symbol).
- **11.2 (P0) Correlation** — `runtime.Correlator.CorrelateChain` resolves
  alert → service → deployment → commit → symbol (the symbol hop comes from
  error-event `symbol`/`file` attributes). Verified in the slice.
- **11.3 (P0) Root cause** — `incident.Engine.RootCause` derives ranked
  hypotheses (statement/source/confidence/score/evidence) and promotes a
  hypothesis to RootCause only when it is INFERENCE/FACT with non-git
  (runtime) evidence.
- **11.4 (P0) Candidate fix** — `incident.Engine.ApplyAndVerifyFix`
  (risk firewall gate → sandbox worktree → build verification) +
  `RequestApproval`/`Approve` (human gate) + `CreateFixPR` (rendered PR body,
  real PR via provider).
- **11.5 (P1) Learning** — `TaskService.Learn` (pattern extractor →
  engineering memory).

## Gap found and closed

### G1 — The remediation pipeline had zero production callers (exit gate unreachable)

`ApplyAndVerifyFix`/`CreateFixPR`/`FixAndPR` were engine-internal and only
exercised in engine tests. `TaskService.InvestigateIncident` stops at root
cause; nothing drove risk → approval → sandbox → verify → PR through the app
layer, so "controlled incident becomes a verified remediation PR" was not
reachable from any interface.

**Fix (`internal/app/task.go`):** new `TaskService.RemediateIncident(alert,
apply, branch, approver)`:
1. Creates a task, transitions ANALYZING, wires the incident engine (shared
   correlator, PR provider, bus).
2. 11.2/11.3 — IngestAlert → Correlate → RootCause.
3. 11.4 — human approval gate (`RequestApproval` + `Approve` by the caller's
   approver identity, Invariant #2), then `FixAndPR` (sandbox → verify → PR).
4. Records diff / verification / pull_request artifacts, publishes
   `incident.resolved`, completes the task with the incident (status
   PR_CREATED) + rendered summary.

**Test:** `TestIncidentVerticalSlice` (internal/app/incident_slice_test.go)
drives the whole loop and asserts:
- correlation chain contains service/deployment/commit/symbol hops (11.2)
- hypotheses non-empty with confidence+score, RootCause resolved with
  evidence (11.3)
- incident reaches PR_CREATED with FixDiff/Verification/PRBody; diff +
  verification_report + pull_request artifacts on the remediation task (11.4)
- Learn completes and records a pattern (11.5)

Test gotchas (both real engine behaviors, now documented in the slice):
- the `file` attribute must contain `/` or `filesReferencedByErrors` drops it
  and the deploy regression never corroborates → RootCause stays nil
- the fixture must build (`package main` needs a `func main`), since the exit
  gate's "verified" means the sandbox build passes.

## Exit gate

> "Controlled incident becomes a verified remediation PR."
> — **MET and LOCKED**: the N+1 incident is correlated to the deploy commit,
> root-caused with evidence-backed hypotheses, human-approved, fixed in a
> sandbox, build-verified, and turned into a remediation PR (status
> PR_CREATED, diff + verification + PR artifacts on the task chain).

## Verification

- `go vet ./...`, `go build ./...`, `-tags treesitter`, `-tags sqlite` — PASS
- **90/90 packages pass, exit 0** (app incl. TestIncidentVerticalSlice,
  incident, runtime, + 87 rest)
- Existing incident engine + failure-scenario + PR suites still PASS

## Non-changes

- The CLI `kern incident` and MCP `kern_incident` remain investigate-only; a
  remediation surface (applying a fix from an interface) is follow-up scope
  (Phase 19/20) — the app-layer capability is now test-proven.