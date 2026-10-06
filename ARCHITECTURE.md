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
monolith (20.0k LOC after the metaroute extraction), capped, not hidden — its
cap (21,275) sits above the baseline, so the subtree can only accrete
modestly before extraction is forced again. The row measures the whole
subtree (root plus every mcp/* leaf), so crossing the cap forces moving a
family OUT of `internal/mcp` to a top-level sibling (the blueprint→bppolicy,
mcp→mcpserve and mcp→metaroute pattern — extracting to another mcp/<leaf>
stays inside the subtree and cannot relieve it), not cap-raising. `internal/blueprint` is modularized into `internal/bppolicy`,
`internal/bpreceipt`, `internal/resilience`, `internal/bpcli`,
`internal/gates`, and `internal/scanners`; what remains (domain, service,
adapters/kern, audit, sandbox, checks/diffgate) is 6.4k LOC and capped like
every other subsystem. `internal/app`'s TaskService cluster was extracted to `internal/tasklife`
(7.6k LOC, 29 internal deps — now the top fan-out subsystem); `internal/app`
keeps the Platform composition root plus review, benchmark and calibration
surfaces (2.9k LOC). Crossing either cap forces extraction, not cap-raising.
`internal/integration` is 0-LOC by design: a test-only cross-subsystem
integration suite with no production code.

**Allowed deps per subsystem: `docs/architecture/ledger-details.md`** — part 2 of this machine-read ledger, parsed by the same test: the two halves must name exactly the same subsystems (any divergence fails).

| subsystem | dir | LOC baseline | cap |
|---|---|---|---|
| `cmd/kern` | 19979 | 23718 |
| `internal/agent` | 1882 | 2900 |
| `internal/agents` | 1055 | 1700 |
| `internal/app` | 2930 | 4395 |
| `internal/architecture` | 1439 | 1800 |
| `internal/blueprint` | 6410 | 9800 |
| `internal/bpcli` | 6497 | 8954 |
| `internal/bppolicy` | 1171 | 1727 |
| `internal/bpreceipt` | 991 | 1487 |
| `internal/brief` | 632 | 948 |
| `internal/budget` | 212 | 400 |
| `internal/cache` | 530 | 800 |
| `internal/calibrate` | 770 | 1100 |
| `internal/ci` | 241 | 400 |
| `internal/cockpit` | 1179 | 1800 |
| `internal/code` | 936 | 1400 |
| `internal/coder` | 464 | 800 |
| `internal/commitmsg` | 1036 | 1600 |
| `internal/compress` | 753 | 1000 |
| `internal/config` | 645 | 800 |
| `internal/context` | 3906 | 5500 |
| `internal/council` | 426 | 700 |
| `internal/deployment` | 165 | 300 |
| `internal/diff` | 879 | 1100 |
| `internal/docbudget` | 124 | 200 |
| `internal/docsearch` | 888 | 1200 |
| `internal/doctor` | 1017 | 1500 |
| `internal/domain` | 2404 | 3200 |
| `internal/draft` | 746 | 1119 |
| `internal/enterprise` | 2280 | 3420 |
| `internal/eval` | 321 | 500 |
| `internal/eventbus` | 715 | 1100 |
| `internal/evidence` | 1378 | 1800 |
| `internal/execution` | 802 | 900 |
| `internal/fetch` | 245 | 400 |
| `internal/fit` | 255 | 400 |
| `internal/flight` | 447 | 700 |
| `internal/flock` | 105 | 200 |
| `internal/fsutil` | 144 | 216 |
| `internal/fw` | 1348 | 2100 |
| `internal/fragility` | 282 | 500 |
| `internal/gates` | 1069 | 1600 |
| `internal/gitblocks` | 296 | 444 |
| `internal/governance` | 6041 | 6344 |
| `internal/guard` | 639 | 959 |
| `internal/heal` | 634 | 1000 |
| `internal/hook` | 539 | 900 |
| `internal/host` | 391 | 600 |
| `internal/ignore` | 288 | 500 |
| `internal/incident` | 1095 | 1400 |
| `internal/index` | 12687 | 12900 |
| `internal/integration` | 0 | 100 |
| `internal/intel` | 10772 | 11800 |
| `internal/learnclaim` | 90 | 135 |
| `internal/learning` | 698 | 1100 |
| `internal/lenses` | 281 | 500 |
| `internal/llm` | 1822 | 2800 |
| `internal/lock` | 378 | 600 |
| `internal/loop` | 2013 | 2600 |
| `internal/lsp` | 658 | 1000 |
| `internal/lspbridge` | 996 | 1600 |
| `internal/mcp` | 20018 | 21275 |
| `internal/mcp/agentctl` | 220 | 300 |
| `internal/mcp/bridge` | 81 | 200 |
| `internal/mcp/blueprint` | 174 | 350 |
| `internal/mcp/catalog` | 2044 | 3200 |
| `internal/mcp/compose` | 166 | 300 |
| `internal/mcp/context` | 361 | 600 |
| `internal/mcp/contextwatch` | 164 | 300 |
| `internal/mcp/coord` | 368 | 400 |
| `internal/mcp/crossrepo` | 34 | 100 |
| `internal/mcp/deploy` | 60 | 150 |
| `internal/mcp/doc` | 332 | 431 |
| `internal/mcp/envelope` | 79 | 150 |
| `internal/mcp/etag` | 285 | 375 |
| `internal/mcp/evidence` | 455 | 700 |
| `internal/mcp/exec` | 527 | 850 |
| `internal/mcp/explain` | 24 | 100 |
| `internal/mcp/fingerprint` | 145 | 300 |
| `internal/mcp/flight` | 36 | 100 |
| `internal/mcp/fragility` | 111 | 250 |
| `internal/mcp/gov` | 369 | 600 |
| `internal/mcp/governance` | 285 | 450 |
| `internal/mcp/graph` | 1044 | 1401 |
| `internal/mcp/health` | 118 | 250 |
| `internal/mcp/highlevel` | 856 | 1250 |
| `internal/mcp/lsp` | 139 | 300 |
| `internal/mcp/mcpargs` | 147 | 300 |
| `internal/mcp/memory` | 195 | 350 |
| `internal/mcp/meta` | 1381 | 1450 |
| `internal/mcp/merge` | 187 | 350 |
| `internal/mcp/mutation` | 95 | 200 |
| `internal/mcp/optimize` | 284 | 400 |
| `internal/mcp/orchestrate` | 61 | 150 |
| `internal/mcp/org` | 562 | 900 |
| `internal/mcp/planner` | 58 | 150 |
| `internal/mcp/policydsl` | 132 | 250 |
| `internal/mcp/preedit` | 160 | 300 |
| `internal/mcp/prompt` | 122 | 250 |
| `internal/mcp/prose` | 29 | 100 |
| `internal/mcp/provenance` | 224 | 336 |
| `internal/mcp/rbac` | 255 | 300 |
| `internal/mcp/refactor` | 118 | 250 |
| `internal/mcp/repair` | 73 | 200 |
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
| `internal/mcpclient` | 528 | 800 |
| `internal/mcpgate` | 355 | 533 |
| `internal/mcpguide` | 237 | 356 |
| `internal/mcpserve` | 223 | 335 |
| `internal/memory` | 1949 | 2900 |
| `internal/metaroute` | 1344 | 2016 |
| `internal/metrics` | 706 | 1100 |
| `internal/modernization` | 812 | 900 |
| `internal/mutation` | 715 | 1100 |
| `internal/optimize` | 540 | 700 |
| `internal/orgapprovals` | 918 | 1000 |
| `internal/ownership` | 150 | 300 |
| `internal/pack` | 731 | 1100 |
| `internal/pii` | 454 | 700 |
| `internal/planner` | 131 | 200 |
| `internal/policy` | 302 | 453 |
| `internal/precache` | 179 | 300 |
| `internal/processgroup` | 46 | 100 |
| `internal/profiles` | 283 | 500 |
| `internal/project` | 1397 | 2100 |
| `internal/prompt` | 52 | 100 |
| `internal/prprovider` | 274 | 300 |
| `internal/relay` | 428 | 600 |
| `internal/remove` | 409 | 500 |
| `internal/rename` | 1210 | 1700 |
| `internal/refactor` | 231 | 300 |
| `internal/repair` | 343 | 600 |
| `internal/resilience` | 1199 | 1799 |
| `internal/retrieval` | 632 | 900 |
| `internal/reviewpack` | 535 | 900 |
| `internal/rulesblock` | 172 | 260 |
| `internal/runtime` | 1959 | 3000 |
| `internal/sandbox` | 1746 | 2507 |
| `internal/sandbox/landlock` | 471 | 707 |
| `internal/schema` | 253 | 400 |
| `internal/scanners` | 2069 | 3104 |
| `internal/script` | 670 | 1000 |
| `internal/sdk` | 279 | 500 |
| `internal/sec` | 648 | 971 |
| `internal/secscan` | 1296 | 1887 |
| `internal/semcache` | 890 | 1335 |
| `internal/session` | 182 | 273 |
| `internal/setup` | 3582 | 3800 |
| `internal/skills` | 402 | 600 |
| `internal/stats` | 231 | 400 |
| `internal/storage` | 697 | 1100 |
| `internal/strutil` | 59 | 100 |
| `internal/swap` | 330 | 455 |
| `internal/tasklife` | 7874 | 11430 |
| `internal/synthtest` | 1238 | 1700 |
| `internal/terse` | 876 | 1100 |
| `internal/testfixture` | 202 | 400 |
| `internal/tokenize` | 1050 | 1500 |
| `internal/tokstats` | 271 | 407 |
| `internal/transform` | 461 | 600 |
| `internal/twin` | 1680 | 1900 |
| `internal/validate` | 797 | 1300 |
| `internal/verdict` | 782 | 1173 |
| `internal/verification` | 3200 | 4800 |
| `internal/verifycmd` | 828 | 1242 |
| `internal/version` | 364 | 600 |
| `internal/web` | 4452 | 4700 |
| `internal/webhook` | 204 | 400 |
| `internal/whatif` | 1013 | 1400 |

Change history lives in `git log`, not in this file.
