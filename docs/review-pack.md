# Review Pack (KERN-P2-001)

Deterministic, immutable review packets: one evidence artifact that a human,
one model, or several council members can all be given verbatim — every
reviewer sees identical evidence, and the pack is reproducible.

## Command

```text
kern review-pack [root] --task TASK [--lens L] [--max-tokens N] [--json] [--out PATH]
```

Flags:

- `--task` — the task under review (required).
- `--lens` — review lens applied to the packet facts before planning
  (`security`, `performance`, `maintainability`, `architecture`, `balanced`,
  or a `+`/`,`-joined combination).
- `--max-tokens` — planner budget for evidence selection. `<= 0` uses the
  task-type policy default.
- `--json` — emit the pack as canonical JSON instead of rendered text.
- `--out PATH` — write the pack JSON artifact to a file and print its content
  hash.

## Pack contents

| Section | Source | Deterministic? |
|---|---|---|
| commit + dirty-state hash | `git rev-parse --short HEAD` + sha256 over changed-file names and diff stat | yes |
| task | `--task` argument | yes |
| selected evidence with reasons | `internal/context` planner (`PlanPacket` → budget-fit selections, each with a machine-generated reason) | yes |
| relevant symbols + call paths | packet symbols enriched with callers/callees/blast radius; shortest paths between consecutive symbols | yes |
| changed code | `git diff HEAD --name-only` + `--stat` + capped raw diff preview | yes |
| tests | test gaps for the relevant symbols + changed `_test.go` files | yes |
| project constraints | packet architecture rules | yes |
| observed claims | packet facts with `observed`/`verified_derived` status (or unclassified with evidence) | yes |
| unverified assumptions | packet facts with `reported`/`inferred`/`stale` status or no evidence | yes |
| exact token count | `tokenize.Count` over the rendered body; per-section counts; sections sum to the total exactly | yes |

## Immutability

`ContentHash` is sha256 over the canonical pack JSON with the hash and
`GeneratedAt` blanked (the timestamp is provenance metadata, not content).
Two builds over the same repository state produce byte-identical JSON and
identical hashes — the pack can be compared, cached, and audited.

## Design notes

- Pure logic lives in `internal/reviewpack` (`Build(root, task, pkt, ix, opts)`
  takes an already-assembled context packet and index); the CLI
  (`cmd/kern/cmd_review_pack.go`) wires `app.New` → `Platform.Analyze` →
  `Build`.
- The lens re-ranks packet facts before planning (never drops claims).
- Non-git roots degrade to empty commit/changed-files with a deterministic
  dirty hash of the empty state — packs remain reproducible.
- Feeds `kern review-consensus` (KERN-P2-002): packs are the input artifacts
  whose claims are normalized into consensus/divergence reports.