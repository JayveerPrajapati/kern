# PHASE 8 — ENGINEERING CONSTITUTION + PLAN VALIDATION — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 8.1–8.5.
Go: 1.23, stdlib-only default build (tree-sitter + sqlite behind build tags).

## Scope

Make engineering rules explicit and executable: a constraint model, a
`.kern/constitution.yaml` file, a plan validator, rule provenance, and
non-activating rule suggestions (exit gate: "mandatory rules can block a plan
before execution").

## Work completed (micro-phases)

### 8.1 — Constraint model (P0)

Verified `domain.ConstraintType`: MUST, MUST_NOT, SHOULD, SHOULD_NOT. Rules
carry id/type/category/description + typed fields (cannot_depend_on,
never_log, required, approval, require_tests) + provenance.

### 8.2 — `.kern/constitution.yaml` (P0)

Verified `LoadConstitution` (`governance/constitution.go`): reads
`.kern/constitution.yaml` with a stdlib-only line parser (no YAML dep); a
missing file yields an empty constitution (no constraints), not an error.
Spec's exact example (payments/marketing, secrets, tenant_isolation,
destructive_changes, public_api_change) covered by
`TestLoadConstitution`. Category mapping: architecture (cannot_depend_on),
security (never_log), data (required), database (approval), testing
(require_tests).

### 8.3 — Plan validator (P0)

Verified `ValidatePlan` runs the spec chain (plan → architecture → security →
constraints → impact → risk → policy) via `checkRule` per category. MUST /
MUST_NOT violations are blocking (`PlanViolation.IsBlocking` →
`PlanValidation.Passed=false`); SHOULD / SHOULD_NOT are warnings. Tests:
`TestValidatePlanArchitectureViolation`, `TestValidatePlanPasses`,
`TestValidatePlanDatabaseApproval`, `TestValidatePlanTestingRequires`.

- **Closed a gap (exit gate)**: `ValidatePlan` had ZERO production callers —
  mandatory rules could not block a plan. Wired it into `TaskService.Plan`
  right after `assemblePlan`: the assembled plan is validated against
  `.kern/constitution.yaml`; a blocking violation fails the task ("plan
  blocked by constitution: …") BEFORE the task completes, so the plan cannot
  proceed to execution. Missing constitution = pass (backward compatible).
- Tests: `TestPlanBlockedByConstitution` (MUST_NOT architecture rule on a
  dependency the plan produces → Plan errors, task FAILED),
  `TestPlanPassesWithoutConstitution` (no constitution → plan completes).

### 8.4 — Provenance (P1)

Verified every rule identifies origin: `ConstitutionRule.Provenance` (adr /
incident / policy / team-rule / manual-rule) is propagated onto violations
and the `PlanValidation.Provenance` (`ruleProvenance` defaults to
"manual-rule"). Tests: `TestValidatePlanPropagatesProvenance`,
`TestValidatePlanDefaultsProvenance`.

### 8.5 — Rule suggestions (P2)

Verified `SuggestRules` proposes draft rules from blocking violations +
defensive defaults (destructive DB, public-API-without-tests, logging) — it
NEVER activates them (returns `[]RuleSuggestion`, never writes the
constitution). Tests: `TestSuggestRulesFromViolations`,
`TestSuggestRulesDefensive`.

## Tests

- `go vet ./...` — PASS; `go build ./...` — PASS; `-tags treesitter`,
  `-tags sqlite` — PASS
- `go test ./internal/governance/` — PASS (constitution loader/validator/
  provenance/suggestions suite)
- `go test ./internal/app/` — PASS (98s; incl. new
  `TestPlanBlockedByConstitution`, `TestPlanPassesWithoutConstitution`)
- Remaining 88 packages — PASS, exit 0
- `go test -race ./internal/app/ -run 'Constitution'` — PASS

## Exit gate

> "Mandatory rules can block a plan before execution." — MET and LOCKED.
> `TaskService.Plan` validates the assembled plan against
> `.kern/constitution.yaml` before the task completes; a MUST/MUST_NOT
> violation fails the task, so the plan cannot reach execution.

## Notes / non-changes

- The wiring is exactly at the assembly→completion seam: the plan is fully
  assembled (auditable artifact chain preserved) and only the completion step
  is gated, so a blocked plan leaves a FAILED task with its violations in the
  task output rather than silently producing an incomplete plan.
- Constitution is opt-in per project (empty file/missing = no constraints);
  existing projects are unaffected (backward compatible, verified).