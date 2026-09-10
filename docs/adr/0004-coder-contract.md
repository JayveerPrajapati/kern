# ADR-0004: kern is a thin deterministic gate around external coding agents

- Status: Accepted (2026-09-08)
- Deciders: maintainer (ADR review with orchestrator; audit oracle
  recommendation)
- Scope: the autonomous coding path (`kern do`, kern_do, the coder agent)

## Context

kern has two possible coder contracts (gap F3, 2026-09-08 audit):

1. A first-class in-process coding agent (internal/coder) driving
   plan→code→verify on the configured LLM (default local Ollama).
2. A thin deterministic gate: kern supplies context, evidence, governance,
   and verification; EXTERNAL coding agents (Claude Code, opencode,
   Cursor, ...) do the actual code generation through the MCP surface.

kern's differentiation is its index, evidence chains, blast radius, and
governance — not a homegrown coding loop competing with dedicated agent
products. Every hour on the in-process one-shot path is potentially sunk
against that competition.

## Decision

**The external-agent contract is the investment path.** kern's role in
autonomous coding is the trust/verification/control layer:

- **Before**: intent classification, grounded context assembly
  (Platform.CodeContext — relevant files + impact set), blast radius,
  what-if simulation, architecture boundaries.
- **During**: governance gates (exec firewall, approval workflow), the
  sandbox/worktree execution layer, `kern_do` as the MCP entry point
  external agents call.
- **After**: polyglot verification (detected or configured per project),
  evidence bundles for every claim, audit chain, learning/memory capture.

The in-process coder (internal/coder, the C1 grounding work) is RETAINED
as the local/offline fallback — it shares the same context assembly,
verification, and governance layers — but receives no further
investment in its own generation quality (no round-budget growth, no
model-specific prompt tuning). New autonomous-coding features land on
the MCP surface (tools, plugin parity, catalog) that every external
agent consumes.

## Consequences

- The C1 grounding work is not wasted: the context assembly is exactly
  what external agents consume via kern_do / the MCP tools.
- internal/coder keeps its current API surface (Code with context,
  search/replace edits, apply-failure feedback) and stays tested, but is
  treated as a fallback, not a roadmap item.
- Autonomous-coding progress is measured on MCP surface completeness
  and verification trust, not on in-process coding quality.
