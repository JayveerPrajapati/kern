# ARCHITECTURE.md — subsystem ledger (part 1: caps)

This file is the **machine-readable source of truth for kern's package
architecture**. `go test ./internal/architecture/` (TestArchitectureDocParity)
parses the table below and fails on divergence:

- every listed directory must exist;
- its non-test Go LOC must not exceed the **cap** column.

The **cap** column is the drift gate (caps are the LOC baseline times a
growth budget: ~1.5x rounded up for most subsystems, deliberately ~1.0–1.2x
for contained hotspots such as `internal/governance` and
`internal/mcp`). The LOC baseline is informational — non-test Go LOC, refreshed against
live `MeasureDir` output when a measured value drifts; caps and allowed deps
are the contract.
Allowed deps are the actual import set at baseline: adding a new internal
import means updating the allowed-deps list first — that is the drift gate.
Stdlib and third-party (e.g. build-tagged tree-sitter) imports are always
allowed; a subsystem's own subpackages are always allowed.

Known drift (documented honestly, caps set accordingly): `internal/mcp` is a
monolith (18.0k LOC after the catalog+meta extraction), capped, not hidden —
its cap (19,000) is ratcheted BELOW the pre-extraction baseline (20,018), so
the subtree can only accrete ~1.0k LOC before extraction is forced again.
The row measures the whole subtree (root plus every mcp/* leaf), so crossing
the cap forces moving a family OUT of `internal/mcp` to a top-level sibling
(the blueprint→bppolicy, mcp→mcpserve and mcp→metaroute pattern — extracting
to another mcp/<leaf> stays inside the subtree and cannot relieve it), not
cap-raising. Three families already moved: the tool catalog (Tool type, the
registration table, phase/risk constants, the diffgate bridge) to
`internal/mcpcatalog` (2.1k LOC), the kern_meta dispatch surface to
`internal/mcpmeta` (0.4k LOC), and the CLI mirror family (the command
shells for pre-edit, prompt-fill, semantic-diff, semantic-merge,
evidence-anchor, context-watch, ast-transform and compose) to
`internal/mcpcli` (1.0k LOC) — the mirrors must live OUTSIDE the subtree
precisely because landing them in an mcp/<leaf> would grow it. The blueprint
family (`internal/blueprint` plus its split-out siblings `internal/bppolicy`,
`internal/bpreceipt`, `internal/resilience`, `internal/bpcli`,
`internal/gates`, and `internal/scanners` — none of which exist) was deleted
in the P4 pivot (commit a05c91c7, "refactor(pivot): P4 — delete the blueprint
family (19.7k LOC)"); the change-governance suite now lives standalone in the
blueprint repo. `internal/app`'s TaskService cluster was extracted to `internal/tasklife`
(7.6k LOC, 29 internal deps — now the top fan-out subsystem); `internal/app`
keeps the Platform composition root plus review, benchmark and calibration
surfaces (2.9k LOC). Crossing either cap forces extraction, not cap-raising.
The community/structure family (arch, communities, hubs, purity, surprising,
Violation) was extracted to `internal/topology`; crossing intel's cap forces
the next family out.
The CLI follows the same ratchet: the approval/governance command family
(approve/reject/approval/audit/policy) was extracted to
`internal/approvalcli` (1.5k LOC), and the verify family (`kern verify`'s
default run path, ~800 LOC) was extracted to `internal/verifycmd` (2.2k LOC)
the same way; `cmd/kern`'s cap (19,600) is
ratcheted BELOW the pre-extraction baseline (19,979) — the command surface
can only shrink; crossing the cap forces the next family out, never a
cap raise. The layering contract behind the ratchet is a standing rule of
this file: `cmd/` = flags + delegation, logic lives in `internal/*`.
`internal/integration` is 0-LOC by design: a test-only cross-subsystem
integration suite with no production code.

**Allowed deps per subsystem: `docs/architecture/ledger-details.md`** — part 2 of this machine-read ledger, parsed by the same test: the two halves must name exactly the same subsystems (any divergence fails).

| subsystem | dir | LOC baseline | cap |
|---|---|---|---|
| `cmd/kern` | 13991 | 19600 |
| `internal/agent` | 1882 | 2900 |
| `internal/agents` | 1055 | 1700 |
| `internal/agentwire` | 1385 | 2150 |
| `internal/app` | 2930 | 4395 |
| `internal/approvalcli` | 1390 | 2087 |
| `internal/architecture` | 1439 | 1800 |
| `internal/asttypes` | 184 | 280 |
| `internal/brief` | 632 | 948 |
| `internal/budget` | 212 | 400 |
| `internal/cache` | 530 | 800 |
| `internal/calibrate` | 770 | 1100 |
| `internal/calleefilter` | 68 | 150 |
| `internal/ci` | 241 | 400 |
| `internal/clietag` | 95 | 143 |
| `internal/cliflags` | 319 | 425 |
| `internal/clihints` | 123 | 185 |
| `internal/cliout` | 160 | 200 |
| `internal/code` | 1006 | 1400 |
| `internal/coder` | 464 | 800 |
| `internal/commitmsg` | 1327 | 2200 |
| `internal/compress` | 753 | 1000 |
| `internal/configstrip` | 229 | 350 |
| `internal/config` | 645 | 800 |
| `internal/context` | 3906 | 5500 |
| `internal/contextcli` | 1230 | 1983 |
| `internal/deployment` | 165 | 300 |
| `internal/diff` | 879 | 1100 |
| `internal/docbudget` | 124 | 200 |
| `internal/diffgate` | 1360 | 1750 |
| `internal/docsearch` | 1116 | 1200 |
| `internal/doctor` | 1017 | 1500 |
| `internal/domain` | 2404 | 3200 |
| `internal/draft` | 746 | 1119 |
| `internal/eval` | 321 | 500 |
| `internal/eventbus` | 715 | 1100 |
| `internal/explorebudget` | 274 | 411 |
| `internal/evidence` | 1378 | 1800 |
| `internal/execution` | 1793 | 2844 |
| `internal/fetch` | 245 | 400 |
| `internal/filecache` | 170 | 255 |
| `internal/fit` | 255 | 400 |
| `internal/flight` | 447 | 700 |
| `internal/flock` | 105 | 200 |
| `internal/fsutil` | 248 | 372 |
| `internal/fw` | 1348 | 2100 |
| `internal/gitrun` | 24 | 100 |
| `internal/gitblocks` | 296 | 444 |
| `internal/governance` | 5986 | 6344 |
| `internal/govtoken` | 96 | 145 |
| `internal/graphcli` | 2497 | 4160 |
| `internal/guard` | 639 | 959 |
| `internal/heal` | 1031 | 1569 |
| `internal/hook` | 820 | 900 |
| `internal/host` | 391 | 600 |
| `internal/ignore` | 288 | 500 |
| `internal/incident` | 1095 | 1400 |
| `internal/index` | 11538 | 12900 |
| `internal/integration` | 0 | 100 |
| `internal/intel` | 10644 | 10900 |
| `internal/langparse` | 3634 | 5450 |
| `internal/learnclaim` | 90 | 135 |
| `internal/learning` | 698 | 1100 |
| `internal/lenses` | 281 | 500 |
| `internal/llm` | 1822 | 2800 |
| `internal/lock` | 378 | 600 |
| `internal/loop` | 2013 | 2600 |
| `internal/lspbridge` | 996 | 1600 |
| `internal/mcp` | 18365 | 19000 |
| `internal/mcp/bridge` | 81 | 200 |
| `internal/mcp/compose` | 166 | 300 |
| `internal/mcp/context` | 361 | 600 |
| `internal/mcp/contextwatch` | 164 | 300 |
| `internal/mcp/coord` | 368 | 400 |
| `internal/mcp/crossrepo` | 34 | 100 |
| `internal/mcp/doc` | 361 | 431 |
| `internal/mcp/envelope` | 79 | 150 |
| `internal/mcp/etag` | 285 | 375 |
| `internal/mcp/evidence` | 455 | 700 |
| `internal/mcp/exec` | 527 | 850 |
| `internal/mcp/explain` | 24 | 100 |
| `internal/mcp/fingerprint` | 145 | 300 |
| `internal/mcp/flight` | 36 | 100 |
| `internal/mcp/gov` | 369 | 600 |
| `internal/mcp/governance` | 285 | 450 |
| `internal/mcp/graph` | 1199 | 1401 |
| `internal/mcp/health` | 118 | 250 |
| `internal/mcp/highlevel` | 935 | 1250 |
| `internal/mcp/lsp` | 139 | 300 |
| `internal/mcp/mcpargs` | 147 | 300 |
| `internal/mcp/memory` | 195 | 350 |
| `internal/mcp/merge` | 187 | 350 |
| `internal/mcp/mutation` | 95 | 200 |
| `internal/mcp/optimize` | 284 | 400 |
| `internal/mcp/orchestrate` | 61 | 150 |
| `internal/mcp/planner` | 58 | 150 |
| `internal/mcp/policydsl` | 132 | 250 |
| `internal/mcp/preedit` | 160 | 300 |
| `internal/mcp/prompt` | 122 | 250 |
| `internal/mcp/prose` | 29 | 100 |
| `internal/mcp/provenance` | 224 | 336 |
| `internal/mcp/refactor` | 118 | 250 |
| `internal/mcp/retrieve` | 239 | 305 |
| `internal/mcp/review` | 109 | 250 |
| `internal/mcp/root` | 23 | 100 |
| `internal/mcp/runtime` | 147 | 250 |
| `internal/mcp/security` | 299 | 500 |
| `internal/mcp/skill` | 76 | 200 |
| `internal/mcp/stream` | 157 | 300 |
| `internal/mcp/synthtest` | 140 | 250 |
| `internal/mcp/toolsurface` | 84 | 126 |
| `internal/mcp/transform` | 100 | 250 |
| `internal/mcp/transport` | 325 | 525 |
| `internal/mcp/watcher` | 214 | 321 |
| `internal/mcpcatalog` | 2114 | 3200 |
| `internal/mcpcli` | 798 | 1197 |
| `internal/mcpmeta` | 392 | 600 |
| `internal/mcpclient` | 528 | 800 |
| `internal/mcpgate` | 358 | 533 |
| `internal/mcpguide` | 237 | 356 |
| `internal/mcphints` | 182 | 275 |
| `internal/mcpserve` | 324 | 335 |
| `internal/memory` | 1949 | 2900 |
| `internal/metaroute` | 1344 | 2016 |
| `internal/metrics` | 706 | 1100 |
| `internal/modernization` | 812 | 900 |
| `internal/mutation` | 715 | 1100 |
| `internal/optimize` | 540 | 700 |
| `internal/ownership` | 150 | 300 |
| `internal/pack` | 731 | 1100 |
| `internal/pii` | 454 | 700 |
| `internal/planner` | 131 | 200 |
| `internal/policy` | 302 | 453 |
| `internal/pluginhash` | 122 | 185 |
| `internal/precache` | 179 | 300 |
| `internal/processgroup` | 46 | 100 |
| `internal/profiles` | 283 | 500 |
| `internal/project` | 1397 | 2100 |
| `internal/prompt` | 52 | 100 |
| `internal/prprovider` | 274 | 300 |
| `internal/relay` | 428 | 600 |
| `internal/rename` | 1210 | 1700 |
| `internal/refactor` | 231 | 300 |
| `internal/repair` | 343 | 600 |
| `internal/retrieval` | 632 | 900 |
| `internal/rulesblock` | 172 | 260 |
| `internal/runtime` | 1959 | 3000 |
| `internal/sandbox` | 2214 | 2507 |
| `internal/sandbox/landlock` | 471 | 707 |
| `internal/schema` | 253 | 400 |
| `internal/script` | 670 | 1000 |
| `internal/sdk` | 279 | 500 |
| `internal/sec` | 648 | 971 |
| `internal/secscan` | 1296 | 1887 |
| `internal/semcache` | 890 | 1335 |
| `internal/servedledger` | 155 | 235 |
| `internal/session` | 182 | 273 |
| `internal/setup` | 3955 | 5200 |
| `internal/skills` | 402 | 600 |
| `internal/stats` | 400 | 400 |
| `internal/storage` | 697 | 1100 |
| `internal/strutil` | 59 | 100 |
| `internal/swap` | 330 | 455 |
| `internal/tasklife` | 7874 | 11430 |
| `internal/synthtest` | 1238 | 1700 |
| `internal/terse` | 876 | 1100 |
| `internal/testfixture` | 202 | 400 |
| `internal/tokenize` | 1050 | 1500 |
| `internal/tokstats` | 301 | 407 |
| `internal/topology` | 1151 | 1709 |
| `internal/transform` | 461 | 600 |
| `internal/twin` | 1679 | 1900 |
| `internal/twinindex` | 225 | 340 |
| `internal/validate` | 797 | 1300 |
| `internal/verdict` | 782 | 1173 |
| `internal/verification` | 3200 | 4800 |
| `internal/verifycmd` | 2026 | 2900 |
| `internal/version` | 364 | 600 |
| `internal/webhook` | 204 | 400 |
| `internal/whatif` | 1013 | 1400 |

## Recorded coupling debt (cross-community edges — paid down when each file is next touched)
These edges cross the documented community boundary (execution ↔ apply,
analysis ↔ consistency). Recorded per review plan 3.7-A; the fix is Phase-2
3.7-B+C work — do not widen them while editing nearby code. (The two
blueprint entries — analysis ↔ policy verdict and service ↔ metrics — were
deleted with the family in the P4 pivot, commit a05c91c7.)
- `internal/execution/worktree.go:76` — `Agent.Code` (internal/coder/coder.go:113) calls `Worktree.Apply`; execution reaches into apply logic.
- `internal/app/efficiency.go:98` — `BuildEfficiencyReport` calls `buildContextQuality`.
- `internal/context/consistency.go:53` — `ApplyConsistency` calls `CheckConsistency` (fifth edge, found in the 2026-10-13 verification pass, beyond the plan's four).

Change history lives in `git log`, not in this file.
