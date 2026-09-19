# ARCHITECTURE.md — subsystem ledger

This file is the **machine-readable source of truth for kern's package
architecture**. `go test ./internal/architecture/` (TestArchitectureDocParity)
parses the table below and fails on divergence:

- every listed directory must exist;
- its non-test Go LOC must not exceed the **cap** column;
- its kern-internal imports must stay within the **allowed deps** column.

The baseline column records LOC measured on 2026-09-12 (informational);
rows added on 2026-09-15 carry that day's measurement. Caps are ~1.5x
baseline, rounded up. Allowed deps are the actual import set at baseline: adding a new internal import means updating this table first —
that is the drift gate. Stdlib and third-party (e.g. build-tagged
tree-sitter) imports are always allowed; a subsystem's own subpackages are
always allowed.

Known drift (documented honestly, caps set accordingly): `internal/mcp` is a
monolith (13.7k LOC, 66 deps), capped, not hidden. `internal/blueprint` was
modularized 2026-09-18 (blueprint modularization Stages A–C) into
`internal/bppolicy`, `internal/bpreceipt`, `internal/resilience`,
`internal/bpcli`, `internal/gates`, and `internal/scanners`; what remains
(domain, service, adapters/kern, audit, sandbox, checks/diffgate) is 6.7k LOC
and capped like every other subsystem.

