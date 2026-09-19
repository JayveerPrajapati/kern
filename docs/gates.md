# Blueprint Firewall Gates (G0–G39, G10 retired)

This document is the catalog of the 39 Blueprint change-firewall gates (G0 through G39,
G10 retired) implemented in [`internal/gates/registry.go`](../internal/gates/registry.go).
These gates are enforced by `kern check` and `kern ci` to ensure architectural integrity,
security, and code health.

---

## Gate Inventory

| Gate | Name | Enforcement | Verifies |
|---|---|---|---|
| **G0** | Baseline build & vets | Block | Baseline builds+vets, ownership docs exist, exit-code contract |
| **G1** | Validation engine | Block | PASS/BLOCK aggregation, precedence ERROR>BLOCK, JSON, SKIP |
| **G2** | Architecture/boundary enforcement | Block | Architecture/boundary enforcement via kern guard |
| **G3** | Secret detection & redaction | Block | Secret detection + redaction (gitleaks adapter primary, kern sec fallback) |
| **G4** | Pre-commit hook via CLI | Block | Pre-commit hook: clean/block/JSON/bypass/idempotent/foreign-hook |
| **G5** | MCP validate_staged tool | Block | MCP server validate_staged tool |
| **G6** | Duplication check | Warn | Duplication: precision/recall on 7 fixtures, never-blocks, format |
| **G7** | Agent repair loop & feedback | Block | Agent repair loop + feedback contract (BLOCK carries evidence) |
| **G8** | Sandboxed build/test isolation | Block | Sandboxed build/test isolation |
| **G9** | Resilience scenarios | Warn | Resilience: injected timeouts, network leakage, cleanup, shell scenarios |
| ~~**G10**~~ | *(retired)* | — | *(Retired alongside internal/blueprint/watcher)* |
| **G11** | CI command | Block | CI command: clean/block PR, determinism, JSON artifact, detached-head no-mutation |
| **G12** | Metrics | Info | Metrics: latency benchmarks, persistence, cap, atomic save |
| **G13** | Fresh-machine end-to-end | Block | Fresh-machine end-to-end (build+install+MCP+check) |
| **G14** | Versioned kern contract | Block | Versioned kern contract, fail-closed |
| **G15** | Pre-write validation (validate_proposed) | Block | Pre-write validation for agents via validate_proposed |
| **G16** | Source-aware policy | Warn | Source-aware policy (source override changes status, never passes block, warn cap) |
| **G17** | Doctor preflight | Info | blueprint doctor preflight (env/config/git) |
| **G18** | Policy in MCP handlers | Block | Policy evaluator wired into MCP handlers |
| **G19** | Audit trail on validation | Block | Audit trail written on validation |
| **G20** | Suppressions & owners | Info | Suppressions + owners policy |
| **G21** | Duplication on-disk/content-path | Warn | Duplication on-disk / content-path warn, confidence=similarity |
| **G22** | blueprint fix command | Block | blueprint fix: proposed fix, confinement, worktree cleanup, JSON |
| **G23** | Resilience check wiring | Warn | Resilience check wiring / scenarios (YAML) |
| **G24** | Latency budget gate | Block | Latency budget gate, strict-latency hard-fail |
| **G25** | Evidence provenance fields | Info | Kern 2.0 evidence provenance fields |
| **G26** | Sandbox tests opt-in | Block | Sandbox build/test check behind --tests opt-in |
| **G27** | Audit chain linked to kern | Block | audit chain linked to kern's tamper-evident chain via kern audit append |
| **G28** | Repair loop via MCP | Block | Repair-loop end-to-end via MCP repair_guidance tool |
| **G29** | Approval gate (two-person rule) | Block | high-risk agent changes require explicit human approval before proceeding |
| **G30** | Diff gate: gofmt | Warn | diff-gate format:gofmt flags unformatted changed .go files (advisory WARN; gofmt -l is deterministic) |
| **G31** | Diff gate: vulnerabilities | Warn | diff-gate vulnerability:sec maps the in-house sec scanner onto the changed set (error→BLOCK, warning→WARN, info→INFO) |
| **G32** | Diff gate: schema drift | Warn | diff-gate schema:drift fingerprints the MCP tool catalog (name/phase/risk/InputSchema sha256) and detects baseline drift; --init-baseline round-trips |
| **G33** | Diff gate: unsafe execution | Warn | diff-gate exec:unsafe flags changed non-test .go files importing os/exec, calling exec.Command, or containing sh -c (advisory WARN) |
| **G34** | Diff gate: changelog | Warn | diff-gate changelog:missing warns on non-doc source changes without a CHANGELOG.md entry (advisory WARN) |
| **G35** | Diff gate: MCP catalog drift | Block | diff-gate catalog:drift BLOCKs when the opencode plugin tool set diverges from the MCP catalog (real drift guard) |
| **G36** | Diff gate: tool catalog doc freshness | Block | diff-gate catalog:doc BLOCKs when docs/tool-catalog.md is missing, does not document a registered MCP tool, or differs from a fresh `kern gen-catalog` generation (docs never drift from the catalog) |
| **G37** | Decision-record format | Block | diff-gate note:format BLOCKs when the docs/notes/ decision-record tree violates its path-encoded lifecycle/class contract or in-file format (kern note validate contract) |
| **G38** | Decision-record on change | Warn | diff-gate note:missing WARNs when non-doc source changes carry no docs/notes/ decision record (advisory, mirrors the changelog gate) |
| **G39** | Documentation budget | Block | diff-gate doc:budget BLOCKs when a document listed in docs/doc-budgets.json exceeds its word ceiling or goes missing (one home per fact, enforced ceilings) |

---

## Enforcement Policies

* **Block**: The gate violation halts the pipeline immediately and exits with code 2. The change cannot be merged or executed without remediation or explicit override.
* **Warn**: An advisory notice is emitted in reports and logs, but execution proceeds.
* **Info**: Informational only — reported in diagnostics (`blueprint doctor --json`) but does not affect pipeline status.
