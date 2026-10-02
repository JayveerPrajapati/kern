# Architecture ledger — part 2: allowed deps

This file is the second half of the machine-read subsystem ledger that
`go test ./internal/architecture/` (TestArchitectureDocParity) parses.
Part 1 is [`ARCHITECTURE.md`](../../ARCHITECTURE.md): every subsystem's dir,
informational LOC baseline, and **cap** (the LOC drift gate). This part
carries the other enforcement half — each subsystem's **allowed deps**
column, copied verbatim from the single-file ledger. Change history lives
in `git log`, not in this file.

Both halves are parsed by the same test with the same row sanity:

- every row in ARCHITECTURE.md must have exactly one matching row here and
  vice versa (matched by the subsystem dir) — divergence fails;
- every kern-internal import of a subsystem must lie within its allowed deps
  ∪ its own subpackages — the import drift gate.

Each allowed-dep entry is a subtree root: `internal/foo` permits
`internal/foo/**`. Stdlib and third-party (e.g. build-tagged tree-sitter)
imports are always allowed; a subsystem's own subpackages are always allowed.

| subsystem | dir | allowed deps |
|---|---|---|
| `cmd/kern` | `cmd/kern` | `internal/agent` `internal/agents` `internal/app` `internal/architecture` `internal/blueprint` `internal/bpcli` `internal/brief` `internal/budget` `internal/cache` `internal/calibrate` `internal/cockpit` `internal/code` `internal/commitmsg` `internal/config` `internal/context` `internal/council` `internal/diff` `internal/docsearch` `internal/doctor` `internal/domain` `internal/draft` `internal/enterprise` `internal/eval` `internal/eventbus` `internal/evidence` `internal/fetch` `internal/fit` `internal/flight` `internal/fragility` `internal/fw` `internal/gates` `internal/governance` `internal/guard` `internal/heal` `internal/hook` `internal/host` `internal/incident` `internal/index` `internal/intel` `internal/lenses` `internal/llm` `internal/lock` `internal/loop` `internal/lsp` `internal/lspbridge` `internal/mcp` `internal/mcpclient` `internal/memory` `internal/metrics` `internal/mutation` `internal/optimize` `internal/ownership` `internal/pack` `internal/pii` `internal/precache` `internal/profiles` `internal/project` `internal/prompt` `internal/refactor` `internal/relay` `internal/remove` `internal/rename` `internal/repair` `internal/retrieval` `internal/reviewpack` `internal/runtime` `internal/sandbox` `internal/schema` `internal/script` `internal/sec` `internal/secscan` `internal/semcache` `internal/setup` `internal/skills` `internal/stats` `internal/storage` `internal/strutil` `internal/swap` `internal/tasklife` `internal/terse` `internal/tokenize` `internal/transform` `internal/twin` `internal/validate` `internal/verdict` `internal/verification` `internal/version` `internal/web` `internal/whatif` |
| `internal/agent` | `internal/agent` | `internal/cache` `internal/context` `internal/domain` `internal/eventbus` `internal/governance` `internal/fsutil` `internal/llm` `internal/metrics` `internal/verdict` `internal/verification` `internal/whatif` |
| `internal/agents` | `internal/agents` | `internal/agent` `internal/config` `internal/domain` `internal/eventbus` `internal/governance` |
| `internal/app` | `internal/app` | `internal/agent` `internal/agents` `internal/budget` `internal/cache` `internal/calibrate` `internal/coder` `internal/context` `internal/deployment` `internal/domain` `internal/eventbus` `internal/execution` `internal/flight` `internal/fsutil` `internal/governance` `internal/guard` `internal/incident` `internal/index` `internal/intel` `internal/learning` `internal/lenses` `internal/loop` `internal/memory` `internal/modernization` `internal/orgapprovals` `internal/planner` `internal/profiles` `internal/prprovider` `internal/runtime` `internal/storage` `internal/tasklife` `internal/tokenize` `internal/twin` `internal/verdict` `internal/verification` `internal/whatif` |
| `internal/architecture` | `internal/architecture` | `internal/index` `internal/domain` `internal/intel` `internal/guard` |
| `internal/blueprint` | `internal/blueprint` | `internal/bpreceipt` `internal/docbudget` `internal/execution` `internal/flock` `internal/fsutil` `internal/resilience` `internal/sec` `internal/secscan` |
| `internal/bpcli` | `internal/bpcli` | `internal/blueprint` `internal/bppolicy` `internal/bpreceipt` `internal/gates` `internal/governance` `internal/resilience` `internal/scanners` `internal/storage` `internal/strutil` |
| `internal/bppolicy` | `internal/bppolicy` | `internal/blueprint` |
| `internal/bpreceipt` | `internal/bpreceipt` | `internal/blueprint` `internal/fsutil` `internal/version` |
| `internal/brief` | `internal/brief` | `internal/code` `internal/index` `internal/intel` `internal/memory` `internal/stats` |
| `internal/budget` | `internal/budget` | `internal/code` `internal/tokenize` |
| `internal/cache` | `internal/cache` | `internal/config` `internal/flock` |
| `internal/calibrate` | `internal/calibrate` | `internal/fsutil` `internal/index` `internal/intel` `internal/version` |
| `internal/ci` | `internal/ci` |  |
| `internal/cockpit` | `internal/cockpit` | `internal/blueprint` `internal/bpreceipt` `internal/domain` `internal/eventbus` `internal/execution` `internal/gates` `internal/incident` `internal/index` `internal/loop` `internal/memory` `internal/optimize` `internal/runtime` |
| `internal/code` | `internal/code` | `internal/cache` `internal/ignore` |
| `internal/coder` | `internal/coder` | `internal/agent` `internal/agents` `internal/execution` `internal/llm` `internal/metrics` `internal/pii` `internal/tokenize` `internal/verdict` `internal/verification` |
| `internal/commitmsg` | `internal/commitmsg` | `internal/code` |
| `internal/compress` | `internal/compress` | `internal/semcache` `internal/terse` |
| `internal/config` | `internal/config` |  |
| `internal/context` | `internal/context` | `internal/budget` `internal/config` `internal/domain` `internal/eventbus` `internal/evidence` `internal/governance` `internal/guard` `internal/index` `internal/intel` `internal/lenses` `internal/memory` `internal/metrics` `internal/runtime` `internal/skills` `internal/tokenize` `internal/whatif` |
| `internal/council` | `internal/council` | `internal/reviewpack` |
| `internal/deployment` | `internal/deployment` | `internal/config` |
| `internal/diff` | `internal/diff` | `internal/index` |
| `internal/docbudget` | `internal/docbudget` |  |
| `internal/docsearch` | `internal/docsearch` | `internal/cache` `internal/index` |
| `internal/doctor` | `internal/doctor` | `internal/cache` `internal/index` `internal/intel` `internal/llm` `internal/metrics` `internal/runtime` `internal/script` `internal/setup` `internal/stats` `internal/version` |
| `internal/domain` | `internal/domain` | `internal/index` `internal/sec` `internal/secscan` |
| `internal/draft` | `internal/draft` | `internal/index` |
| `internal/enterprise` | `internal/enterprise` | `internal/agent` `internal/architecture` `internal/domain` `internal/eventbus` `internal/governance` `internal/intel` `internal/memory` `internal/orgapprovals` `internal/storage` |
| `internal/eval` | `internal/eval` | `internal/budget` `internal/llm` `internal/tokenize` |
| `internal/eventbus` | `internal/eventbus` |  |
| `internal/evidence` | `internal/evidence` | `internal/domain` `internal/governance` `internal/index` `internal/sec` `internal/secscan` `internal/storage` |
| `internal/execution` | `internal/execution` | `internal/governance` `internal/ignore` `internal/index` `internal/metrics` `internal/sandbox` |
| `internal/fetch` | `internal/fetch` |  |
| `internal/fit` | `internal/fit` | `internal/code` `internal/index` `internal/tokenize` |
| `internal/flight` | `internal/flight` | `internal/storage` |
| `internal/flock` | `internal/flock` |  |
| `internal/fsutil` | `internal/fsutil` |  |
| `internal/fw` | `internal/fw` | `internal/ignore` `internal/index` |
| `internal/fragility` | `internal/fragility` | `internal/index` |
| `internal/gates` | `internal/gates` | `internal/blueprint` `internal/bppolicy` |
| `internal/governance` | `internal/governance` | `internal/cache` `internal/config` `internal/domain` `internal/eventbus` `internal/flock` `internal/index` `internal/metrics` `internal/storage` |
| `internal/guard` | `internal/guard` | `internal/domain` `internal/eventbus` `internal/index` `internal/intel` |
| `internal/heal` | `internal/heal` | `internal/diff` `internal/index` `internal/intel` `internal/llm` `internal/sandbox` `internal/validate` |
| `internal/hook` | `internal/hook` | `internal/memory` `internal/optimize` |
| `internal/host` | `internal/host` | `internal/budget` `internal/domain` |
| `internal/ignore` | `internal/ignore` |  |
| `internal/incident` | `internal/incident` | `internal/cache` `internal/domain` `internal/eventbus` `internal/evidence` `internal/execution` `internal/fsutil` `internal/governance` `internal/index` `internal/intel` `internal/memory` `internal/metrics` `internal/prprovider` `internal/runtime` `internal/verdict` `internal/verification` |
| `internal/index` | `internal/index` | `internal/cache` `internal/ignore` `internal/lock` `internal/metrics` `internal/tokenize` |
| `internal/integration` | `internal/integration` |  |
| `internal/intel` | `internal/intel` | `internal/budget` `internal/domain` `internal/cache` `internal/code` `internal/eventbus` `internal/index` `internal/tokenize` |
| `internal/learnclaim` | `internal/learnclaim` | `internal/domain` |
| `internal/learning` | `internal/learning` | `internal/cache` `internal/domain` `internal/learnclaim` `internal/memory` |
| `internal/lenses` | `internal/lenses` | `internal/domain` |
| `internal/llm` | `internal/llm` | `internal/config` |
| `internal/lock` | `internal/lock` | `internal/flock` |
| `internal/loop` | `internal/loop` | `internal/blueprint` `internal/bppolicy` `internal/coder` `internal/deployment` `internal/domain` `internal/eventbus` `internal/execution` `internal/flight` `internal/governance` `internal/incident` `internal/learning` `internal/memory` `internal/planner` `internal/runtime` `internal/scanners` `internal/verdict` `internal/verification` |
| `internal/lsp` | `internal/lsp` | `internal/index` |
| `internal/lspbridge` | `internal/lspbridge` |  |
| `internal/mcp` | `internal/mcp` | `internal/agent` `internal/app` `internal/blueprint/checks/diffgate` `internal/bpcli` `internal/brief` `internal/budget` `internal/cache` `internal/code` `internal/commitmsg` `internal/config` `internal/context` `internal/diff` `internal/docsearch` `internal/domain` `internal/draft` `internal/enterprise` `internal/evidence` `internal/fetch` `internal/fit` `internal/flight` `internal/fragility` `internal/fsutil` `internal/fw` `internal/governance` `internal/guard` `internal/heal` `internal/incident` `internal/index` `internal/intel` `internal/lenses` `internal/llm` `internal/lock` `internal/loop` `internal/lspbridge` `internal/mcp/agentctl` `internal/mcp/blueprint` `internal/mcp/bridge` `internal/mcp/catalog` `internal/mcp/compose` `internal/mcp/context` `internal/mcp/contextwatch` `internal/mcp/coord` `internal/mcp/crossrepo` `internal/mcp/deploy` `internal/mcp/doc` `internal/mcp/envelope` `internal/mcp/evidence` `internal/mcp/exec` `internal/mcp/explain` `internal/mcp/fingerprint` `internal/mcp/flight` `internal/mcp/fragility` `internal/mcp/gov` `internal/mcp/governance` `internal/mcp/graph` `internal/mcp/health` `internal/mcp/highlevel` `internal/mcp/lsp` `internal/mcp/mcpargs` `internal/mcp/memory` `internal/mcp/merge` `internal/mcp/meta` `internal/mcp/mutation` `internal/mcp/optimize` `internal/mcp/orchestrate` `internal/mcp/org` `internal/orgapprovals` `internal/mcp/planner` `internal/mcp/policydsl` `internal/mcp/preedit` `internal/mcp/prompt` `internal/mcp/prose` `internal/mcp/provenance` `internal/mcp/rbac` `internal/mcp/refactor` `internal/mcp/repair` `internal/mcp/retrieve` `internal/mcp/review` `internal/mcp/root` `internal/mcp/runtime` `internal/mcp/security` `internal/mcp/skill` `internal/mcp/stream` `internal/mcp/synthtest` `internal/mcp/transform` `internal/mcp/transport` `internal/mcp/watcher` `internal/mcpclient` `internal/mcpserve` `internal/memory` `internal/metrics` `internal/mutation` `internal/optimize` `internal/pack` `internal/pii` `internal/policy` `internal/precache` `internal/profiles` `internal/project` `internal/prompt` `internal/refactor` `internal/relay` `internal/rename` `internal/repair` `internal/retrieval` `internal/runtime` `internal/sandbox` `internal/schema` `internal/script` `internal/sec` `internal/secscan` `internal/semcache` `internal/session` `internal/skills` `internal/stats` `internal/storage` `internal/strutil` `internal/swap` `internal/synthtest` `internal/tasklife` `internal/terse` `internal/tokenize` `internal/transform` `internal/twin` `internal/validate` `internal/verdict` `internal/verification` `internal/version` `internal/whatif` |
| `internal/mcp/agentctl` | `internal/mcp/agentctl` | `internal/app` `internal/governance` `internal/llm` `internal/mcp/coord` `internal/mcp/fingerprint` `internal/mcp/mcpargs` `internal/mcp/rbac` `internal/mcp/root` `internal/tasklife` |
| `internal/mcp/bridge` | `internal/mcp/bridge` | `internal/mcp/mcpargs` `internal/mcp/root` `internal/mcpclient` |
| `internal/mcp/blueprint` | `internal/mcp/blueprint` | `internal/bpcli/mcp` `internal/fsutil` |
| `internal/mcp/catalog` | `internal/mcp/catalog` | `internal/blueprint/checks/diffgate` |
| `internal/mcp/compose` | `internal/mcp/compose` | `internal/mcp/mcpargs` |
| `internal/mcp/context` | `internal/mcp/context` | `internal/brief` `internal/code` `internal/context` `internal/fit` `internal/fsutil` `internal/index` `internal/intel` `internal/mcp/mcpargs` `internal/pack` `internal/verification` |
| `internal/mcp/contextwatch` | `internal/mcp/contextwatch` | `internal/tokenize` |
| `internal/mcp/coord` | `internal/mcp/coord` | `internal/mcp/mcpargs` |
| `internal/mcp/crossrepo` | `internal/mcp/crossrepo` | `internal/intel` |
| `internal/mcp/deploy` | `internal/mcp/deploy` | `internal/agent` `internal/app` `internal/mcp/mcpargs` `internal/mcp/root` `internal/tasklife` |
| `internal/mcp/doc` | `internal/mcp/doc` | `internal/cache` `internal/commitmsg` `internal/docsearch` `internal/fetch` `internal/index` `internal/intel` `internal/llm` `internal/mcp/gov` `internal/mcp/mcpargs` `internal/precache` `internal/strutil` |
| `internal/mcp/envelope` | `internal/mcp/envelope` | `internal/app` `internal/budget` `internal/context` `internal/domain` `internal/index` `internal/mcp/mcpargs` `internal/tokenize` |
| `internal/mcp/etag` | `internal/mcp/etag` |  |
| `internal/mcp/evidence` | `internal/mcp/evidence` | `internal/evidence` `internal/fetch` `internal/governance` `internal/index` `internal/mcp/mcpargs` `internal/mcp/root` `internal/pii` `internal/storage` |
| `internal/mcp/exec` | `internal/mcp/exec` | `internal/diff` `internal/fsutil` `internal/governance` `internal/heal` `internal/incident` `internal/index` `internal/mcp/mcpargs` `internal/mcp/root` `internal/optimize` `internal/pii` `internal/sandbox` `internal/script` `internal/strutil` `internal/validate` |
| `internal/mcp/explain` | `internal/mcp/explain` | `internal/index` `internal/intel` |
| `internal/mcp/fingerprint` | `internal/mcp/fingerprint` | `internal/governance` `internal/mcp/mcpargs` |
| `internal/mcp/flight` | `internal/mcp/flight` | `internal/flight` `internal/mcp/mcpargs` `internal/mcp/root` |
| `internal/mcp/fragility` | `internal/mcp/fragility` | `internal/fragility` `internal/mcp/mcpargs` `internal/mcp/root` |
| `internal/mcp/gov` | `internal/mcp/gov` | `internal/domain` `internal/governance` `internal/index` `internal/mcp/mcpargs` `internal/mcp/provenance` |
| `internal/mcp/governance` | `internal/mcp/governance` | `internal/governance` `internal/index` `internal/intel` `internal/lock` `internal/mcp/gov` `internal/mcp/mcpargs` `internal/mcp/provenance` `internal/mcp/root` `internal/rename` |
| `internal/mcp/graph` | `internal/mcp/graph` | `internal/budget` `internal/context` `internal/fw` `internal/index` `internal/intel` `internal/lenses` `internal/llm` `internal/mcp/gov` `internal/mcp/mcpargs` `internal/mcp/provenance` `internal/profiles` `internal/project` `internal/retrieval` `internal/twin` |
| `internal/mcp/health` | `internal/mcp/health` | `internal/index` `internal/mcp/mcpargs` `internal/metrics` |
| `internal/mcp/highlevel` | `internal/mcp/highlevel` | `internal/agent` `internal/app` `internal/domain` `internal/governance` `internal/llm` `internal/loop` `internal/mcp/mcpargs` `internal/mcp/meta` `internal/mcp/root` `internal/metrics` `internal/pii` `internal/profiles` `internal/runtime` `internal/tasklife` `internal/verdict` `internal/verification` `internal/whatif` |
| `internal/mcp/lsp` | `internal/mcp/lsp` | `internal/lspbridge` `internal/mcp/mcpargs` `internal/mcp/root` |
| `internal/mcp/mcpargs` | `internal/mcp/mcpargs` |  |
| `internal/mcp/memory` | `internal/mcp/memory` | `internal/mcp/mcpargs` `internal/mcp/root` `internal/memory` |
| `internal/mcp/meta` | `internal/mcp/meta` | `internal/mcp/catalog` `internal/mcp/mcpargs` `internal/skills` |
| `internal/mcp/merge` | `internal/mcp/merge` | `internal/diff` `internal/index` `internal/intel` `internal/mcp/mcpargs` `internal/mcp/root` |
| `internal/mcp/mutation` | `internal/mcp/mutation` | `internal/mcp/mcpargs` `internal/mcp/root` `internal/mutation` |
| `internal/mcp/optimize` | `internal/mcp/optimize` | `internal/budget` `internal/mcp/mcpargs` `internal/mcp/root` `internal/optimize` `internal/semcache` `internal/strutil` `internal/swap` `internal/terse` `internal/tokenize` |
| `internal/mcp/orchestrate` | `internal/mcp/orchestrate` | `internal/app` `internal/context` `internal/mcp/mcpargs` |
| `internal/mcp/org` | `internal/mcp/org` | `internal/domain` `internal/enterprise` `internal/governance` `internal/intel` `internal/mcp/mcpargs` |
| `internal/mcp/planner` | `internal/mcp/planner` | `internal/app` `internal/context` `internal/mcp/mcpargs` |
| `internal/mcp/policydsl` | `internal/mcp/policydsl` | `internal/intel` `internal/mcp/mcpargs` `internal/mcp/root` `internal/policy` |
| `internal/mcp/preedit` | `internal/mcp/preedit` | `internal/index` `internal/intel` `internal/guard` |
| `internal/mcp/prompt` | `internal/mcp/prompt` | `internal/code` `internal/mcp/mcpargs` `internal/mcp/root` `internal/memory` `internal/prompt` |
| `internal/mcp/prose` | `internal/mcp/prose` | `internal/index` |
| `internal/mcp/provenance` | `internal/mcp/provenance` | `internal/governance` `internal/index` |
| `internal/mcp/rbac` | `internal/mcp/rbac` | `internal/governance` `internal/mcp/mcpargs` `internal/orgapprovals` |
| `internal/mcp/refactor` | `internal/mcp/refactor` | `internal/mcp/mcpargs` `internal/mcp/root` `internal/refactor` |
| `internal/mcp/repair` | `internal/mcp/repair` | `internal/mcp/blueprint` `internal/mcp/mcpargs` `internal/mcp/root` `internal/repair` |
| `internal/mcp/retrieve` | `internal/mcp/retrieve` | `internal/index` `internal/mcp/graph` `internal/mcp/gov` `internal/mcp/mcpargs` `internal/mcp/provenance` `internal/retrieval` |
| `internal/mcp/review` | `internal/mcp/review` | `internal/index` `internal/intel` `internal/lenses` `internal/mcp/mcpargs` `internal/profiles` `internal/runtime` |
| `internal/mcp/root` | `internal/mcp/root` |  |
| `internal/mcp/runtime` | `internal/mcp/runtime` | `internal/domain` `internal/index` `internal/mcp/mcpargs` `internal/mcp/root` `internal/runtime` |
| `internal/mcp/security` | `internal/mcp/security` | `internal/draft` `internal/guard` `internal/index` `internal/intel` `internal/mcp/mcpargs` `internal/mcp/root` `internal/pii` `internal/relay` `internal/schema` `internal/sec` `internal/secscan` `internal/verification` |
| `internal/mcp/skill` | `internal/mcp/skill` | `internal/mcp/mcpargs` `internal/mcp/root` `internal/skills` |
| `internal/mcp/stream` | `internal/mcp/stream` | `internal/mcp/mcpargs` |
| `internal/mcp/synthtest` | `internal/mcp/synthtest` | `internal/index` `internal/mcp/mcpargs` `internal/mcp/root` `internal/sec` `internal/secscan` `internal/synthtest` |
| `internal/mcp/transform` | `internal/mcp/transform` | `internal/index` `internal/mcp/mcpargs` `internal/mcp/root` `internal/transform` |
| `internal/mcp/transport` | `internal/mcp/transport` |  |
| `internal/mcp/watcher` | `internal/mcp/watcher` | `internal/index` |
| `internal/mcpclient` | `internal/mcpclient` |  |
| `internal/mcpserve` | `internal/mcpserve` | `internal/config` `internal/mcp/mcpargs` `internal/optimize` `internal/stats` `internal/tokenize` |
| `internal/memory` | `internal/memory` | `internal/cache` `internal/domain` `internal/fsutil` `internal/metrics` `internal/storage` |
| `internal/metrics` | `internal/metrics` |  |
| `internal/modernization` | `internal/modernization` | `internal/index` `internal/intel` |
| `internal/mutation` | `internal/mutation` | `internal/execution` |
| `internal/optimize` | `internal/optimize` | `internal/cache` `internal/compress` `internal/config` `internal/llm` `internal/memory` `internal/pii` `internal/semcache` `internal/stats` `internal/tokenize` |
| `internal/orgapprovals` | `internal/orgapprovals` | `internal/cache` `internal/domain` `internal/governance` |
| `internal/ownership` | `internal/ownership` |  |
| `internal/pack` | `internal/pack` | `internal/budget` `internal/code` `internal/ignore` `internal/index` `internal/sec` `internal/secscan` `internal/tokenize` |
| `internal/pii` | `internal/pii` |  |
| `internal/planner` | `internal/planner` | `internal/agent` `internal/agents` `internal/llm` `internal/pii` |
| `internal/policy` | `internal/policy` |  |
| `internal/precache` | `internal/precache` | `internal/brief` `internal/cache` `internal/code` `internal/docsearch` `internal/index` `internal/semcache` |
| `internal/processgroup` | `internal/processgroup` |  |
| `internal/profiles` | `internal/profiles` | `internal/terse` `internal/tokenize` |
| `internal/project` | `internal/project` | `internal/index` `internal/intel` `internal/stats` |
| `internal/prompt` | `internal/prompt` |  |
| `internal/prprovider` | `internal/prprovider` |  |
| `internal/relay` | `internal/relay` | `internal/eventbus` |
| `internal/remove` | `internal/remove` | `internal/index` `internal/intel` `internal/rename` |
| `internal/rename` | `internal/rename` | `internal/index` |
| `internal/refactor` | `internal/refactor` | `internal/diff` |
| `internal/repair` | `internal/repair` |  |
| `internal/resilience` | `internal/resilience` | `internal/blueprint` |
| `internal/retrieval` | `internal/retrieval` | `internal/budget` `internal/context` `internal/evidence` `internal/index` `internal/intel` `internal/tokenize` |
| `internal/reviewpack` | `internal/reviewpack` | `internal/context` `internal/domain` `internal/index` `internal/intel` `internal/lenses` `internal/tokenize` |
| `internal/runtime` | `internal/runtime` | `internal/config` `internal/domain` |
| `internal/sandbox` | `internal/sandbox` | `internal/fsutil` `internal/governance` `internal/index` `internal/intel` `internal/processgroup` |
| `internal/sandbox/landlock` | `internal/sandbox/landlock` |  |
| `internal/schema` | `internal/schema` |  |
| `internal/scanners` | `internal/scanners` | `internal/blueprint` `internal/fsutil` |
| `internal/script` | `internal/script` | `internal/governance` `internal/processgroup` |
| `internal/sdk` | `internal/sdk` | `internal/domain` `internal/mcp` |
| `internal/sec` | `internal/sec` | `internal/index` `internal/secscan` |
| `internal/secscan` | `internal/secscan` | `internal/ignore` `internal/index` `internal/pii` |
| `internal/semcache` | `internal/semcache` | `internal/cache` |
| `internal/session` | `internal/session` | `internal/project` |
| `internal/setup` | `internal/setup` | `internal/bpcli/cli` `internal/skills` `internal/strutil` `internal/version` |
| `internal/skills` | `internal/skills` | `internal/governance` |
| `internal/stats` | `internal/stats` | `internal/cache` `internal/context` |
| `internal/storage` | `internal/storage` |  |
| `internal/strutil` | `internal/strutil` |  |
| `internal/swap` | `internal/swap` | `internal/budget` `internal/code` `internal/tokenize` |
| `internal/tasklife` | `internal/tasklife` | `internal/agent` `internal/agents` `internal/cache` `internal/calibrate` `internal/coder` `internal/context` `internal/deployment` `internal/domain` `internal/eventbus` `internal/execution` `internal/flight` `internal/fsutil` `internal/governance` `internal/incident` `internal/index` `internal/intel` `internal/learning` `internal/lenses` `internal/loop` `internal/memory` `internal/modernization` `internal/orgapprovals` `internal/planner` `internal/prprovider` `internal/runtime` `internal/storage` `internal/twin` `internal/verdict` `internal/verification` `internal/whatif` |
| `internal/synthtest` | `internal/synthtest` | `internal/diff` `internal/index` `internal/intel` |
| `internal/terse` | `internal/terse` | `internal/tokenize` |
| `internal/testfixture` | `internal/testfixture` |  |
| `internal/tokenize` | `internal/tokenize` | `internal/config` |
| `internal/transform` | `internal/transform` | `internal/diff` `internal/index` |
| `internal/twin` | `internal/twin` | `internal/domain` `internal/index` `internal/intel` `internal/runtime` |
| `internal/validate` | `internal/validate` | `internal/index` `internal/processgroup` |
| `internal/verdict` | `internal/verdict` | `internal/domain` |
| `internal/verification` | `internal/verification` | `internal/budget` `internal/calibrate` `internal/ci` `internal/config` `internal/context` `internal/domain` `internal/eval` `internal/eventbus` `internal/evidence` `internal/governance` `internal/guard` `internal/host` `internal/index` `internal/intel` `internal/memory` `internal/metrics` `internal/retrieval` `internal/sandbox` `internal/secscan` `internal/tokenize` `internal/validate` `internal/verdict` `internal/version` |
| `internal/version` | `internal/version` |  |
| `internal/web` | `internal/web` | `internal/agent` `internal/agents` `internal/app` `internal/architecture` `internal/domain` `internal/eventbus` `internal/governance` `internal/guard` `internal/incident` `internal/index` `internal/intel` `internal/learning` `internal/loop` `internal/memory` `internal/metrics` `internal/modernization` `internal/orgapprovals` `internal/relay` `internal/runtime` `internal/tasklife` `internal/verification` `internal/version` `internal/whatif` |
| `internal/webhook` | `internal/webhook` | `internal/eventbus` |
| `internal/whatif` | `internal/whatif` | `internal/domain` `internal/evidence` `internal/index` `internal/intel` |