| subsystem | dir | LOC baseline (2026-09-12) | cap | allowed deps |
|---|---|---|---|---|
| `internal/agent` | 1908 | 2900 | `internal/cache` `internal/context` `internal/domain` `internal/eventbus` `internal/governance` `internal/fsutil` `internal/llm` `internal/metrics` `internal/verification` `internal/whatif` |
| `internal/agents` | 1080 | 1700 | `internal/agent` `internal/config` `internal/domain` `internal/eventbus` `internal/governance` |
| `internal/app` | 6126 | 9200 | `internal/agent` `internal/agents` `internal/cache` `internal/coder` `internal/context` `internal/deployment` `internal/domain` `internal/eventbus` `internal/execution` `internal/flight` `internal/fsutil` `internal/governance` `internal/incident` `internal/index` `internal/intel` `internal/intelligence` `internal/learning` `internal/lenses` `internal/loop` `internal/memory` `internal/modernization` `internal/planner` `internal/prprovider` `internal/runtime` `internal/storage` `internal/twin` `internal/verification` `internal/whatif` |
| `internal/architecture` | 1152 | 1800 | `internal/index` `internal/intel` |
| `internal/blueprint` | 6472 | 9800 | `internal/bpreceipt` `internal/docbudget` `internal/execution` `internal/flock` `internal/fsutil` `internal/note` `internal/resilience` `internal/sec` |
| `internal/bpcli` | 5969 | 8954 | `internal/blueprint` `internal/bppolicy` `internal/bpreceipt` `internal/gates` `internal/governance` `internal/resilience` `internal/scanners` `internal/storage` |
| `internal/bppolicy` | 1151 | 1727 | `internal/blueprint` |
| `internal/bpreceipt` | 991 | 1487 | `internal/blueprint` `internal/fsutil` |
| `internal/brief` | 346 | 600 | `internal/code` `internal/index` `internal/intel` `internal/memory` `internal/stats` |
| `internal/budget` | 212 | 400 | `internal/code` `internal/tokenize` |
| `internal/cache` | 501 | 800 | `internal/config` `internal/flock` |
| `internal/calibrate` | 405 | 700 | `internal/fsutil` `internal/index` `internal/intel` `internal/version` |
| `internal/ci` | 222 | 400 |  |
| `internal/cockpit` | 1176 | 1800 | `internal/blueprint` `internal/bpreceipt` `internal/domain` `internal/eventbus` `internal/execution` `internal/gates` `internal/incident` `internal/index` `internal/loop` `internal/memory` `internal/optimize` `internal/runtime` |
| `internal/code` | 903 | 1400 | `internal/cache` `internal/ignore` |
| `internal/coder` | 467 | 800 | `internal/agent` `internal/agents` `internal/execution` `internal/llm` `internal/pii` `internal/verification` |
| `internal/commitmsg` | 644 | 1000 | `internal/code` |
| `internal/compress` | 652 | 1000 | `internal/terse` |
| `internal/config` | 501 | 800 |  |
| `internal/consistency` | 237 | 400 | `internal/context` `internal/domain` |
| `internal/context` | 3645 | 5500 | `internal/budget` `internal/config` `internal/domain` `internal/eventbus` `internal/evidence` `internal/governance` `internal/index` `internal/intel` `internal/intelligence` `internal/lenses` `internal/memory` `internal/metrics` `internal/runtime` `internal/skills` `internal/tokenize` `internal/whatif` |
| `internal/council` | 426 | 700 | `internal/reviewpack` |
| `internal/deployment` | 165 | 300 | `internal/config` |
| `internal/diff` | 728 | 1100 | `internal/index` |
| `internal/docbudget` | 121 | 200 |  |
| `internal/docsearch` | 742 | 1200 | `internal/cache` `internal/index` |
| `internal/doctor` | 534 | 900 | `internal/cache` `internal/index` `internal/intel` `internal/llm` `internal/runtime` `internal/script` `internal/setup` `internal/stats` `internal/version` |
| `internal/domain` | 2132 | 3200 | `internal/index` `internal/intel` `internal/sec` |
| `internal/enterprise` | 1461 | 2200 | `internal/domain` `internal/eventbus` `internal/governance` `internal/intel` `internal/memory` `internal/storage` `internal/web` |
| `internal/eval` | 326 | 500 | `internal/budget` `internal/llm` `internal/tokenize` |
| `internal/eventbus` | 714 | 1100 |  |
| `internal/evidence` | 1153 | 1800 | `internal/domain` `internal/governance` `internal/index` `internal/sec` `internal/storage` |
| `internal/execution` | 540 | 900 | `internal/governance` `internal/ignore` `internal/metrics` `internal/sandbox` |
| `internal/fetch` | 245 | 400 |  |
| `internal/fit` | 248 | 400 | `internal/code` `internal/index` `internal/tokenize` |
| `internal/flight` | 460 | 700 | `internal/storage` |
| `internal/flock` | 105 | 200 |  |
| `internal/fsutil` | 35 | 100 |  |
| `internal/fw` | 1337 | 2100 | `internal/ignore` `internal/index` |
| `internal/fragility` | 282 | 500 | `internal/index` |
| `internal/gates` | 1064 | 1600 | `internal/blueprint` `internal/bppolicy` |
| `internal/governance` | 3669 | 5600 | `internal/cache` `internal/config` `internal/domain` `internal/eventbus` `internal/flock` `internal/index` `internal/metrics` `internal/storage` |
| `internal/heal` | 252 | 500 | `internal/diff` `internal/index` `internal/intel` `internal/llm` `internal/sandbox` `internal/validate` |
| `internal/hook` | 330 | 500 | `internal/memory` `internal/optimize` |
| `internal/hooks` | 159 | 300 | `internal/memory` |
| `internal/host` | 389 | 600 | `internal/budget` `internal/domain` |
| `internal/ignore` | 287 | 500 |  |
| `internal/incident` | 868 | 1400 | `internal/cache` `internal/domain` `internal/eventbus` `internal/evidence` `internal/execution` `internal/fsutil` `internal/governance` `internal/index` `internal/intelligence` `internal/memory` `internal/prprovider` `internal/runtime` `internal/verification` |
| `internal/index` | 8583 | 12900 | `internal/cache` `internal/ignore` `internal/metrics` `internal/tokenize` |
| `internal/integration` | 0 | 100 |  |
| `internal/intel` | 7837 | 11800 | `internal/budget` `internal/cache` `internal/code` `internal/eventbus` `internal/index` `internal/tokenize` |
| `internal/intelligence` | 855 | 1300 | `internal/domain` `internal/index` |
| `internal/kernconfig` | 135 | 300 |  |
| `internal/learning` | 197 | 300 | `internal/cache` `internal/domain` `internal/memory` |
| `internal/lenses` | 289 | 500 | `internal/domain` |
| `internal/llm` | 1102 | 1700 | `internal/config` |
| `internal/lock` | 367 | 600 | `internal/flock` |
| `internal/loop` | 1700 | 2600 | `internal/blueprint` `internal/bppolicy` `internal/coder` `internal/deployment` `internal/domain` `internal/eventbus` `internal/execution` `internal/flight` `internal/governance` `internal/incident` `internal/learning` `internal/memory` `internal/planner` `internal/runtime` `internal/scanners` `internal/verification` |
| `internal/lsp` | 658 | 1000 | `internal/index` |
| `internal/lspbridge` | 1006 | 1600 |  |
| `internal/mcp` | 13775 | 20700 | `internal/agent` `internal/app` `internal/blueprint/checks/diffgate` `internal/bpcli` `internal/brief` `internal/budget` `internal/cache` `internal/code` `internal/commitmsg` `internal/config` `internal/context` `internal/diff` `internal/docsearch` `internal/domain` `internal/enterprise` `internal/evidence` `internal/fetch` `internal/fit` `internal/flight` `internal/fragility` `internal/fw` `internal/governance` `internal/heal` `internal/index` `internal/intel` `internal/lenses` `internal/llm` `internal/lock` `internal/loop` `internal/lspbridge` `internal/mcp/agentctl` `internal/mcp/bridge` `internal/mcp/catalog` `internal/mcp/compose` `internal/mcp/contextwatch` `internal/mcp/coord` `internal/mcp/crossrepo` `internal/mcp/deploy` `internal/mcp/doc` `internal/mcp/evidence` `internal/mcp/exec` `internal/mcp/explain` `internal/mcp/fingerprint` `internal/mcp/flight` `internal/mcp/fragility` `internal/mcp/gov` `internal/mcp/graph` `internal/mcp/health` `internal/mcp/lsp` `internal/mcp/mcpargs` `internal/mcp/memory` `internal/mcp/merge` `internal/mcp/mutation` `internal/mcp/note` `internal/mcp/optimize` `internal/mcp/org` `internal/mcp/policydsl` `internal/mcp/preedit` `internal/mcp/prompt` `internal/mcp/prose` `internal/mcp/provenance` `internal/mcp/rbac` `internal/mcp/refactor` `internal/mcp/repair` `internal/mcp/retrieve` `internal/mcp/review` `internal/mcp/runtime` `internal/mcp/security` `internal/mcp/skill` `internal/mcp/stream` `internal/mcp/synthtest` `internal/mcp/transform` `internal/mcp/transport` `internal/mcpclient` `internal/memory` `internal/metrics` `internal/mutation` `internal/note` `internal/optimize` `internal/pack` `internal/pii` `internal/precache` `internal/profiles` `internal/project` `internal/prompt` `internal/refactor` `internal/relay` `internal/rename` `internal/repair` `internal/retrieval` `internal/runtime` `internal/sandbox` `internal/schema` `internal/script` `internal/sec` `internal/semcache` `internal/service` `internal/skills` `internal/stats` `internal/storage` `internal/strutil` `internal/swap` `internal/synthtest` `internal/terse` `internal/tokenize` `internal/transform` `internal/validate` `internal/verification` `internal/verify` `internal/version` `internal/whatif` |
| `internal/mcp/agentctl` | 165 | 300 | `internal/app` `internal/llm` `internal/mcp/mcpargs` |
| `internal/mcp/bridge` | 68 | 200 | `internal/mcp/mcpargs` `internal/mcpclient` |
| `internal/mcp/catalog` | 2057 | 3200 | `internal/blueprint/checks/diffgate` |
| `internal/mcp/compose` | 157 | 300 | `internal/mcp/mcpargs` |
| `internal/mcp/contextwatch` | 155 | 300 | `internal/tokenize` |
| `internal/mcp/coord` | 240 | 400 | `internal/mcp/mcpargs` |
| `internal/mcp/crossrepo` | 25 | 100 | `internal/intel` |
| `internal/mcp/deploy` | 51 | 150 | `internal/agent` `internal/app` `internal/mcp/mcpargs` |
| `internal/mcp/doc` | 287 | 431 | `internal/cache` `internal/commitmsg` `internal/docsearch` `internal/fetch` `internal/index` `internal/intel` `internal/llm` `internal/mcp/gov` `internal/mcp/mcpargs` `internal/precache` `internal/strutil` |
| `internal/mcp/evidence` | 455 | 700 | `internal/evidence` `internal/fetch` `internal/governance` `internal/index` `internal/mcp/mcpargs` `internal/storage` |
| `internal/mcp/exec` | 185 | 350 | `internal/governance` `internal/mcp/mcpargs` `internal/optimize` `internal/pii` `internal/sandbox` `internal/script` |
| `internal/mcp/explain` | 25 | 100 | `internal/index` `internal/intel` |
| `internal/mcp/fingerprint` | 148 | 300 | `internal/governance` `internal/mcp/mcpargs` |
| `internal/mcp/flight` | 26 | 100 | `internal/flight` `internal/mcp/mcpargs` |
| `internal/mcp/fragility` | 98 | 250 | `internal/fragility` `internal/mcp/mcpargs` |
| `internal/mcp/gov` | 252 | 380 | `internal/domain` `internal/governance` `internal/index` `internal/mcp/mcpargs` `internal/mcp/provenance` |
| `internal/mcp/graph` | 1110 | 1401 | `internal/budget` `internal/context` `internal/fw` `internal/index` `internal/intel` `internal/lenses` `internal/llm` `internal/mcp/gov` `internal/mcp/mcpargs` `internal/mcp/provenance` `internal/profiles` `internal/project` `internal/retrieval` |
| `internal/mcp/health` | 108 | 250 | `internal/index` `internal/mcp/mcpargs` `internal/metrics` |
| `internal/mcp/lsp` | 139 | 300 | `internal/lspbridge` `internal/mcp/mcpargs` |
| `internal/mcp/mcpargs` | 147 | 300 |  |
| `internal/mcp/memory` | 196 | 350 | `internal/mcp/mcpargs` `internal/memory` |
| `internal/mcp/merge` | 187 | 350 | `internal/diff` `internal/index` `internal/intel` `internal/mcp/mcpargs` |
| `internal/mcp/mutation` | 95 | 200 | `internal/mcp/mcpargs` `internal/mutation` |
| `internal/mcp/note` | 126 | 300 | `internal/mcp/mcpargs` `internal/note` |
| `internal/mcp/optimize` | 245 | 400 | `internal/budget` `internal/mcp/mcpargs` `internal/optimize` `internal/semcache` `internal/strutil` `internal/swap` `internal/terse` `internal/tokenize` |
| `internal/mcp/org` | 353 | 530 | `internal/domain` `internal/enterprise` `internal/governance` `internal/intel` `internal/mcp/mcpargs` |
| `internal/mcp/policydsl` | 128 | 250 | `internal/intel` `internal/mcp/mcpargs` |
| `internal/mcp/preedit` | 145 | 300 | `internal/index` `internal/intel` |
| `internal/mcp/prompt` | 106 | 250 | `internal/code` `internal/mcp/mcpargs` `internal/memory` `internal/prompt` |
| `internal/mcp/prose` | 30 | 100 | `internal/index` |
| `internal/mcp/provenance` | 148 | 225 | `internal/governance` `internal/index` |
| `internal/mcp/rbac` | 160 | 300 | `internal/mcp/mcpargs` |
| `internal/mcp/refactor` | 108 | 250 | `internal/mcp/mcpargs` `internal/refactor` |
| `internal/mcp/repair` | 59 | 200 | `internal/mcp/mcpargs` `internal/repair` |
| `internal/mcp/retrieve` | 203 | 305 | `internal/index` `internal/mcp/graph` `internal/mcp/gov` `internal/mcp/mcpargs` `internal/mcp/provenance` `internal/retrieval` |
| `internal/mcp/review` | 109 | 250 | `internal/index` `internal/intel` `internal/lenses` `internal/mcp/mcpargs` `internal/profiles` `internal/runtime` |
| `internal/mcp/runtime` | 105 | 250 | `internal/domain` `internal/index` `internal/mcp/mcpargs` `internal/runtime` |
| `internal/mcp/security` | 309 | 500 | `internal/index` `internal/intel` `internal/mcp/mcpargs` `internal/relay` `internal/schema` `internal/sec` `internal/service` `internal/verify` |
| `internal/mcp/skill` | 67 | 200 | `internal/mcp/mcpargs` `internal/skills` |
| `internal/mcp/stream` | 145 | 300 | `internal/mcp/mcpargs` |
| `internal/mcp/synthtest` | 79 | 250 | `internal/index` `internal/mcp/mcpargs` `internal/synthtest` |
| `internal/mcp/transform` | 100 | 250 | `internal/index` `internal/mcp/mcpargs` `internal/transform` |
| `internal/mcp/transport` | 350 | 525 |  |
| `internal/mcpclient` | 487 | 800 |  |
| `internal/memory` | 1871 | 2900 | `internal/cache` `internal/domain` `internal/fsutil` `internal/metrics` `internal/storage` |
| `internal/metrics` | 715 | 1100 |  |
| `internal/modernization` | 558 | 900 | `internal/index` `internal/intel` |
| `internal/mutation` | 400 | 700 |  |
| `internal/note` | 320 | 500 |  |
| `internal/optimize` | 425 | 700 | `internal/cache` `internal/compress` `internal/kernconfig` `internal/llm` `internal/memory` `internal/pii` `internal/semcache` `internal/stats` `internal/tokenize` |
| `internal/ownership` | 160 | 300 |  |
| `internal/pack` | 717 | 1100 | `internal/budget` `internal/code` `internal/ignore` `internal/index` `internal/sec` `internal/tokenize` |
| `internal/pii` | 426 | 700 |  |
| `internal/planner` | 131 | 200 | `internal/agent` `internal/agents` `internal/llm` `internal/pii` |
| `internal/precache` | 185 | 300 | `internal/brief` `internal/cache` `internal/code` `internal/docsearch` `internal/index` |
| `internal/processgroup` | 46 | 100 |  |
| `internal/profiles` | 283 | 500 | `internal/terse` `internal/tokenize` |
| `internal/project` | 1381 | 2100 | `internal/index` `internal/intel` `internal/stats` |
| `internal/prompt` | 52 | 100 |  |
| `internal/prprovider` | 195 | 300 |  |
| `internal/relay` | 385 | 600 | `internal/eventbus` |
| `internal/remove` | 294 | 500 | `internal/index` `internal/intel` `internal/rename` |
| `internal/rename` | 1074 | 1700 | `internal/index` |
| `internal/refactor` | 198 | 300 | `internal/diff` |
| `internal/repair` | 343 | 600 |  |
| `internal/resilience` | 1199 | 1799 | `internal/blueprint` |
| `internal/retrieval` | 541 | 900 | `internal/budget` `internal/context` `internal/evidence` `internal/index` `internal/intel` `internal/tokenize` |
| `internal/reviewpack` | 535 | 900 | `internal/context` `internal/domain` `internal/index` `internal/intel` `internal/lenses` `internal/tokenize` |
| `internal/runtime` | 1944 | 3000 | `internal/config` `internal/domain` |
| `internal/sandbox` | 737 | 1200 | `internal/fsutil` `internal/governance` `internal/index` `internal/intel` `internal/processgroup` |
| `internal/schema` | 252 | 400 |  |
| `internal/scanners` | 2069 | 3104 | `internal/blueprint` `internal/fsutil` |
| `internal/script` | 659 | 1000 | `internal/governance` `internal/processgroup` |
| `internal/sdk` | 279 | 500 | `internal/domain` |
| `internal/sec` | 1247 | 1900 | `internal/ignore` `internal/index` `internal/pii` |
| `internal/semcache` | 491 | 800 | `internal/cache` |
| `internal/service` | 848 | 1300 | `internal/bpcli/cli` `internal/domain` `internal/governance` `internal/index` `internal/intel` `internal/memory` `internal/pii` `internal/project` `internal/schema` `internal/sec` `internal/storage` `internal/validate` |
| `internal/setup` | 2495 | 3800 | `internal/skills` `internal/version` |
| `internal/skills` | 396 | 600 | `internal/governance` |
| `internal/stats` | 212 | 400 | `internal/cache` |
| `internal/storage` | 693 | 1100 |  |
| `internal/strutil` | 59 | 100 |  |
| `internal/swap` | 303 | 455 | `internal/budget` `internal/code` `internal/tokenize` |
| `internal/synthtest` | 1067 | 1700 | `internal/diff` `internal/index` `internal/intel` |
| `internal/terse` | 680 | 1100 | `internal/tokenize` |
| `internal/testfixture` | 200 | 400 |  |
| `internal/tokenize` | 990 | 1500 | `internal/config` |
| `internal/transform` | 364 | 600 | `internal/diff` `internal/index` |
| `internal/twin` | 1221 | 1900 | `internal/domain` `internal/index` `internal/intelligence` `internal/runtime` |
| `internal/validate` | 804 | 1300 | `internal/index` `internal/processgroup` |
| `internal/verification` | 1768 | 2700 | `internal/budget` `internal/ci` `internal/config` `internal/context` `internal/domain` `internal/eval` `internal/eventbus` `internal/evidence` `internal/governance` `internal/host` `internal/index` `internal/intel` `internal/intelligence` `internal/memory` `internal/metrics` `internal/retrieval` `internal/sandbox` `internal/sec` `internal/tokenize` `internal/validate` `internal/version` |
| `internal/verify` | 1056 | 1600 | `internal/index` |
| `internal/version` | 26 | 100 |  |
| `internal/web` | 3108 | 4700 | `internal/agent` `internal/agents` `internal/app` `internal/architecture` `internal/domain` `internal/eventbus` `internal/governance` `internal/incident` `internal/index` `internal/intel` `internal/intelligence` `internal/learning` `internal/loop` `internal/memory` `internal/metrics` `internal/modernization` `internal/relay` `internal/runtime` `internal/service` `internal/verification` `internal/whatif` |
| `internal/webhook` | 204 | 400 | `internal/eventbus` |
| `internal/whatif` | 898 | 1400 | `internal/domain` `internal/evidence` `internal/index` `internal/intelligence` |

