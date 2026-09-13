# ARCHITECTURE.md — subsystem ledger

This file is the **machine-readable source of truth for kern's package
architecture**. `go test ./internal/architecture/` (TestArchitectureDocParity)
parses the table below and fails on divergence:

- every listed directory must exist;
- its non-test Go LOC must not exceed the **cap** column;
- its kern-internal imports must stay within the **allowed deps** column.

The baseline column records LOC measured on 2026-09-12 (informational);
caps are ~1.5x baseline, rounded up. Allowed deps are the actual import set
at baseline: adding a new internal import means updating this table first —
that is the drift gate. Stdlib and third-party (e.g. build-tagged
tree-sitter) imports are always allowed; a subsystem's own subpackages are
always allowed.

Known drift (documented honestly, caps set accordingly): `internal/mcp` is a
monolith (13.7k LOC, 56 deps) and `internal/blueprint` is large (18.9k LOC);
both are capped, not hidden.

| subsystem | dir | LOC baseline (2026-09-12) | cap | allowed deps |
|---|---|---|---|---|
| `internal/agent` | 1908 | 2900 | `internal/cache` `internal/context` `internal/domain` `internal/eventbus` `internal/governance` `internal/llm` `internal/metrics` `internal/verification` `internal/whatif` |
| `internal/agents` | 1080 | 1700 | `internal/agent` `internal/config` `internal/domain` `internal/eventbus` `internal/governance` |
| `internal/app` | 6126 | 9200 | `internal/agent` `internal/agents` `internal/cache` `internal/coder` `internal/context` `internal/deployment` `internal/domain` `internal/eventbus` `internal/execution` `internal/flight` `internal/governance` `internal/incident` `internal/index` `internal/intel` `internal/intelligence` `internal/learning` `internal/lenses` `internal/loop` `internal/memory` `internal/modernization` `internal/planner` `internal/prprovider` `internal/runtime` `internal/storage` `internal/twin` `internal/verification` `internal/whatif` |
| `internal/architecture` | 1152 | 1800 | `internal/index` `internal/intel` |
| `internal/blueprint` | 18955 | 28500 | `internal/docbudget` `internal/execution` `internal/flock` `internal/note` `internal/sec` |
| `internal/brief` | 350 | 600 | `internal/code` `internal/index` `internal/intel` `internal/memory` `internal/stats` |
| `internal/budget` | 212 | 400 | `internal/code` `internal/tokenize` |
| `internal/cache` | 501 | 800 | `internal/config` `internal/flock` |
| `internal/calibrate` | 198 | 300 | `internal/index` `internal/intel` |
| `internal/ci` | 222 | 400 |  |
| `internal/cicd` | 180 | 300 | `internal/app` `internal/domain` `internal/eventbus` `internal/governance` `internal/prprovider` |
| `internal/cockpit` | 1176 | 1800 | `internal/blueprint` `internal/domain` `internal/eventbus` `internal/execution` `internal/incident` `internal/index` `internal/loop` `internal/memory` `internal/optimize` `internal/runtime` |
| `internal/code` | 903 | 1400 | `internal/cache` `internal/ignore` |
| `internal/coder` | 467 | 800 | `internal/agent` `internal/agents` `internal/execution` `internal/llm` `internal/pii` `internal/verification` |
| `internal/commitmsg` | 644 | 1000 | `internal/code` |
| `internal/compress` | 652 | 1000 | `internal/terse` |
| `internal/config` | 501 | 800 |  |
| `internal/consistency` | 237 | 400 | `internal/domain` |
| `internal/context` | 3645 | 5500 | `internal/budget` `internal/config` `internal/domain` `internal/eventbus` `internal/evidence` `internal/governance` `internal/index` `internal/intel` `internal/intelligence` `internal/lenses` `internal/memory` `internal/metrics` `internal/runtime` `internal/skills` `internal/tokenize` `internal/whatif` |
| `internal/council` | 426 | 700 | `internal/reviewpack` |
| `internal/deployment` | 165 | 300 | `internal/config` |
| `internal/diff` | 728 | 1100 |  |
| `internal/docbudget` | 121 | 200 |  |
| `internal/docsearch` | 742 | 1200 | `internal/cache` |
| `internal/doctor` | 534 | 900 | `internal/cache` `internal/index` `internal/llm` `internal/runtime` `internal/script` `internal/setup` `internal/stats` `internal/version` |
| `internal/domain` | 2132 | 3200 | `internal/index` `internal/intel` `internal/sec` |
| `internal/enterprise` | 1461 | 2200 | `internal/domain` `internal/eventbus` `internal/governance` `internal/intel` `internal/memory` `internal/storage` `internal/web` |
| `internal/eval` | 326 | 500 | `internal/budget` `internal/llm` `internal/tokenize` |
| `internal/eventbus` | 714 | 1100 |  |
| `internal/evidence` | 1153 | 1800 | `internal/domain` `internal/governance` `internal/index` `internal/sec` `internal/storage` |
| `internal/execution` | 540 | 900 | `internal/governance` `internal/metrics` `internal/sandbox` |
| `internal/fetch` | 245 | 400 |  |
| `internal/flight` | 460 | 700 | `internal/storage` |
| `internal/flock` | 105 | 200 |  |
| `internal/fw` | 800 | 1300 | `internal/ignore` |
| `internal/governance` | 3669 | 5600 | `internal/cache` `internal/config` `internal/domain` `internal/eventbus` `internal/flock` `internal/index` `internal/metrics` `internal/storage` |
| `internal/heal` | 252 | 400 | `internal/diff` `internal/llm` `internal/sandbox` `internal/validate` |
| `internal/hook` | 330 | 500 | `internal/memory` `internal/optimize` |
| `internal/hooks` | 159 | 300 | `internal/memory` |
| `internal/host` | 389 | 600 | `internal/domain` |
| `internal/ignore` | 287 | 500 |  |
| `internal/incident` | 868 | 1400 | `internal/cache` `internal/domain` `internal/eventbus` `internal/evidence` `internal/execution` `internal/governance` `internal/index` `internal/intelligence` `internal/memory` `internal/prprovider` `internal/runtime` `internal/verification` |
| `internal/index` | 8583 | 12900 | `internal/cache` `internal/ignore` `internal/metrics` `internal/tokenize` |
| `internal/intel` | 7837 | 11800 | `internal/budget` `internal/cache` `internal/code` `internal/eventbus` `internal/index` `internal/tokenize` |
| `internal/intelligence` | 855 | 1300 | `internal/domain` `internal/index` |
| `internal/learning` | 197 | 300 | `internal/cache` `internal/domain` `internal/memory` |
| `internal/lenses` | 289 | 500 | `internal/domain` |
| `internal/llm` | 1102 | 1700 | `internal/config` |
| `internal/lock` | 367 | 600 | `internal/flock` |
| `internal/loop` | 1700 | 2600 | `internal/blueprint` `internal/coder` `internal/deployment` `internal/domain` `internal/eventbus` `internal/execution` `internal/flight` `internal/governance` `internal/incident` `internal/learning` `internal/memory` `internal/planner` `internal/runtime` `internal/verification` |
| `internal/lsp` | 658 | 1000 | `internal/index` |
| `internal/mcp` | 13775 | 20700 | `internal/agent` `internal/blueprint` `internal/app` `internal/brief` `internal/budget` `internal/cache` `internal/code` `internal/commitmsg` `internal/config` `internal/diff` `internal/docsearch` `internal/domain` `internal/enterprise` `internal/evidence` `internal/fetch` `internal/flight` `internal/fw` `internal/governance` `internal/heal` `internal/index` `internal/intel` `internal/lenses` `internal/llm` `internal/lock` `internal/loop` `internal/mcpclient` `internal/memory` `internal/metrics` `internal/note` `internal/optimize` `internal/pack` `internal/pii` `internal/precache` `internal/profiles` `internal/project` `internal/prompt` `internal/relay` `internal/rename` `internal/retrieval` `internal/runtime` `internal/sandbox` `internal/script` `internal/sec` `internal/semcache` `internal/service` `internal/skills` `internal/stats` `internal/storage` `internal/strutil` `internal/swap` `internal/synthtest` `internal/terse` `internal/tokenize` `internal/transform` `internal/validate` `internal/verification` `internal/verify` `internal/whatif` |
| `internal/mcpclient` | 487 | 800 |  |
| `internal/memory` | 1871 | 2900 | `internal/cache` `internal/domain` `internal/metrics` `internal/storage` |
| `internal/metrics` | 715 | 1100 |  |
| `internal/modernization` | 558 | 900 | `internal/index` `internal/intel` |
| `internal/note` | 320 | 500 |  |
| `internal/optimize` | 409 | 700 | `internal/cache` `internal/compress` `internal/llm` `internal/memory` `internal/pii` `internal/semcache` `internal/stats` `internal/tokenize` |
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
| `internal/retrieval` | 541 | 900 | `internal/budget` `internal/context` `internal/evidence` `internal/index` `internal/intel` `internal/tokenize` |
| `internal/reviewpack` | 535 | 900 | `internal/domain` `internal/index` `internal/intel` `internal/lenses` `internal/tokenize` |
| `internal/runtime` | 1944 | 3000 | `internal/config` `internal/domain` |
| `internal/sandbox` | 737 | 1200 | `internal/governance` `internal/processgroup` |
| `internal/schema` | 252 | 400 |  |
| `internal/script` | 659 | 1000 | `internal/governance` `internal/processgroup` |
| `internal/sdk` | 279 | 500 | `internal/domain` |
| `internal/sec` | 1247 | 1900 | `internal/ignore` `internal/index` `internal/pii` |
| `internal/semcache` | 491 | 800 | `internal/cache` |
| `internal/service` | 848 | 1300 | `internal/domain` `internal/governance` `internal/index` `internal/intel` `internal/memory` `internal/pii` `internal/project` `internal/sec` `internal/storage` `internal/validate` |
| `internal/setup` | 2495 | 3800 | `internal/skills` |
| `internal/skills` | 396 | 600 | `internal/governance` |
| `internal/stats` | 212 | 400 | `internal/cache` |
| `internal/storage` | 693 | 1100 |  |
| `internal/strutil` | 59 | 100 |  |
| `internal/swap` | 119 | 200 | `internal/budget` `internal/code` `internal/tokenize` |
| `internal/synthtest` | 558 | 900 | `internal/diff` `internal/index` `internal/intel` |
| `internal/terse` | 680 | 1100 |  |
| `internal/tokenize` | 990 | 1500 | `internal/config` |
| `internal/transform` | 364 | 600 | `internal/diff` `internal/index` |
| `internal/twin` | 1221 | 1900 | `internal/domain` `internal/index` `internal/intelligence` `internal/runtime` |
| `internal/validate` | 422 | 700 | `internal/index` `internal/processgroup` |
| `internal/verification` | 1768 | 2700 | `internal/budget` `internal/ci` `internal/config` `internal/context` `internal/domain` `internal/eval` `internal/eventbus` `internal/evidence` `internal/governance` `internal/host` `internal/index` `internal/intel` `internal/intelligence` `internal/memory` `internal/metrics` `internal/retrieval` `internal/sandbox` `internal/sec` `internal/tokenize` `internal/validate` |
| `internal/verify` | 1056 | 1600 | `internal/index` |
| `internal/version` | 26 | 100 |  |
| `internal/web` | 3108 | 4700 | `internal/agent` `internal/agents` `internal/app` `internal/architecture` `internal/domain` `internal/eventbus` `internal/governance` `internal/incident` `internal/index` `internal/intel` `internal/intelligence` `internal/learning` `internal/loop` `internal/memory` `internal/metrics` `internal/modernization` `internal/relay` `internal/runtime` `internal/service` `internal/verification` `internal/whatif` |
| `internal/webhook` | 204 | 400 | `internal/eventbus` |
| `internal/whatif` | 898 | 1400 | `internal/domain` `internal/evidence` `internal/index` `internal/intelligence` |

_Generated from the codebase on 2026-09-12; regenerate the table when the architecture changes, then update this file deliberately — never silently._
