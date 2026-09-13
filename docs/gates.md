# Blueprint Firewall Gates (G0–G39)

This document is the catalog of the 40 Blueprint change-firewall gates (G0 through G39)
implemented in [`internal/blueprint/gates/registry.go`](../internal/blueprint/gates/registry.go).
These gates are enforced by `kern check` and `kern ci` to ensure architectural integrity,
security, and code health.

---

## Gate Inventory

| Gate | Name | Category | Enforcement | Verifies |
|---|---|---|---|---|
| **G0** | Baseline build & vets | Core | Block | Baseline builds+vets, ownership docs exist, exit-code contract |
| **G1** | Scope containment | Core | Block | Changes remain strictly within authorized task scope |
| **G2** | Architectural boundaries | Architecture | Block | Prohibits cross-tier package violations (e.g. CLI importing internal packages directly) |
| **G3** | Secret detection | Security | Block | Prevents hardcoded API keys, tokens, and private keys |
| **G4** | Public API drift | Interface | Warn | Tracks additions and breaks in exported API surface |
| **G5** | Taint & injection | Security | Block | Flags unvalidated inputs reaching system sinks (exec, SQL, HTML) |
| **G6** | Code duplication | Quality | Warn | Pass-1 AST similarity triage and jscpd clone detection |
| **G7** | Cache GC & retention | Hygiene | Warn | Verifies cache expiration and dormant entry archiving |
| **G8** | Test coverage floor | Testing | Block | Ensures modified functions maintain test coverage thresholds |
| **G9** | Package import cycles | Architecture | Block | Rejects circular package dependencies (Tarjan SCC) |
| **G10** | Error handling contracts | Reliability | Block | Rejects ignored errors and enforces structured error wrapping |
| **G11** | Concurrency safety | Reliability | Block | Detects data races and unbuffered channel leaks |
| **G12** | Memory leaks & allocs | Performance | Warn | Bounds heap allocation growth in hot execution paths |
| **G13** | Schema validation | Integrity | Block | Validates JSON/YAML configurations against schema definitions |
| **G14** | Audit chain continuity | Governance | Block | Verifies cryptographic hash continuity of the audit trail |
| **G15** | Role-based tool access (RBAC)| Governance | Block | Enforces agent role permissions (junior_dev, reviewer, admin) |
| **G16** | Context budget limits | Efficiency | Warn | Monitors conversation token bloat and prompts compaction |
| **G17** | Health & freshness | Diagnostics | Warn | Verifies MCP server health and symbol index freshness |
| **G18** | Rollback integrity | Sandbox | Block | Validates ephemeral snapshot restoration on execution failure |
| **G19** | Conventional commits | Metadata | Block | Asserts deterministic type/scope formatting in commit messages |
| **G20** | License compliance | Compliance | Block | Rejects forbidden third-party dependency licenses |
| **G21** | Dead code elimination | Hygiene | Warn | Flags unreachable private functions and unused constants |
| **G22** | Large file limits | Hygiene | Warn | Flags god-functions (>200 lines) and oversized files |
| **G23** | Hub coupling control | Architecture | Warn | Limits transitive fan-out on core system hub functions |
| **G24** | Bridge qualification | Architecture | Warn | Requires qualified package.Symbol naming on cross-package bridges |
| **G25** | PII masking enforcement | Privacy | Block | Asserts secrets and IPs are scrubbed before remote egress |
| **G26** | Non-interactive safety | Autonomy | Block | Disallows unmonitored interactive prompts in CI/headless mode |
| **G27** | Pre-edit blast radius | Safety | Block | Requires operator approval before editing HIGH-risk hubs |
| **G28** | Reproduction test synthesis | SRE | Block | Requires minimal failing test before applying incident repairs |
| **G29** | In-toto attestation | Security | Block | Attests build provenance with SLSA-compatible metadata |
| **G30** | Code formatting (gofmt) | Style | Block | Enforces standard formatting without whitespace drift |
| **G31** | Network egress lockdown | Security | Block | Enforces private network namespaces during test/exec execution |
| **G32** | Event relay fan-out | Observability| Warn | Verifies event bus publication across concurrent watchers |
| **G33** | Workspace lock lifecycle | Concurrency | Block | Prevents race conditions among concurrent autonomous agents |
| **G34** | Memory pattern extraction | Learning | Warn | Converts recurrent failure patterns into memory constraints |
| **G35** | Evidence bundle signing | Integrity | Block | Generates SHA-256 evidence certificates for AST claims |
| **G36** | Tool catalog parity | Contracts | Block | Asserts MCP registration table matches generated catalog docs |
| **G37** | Shell completion validity | CLI | Block | Validates shell completion scripts for bash, zsh, and fish |
| **G38** | JSON schema stability | Contracts | Block | Enforces versioned, backwards-compatible JSON CLI output |
| **G39** | Host model delegation | Autonomy | Block | Validates MCP sampling contract and provider auto-chaining |

---

## Enforcement Policies

* **Block**: The gate violation halts the pipeline immediately and exits with code 2. The change cannot be merged or executed without remediation or explicit override.
* **Warn**: An advisory notice is emitted in reports and logs, but execution proceeds.
