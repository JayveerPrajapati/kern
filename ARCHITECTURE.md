# ARCHITECTURE.md — subsystem ledger (part 1: caps)

This file is the **machine-readable source of truth for kern's package
architecture**. `go test ./internal/architecture/` (TestArchitectureDocParity)
parses the table below and fails on divergence:

- every listed directory must exist;
- its non-test Go LOC must not exceed the **cap** column;
- every Go package directory under internal/, cmd/ and evaluate/ must be
  listed — its own row, or an ancestor's row whose subtree measurement
  governs it. A package dir with neither fails the gate (the reverse
  direction is what let internal/duplication ship unlisted for months).

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
| `cmd/kern` | 12475 | 19600 |
| `cmd/kern-mcp` | 194 | 291 |
| `internal/agent` | 1888 | 2900 |
| `internal/agents` | 1055 | 1700 |
| `internal/agentwire` | 1561 | 2150 |
| `internal/app` | 2827 | 4395 |
| `internal/apply` | 268 | 402 |
| `internal/approvalcli` | 2061 | 2087 |
| `internal/approvalkeycli` | 244 | 290 |
| `internal/approvalqueue` | 99 | 149 |
| `internal/approvalsign` | 453 | 600 |
| `internal/architecture` | 1638 | 1800 |
| `internal/asttypes` | 272 | 280 |
| `internal/astrefs` | 256 | 280 |
| `internal/auditdiag` | 226 | 300 |
| `internal/auditrot` | 123 | 190 |
| `internal/brief` | 862 | 948 |
| `internal/budget` | 212 | 400 |
| `internal/cache` | 716 | 800 |
| `internal/calibrate` | 777 | 1100 |
| `internal/calleefilter` | 120 | 150 |
| `internal/ci` | 241 | 400 |
| `internal/claims` | 658 | 730 |
| `internal/classparity` | 208 | 350 |
| `internal/clietag` | 95 | 143 |
| `internal/cliflags` | 319 | 425 |
| `internal/clip` | 23 | 100 |
| `internal/clihints` | 139 | 185 |
| `internal/cliout` | 186 | 200 |
| `internal/code` | 1017 | 1400 |
| `internal/coder` | 500 | 800 |
| `internal/commitmsg` | 1332 | 2200 |
| `internal/compress` | 861 | 1000 |
| `internal/configstrip` | 229 | 350 |
| `internal/config` | 728 | 800 |
| `internal/context` | 3971 | 5500 |
| `internal/contextcli` | 1725 | 1983 |
| `internal/daemon` | 543 | 560 |
| `internal/deployment` | 165 | 300 |
| `internal/destructive` | 105 | 200 |
| `internal/destrules` | 382 | 440 |
| `internal/diff` | 879 | 1100 |
| `internal/docbudget` | 124 | 200 |
| `internal/diffgate` | 1428 | 1750 |
| `internal/docsearch` | 1116 | 1200 |
| `internal/diagnostics/repolaunch` | 98 | 150 |
| `internal/doctor` | 1497 | 1500 |
| `internal/domain` | 2477 | 3200 |
| `internal/duplication` | 250 | 375 |
| `internal/eval` | 321 | 500 |
| `internal/eventbus` | 768 | 1100 |
| `internal/explorebudget` | 284 | 411 |
| `internal/evidence` | 1578 | 1800 |
| `internal/execapproval` | 230 | 260 |
| `internal/execclassify` | 479 | 500 |
| `internal/execinterp` | 89 | 240 |
| `internal/execshell` | 29 | 150 |
| `internal/execwin` | 91 | 220 |
| `internal/execution` | 2091 | 2844 |
| `internal/fetch` | 245 | 400 |
| `internal/filecache` | 182 | 255 |
| `internal/fit` | 284 | 400 |
| `internal/flight` | 447 | 700 |
| `internal/flock` | 106 | 200 |
| `internal/fsutil` | 338 | 372 |
| `internal/fw` | 1348 | 2100 |
| `internal/gitrun` | 20 | 100 |
| `internal/gitblocks` | 412 | 444 |
| `internal/graphcache` | 130 | 195 |
| `internal/governance` | 5285 | 5369 |
| `internal/govaudit` | 1663 | 2304 |
| `internal/govtoken` | 88 | 145 |
| `internal/graphcli` | 2748 | 4160 |
| `internal/grepscope` | 137 | 200 |
| `internal/guard` | 710 | 959 |
| `internal/guardrule` | 398 | 420 |
| `internal/guardrulecache` | 178 | 270 |
| `internal/guardread` | 665 | 700 |
| `internal/guardserve` | 353 | 530 |
| `internal/inplaceedit` | 76 | 120 |
| `internal/guardpause` | 463 | 623 |
| `internal/guardpaths` | 348 | 356 |
| `internal/guardstats` | 200 | 240 |
| `internal/guardhook` | 610 | 700 |
| `internal/guardtelemetry` | 61 | 150 |
| `internal/heal` | 1031 | 1569 |
| `internal/hook` | 873 | 900 |
| `internal/host` | 391 | 600 |
| `internal/ignore` | 455 | 500 |
| `internal/incident` | 1096 | 1400 |
| `internal/index` | 12914 | 13050 |
| `internal/indexdb` | 860 | 1000 |
| `internal/indexmem` | 182 | 234 |
| `internal/indexwatch` | 442 | 663 |
| `internal/integration` | 0 | 100 |
| `internal/intel` | 11361 | 11500 |
| `internal/research` | 525 | 630 |
| `internal/langparse` | 4352 | 5450 |
| `internal/learnclaim` | 90 | 135 |
| `internal/learning` | 698 | 1100 |
| `internal/lenses` | 281 | 500 |
| `internal/licensescan` | 160 | 250 |
| `internal/llm` | 2009 | 2800 |
| `internal/lock` | 387 | 600 |
| `internal/loop` | 1605 | 2600 |
| `internal/lspbridge` | 1003 | 1600 |
| `internal/mcp` | 17888 | 19000 |
| `internal/mcp/bridge` | 67 | 200 |
| `internal/mcp/apply` | 130 | 200 |
| `internal/mcp/compose` | 166 | 300 |
| `internal/mcp/context` | 403 | 600 |
| `internal/mcp/contextwatch` | 164 | 300 |
| `internal/mcp/crossrepo` | 34 | 100 |
| `internal/mcp/doc` | 325 | 431 |
| `internal/mcp/envelope` | 79 | 150 |
| `internal/mcp/etag` | 338 | 375 |
| `internal/mcp/evidence` | 486 | 700 |
| `internal/mcp/exec` | 644 | 850 |
| `internal/mcp/explain` | 24 | 100 |
| `internal/mcp/fingerprint` | 146 | 300 |
| `internal/mcp/flight` | 22 | 100 |
| `internal/mcp/gov` | 380 | 600 |
| `internal/mcp/governance` | 222 | 450 |
| `internal/mcp/graph` | 1248 | 1401 |
| `internal/mcp/research` | 248 | 300 |
| `internal/mcp/health` | 118 | 250 |
| `internal/mcp/highlevel` | 1166 | 1250 |
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
| `internal/mcp/provenance` | 334 | 336 |
| `internal/mcp/refactor` | 119 | 250 |
| `internal/mcp/retrieve` | 260 | 305 |
| `internal/mcp/review` | 110 | 250 |
| `internal/mcp/root` | 80 | 100 |
| `internal/mcp/runtime` | 142 | 250 |
| `internal/mcp/security` | 48 | 500 |
| `internal/mcp/skill` | 63 | 200 |
| `internal/mcp/stream` | 157 | 300 |
| `internal/mcp/synthtest` | 147 | 250 |
| `internal/mcp/toolsurface` | 99 | 126 |
| `internal/mcp/transform` | 86 | 250 |
| `internal/mcp/transport` | 359 | 525 |
| `internal/mcp/watcher` | 214 | 321 |
| `internal/mcp/verifyorchestration` | 120 | 150 |
| `internal/mcpcatalog` | 1125 | 3200 |
| `internal/mcpcli` | 798 | 1197 |
| `internal/mcpmeta` | 503 | 600 |
| `internal/mcpclient` | 528 | 800 |
| `internal/mcpgate` | 446 | 533 |
| `internal/mcpguide` | 56 | 356 |
| `internal/mcphints` | 182 | 275 |
| `internal/mcpserve` | 95 | 143 |
| `internal/serveoutput` | 240 | 360 |
| `internal/mermaid` | 55 | 120 |
| `internal/merkle` | 129 | 192 |
| `internal/memory` | 2482 | 2900 |
| `internal/metaroute` | 1493 | 2016 |
| `internal/metrics` | 902 | 1100 |
| `internal/modernization` | 813 | 900 |
| `internal/mutation` | 828 | 1100 |
| `internal/optimize` | 687 | 700 |
| `internal/ownership` | 150 | 300 |
| `internal/pack` | 758 | 1100 |
| `internal/pii` | 587 | 700 |
| `internal/planner` | 131 | 200 |
| `internal/policy` | 302 | 453 |
| `internal/pluginhash` | 161 | 185 |
| `internal/projectstate` | 214 | 300 |
| `internal/precache` | 208 | 300 |
| `internal/processgroup` | 46 | 100 |
| `internal/profiles` | 283 | 500 |
| `internal/project` | 1690 | 2100 |
| `internal/prompt` | 52 | 100 |
| `internal/prprovider` | 274 | 300 |
| `internal/qualify` | 176 | 260 |
| `internal/relay` | 480 | 600 |
| `internal/rename` | 1225 | 1700 |
| `internal/refactor` | 286 | 300 |
| `internal/refscan` | 304 | 350 |
| `internal/repair` | 354 | 600 |
| `internal/retrieval` | 671 | 900 |
| `internal/rulesblock` | 172 | 260 |
| `internal/runtime` | 2194 | 3000 |
| `internal/saferoot` | 82 | 150 |
| `internal/sandbox` | 2384 | 2507 |
| `internal/sandbox/landlock` | 480 | 707 |
| `internal/schema` | 253 | 400 |
| `internal/script` | 880 | 1000 |
| `internal/sdk` | 401 | 500 |
| `internal/sec` | 652 | 971 |
| `internal/secscan` | 1494 | 1887 |
| `internal/semcache` | 1088 | 1335 |
| `internal/servedledger` | 180 | 235 |
| `internal/session` | 182 | 273 |
| `internal/setup` | 4441 | 5200 |
| `internal/setupundo` | 1084 | 1626 |
| `internal/skills` | 403 | 600 |
| `internal/stats` | 389 | 400 |
| `internal/storage` | 818 | 1100 |
| `internal/strutil` | 88 | 100 |
| `internal/swap` | 330 | 455 |
| `internal/tasklife` | 8329 | 11430 |
| `internal/synthtest` | 1198 | 1700 |
| `internal/terse` | 945 | 1100 |
| `internal/testfixture` | 202 | 400 |
| `internal/tokenize` | 1289 | 1500 |
| `internal/toolkind` | 58 | 150 |
| `internal/tokstats` | 301 | 407 |
| `internal/topology` | 1217 | 1709 |
| `internal/transform` | 468 | 600 |
| `internal/typealias` | 224 | 340 |
| `internal/typeimpact` | 147 | 160 |
| `internal/twin` | 1679 | 1900 |
| `internal/twinindex` | 225 | 340 |
| `internal/validate` | 918 | 1300 |
| `internal/verdict` | 1071 | 1173 |
| `internal/verification` | 4678 | 4800 |
| `internal/verifycmd` | 2599 | 2900 |
| `internal/version` | 461 | 600 |
| `internal/webhook` | 241 | 400 |
| `internal/whatif` | 1115 | 1400 |
| `evaluate/agentic` | 2417 | 3626 |
| `evaluate/agentbench` | 268 | 330 |
| `evaluate/bench` | 1270 | 1905 |
| `evaluate/calibration` | 59 | 89 |

