# ADR-0006: Deterministic Implementation Planning

## Status

Accepted (2026-08-23, first landed with the Kern 2.0 control-plane work)

## Context

`kern plan` / `kern_plan` promise a reproducible, diffable implementation plan
for a change intent. If planning depended on an LLM, two runs over the same
analysis could produce different plans, plans would be unavailable offline, and
prompt contents (intent, project memory) would leave the machine. The analysis
layer already computes everything a plan needs — objective, scope, risk,
workflow steps — so planning can be derived from it rather than guessed.

## Decision

Implementation planning is **deterministic by default**:

- `kern plan` is a first-class alias of `kern analyze`: the CLI `plan` entry in
  `cmd/kern/dispatch_table.go` routes to `runAnalyze`, so the plan is rendered
  from the analysis context packet, not from a separate LLM call.
- The rule-based `DeterministicPlan` (`internal/app/capability.go`, P6.7
  fallback) produces the plan text with a fixed structure — `Objective`, `Risk`
  (defaulted per intent type: deploy/modernization = high, code/security =
  medium, else low), `Scope`, `Target`, `Implementation Steps` (per intent type),
  and `Rollback` — with no LLM in the loop.
- The optional LLM planner (`internal/planner`) exists for when a provider IS
  configured (e.g. `kern loop --mode autonomous`). It is provider-neutral
  (`agent.Provider`), returns `ErrNoProvider` when unset so callers fall back to
  the deterministic plan, and PII-masks the prompt when the provider is remote
  (`llm.MaskRequired()` → `pii.MaskNames`); local Ollama is untouched.
- The plan structure mirrors the LLM planner's output format, so a
  deterministic plan and an LLM plan are interchangeable to downstream stages.
- The high-level `kern verify <types>` form (`build,test,security,architecture,
  dependency,cve,license,secrets`) is disambiguated from the classic
  claims-verification form by `isVerifyTypes` in `cmd/kern/helpers.go` — the
  typed verification gate that follows a plan.

## Consequences

- Easier: plans are reproducible and diffable across runs and machines; the
  plan stage works fully offline with no API keys; intents and recalled
  memories are not sent to a remote LLM unless a provider is explicitly
  configured, and even then only masked.
- Trade-off: rule-based plans are generic per intent type — they cannot capture
  domain nuance an LLM might; the optional planner recovers that at the cost of
  non-determinism and network, which is why it is opt-in.
- Trade-off: two plan paths (deterministic + LLM) must keep the same output
  shape, adding a small contract-maintenance burden.