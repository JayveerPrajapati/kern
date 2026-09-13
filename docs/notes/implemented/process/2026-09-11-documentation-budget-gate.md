# Agent Note: Documentation Budget Gate
Status: implemented

## Problem
Kern's docs had no enforced size discipline; prose grew without a ceiling and no gate noticed.

## Decision
- `internal/docbudget`: a committed manifest at `docs/doc-budgets.json` lists each key document with a word ceiling; `CountWords` excludes fenced code so budgets measure prose. `Validate` flags missing documents and over-limit prose.
- Gate **G39 `doc:budget`** (BLOCK) wires it into `kern check` — a listed document can neither disappear nor silently grow past its budget (the dsh `verify-doc-budgets` analogue).
- Seeded ceilings for 16 key docs at ~current+15% (repo passes now; future growth is what the gate stops). A too-low ceiling is a budget bug, not a goal.

## Consequence
- Given up: subtree-level per-folder budgets and headroom-ratio enforcement (kern's manifest is per-document, matching the docs kern actually ships).