## Headroom policy (tight subsystems)

A subsystem with **less than 2% of its cap left** is *tight*: the next ordinary
edit is likely to trip the gate mid-task, so it needs a scheduled extraction
rather than a cap raise. Two rules follow from that:

1. **Report, don't surprise.** `kern doctor --arch-drift` renders a headroom
   section listing every sub-2% subsystem (tightest first) with its documented
   extraction candidate. The candidate map — the single source of truth, one
   evidence-based family per entry — lives in
   `cmd/kern/cmd_arch_headroom.go` (`extractionCandidates`); ARCHITECTURE.md
   states the rule, not the list, so the two cannot drift.
2. **Advisory gate on crossings.** `kern doctor --headroom-gate` exits 1 only
   when *this change* moved a subsystem from above the line to at/below it
   (before = the baseline recorded at HEAD; after = the live measurement). CI
   runs it in a non-blocking lane: the failure is a prompt to record an
   extraction item in the same change, not a merge blocker.

**Scheduling, not sweeping.** A tight package is extracted *when it is next
edited* — there is no mechanical refactor sweep, and a candidate entry is
deleted the moment its extraction lands (pinned by
`TestExtractionCandidatesAreLedgerRows`, which fails on a recovered package
still carrying a stale candidate). Baselines in the table above are refreshed
from live `MeasureDir` output whenever they drift; the cap remains the only
contract.

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
