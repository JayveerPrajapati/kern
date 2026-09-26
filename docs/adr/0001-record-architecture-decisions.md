# ADR-0001: Record Architecture Decisions

## Status

Accepted

## Context

Kern ships a single binary with 200+ CLI commands and 140 MCP tools. Comments
and tool descriptions reference numbered decisions (e.g. "ADR-0006", "ADR-0009")
but no decision records existed in the repository, so the numbering had no home
and new decisions had no template to follow.

## Decision

Record architecturally significant decisions as lightweight Markdown files under
`docs/adr/`, one file per decision, named `NNNN-title.md`. Each record follows a
MADR-lite structure:

- **Status** — Accepted, Superseded by ADR-NNNN, or Deprecated.
- **Context** — the problem or constraint that motivated the decision.
- **Decision** — what was decided, in concrete terms.
- **Consequences** — what gets easier, and what the trade-offs are.

Records are brief by design: the code is the source of truth, the ADR captures
the *why* and the *what changed*. New ADRs are numbered sequentially starting at
0002 (0001 is this record). References in code, docs, and tool descriptions use
the `ADR-NNNN` form so they resolve to these files.

## Consequences

- Easier: new contributors can trace why a design exists without reading the
  whole codebase; code comments that cite `ADR-NNNN` now point at something
  real.
- Trade-off: ADRs add documentation to maintain; kept minimal by restricting
  records to significant decisions and keeping each record short.
- Trade-off: ADR numbers in older code comments may predate the files; they are
  reconciled as the referenced decisions are written up.