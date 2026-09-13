# Agent Note: Silent Context Orchestration
Status: implemented

## Problem
Nothing ran the Principle-1 chain (classify → plan → evidence → budget → envelope → injection) as one silent, observable flow, and no context reached the model before its first response.

## Decision
- `internal/context/orchestrate.go`: `Engine.Orchestrate` composes the full chain deterministically, emits per-stage eventbus events (`task.received`, `task.classified`, `evidence.selected`, `budget.applied`, `context.delivered`), and returns a content-hash-sealed escalation handle.
- `kern_orchestrate` MCP tool + `kern orchestrate` CLI + `kern_meta` route expose it; the opencode plugin's `experimental.chat.system.transform` hook injects the envelope before the model's first response (fail-closed 3s ceiling, `KERN_SILENT_INJECT=0`).
- Five context modes (`internal/context/mode.go`) override policy family + lens + disclosure + budget; `kern eval run/compare/report` measures latency + omission rate.

## Consequence
- `CheckConsistency` became order-independent (a determinism bug surfaced by `-count=5` testing was fixed).
- Given up: injection is opencode-only for now (Claude/Gemini hooks remain explicit-invoke); model-visible ⟺ logged is enforced by the session log, not yet by a hard gate.