# PHASE 10 — FLAGSHIP SAFE-CHANGE VERTICAL SLICE — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 10.1–10.6.
Go: 1.23, stdlib-only default build.

## Scope

Prove Kern is actually a control plane: the flagship "Add tenant-aware caching
to UserService" request must pass through the ENTIRE safe-change workflow
repeatedly, producing every required artifact, with no governance bypass.

## Micro-phase audit

- **10.1 (P0) Fixture repository** — NEW on-disk Go fixture
  `internal/app/testdata/safechange/`: `user.go` (UserService, UserRepository,
  CacheService, TenantContext with tenant-scoped cache keys), `user_test.go`
  (2 passing tests incl. tenant-isolation), `go.mod`, and an architecture rule
  `.kern/constitution.yaml` (cache must never depend on the repository; no ORM
  in the service layer). The existing data-driven fixture
  (`testdata/user_service_slice.json`) is kept for the flagship artifact test.
- **10.2 (P0) User request** — the exact request now resolves. FIXED a real
  robustness gap: `Platform.Analyze`/`Platform.Risk` failed on
  "Add tenant-aware caching to UserService" because `whatif.ExtractSymbols`
  returns "tenant" first and the code took `cands[0]` blindly.
  New `analyzeChangeResolvable` tries the change text then every extracted
  candidate until one resolves in the graph.
- **10.3 (P0) Full workflow** — `TestSafeChangeVerticalSlice` drives
  intent → Task → context → memory → evidence → impact → risk → policy →
  plan (constitution validation) → approval → sandbox → code → verification →
  artifacts → PR → deploy → observe → audit against the fixture.
- **10.4 (P0) Artifact validation** — all 10 required artifacts asserted:
  ContextPacket, AnalysisReport, ImpactReport, RiskReport, Plan, CodePatch,
  VerificationReport, Diff, PR, Audit — plus parent-chain integrity
  (every artifact's parent was recorded before it).
- **10.5 (P1) Failure tests** — verified present: `TestSevenFailureDrill`
  (flagship_slice_test.go) + `failure_drill_test.go` (policy denial, test
  failure, security failure, architecture failure, agent timeout, tool
  failure, sandbox failure).
- **10.6 (P1) Efficiency metrics** — verified present: per-stage durations
  (`lifecycleMetrics` in TestFlagshipVerticalSlice) plus the process metrics
  snapshot (tool calls, agent runs, token reduction).

## Gaps found and closed

### G1 — The flagship request could not be analyzed (10.2)

`whatif.ExtractSymbols("Add tenant-aware caching to UserService")` returns
`tenant` (from "tenant-aware") first; `Platform.Analyze` took `cands[0]` and
failed with `context: symbol "tenant" not found in graph`. `Platform.Risk`
passed the raw multi-word change straight into the context engine and failed
identically.

**Fix (`internal/app/platform.go`):** `analyzeChangeResolvable(change)` tries
the change text itself, then every extracted symbol candidate in order, and
returns the first that resolves in the graph; the first candidate's error is
surfaced only when nothing resolves. Both `Analyze` and `Risk` use it.
Tests: `TestAnalyzeFallsBackToResolvableSymbol`,
`TestRiskFallsBackToResolvableSymbol` (both assert the flagship request
resolves to UserService).

### G2 — The exit gate ("pass repeatedly") had no fixture-driven repeat test

The existing flagship test ran once against the kern repo. The spec requires
the workflow to pass REPEATEDLY against a real fixture repo.

**Fix:** `TestSafeChangeVerticalSlice` runs the full chain TWICE against two
fresh copies of the safechange fixture, asserting all 10 artifacts each
iteration and no governance bypass (exec gate checked, approval workflow
exercised, architecture rule validated via `governance.ValidatePlan`).

## Exit gate

> "The entire workflow must pass repeatedly with no governance bypass."
> — **MET and LOCKED**: two consecutive full-chain runs on the fixture produce
> all 10 required artifacts with the governance surfaces (policy exec gate,
> human approval, constitution architecture rule) enforced on every path.

## Verification

- `go vet ./...`, `go build ./...`, `-tags treesitter`, `-tags sqlite` — PASS
- **90/90 packages pass, exit 0** (app 4s incl. the two fixture slices)
- New tests: `TestSafeChangeVerticalSlice`, `TestAnalyzeFallsBackToResolvableSymbol`,
  `TestRiskFallsBackToResolvableSymbol`
- Existing flagship + 7-failure-drill + failure-drill suites still PASS