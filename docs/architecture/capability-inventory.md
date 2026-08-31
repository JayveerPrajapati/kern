# Kern 2.0 — Capability Inventory (Phase 0.2)

For each major capability: package / CLI / MCP / API / inputs / outputs / events / artifacts / tests / limitations.

| Capability | Package | CLI | MCP | REST |
|-----------|---------|-----|-----|------|
| Task lifecycle | `app.TaskService` | `kern task`, `kern run` | yes | /v1/tasks |
| Intent compile | `app.CompileIntent` | via run | yes | /v1/analyze |
| Context runtime | `context` | `kern context` | yes | /v1/context |
| Code intelligence | `intelligence` | `kern graph/search/explore/probe` | yes | /v1/graph |
| Impact / risk | `app.Platform` | `kern impact` | yes | /v1/impact, /v1/risk |
| What-if | `whatif` | `kern what-if` | yes | /v1/what-if |
| Evidence | `evidence` | (via tasks) | — | /v1/artifacts |
| Artifacts | `app.ArtifactStore` | `kern artifacts` | yes | /v1/artifacts |
| Governance | `governance` | `kern approve/audit` | yes | /api/approvals |
| Constitution | `governance.Constitution` | — | yes | — |
| Sandbox/exec | `sandbox`+`execution` | `kern exec` | yes | /v1/execute |
| Verification | `verification` | `kern verify` | yes | /v1/verify |
| Loop / autonomy | `loop` | `kern loop`/`kern do` | yes | /v1/loop |
| Agent runtime | `agent`+`agents` | `kern agent` | yes | /v1/agents |
| Incident | `incident` | `kern incident` | yes | /v1/incidents |
| Modernization | `modernization` | `kern modernize` | yes | /v1/modernize |
| Runtime correl. | `runtime` | — | yes | /v1/correlate |
| Deployment | `deployment` | via task pipeline (`kern run`/`kern do`) | yes | /v1/tasks |
| Memory | `memory` | `kern memory` | yes | /v1/memory |
| Learning | `learning` | — | yes | /v1/learn |
| Flight/audit | `flight` | `kern audit` | yes | /v1/audit |
| Digital twin | `twin` | — | yes | /api/graph |
| Web console | `web` | `kern-server` | — | full API |
| Enterprise | `enterprise` | `kern-server` | — | /org/* |
| Go SDK | `sdk` | — | — | — |
| Py/TS SDK | `sdk/python`,`sdk/typescript` | — | — | — |

## Event coverage

62 Kind constants across task/agent/policy/approval/verification/pr/deployment/runtime/incident/memory/risk/architecture/security. Async publish (goroutine, panic-recovered) with `Flush()`.

## Artifacts produced by TaskService

ContextPacket, AnalysisReport, ImpactReport, Plan, CodePatch, VerificationReport, Diff, PullRequest, Deployment (recordArtifact calls throughout task.go). RiskReport not yet recorded.

## Limitations

- No per-item context authorization, paging, leases, or context replay.
- No event idempotency / retry / dead-letter / persisted replay.
- No capability registry (hardcoded DefaultCapabilities), no discovery, no tool fallback.
- No gateway dry-run / explain-deny.
- No rule-suggestions; constitution provenance not populated.
- Correlation contract (FACTUAL/INFERRED/UNKNOWN) + change fingerprint absent.
- Conflict-result enum + memory supersession absent.
- No `kern efficiency` report.
- Web console has only 2 HTML pages (rest JSON).
- See gap-analysis.md for full classification.

## Failure modes
- Capability missing a required tool: a capability whose `tools` list references an unregistered tool cannot satisfy its task. Mitigation: capability registry validates tool references at registration; a gap surfaces as an explicit planning/run error rather than a silent no-op.
- Discovery returns no match: an intent with no discoverable capability fails to route. Mitigation: fallback/unknown result so the run fails clearly (or prompts) instead of executing with the wrong capability.
- High-risk capability invoked without an environment: a sandboxed/high-risk capability (e.g. deploy, exec) run without its required environment/approval gate bypasses governance. Mitigation: fail-closed enforcement tied to risk class and permission; denial is audited.
- Hardcoded DefaultCapabilities drift: because the registry is hardcoded (no discovery, no tool fallback), a new tool added to the codebase is not auto-exposed. Mitigation: keep DefaultCapabilities in sync with the tool catalog; add registry + discovery to remove the drift.
- Tool fallback absence: with no tool fallback, a preferred tool outage fails the step outright. Mitigation: add fallback routing so alternate tools can satisfy the capability.
- Approval/risk divergence: the approval store and risk service can disagree if not updated transactionally. Mitigation: single control-plane write path and audit trail so the recorded decision matches the enforced one.