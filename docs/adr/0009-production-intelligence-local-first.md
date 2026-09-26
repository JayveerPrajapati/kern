# ADR-0009: Production Intelligence Stays Local-First

## Status

Accepted (2026-08-20, first landed with the Kern 2.0 consolidate commit)

## Context

Production incident handling normally implies a SaaS vendor: telemetry ships
out, correlation happens in a hosted dashboard, and the fix workflow lives
behind a web UI. That contradicts kern's core wedge — one binary, zero network —
and its security posture (no telemetry, fail-closed execution, source stays on
the machine). Production intelligence needs to work against the same local
principles: deterministic, offline, and auditable.

## Decision

Production intelligence is **local-first and offline** — no network, no vendor:

- `internal/runtime` is the deterministic source of production truth. Its
  `local.go` defines the on-disk `Snapshot` (telemetry events, deployments,
  commits) as "the local-first, offline path for production intelligence (no
  network, no vendor)"; `store.go` provides an in-memory deterministic `Store`
  used both directly and as the sink that vendor adapters feed into, so vendor
  data is normalized locally rather than queried remotely. `events.go`
  documents the runtime as "a deterministic, [local] production intelligence"
  layer whose `Event` is the atomic unit.
- `internal/incident` runs the whole Workflow D pipeline locally —
  Alert → Correlate → Root Cause → Candidate Fix → Sandbox → Verify → PR —
  reusing the local knowledge graph, memory, evidence store, governance
  (firewall + approval) and execution/verification engines. A production fix
  never advances past the sandboxed, verified stage until an approval gate is
  granted, and all of it happens in-process.
- The CLI surface (`kern runtime status|drift`, `kern incident`, `kern
  correlate`) and MCP tools (`kern_incident_triage`, `kern_correlate_evidence`)
  read local runtime snapshots and the local index; nothing phones home.
- Runtime evidence (deployments, commits, PR refs) is correlated against local
  AST symbols (`internal/runtime/chain.go` correlation chains), keeping the
  deep evidence chain on-machine.

## Consequences

- Easier: incident triage works in air-gapped environments and CI without
  credentials; telemetry and source never leave the box, matching the
  zero-telemetry guarantee; correlation is deterministic and reproducible.
- Trade-off: local-only telemetry means alert volume and fidelity depend on
  what the repo's own adapters/snapshots capture — no hosted vendor analytics.
  Vendor adapters may exist, but they feed the local store rather than being
  queried live.
- Trade-off: cross-service production observability beyond what the runtime
  snapshot encodes requires users to bring their own data into the snapshot
  format.