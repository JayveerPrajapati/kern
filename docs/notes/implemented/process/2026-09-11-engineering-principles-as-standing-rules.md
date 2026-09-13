# Agent Note: Engineering Principles as Standing Rules
Status: implemented

## Problem
Five principles from the dsh exploration were followed ad hoc but never stated as repo rules, so future packages and agents had no standing contract.

## Decision
- Folded the five principles into root AGENTS.md (and the embedded setup copy, parity-enforced) as an "Engineering principles (standing rules)" section: model-visible ⟺ logged, registrations are effects, monotonic SCHEMA_VERSION, capability seam = Service Definition / Provider / Consumer, misconfiguration fails loud.
- Each rule names its kern mechanism (evidence/audit + flight recorder; eventbus/tool registry; `.kern/` format versioning; `intel`/`llm`/`runtime` template).

## Consequence
- The doc-budget gate (G39) now guards AGENTS.md growth; the parity test guards the embedded copy.
