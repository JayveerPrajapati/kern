# ADR-0001 — Kern 2.0: Control Plane

> **Note:** This ADR describes kern's aspirational north-star architecture. The currently delivered scope is narrower — see the [README](../../README.md) for what's shipped today. The "control plane" framing here is the long-term direction, not the current state.

- **Status:** Accepted
- **Date:** 2026-08-23
- **Driver:** KERN 2.0 CANONICAL END-TO-END BUILD SPEC V3

## Context
Kern must evolve from a local context/code-intelligence/MCP system into a control plane that governs and coordinates AI agents across the software engineering lifecycle — while preserving the original context-optimization mission.

## Decision
1. **Kern is the platform; MCP is one interface among many** (CLI, REST, SDK, IDE, Git, CI/CD, K8s, Webhooks, Events, Web UI). All interfaces use the same application/domain layer (invariant 19).
2. **Reuse, don't rewrite.** The 20 spec-required packages already exist. Strategy is integrate → refactor where required → strengthen boundaries → prove end-to-end. No `*2` duplicate subsystems unless audit proves the existing one cannot be evolved safely.
3. **Deterministic stays deterministic** — AST/graph/hash/policy/tests are not LLM-derived. LLMs only for planning/reasoning/summarization (invariant 3).
4. **Governance is mandatory** for governed execution; high-risk fails closed (invariants 2, 18); production mutation disabled by default (invariant 7).
5. **Evidence-backed claims** with explicit uncertainty (invariant 5); auditable important actions (invariant 4); immutable final artifacts (invariant 8); idempotent event consumers (invariant 9).
6. **Provider-agnostic LLM layer** (invariant 10) — `internal/llm` provider abstraction.
7. **Strict gated execution** — phase/micro-phase/priority order; exit gate must PASS before advancing; never bypass governance; preserve backward compat (invariant 20).

## Consequences
- New subsystems must document purpose/inputs/outputs/dependencies/storage/security/failure-modes/tests/performance/trade-offs/migration.
- Deterministic test paths for every major behavior (invariant 22).
- Local-first operation preserved (invariant 12).

## Status of this document
Companion to docs/architecture/{current-state,target-state,gap-analysis,capability-inventory,domain-model,workflow-model}.md and docs/roadmap.md.