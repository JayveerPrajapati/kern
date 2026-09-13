# Agent Note: Eval Corpus and Product Docs
Status: implemented

## Problem
Product polish gaps: no benchmark corpus for skill-assisted vs bare envelopes, no docs for user skills or the external-MCP client.

## Decision
- kern eval pipeline: candidate now uses the delivered render (res.FittedText, skill section included) instead of RenderPlan alone — skill-assisted cases are measurably different (-10.64 vs -5.11 reduction, both retention 1.00).
- OrchestrateResult.FittedText is now the authoritative delivered render (RenderPlan + skill section); it was empty for many task types before, so skill sections were being appended to nothing.
- Corpus: docs/benchmarks/fixtures/eval/ (bare + skill-assisted intent cases); `kern eval run <dir> [--with-skill X]` compares them.
- Docs: docs/skills.md (bundled + user skills, silent mode), docs/mcp-client.md (config + kern_mcp_call).

## Consequence
- The skill story is now benchmarkable end to end; user skills and the MCP client are documented.
