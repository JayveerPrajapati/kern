# ARCHITECTURE.md — subsystem ledger (part 1: caps)

This file is the **machine-readable source of truth for kern's package
architecture**. `go test ./internal/architecture/` (TestArchitectureDocParity)
parses the table below and fails on divergence:

- every listed directory must exist;
- its non-test Go LOC must not exceed the **cap** column.

The **cap** column is the drift gate (caps are ~1.5x the LOC baseline, rounded
up). The LOC baseline is informational (measured 2026-09-12; rows added
2026-09-15 carry that day's measurement; **baseline column refreshed
2026-09-22** against live `MeasureDir` output — non-test Go LOC only — for
every row whose measured value had drifted; caps and allowed deps unchanged).
Allowed deps are the actual import set at baseline: adding a new internal
import means updating the allowed-deps list first — that is the drift gate.
Stdlib and third-party (e.g. build-tagged tree-sitter) imports are always
allowed; a subsystem's own subpackages are always allowed.

Known drift (documented honestly, caps set accordingly): `internal/mcp` is a
monolith (19.9k LOC, 121 deps), capped, not hidden — its cap was tightened
2026-09-25 to a ~1.07x growth budget (21,275), deliberately below the ~1.5x
convention, so the monolith cannot quietly accrete: crossing the cap forces
extraction of the next mcp/<leaf> (the mcp/* leaf rows already carve off
handler families), not cap-raising. `internal/blueprint` was
modularized 2026-09-18 (blueprint modularization Stages A–C) into
`internal/bppolicy`, `internal/bpreceipt`, `internal/resilience`,
`internal/bpcli`, `internal/gates`, and `internal/scanners`; what remains
(domain, service, adapters/kern, audit, sandbox, checks/diffgate) is 6.7k LOC
and capped like every other subsystem. `internal/app` is the top fan-out
subsystem (31 internal deps, 9.5k LOC, growing); its cap was tightened
2026-09-24 to a ~1.3x growth budget (12,300) — deliberately below the ~1.5x
convention — so it cannot silently become the next `internal/mcp` monolith:
crossing the cap forces extraction, not cap-raising. `internal/service` (a
redundant second middle layer duplicating engine functions) was dissolved
2026-09-24: its genuinely-useful logic was absorbed into the engine packages
(`internal/index`, `internal/governance`, `internal/intel`) and its ~29 call
sites migrated to engines / `internal/app`; the package is deleted and its
ledger row removed.

**Allowed deps per subsystem + the full changelog: `docs/architecture/ledger-details.md`** — part 2 of this machine-read ledger, parsed by the same test: the two halves must name exactly the same subsystems (any divergence fails).

| subsystem | dir | LOC baseline | cap |
|---|---|---|---|
| `cmd/kern` | 18244 | 23718 |
| `internal/agent` | 1882 | 2900 |
| `internal/agents` | 1055 | 1700 |
| `internal/app` | 9460 | 12300 |
| `internal/architecture` | 1362 | 1800 |
| `internal/blueprint` | 6481 | 9800 |
| `internal/bpcli` | 6150 | 8954 |
| `internal/bppolicy` | 1171 | 1727 |
| `internal/bpreceipt` | 991 | 1487 |
| `internal/brief` | 343 | 600 |
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
| `internal/context` | 3685 | 5500 |
| `internal/council` | 426 | 700 |
| `internal/deployment` | 165 | 300 |
| `internal/diff` | 879 | 1100 |
| `internal/docbudget` | 124 | 200 |
| `internal/docsearch` | 818 | 1200 |
| `internal/doctor` | 912 | 1500 |
| `internal/domain` | 2379 | 3200 |
| `internal/enterprise` | 2280 | 3420 |
| `internal/eval` | 321 | 500 |
| `internal/eventbus` | 715 | 1100 |
| `internal/evidence` | 1378 | 1800 |
| `internal/execution` | 658 | 900 |
| `internal/fetch` | 245 | 400 |
| `internal/fit` | 255 | 400 |
| `internal/flight` | 447 | 700 |
| `internal/flock` | 105 | 200 |
| `internal/fsutil` | 62 | 100 |
| `internal/fw` | 1348 | 2100 |
| `internal/fragility` | 282 | 500 |
| `internal/gates` | 1069 | 1600 |
| `internal/governance` | 5398 | 5600 |
| `internal/heal` | 634 | 1000 |
| `internal/hook` | 489 | 500 |
| `internal/host` | 391 | 600 |
| `internal/ignore` | 288 | 500 |
| `internal/incident` | 1076 | 1400 |
| `internal/index` | 10514 | 12900 |
| `internal/integration` | 0 | 100 |
| `internal/intel` | 10656 | 11800 |
| `internal/learnclaim` | 90 | 135 |
| `internal/learning` | 698 | 1100 |
| `internal/lenses` | 281 | 500 |
| `internal/llm` | 1620 | 1700 |
| `internal/lock` | 378 | 600 |
| `internal/loop` | 2013 | 2600 |
| `internal/lsp` | 658 | 1000 |
| `internal/lspbridge` | 996 | 1600 |
| `internal/mcp` | 19883 | 21275 |
| `internal/mcp/agentctl` | 171 | 300 |
| `internal/mcp/bridge` | 81 | 200 |
| `internal/mcp/blueprint` | 214 | 350 |
| `internal/mcp/catalog` | 2148 | 3200 |
| `internal/mcp/compose` | 166 | 300 |
| `internal/mcp/context` | 393 | 600 |
| `internal/mcp/contextwatch` | 164 | 300 |
| `internal/mcp/coord` | 368 | 400 |
| `internal/mcp/crossrepo` | 34 | 100 |
| `internal/mcp/deploy` | 73 | 150 |
| `internal/mcp/doc` | 332 | 431 |
| `internal/mcp/envelope` | 79 | 150 |
| `internal/mcp/evidence` | 455 | 700 |
| `internal/mcp/exec` | 542 | 850 |
| `internal/mcp/explain` | 24 | 100 |
| `internal/mcp/fingerprint` | 145 | 300 |
| `internal/mcp/flight` | 36 | 100 |
| `internal/mcp/fragility` | 111 | 250 |
| `internal/mcp/gov` | 369 | 600 |
| `internal/mcp/governance` | 285 | 450 |
| `internal/mcp/graph` | 1044 | 1401 |
| `internal/mcp/health` | 118 | 250 |
| `internal/mcp/highlevel` | 830 | 1250 |
| `internal/mcp/lsp` | 139 | 300 |
| `internal/mcp/mcpargs` | 147 | 300 |
| `internal/mcp/memory` | 195 | 350 |
| `internal/mcp/meta` | 952 | 1450 |
| `internal/mcp/merge` | 187 | 350 |
| `internal/mcp/mutation` | 95 | 200 |
| `internal/mcp/optimize` | 284 | 400 |
| `internal/mcp/orchestrate` | 61 | 150 |
| `internal/mcp/org` | 562 | 900 |
| `internal/mcp/planner` | 58 | 150 |
| `internal/mcp/policydsl` | 144 | 250 |
| `internal/mcp/preedit` | 159 | 300 |
| `internal/mcp/prompt` | 122 | 250 |
| `internal/mcp/prose` | 29 | 100 |
| `internal/mcp/provenance` | 224 | 225 |
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
| `internal/mcp/synthtest` | 153 | 250 |
| `internal/mcp/transform` | 100 | 250 |
| `internal/mcp/transport` | 325 | 525 |
| `internal/mcpclient` | 528 | 800 |
| `internal/memory` | 1949 | 2900 |
| `internal/metrics` | 706 | 1100 |
| `internal/modernization` | 812 | 900 |
| `internal/mutation` | 400 | 700 |
| `internal/optimize` | 540 | 700 |
| `internal/orgapprovals` | 918 | 1000 |
| `internal/ownership` | 150 | 300 |
| `internal/pack` | 731 | 1100 |
| `internal/pii` | 454 | 700 |
| `internal/planner` | 131 | 200 |
| `internal/precache` | 179 | 300 |
| `internal/processgroup` | 46 | 100 |
| `internal/profiles` | 283 | 500 |
| `internal/project` | 1397 | 2100 |
| `internal/prompt` | 52 | 100 |
| `internal/prprovider` | 274 | 300 |
| `internal/relay` | 428 | 600 |
| `internal/remove` | 338 | 500 |
| `internal/rename` | 1074 | 1700 |
| `internal/refactor` | 231 | 300 |
| `internal/repair` | 343 | 600 |
| `internal/resilience` | 1199 | 1799 |
| `internal/retrieval` | 632 | 900 |
| `internal/reviewpack` | 535 | 900 |
| `internal/runtime` | 1959 | 3000 |
| `internal/sandbox` | 1671 | 2507 |
| `internal/sandbox/landlock` | 471 | 707 |
| `internal/schema` | 253 | 400 |
| `internal/scanners` | 2069 | 3104 |
| `internal/script` | 670 | 1000 |
| `internal/sdk` | 279 | 500 |
| `internal/sec` | 1578 | 1900 |
| `internal/semcache` | 890 | 1335 |
| `internal/setup` | 2661 | 3800 |
| `internal/skills` | 402 | 600 |
| `internal/stats` | 231 | 400 |
| `internal/storage` | 697 | 1100 |
| `internal/strutil` | 59 | 100 |
| `internal/swap` | 303 | 455 |
| `internal/synthtest` | 1084 | 1700 |
| `internal/terse` | 876 | 1100 |
| `internal/testfixture` | 202 | 400 |
| `internal/tokenize` | 1050 | 1500 |
| `internal/transform` | 461 | 600 |
| `internal/twin` | 1680 | 1900 |
| `internal/validate` | 797 | 1300 |
| `internal/verification` | 4583 | 4800 |
| `internal/version` | 364 | 600 |
| `internal/web` | 4191 | 4700 |
| `internal/webhook` | 204 | 400 |
| `internal/whatif` | 1013 | 1400 |

## Changelog

1 subsystem row added 2026-09-25 (internal/mcp/root — the resolveRoot consolidation: 24 byte-identical local resolveRoot copies across the internal/mcp/* tool packages plus the internal/mcp/server.go variant (empty-root Getwd-failure divergence) collapsed into the shared leaf root.ResolveRoot (23 LOC/cap 100, stdlib-only); the 24 package-level funcs deleted and every call site re-pointed (rootpkg alias in mcp/evidence + mcp/highlevel where a local root variable shadows the package name); server.go keeps a one-line resolveRoot delegate so its in-package callers are untouched; the internal/config variant aligned in place with filepath.Clean (config cannot import internal/mcp — layer rules); 25 rows gained the `internal/mcp/root` allowed dep; now-unused os/path/filepath imports dropped; behavior identical except the server.go empty-root Getwd-failure fallback, which now matches canonical "."; ARCHITECTURE.md row added).
1 subsystem row added 2026-09-25 (cmd/kern — the CLI brought under the ledger: 18,244 non-test LOC/cap 23,718, a deliberately tight ~1.3x growth budget matching the internal/app precedent so the CLI cannot silently become a monolith; allowed deps = its 85 collapsed top-level internal imports, see ledger-details.md).
1 subsystem row updated 2026-09-25 (internal/mcp cap 29400→21275, baseline refreshed 19595→19883 — the monolith gets a ~1.07x budget, a forcing function for the next mcp/<leaf> extraction instead of accretion; the old 29400 cap was ~1.5x and gave the monolith room to grow).
1 subsystem row updated 2026-09-25 (internal/enterprise baseline refreshed 1767→2280, cap raised 2200→3420 — the ProjectApp seam (app.go interface + SetAppFactory) adds ~86 LOC to break the mcp → org → enterprise → web closure; the old cap 2200 was razor-thin against the real 2194 and the seam pushed it over, so the cap was re-set per the ~1.5x convention; web baseline refreshed 3785→4191 — ArchitectureReport now converts its JSON DTO to architecture.Report so enterprise can name the type without importing web).
1 subsystem row added 2026-09-25 (internal/sandbox/landlock — the sandbox LOC-cap extraction: the Linux Landlock confinement subsystem (landlock_paths.go allowlist builder, landlock_linux.go trampoline/apply/probe, landlock_other.go stub; 471 LOC/cap 707) moved verbatim from internal/sandbox into the leaf package landlock — a self-contained subsystem with zero kern-internal imports (stdlib + golang.org/x/sys only, so the ledger allowed-deps cell is empty); the trampoline child spec now carries the parent-computed network-isolation argv prefix (netIsolationPrefix stays in internal/sandbox — the leaf must not import it back), and every internal/sandbox reference re-pointed to landlock.* (LandlockAvailable/ChildSpecJSON/ChildSpecEnv/AllowRule/StripEnvVar/LandlockAllowPaths/FsAccessWrite/FsAccessWriteFile); a failed LandlockAvailable probe now logs "landlock fs confinement unavailable: %v" once and NetworkPolicy.Summary reports "; fs confinement unavailable (degraded)" when NetnsAvail && !FSConfined — operators can see the sensitive-path blocklist is off; behavior identical, pure move; ARCHITECTURE.md row added; sandbox baseline refreshed 960→1671 — note MeasureDir walks subpackages (the internal/mcp precedent), so the sandbox row measures the whole subtree including the landlock leaf and the parent cap was re-set 1200→2507 (exactly ~1.5x the measured 1671, per the ledger convention — mirrors the internal/enterprise re-set precedent; MeasureDir walks subpackages, so the parent row measures the whole subtree including the landlock leaf, and the leaf's own 707 cap remains the confinement subsystem's real growth gate).
1 subsystem row removed 2026-09-25 (internal/consistency — dead package: the cross-engine consistency checker (engine.go, 237 LOC) has zero importers anywhere (its only reference was its own white-box test engine_test.go); deleted whole, ledger row removed, allowed deps (`internal/context` `internal/domain`) gone with it).
1 subsystem row removed 2026-09-25 (internal/twin/edges — dead package: the relationship-edge builder (doc.go/edges.go/edges_test.go, 145 LOC) has zero references anywhere; deleted whole; twin row baseline refreshed 1504→1680 (measured live after the deletion, cap 1900 holds); no ledger row existed for the leaf).
