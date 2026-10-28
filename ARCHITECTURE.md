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
| `cmd/kern` | 11067 | 19600 |
| `internal/agent` | 1888 | 2900 |
| `internal/agents` | 1055 | 1700 |
| `internal/agentwire` | 1492 | 2150 |
| `internal/app` | 3132 | 4395 |
| `internal/apply` | 268 | 402 |
| `internal/approvalcli` | 1400 | 2087 |
| `internal/approvalsign` | 380 | 600 |
| `internal/architecture` | 1438 | 1800 |
| `internal/asttypes` | 184 | 280 |
| `internal/astrefs` | 182 | 280 |
| `internal/auditdiag` | 150 | 300 |
| `internal/auditrot` | 123 | 190 |
| `internal/brief` | 743 | 948 |
| `internal/budget` | 212 | 400 |
| `internal/cache` | 670 | 800 |
| `internal/calibrate` | 774 | 1100 |
| `internal/calleefilter` | 120 | 150 |
| `internal/ci` | 241 | 400 |
| `internal/claims` | 486 | 730 |
| `internal/classparity` | 200 | 350 |
| `internal/clietag` | 95 | 143 |
| `internal/cliflags` | 319 | 425 |
| `internal/clip` | 27 | 100 |
| `internal/clihints` | 123 | 185 |
| `internal/cliout` | 160 | 200 |
| `internal/code` | 1008 | 1400 |
| `internal/coder` | 500 | 800 |
| `internal/commitmsg` | 1327 | 2200 |
| `internal/compress` | 861 | 1000 |
| `internal/configstrip` | 229 | 350 |
| `internal/config` | 645 | 800 |
| `internal/context` | 3948 | 5500 |
| `internal/contextcli` | 1439 | 1983 |
| `internal/daemon` | 349 | 560 |
| `internal/deployment` | 165 | 300 |
| `internal/destructive` | 70 | 200 |
| `internal/destrules` | 288 | 440 |
| `internal/diff` | 879 | 1100 |
| `internal/docbudget` | 124 | 200 |
| `internal/diffgate` | 1428 | 1750 |
| `internal/docsearch` | 1116 | 1200 |
| `internal/doctor` | 1500 | 1500 |
| `internal/domain` | 2467 | 3200 |
| `internal/eval` | 321 | 500 |
| `internal/eventbus` | 768 | 1100 |
| `internal/explorebudget` | 273 | 411 |
| `internal/evidence` | 1495 | 1800 |
| `internal/execapproval` | 169 | 260 |
| `internal/execclassify` | 472 | 500 |
| `internal/execinterp` | 120 | 240 |
| `internal/execshell` | 40 | 150 |
| `internal/execwin` | 100 | 220 |
| `internal/execution` | 1936 | 2844 |
| `internal/fetch` | 245 | 400 |
| `internal/filecache` | 182 | 255 |
| `internal/fit` | 284 | 400 |
| `internal/flight` | 447 | 700 |
| `internal/flock` | 106 | 200 |
| `internal/fsutil` | 269 | 372 |
| `internal/fw` | 1348 | 2100 |
| `internal/gitrun` | 20 | 100 |
| `internal/gitblocks` | 404 | 444 |
| `internal/governance` | 4881 | 5369 |
| `internal/govaudit` | 1536 | 2304 |
| `internal/govtoken` | 88 | 145 |
| `internal/graphcli` | 2716 | 4160 |
| `internal/grepscope` | 137 | 200 |
| `internal/guard` | 635 | 959 |
| `internal/guardrule` | 1029 | 1031 |
| `internal/inplaceedit` | 76 | 120 |
| `internal/guardpause` | 415 | 623 |
| `internal/guardpaths` | 348 | 356 |
| `internal/guardstats` | 160 | 240 |
| `internal/guardhook` | 640 | 960 |
| `internal/heal` | 1031 | 1569 |
| `internal/hook` | 820 | 900 |
| `internal/host` | 391 | 600 |
| `internal/ignore` | 455 | 500 |
| `internal/incident` | 1096 | 1400 |
| `internal/index` | 12895 | 12900 |
| `internal/indexdb` | 805 | 840 |
| `internal/indexmem` | 156 | 234 |
| `internal/integration` | 0 | 100 |
| `internal/intel` | 10882 | 10900 |
| `internal/langparse` | 3761 | 5450 |
| `internal/learnclaim` | 90 | 135 |
| `internal/learning` | 698 | 1100 |
| `internal/lenses` | 281 | 500 |
| `internal/licensescan` | 155 | 250 |
| `internal/llm` | 2009 | 2800 |
| `internal/lock` | 387 | 600 |
| `internal/loop` | 1605 | 2600 |
| `internal/lspbridge` | 1003 | 1600 |
| `internal/mcp` | 17453 | 19000 |
| `internal/mcp/bridge` | 67 | 200 |
| `internal/mcp/apply` | 118 | 200 |
| `internal/mcp/compose` | 166 | 300 |
| `internal/mcp/context` | 385 | 600 |
| `internal/mcp/contextwatch` | 164 | 300 |
| `internal/mcp/coord` | 399 | 400 |
| `internal/mcp/crossrepo` | 34 | 100 |
| `internal/mcp/doc` | 325 | 431 |
| `internal/mcp/envelope` | 79 | 150 |
| `internal/mcp/etag` | 338 | 375 |
| `internal/mcp/evidence` | 486 | 700 |
| `internal/mcp/exec` | 623 | 850 |
| `internal/mcp/explain` | 24 | 100 |
| `internal/mcp/fingerprint` | 145 | 300 |
| `internal/mcp/flight` | 22 | 100 |
| `internal/mcp/gov` | 380 | 600 |
| `internal/mcp/governance` | 222 | 450 |
| `internal/mcp/graph` | 1242 | 1401 |
| `internal/mcp/health` | 118 | 250 |
| `internal/mcp/highlevel` | 1138 | 1250 |
| `internal/mcp/lsp` | 125 | 300 |
| `internal/mcp/mcpargs` | 164 | 300 |
| `internal/mcp/memory` | 245 | 350 |
| `internal/mcp/merge` | 202 | 350 |
| `internal/mcp/mutation` | 96 | 200 |
| `internal/mcp/optimize` | 340 | 400 |
| `internal/mcp/orchestrate` | 61 | 150 |
| `internal/mcp/planner` | 62 | 150 |
| `internal/mcp/policydsl` | 132 | 250 |
| `internal/mcp/preedit` | 160 | 300 |
| `internal/mcp/prompt` | 108 | 250 |
| `internal/mcp/prose` | 29 | 100 |
| `internal/mcp/provenance` | 332 | 336 |
| `internal/mcp/refactor` | 119 | 250 |
| `internal/mcp/retrieve` | 260 | 305 |
| `internal/mcp/review` | 110 | 250 |
| `internal/mcp/root` | 23 | 100 |
| `internal/mcp/runtime` | 142 | 250 |
| `internal/mcp/security` | 301 | 500 |
| `internal/mcp/skill` | 63 | 200 |
| `internal/mcp/stream` | 157 | 300 |
| `internal/mcp/synthtest` | 147 | 250 |
| `internal/mcp/toolsurface` | 91 | 126 |
| `internal/mcp/transform` | 86 | 250 |
| `internal/mcp/transport` | 359 | 525 |
| `internal/mcp/watcher` | 214 | 321 |
| `internal/mcpcatalog` | 978 | 3200 |
| `internal/mcpcli` | 798 | 1197 |
| `internal/mcpmeta` | 503 | 600 |
| `internal/mcpclient` | 528 | 800 |
| `internal/mcpgate` | 429 | 533 |
| `internal/mcpguide` | 57 | 356 |
| `internal/mcphints` | 182 | 275 |
| `internal/mcpserve` | 335 | 335 |
| `internal/mermaid` | 55 | 120 |
| `internal/merkle` | 128 | 192 |
| `internal/memory` | 2471 | 2900 |
| `internal/metaroute` | 1501 | 2016 |
| `internal/metrics` | 902 | 1100 |
| `internal/modernization` | 813 | 900 |
| `internal/mutation` | 828 | 1100 |
| `internal/optimize` | 684 | 700 |
| `internal/ownership` | 150 | 300 |
| `internal/pack` | 751 | 1100 |
| `internal/pii` | 532 | 700 |
| `internal/planner` | 131 | 200 |
| `internal/policy` | 302 | 453 |
| `internal/pluginhash` | 144 | 185 |
| `internal/precache` | 208 | 300 |
| `internal/processgroup` | 46 | 100 |
| `internal/profiles` | 283 | 500 |
| `internal/project` | 1690 | 2100 |
| `internal/prompt` | 52 | 100 |
| `internal/prprovider` | 274 | 300 |
| `internal/relay` | 480 | 600 |
| `internal/rename` | 1225 | 1700 |
| `internal/refactor` | 286 | 300 |
| `internal/refscan` | 249 | 350 |
| `internal/repair` | 354 | 600 |
| `internal/retrieval` | 671 | 900 |
| `internal/rulesblock` | 172 | 260 |
| `internal/runtime` | 2194 | 3000 |
| `internal/saferoot` | 82 | 150 |
| `internal/sandbox` | 2259 | 2507 |
| `internal/sandbox/landlock` | 480 | 707 |
| `internal/schema` | 253 | 400 |
| `internal/script` | 800 | 1000 |
| `internal/sdk` | 401 | 500 |
| `internal/sec` | 652 | 971 |
| `internal/secscan` | 1354 | 1887 |
| `internal/semcache` | 1085 | 1335 |
| `internal/servedledger` | 177 | 235 |
| `internal/session` | 182 | 273 |
| `internal/setup` | 4624 | 5200 |
| `internal/skills` | 403 | 600 |
| `internal/stats` | 389 | 400 |
| `internal/storage` | 797 | 1100 |
| `internal/strutil` | 88 | 100 |
| `internal/swap` | 330 | 455 |
| `internal/tasklife` | 8059 | 11430 |
| `internal/synthtest` | 1198 | 1700 |
| `internal/terse` | 945 | 1100 |
| `internal/testfixture` | 202 | 400 |
| `internal/tokenize` | 1055 | 1500 |
| `internal/tokstats` | 301 | 407 |
| `internal/topology` | 1151 | 1709 |
| `internal/transform` | 468 | 600 |
| `internal/typeimpact` | 98 | 160 |
| `internal/twin` | 1679 | 1900 |
| `internal/twinindex` | 225 | 340 |
| `internal/validate` | 918 | 1300 |
| `internal/verdict` | 1071 | 1173 |
| `internal/verification` | 4628 | 4800 |
| `internal/verifycmd` | 2184 | 2900 |
| `internal/version` | 461 | 600 |
| `internal/webhook` | 241 | 400 |
| `internal/whatif` | 1115 | 1400 |

## Recorded coupling debt (cross-community edges — paid down when each file is next touched)
This section is EMPTY as of 2026-10-21: the three edges recorded from review
plan 3.7-A were verified against live source and closed — `Agent.Code`
(internal/coder/coder.go:113) is decoupled from `internal/execution` via a
consumer-side `worktree` interface (no production import; the concrete
`*execution.Worktree` is injected by internal/loop/loop.go:585), and the
`BuildEfficiencyReport`→`buildContextQuality` (internal/app/efficiency.go) and
`ApplyConsistency`→`CheckConsistency` (internal/context/consistency.go) edges
are intra-package calls, not cross-community couplings. (The two blueprint
entries — analysis ↔ policy verdict and service ↔ metrics — were deleted with
the family in the P4 pivot, commit a05c91c7.) Do not re-record intra-package
or interface-seamed calls here; only genuine cross-package community edges
belong in this ledger.
Change history lives in `git log`, not in this file.