_Generated from the codebase on 2026-09-12; 9 subsystem rows added 2026-09-15 (fit, fragility, integration, lspbridge, merge3, mutation, refactor, repair, testfixture); 3 subsystem rows added 2026-09-18 (bppolicy, bpreceipt, resilience — blueprint modularization Stage A); 1 subsystem row added 2026-09-18 (bpcli — blueprint modularization Stage C); 1 subsystem row tightened 2026-09-18 (blueprint cap 28500→10100 — modularization Stage D settle); 2 subsystem rows updated 2026-09-18 (approval folded into gates: gates 804→1064/cap 1600; blueprint 6733→6472/cap tightened 10100→9800 — modularization Stage D follow-up). 1 subsystem row updated 2026-09-18 (fw 800→1337/cap 2100 — F-010 Go stdlib baseline moved into fw.WithGoStdlib, QA-sweep commit 6e2d863; prose mcp dep count 56→66 to match the table). 1 subsystem row added 2026-09-18 (mcp/transport — Item 1 Stage A′ transport-leaf extraction: TLS config/precedence, loopback bind policy, origin check; 67 deps→67+1 with mcp/transport listed). 1 subsystem row updated 2026-09-18 (mcp/transport 72→149/cap 225 — Stage A′ commit 2: listener engine transport.ServeListener absorbed the unix-socket/loopback/TLS/graceful-drain lifecycle from mcp's ServeHTTPContextWithTLS). 1 subsystem row updated 2026-09-18 (mcp/transport 149→350/cap 525 — Stage A′ commit 3: stdio drain choreography moved to transport.ServeStdio with the StdioServer interface; Server.StartBackgroundWatch dropped its vestigial *Server chain return so *Server satisfies the interface structurally). 1 subsystem row added 2026-09-18 (mcp/provenance — Item 1 Stage B commit 1: the P1.2 provenance contract extracted as a Server-independent leaf, commit func injected; govern.go policy-source labels and simpleName now alias the leaf so the stamps and the governance layer cannot drift; mcp keeps thin *Server wrappers + the ctx-scope stamp glue). 2 subsystem rows added 2026-09-18 (mcp/graph + mcp/mcpargs — Item 1 Stage B commit 2: 20 pure graph-family handler bodies moved to internal/mcp/graph as plain functions over the resolved index (14 taking ix, 6 service/registry-backed, 2 of those taking a resolved root); the arg-parsing helpers moved to internal/mcp/mcpargs with mcp keeping unexported wrappers; handlers_graph.go 1219→787 lines keeping the governed variants — search/context/explore/probe/communities/snapshot — plus the provenance pipeline and thin adapters). 1 subsystem row added 2026-09-18 (mcp/gov — Item 1 Stage B commit 3: the per-call context governor extracted to internal/mcp/gov (252 LOC) as a Server-independent leaf — Governor{PolicySource, Proof, Allowed} + New + the five filter methods + TaskScopeFromArgs/GraphSymbolsFromText/SimpleNames; govern.go is now a 49-line compat layer of thin wrappers, and the governed handler call sites use the leaf's exported field/method names). 1 subsystem row updated 2026-09-18 (mcp/graph 441→934/cap 1401 — Item 1 Stage B commit 4: the six governed variants — search/context/explore/graph/communities/probe — plus renderRetrieval, freshnessFooter, searchSymbolNames, parseLevelArg and RetrieveItemNames moved into the leaf; the GovContext hook bundle (governor factory + provenance stamping closures) is injected by the mcp adapters so the leaf stays Server-independent; handlers_graph.go is now 272 lines of adapters + Snapshot; the gov package is imported aliased as mcpgov inside graph because the moved bodies keep their local `gov` governor variable). 1 subsystem row added 2026-09-18 (mcp/org — Item 1 Stage B commit 5: the org family moved wholesale to internal/mcp/org (353 LOC) — all 7 handlers were already Server-dep-free and the 14 action/resolver free functions over the enterprise server moved verbatim; OrgServer + the eight action functions are the leaf's exported API (the mcp tests drive them directly); handlers_org.go is now 38 lines of adapters). 1 subsystem row added 2026-09-18 (mcp/doc — Item 1 Stage B commit 6: the doc family moved to internal/mcp/doc (287 LOC) — DocSearch takes a Hooks bundle (code-index loading + per-call governor for its code-results fallback, injected by the adapter), DocIndex/DocFetch/Commitmsg/Precache are plain (ctx, args) functions; SanitizeDocName + hasSlugChar + docSearchSlug + clip moved from server.go as the leaf's canonical helpers with server.go keeping a thin sanitizeDocName wrapper for the URL-slug path; handlers_doc.go is now 33 lines of adapters). 1 subsystem row added 2026-09-18 (mcp/retrieve — Item 1 Stage B commit 7: the retrieve family moved to internal/mcp/retrieve (203 LOC) — Retrieve and Resolve are governed variants taking (ctx, ix, graph.GovContext, args) with the mcp adapters resolving the index and injecting the govContext hooks, reusing the graph family's hook contract; parseRetrieveLevel and renderWithHandle moved with them; the retrieveItemNames wrapper died (call sites use graph.RetrieveItemNames); handlers_retrieve.go is now 26 lines of adapters — this closes the handler-family extraction campaign: graph/org/doc/retrieve all leafed). 1 subsystem row updated 2026-09-18 (mcp/retrieve gains the mcp/gov dep — the GovContext hook bundle relocated from internal/mcp/graph to internal/mcp/gov so every governed family depends on the governance leaf directly: the six governed graph signatures plus retrieve.Retrieve/Resolve take mcpgov.GovContext, and the mcp adapter constructs the bundle; retrieve keeps graph only for RetrieveItemNames/FreshnessFooter). 1 subsystem row updated 2026-09-18 (mcp/graph 934→1110/cap 1401 — the Snapshot family completed the graph extraction: SnapshotCreate/SnapshotVerify moved into the leaf as pure functions (create takes the resolved index like every other graph handler; verify takes the adapter-resolved root, the RepoSearch/FtsSearch pattern), handleSnapshot is now a thin switch adapter so handlers_graph.go carries zero domain logic; pinned by the graph leaf's first test, TestSnapshotCreateVerifyRoundTrip, driving the create→persist→verify round-trip; the vestigial bare block from the comment sweep went with it). 7 subsystem rows updated 2026-09-18 (parity-gate strictness fix: TestArchitectureDocParity now collects imports with go/parser + go/ast instead of bare-quoted lines, so aliased, single-form and nested imports are enforced and the nested mcp/* rows are parsed — consistency +`internal/context`, diff +`internal/index`, host +`internal/budget`, mcp +`internal/blueprint/checks/diffgate` +`internal/context` +`internal/schema`, reviewpack +`internal/context`, service +`internal/bpcli/cli` +`internal/schema`, setup +`internal/version`; the allowed-deps column is a set of subtree roots, so `internal/foo` permits `internal/foo/**` exactly as the old top-level collapse did). Non-goals this campaign (blueprint-modularization plan Phase 5): `internal/blueprint/audit`, `internal/blueprint/sandbox`, and `internal/blueprint/adapters/kern` stay inside `internal/blueprint`, and the domain-as-shared-kernel merge is deliberately not attempted. Regenerate the table when the architecture changes, then update this file deliberately — never silently._
