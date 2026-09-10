# Consensus & Divergence Schema (KERN-P2-002)

Normalizes review results (review packs) into a consensus/divergence report
**without treating majority vote as truth**: agreement is reported with its
exact scope, disagreement is surfaced, and no winner is selected by
counting votes.

## Command

```text
kern review-consensus <pack1.json> <pack2.json> [more packs...] [--json]
```

Each pack is a `kern review-pack --out` artifact (KERN-P2-001). At least two
packs are required; each pack is one reviewer's immutable evidence artifact,
identified by its content hash.

## Output fields

| Field | Meaning |
|---|---|
| `consensus` | claims shared by two or more packs (statement, type, status, total evidence, agreeing pack hashes) |
| `divergence` | same statement classified differently across packs (e.g. `FACT/observed` in one, `HYPOTHESIS/reported` in another) |
| `minority_positions` | claims held by exactly one pack |
| `supporting_evidence` | evidence totals behind each consensus claim |
| `unsupported_claims` | claims with zero evidence in every pack |
| `assumptions` | claims any pack classified inferred/reported/stale or HYPOTHESIS/INFERENCE |
| `decision_drivers` | consensus claims ranked by evidence then agreement (top 8) |
| `next_verification` | deterministic actions that resolve divergence ("verify") and fill evidence gaps ("obtain evidence") |

## Deterministic matching

Claims are matched across packs by a canonical key: lowercased, trimmed,
whitespace-collapsed statement. Identical inputs always produce identical
reports — pack order does not matter. Everything is stdlib-only; no LLM is
involved.

## Design notes

- Pure logic lives in `internal/council` (`Normalize([]*reviewpack.ReviewPack)
  *Report`); the CLI (`cmd/kern/cmd_review_consensus.go`) reads pack JSON
  files and renders the report.
- Divergence detection is conservative: only exact canonical statements with
  differing classifications diverge. This avoids false "disagreement"
  findings from paraphrase; near-miss statements surface as separate
  minority positions instead.
- A single pack still produces a useful report (minority positions,
  assumptions, unsupported claims, next verification) with empty consensus.
- Feeds architecture decision records (ADRs) and review gates: the
  `next_verification` list is the deterministic handoff to the diff gate
  (KERN-P2-003) and the optional council adapter (KERN-P2-004).