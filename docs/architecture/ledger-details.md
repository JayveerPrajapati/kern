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
| `cmd/kern` | `cmd/kern` | `internal/agent` `internal/agents` `internal/app` `internal/approvalcli` `internal/architecture` `internal/brief` `internal/budget` `internal/cache` `internal/calibrate` `internal/clietag` `internal/cliflags` `internal/cliout` `internal/code` `internal/commitmsg` `internal/config` `internal/context` `internal/contextcli` `internal/diff` `internal/docsearch` `internal/doctor` `internal/domain` `internal/draft` `internal/enterprise` `internal/eval` `internal/eventbus` `internal/evidence` `internal/execution` `internal/fetch` `internal/fit` `internal/flight` `internal/fw` `internal/governance` `internal/graphcli` `internal/guard` `internal/heal` `internal/hook` `internal/host` `internal/incident` `internal/index` `internal/intel` `internal/lenses` `internal/llm` `internal/lock` `internal/loop` `internal/lsp` `internal/lspbridge` `internal/mcp` `internal/mcp/exec` `internal/mcpcatalog` `internal/mcpcli` `internal/mcpclient` `internal/mcpguide` `internal/memory` `internal/metrics` `internal/mutation` `internal/optimize` `internal/ownership` `internal/pack` `internal/pii` `internal/precache` `internal/profiles` `internal/project` `internal/prompt` `internal/refactor` `internal/relay` `internal/remove` `internal/rename` `internal/repair` `internal/retrieval` `internal/reviewpack` `internal/runtime` `internal/sandbox` `internal/schema` `internal/script` `internal/sec` `internal/secscan` `internal/semcache` `internal/setup` `internal/skills` `internal/stats` `internal/storage` `internal/strutil` `internal/swap` `internal/tasklife` `internal/terse` `internal/tokenize` `internal/tokstats` `internal/topology` `internal/transform` `internal/twin` `internal/validate` `internal/verdict` `internal/verification` `internal/version` `internal/verifycmd` `internal/whatif` `internal/diffgate`|
| `internal/agent` | `internal/agent` | `internal/cache` `internal/context` `internal/domain` `internal/eventbus` `internal/governance` `internal/fsutil` `internal/llm` `internal/metrics` `internal/verdict` `internal/verification` `internal/whatif`|
| `internal/agents` | `internal/agents` | `internal/agent` `internal/config` `internal/domain` `internal/eventbus` `internal/governance`|
| `internal/agentwire` | `internal/agentwire` | `internal/configstrip` `internal/pluginhash` `internal/rulesblock` `internal/skills` `internal/strutil` `internal/version`|
| `internal/app` | `internal/app` | `internal/agent` `internal/agents` `internal/budget` `internal/cache` `internal/calibrate` `internal/coder` `internal/context` `internal/deployment` `internal/domain` `internal/eventbus` `internal/execution` `internal/flight` `internal/fsutil` `internal/governance` `internal/guard` `internal/incident` `internal/index` `internal/intel` `internal/learning` `internal/lenses` `internal/loop` `internal/memory` `internal/modernization` `internal/orgapprovals` `internal/planner` `internal/profiles` `internal/prprovider` `internal/runtime` `internal/storage` `internal/tasklife` `internal/tokenize` `internal/twin` `internal/verdict` `internal/verification` `internal/whatif`|
| `internal/approvalcli` | `internal/approvalcli` | `internal/app` `internal/cliflags` `internal/cliout` `internal/context` `internal/domain` `internal/governance` `internal/memory` `internal/storage` `internal/tasklife`|
| `internal/architecture` | `internal/architecture` | `internal/index` `internal/domain` `internal/intel` `internal/guard` `internal/diffgate`|
| `internal/asttypes` | `internal/asttypes` | |
| `internal/brief` | `internal/brief` | `internal/code` `internal/domain` `internal/index` `internal/intel` `internal/memory` `internal/stats` `internal/topology`|
| `internal/budget` | `internal/budget` | `internal/code` `internal/tokenize`|
| `internal/cache` | `internal/cache` | `internal/config` `internal/flock`|
| `internal/calibrate` | `internal/calibrate` | `internal/fsutil` `internal/index` `internal/intel` `internal/version`|
| `internal/calleefilter` | `internal/calleefilter` | |
| `internal/ci` | `internal/ci` | |
| `internal/clietag` | `internal/clietag` | `internal/index` `internal/mcp/etag` `internal/mcp/provenance` `internal/mcpcatalog`|
| `internal/cliflags` | `internal/cliflags` | |
| `internal/clihints` | `internal/clihints` | `internal/index` `internal/intel`|
| `internal/cliout` | `internal/cliout` | `internal/strutil`|
| | | `internal/blueprint` `internal/bpreceipt` `internal/domain` `internal/eventbus` `internal/execution` `internal/gates` `internal/incident` `internal/index` `internal/loop` `internal/memory` `internal/optimize` `internal/runtime` |
| `internal/code` | `internal/code` | `internal/cache` `internal/filecache` `internal/fsutil` `internal/ignore`|
| `internal/coder` | `internal/coder` | `internal/agent` `internal/agents` `internal/llm` `internal/metrics` `internal/pii` `internal/tokenize` `internal/verdict` `internal/verification`|
| `internal/commitmsg` | `internal/commitmsg` | `internal/cliflags` `internal/code`|
| `internal/compress` | `internal/compress` | `internal/semcache` `internal/terse`|
| `internal/configstrip` | `internal/configstrip` | |
| `internal/config` | `internal/config` | |
| `internal/context` | `internal/context` | `internal/budget` `internal/config` `internal/domain` `internal/eventbus` `internal/evidence` `internal/governance` `internal/guard` `internal/index` `internal/intel` `internal/lenses` `internal/memory` `internal/metrics` `internal/runtime` `internal/skills` `internal/tokenize` `internal/whatif`|
| `internal/contextcli` | `internal/contextcli` | `internal/clietag` `internal/clihints` `internal/cliflags` `internal/cliout` `internal/code` `internal/context` `internal/domain` `internal/eventbus` `internal/governance` `internal/guard` `internal/index` `internal/intel` `internal/lenses` `internal/lock` `internal/profiles` `internal/relay` `internal/strutil` `internal/tokenize` `internal/topology` `internal/version`|
| `internal/deployment` | `internal/deployment` | `internal/config`|
| `internal/diff` | `internal/diff` | `internal/index`|
| `internal/docbudget` | `internal/docbudget` | |
| `internal/diffgate` | `internal/diffgate` | `internal/docbudget` `internal/secscan`|
| `internal/docsearch` | `internal/docsearch` | `internal/cache` `internal/index`|
| `internal/doctor` | `internal/doctor` | `internal/agentwire` `internal/cache` `internal/index` `internal/intel` `internal/llm` `internal/metrics` `internal/runtime` `internal/script` `internal/setup` `internal/stats` `internal/version`|
| `internal/domain` | `internal/domain` | `internal/index` `internal/sec` `internal/secscan`|
| `internal/draft` | `internal/draft` | `internal/index`|
| `internal/eval` | `internal/eval` | `internal/budget` `internal/llm` `internal/tokenize`|
| `internal/eventbus` | `internal/eventbus` | |
| `internal/evidence` | `internal/evidence` | `internal/domain` `internal/governance` `internal/index` `internal/sec` `internal/secscan` `internal/storage`|
| `internal/execution` | `internal/execution` | `internal/cliflags` `internal/cliout` `internal/diff` `internal/governance` `internal/ignore` `internal/index` `internal/metrics` `internal/optimize` `internal/pii` `internal/sandbox` `internal/script` `internal/strutil` `internal/validate`|
| `internal/fetch` | `internal/fetch` | |
| `internal/filecache` | `internal/filecache` | |
| `internal/fit` | `internal/fit` | `internal/code` `internal/index` `internal/tokenize`|
| `internal/flight` | `internal/flight` | `internal/storage`|
| `internal/flock` | `internal/flock` | |
| `internal/fsutil` | `internal/fsutil` | |
| `internal/fw` | `internal/fw` | `internal/ignore` `internal/index`|
| `internal/explorebudget` | `internal/explorebudget` | `internal/budget` `internal/calleefilter` `internal/intel` `internal/tokenize`|
| `internal/gitrun` | `internal/gitrun` | |
| `internal/gitblocks` | `internal/gitblocks` | `internal/strutil`|
| `internal/governance` | `internal/governance` | `internal/cache` `internal/config` `internal/domain` `internal/eventbus` `internal/flock` `internal/index` `internal/metrics` `internal/storage`|
| `internal/govtoken` | `internal/govtoken` | `internal/governance`|
| `internal/graphcli` | `internal/graphcli` | `internal/app` `internal/cache` `internal/clietag` `internal/clihints` `internal/cliflags` `internal/cliout` `internal/context` `internal/doctor` `internal/explorebudget` `internal/index` `internal/intel` `internal/llm` `internal/precache` `internal/project` `internal/strutil` `internal/topology` `internal/twin` `internal/version`|
| `internal/guard` | `internal/guard` | `internal/domain` `internal/eventbus` `internal/index` `internal/intel`|
| `internal/heal` | `internal/heal` | `internal/cliflags` `internal/diff` `internal/incident` `internal/index` `internal/intel` `internal/llm` `internal/sandbox` `internal/validate`|
| `internal/hook` | `internal/hook` | `internal/cliflags` `internal/memory` `internal/optimize` `internal/setup`|
| `internal/host` | `internal/host` | `internal/budget` `internal/domain`|
| `internal/ignore` | `internal/ignore` | |
| `internal/incident` | `internal/incident` | `internal/cache` `internal/domain` `internal/eventbus` `internal/evidence` `internal/execution` `internal/fsutil` `internal/governance` `internal/index` `internal/intel` `internal/memory` `internal/metrics` `internal/prprovider` `internal/runtime` `internal/verdict` `internal/verification`|
| `internal/index` | `internal/index` | `internal/asttypes` `internal/cache` `internal/calleefilter` `internal/filecache` `internal/fsutil` `internal/ignore` `internal/langparse` `internal/lock` `internal/metrics` `internal/tokenize` `internal/tokstats`|
| `internal/integration` | `internal/integration` | |
| `internal/intel` | `internal/intel` | `internal/budget` `internal/cache` `internal/calleefilter` `internal/code` `internal/domain` `internal/eventbus` `internal/index` `internal/langparse` `internal/tokenize` `internal/tokstats` `internal/topology`|
| `internal/learnclaim` | `internal/learnclaim` | `internal/domain`|
| `internal/learning` | `internal/learning` | `internal/cache` `internal/domain` `internal/learnclaim` `internal/memory`|
| `internal/lenses` | `internal/lenses` | `internal/domain`|
| `internal/langparse` | `internal/langparse` | `internal/asttypes` `internal/calleefilter`|
| `internal/llm` | `internal/llm` | `internal/config`|
| `internal/lock` | `internal/lock` | `internal/flock`|
| `internal/loop` | `internal/loop` | `internal/coder` `internal/deployment` `internal/domain` `internal/eventbus` `internal/execution` `internal/flight` `internal/governance` `internal/incident` `internal/learning` `internal/memory` `internal/planner` `internal/runtime` `internal/verdict` `internal/verification`|
| `internal/lspbridge` | `internal/lspbridge` | `internal/fsutil` |
| `internal/mcp` | `internal/mcp` | `internal/agent` `internal/app` `internal/brief` `internal/budget` `internal/cache` `internal/cliflags` `internal/code` `internal/config` `internal/context` `internal/diff` `internal/docsearch` `internal/domain` `internal/explorebudget` `internal/draft` `internal/enterprise` `internal/evidence` `internal/execution` `internal/fetch` `internal/fit` `internal/flight` `internal/fsutil` `internal/fw` `internal/governance` `internal/govrbac` `internal/govtoken` `internal/guard` `internal/heal` `internal/incident` `internal/index` `internal/intel` `internal/lenses` `internal/llm` `internal/lock` `internal/loop` `internal/lspbridge` `internal/mcp/bridge` `internal/mcp/compose` `internal/mcp/context` `internal/mcp/contextwatch` `internal/mcp/coord` `internal/mcp/crossrepo` `internal/mcp/doc` `internal/mcp/envelope` `internal/mcp/evidence` `internal/mcp/exec` `internal/mcp/explain` `internal/mcp/fingerprint` `internal/mcp/flight` `internal/mcp/gov` `internal/mcp/governance` `internal/mcp/graph` `internal/mcp/health` `internal/mcp/highlevel` `internal/mcp/lsp` `internal/mcp/mcpargs` `internal/mcp/memory` `internal/mcp/merge` `internal/mcp/mutation` `internal/mcp/optimize` `internal/mcp/orchestrate` `internal/mcp/org` `internal/orgapprovals` `internal/mcp/planner` `internal/mcp/policydsl` `internal/mcp/preedit` `internal/mcp/prompt` `internal/mcp/prose` `internal/mcp/provenance` `internal/mcp/rbac` `internal/mcp/refactor` `internal/mcp/retrieve` `internal/mcp/review` `internal/mcp/root` `internal/mcp/runtime` `internal/mcp/security` `internal/mcp/skill` `internal/mcp/stream` `internal/mcp/synthtest` `internal/mcp/toolsurface` `internal/mcp/transform` `internal/mcp/transport` `internal/mcp/watcher` `internal/mcpcatalog` `internal/mcpmeta` `internal/mcpclient` `internal/mcpgate` `internal/mcpguide` `internal/mcphints` `internal/mcpserve` `internal/memory` `internal/metaroute` `internal/metrics` `internal/mutation` `internal/optimize` `internal/pack` `internal/pii` `internal/policy` `internal/precache` `internal/profiles` `internal/project` `internal/prompt` `internal/refactor` `internal/relay` `internal/rename` `internal/repair` `internal/retrieval` `internal/runtime` `internal/sandbox` `internal/schema` `internal/script` `internal/sec` `internal/secscan` `internal/semcache` `internal/servedledger` `internal/session` `internal/skills` `internal/stats` `internal/storage` `internal/strutil` `internal/swap` `internal/synthtest` `internal/tasklife` `internal/terse` `internal/tokenize` `internal/topology` `internal/transform` `internal/twin` `internal/validate` `internal/verdict` `internal/verification` `internal/version` `internal/verifycmd` `internal/whatif`|
| `internal/mcp/bridge` | `internal/mcp/bridge` | `internal/mcp/mcpargs` `internal/mcp/root` `internal/mcpclient`|
| `internal/mcp/compose` | `internal/mcp/compose` | `internal/cliflags` `internal/mcp/mcpargs`|
| `internal/mcp/context` | `internal/mcp/context` | `internal/brief` `internal/code` `internal/context` `internal/fit` `internal/fsutil` `internal/index` `internal/intel` `internal/mcp/mcpargs` `internal/pack` `internal/verification`|
| `internal/mcp/contextwatch` | `internal/mcp/contextwatch` | `internal/cliflags` `internal/tokenize`|
| `internal/mcp/coord` | `internal/mcp/coord` | `internal/mcp/mcpargs`|
| `internal/mcp/crossrepo` | `internal/mcp/crossrepo` | `internal/intel`|
| `internal/mcp/doc` | `internal/mcp/doc` | `internal/cache` `internal/docsearch` `internal/fetch` `internal/index` `internal/intel` `internal/llm` `internal/mcp/gov` `internal/mcp/mcpargs` `internal/precache` `internal/strutil`|
| `internal/mcp/envelope` | `internal/mcp/envelope` | `internal/app` `internal/budget` `internal/context` `internal/domain` `internal/index` `internal/mcp/mcpargs` `internal/tokenize`|
| `internal/mcp/etag` | `internal/mcp/etag` | |
| `internal/mcp/evidence` | `internal/mcp/evidence` | `internal/cliflags` `internal/evidence` `internal/fetch` `internal/fsutil` `internal/governance` `internal/index` `internal/mcp/mcpargs` `internal/mcp/root` `internal/pii` `internal/storage`|
| `internal/mcp/exec` | `internal/mcp/exec` | `internal/diff` `internal/execution` `internal/fsutil` `internal/governance` `internal/heal` `internal/incident` `internal/index` `internal/mcp/mcpargs` `internal/mcp/root` `internal/optimize` `internal/pii` `internal/sandbox` `internal/script` `internal/strutil` `internal/validate`|
| `internal/mcp/explain` | `internal/mcp/explain` | `internal/index` `internal/intel`|
| `internal/mcp/fingerprint` | `internal/mcp/fingerprint` | `internal/governance` `internal/mcp/mcpargs`|
| `internal/mcp/flight` | `internal/mcp/flight` | `internal/flight` `internal/mcp/mcpargs` `internal/mcp/root`|
| | | `internal/mcp/mcpargs` `internal/mcp/root` |
| `internal/mcp/gov` | `internal/mcp/gov` | `internal/domain` `internal/governance` `internal/govtoken` `internal/index` `internal/mcp/mcpargs` `internal/mcp/provenance`|
| `internal/mcp/governance` | `internal/mcp/governance` | `internal/governance` `internal/govtoken` `internal/index` `internal/intel` `internal/lock` `internal/mcp/gov` `internal/mcp/mcpargs` `internal/mcp/provenance` `internal/mcp/root` `internal/rename`|
| `internal/mcp/graph` | `internal/mcp/graph` | `internal/budget` `internal/context` `internal/explorebudget` `internal/fw` `internal/index` `internal/intel` `internal/lenses` `internal/llm` `internal/mcp/gov` `internal/mcp/mcpargs` `internal/mcp/provenance` `internal/profiles` `internal/project` `internal/retrieval` `internal/topology` `internal/twin`|
| `internal/mcp/health` | `internal/mcp/health` | `internal/index` `internal/mcp/mcpargs` `internal/metrics`|
| `internal/mcp/highlevel` | `internal/mcp/highlevel` | `internal/agent` `internal/app` `internal/domain` `internal/governance` `internal/llm` `internal/loop` `internal/mcpcatalog` `internal/mcp/mcpargs` `internal/mcp/root` `internal/mcp/toolsurface` `internal/metrics` `internal/pii` `internal/profiles` `internal/runtime` `internal/tasklife` `internal/verdict` `internal/verification` `internal/whatif`|
| `internal/mcp/lsp` | `internal/mcp/lsp` | `internal/lspbridge` `internal/mcp/mcpargs` `internal/mcp/root`|
| `internal/mcp/mcpargs` | `internal/mcp/mcpargs` | |
| `internal/mcp/memory` | `internal/mcp/memory` | `internal/mcp/mcpargs` `internal/mcp/root` `internal/memory`|
| `internal/mcp/merge` | `internal/mcp/merge` | `internal/cliflags` `internal/diff` `internal/fsutil` `internal/index` `internal/intel` `internal/mcp/mcpargs` `internal/mcp/root`|
| `internal/mcp/mutation` | `internal/mcp/mutation` | `internal/governance` `internal/mcp/mcpargs` `internal/mcp/root` `internal/mutation`|
| `internal/mcp/optimize` | `internal/mcp/optimize` | `internal/budget` `internal/mcp/mcpargs` `internal/mcp/root` `internal/optimize` `internal/semcache` `internal/strutil` `internal/swap` `internal/terse` `internal/tokenize`|
| `internal/mcp/orchestrate` | `internal/mcp/orchestrate` | `internal/app` `internal/context` `internal/mcp/mcpargs`|
| `internal/mcp/planner` | `internal/mcp/planner` | `internal/app` `internal/context` `internal/mcp/mcpargs`|
| `internal/mcp/policydsl` | `internal/mcp/policydsl` | `internal/intel` `internal/mcp/mcpargs` `internal/mcp/root` `internal/policy`|
| `internal/mcp/preedit` | `internal/mcp/preedit` | `internal/cliflags` `internal/index` `internal/intel` `internal/guard`|
| `internal/mcp/prompt` | `internal/mcp/prompt` | `internal/cliflags` `internal/code` `internal/mcp/mcpargs` `internal/mcp/root` `internal/memory` `internal/prompt`|
| `internal/mcp/prose` | `internal/mcp/prose` | `internal/index`|
| `internal/mcp/provenance` | `internal/mcp/provenance` | `internal/governance` `internal/index`|
| `internal/mcp/refactor` | `internal/mcp/refactor` | `internal/governance` `internal/mcp/mcpargs` `internal/mcp/root` `internal/refactor`|
| `internal/mcp/retrieve` | `internal/mcp/retrieve` | `internal/index` `internal/mcp/graph` `internal/mcp/gov` `internal/mcp/mcpargs` `internal/mcp/provenance` `internal/retrieval`|
| `internal/mcp/review` | `internal/mcp/review` | `internal/index` `internal/intel` `internal/lenses` `internal/mcp/mcpargs` `internal/profiles` `internal/runtime` `internal/topology`|
| `internal/mcp/root` | `internal/mcp/root` | |
| `internal/mcp/runtime` | `internal/mcp/runtime` | `internal/domain` `internal/index` `internal/mcp/mcpargs` `internal/mcp/root` `internal/runtime`|
| `internal/mcp/security` | `internal/mcp/security` | `internal/draft` `internal/guard` `internal/index` `internal/intel` `internal/mcp/mcpargs` `internal/mcp/root` `internal/pii` `internal/relay` `internal/schema` `internal/sec` `internal/secscan` `internal/verification` `internal/topology`|
| `internal/mcp/skill` | `internal/mcp/skill` | `internal/mcp/mcpargs` `internal/mcp/root` `internal/skills`|
| `internal/mcp/stream` | `internal/mcp/stream` | `internal/mcp/mcpargs`|
| `internal/mcp/synthtest` | `internal/mcp/synthtest` | `internal/index` `internal/mcp/mcpargs` `internal/mcp/root` `internal/sec` `internal/secscan` `internal/synthtest`|
| `internal/mcp/toolsurface` | `internal/mcp/toolsurface` | |
| `internal/mcp/transform` | `internal/mcp/transform` | `internal/cliflags` `internal/index` `internal/mcp/mcpargs` `internal/mcp/root` `internal/transform`|
| `internal/mcp/transport` | `internal/mcp/transport` | |
| `internal/mcp/watcher` | `internal/mcp/watcher` | `internal/index`|
| `internal/mcpcatalog` | `internal/mcpcatalog` | `internal/diffgate`|
| `internal/mcpcli` | `internal/mcpcli` | `internal/cliflags` `internal/index` `internal/mcp/compose` `internal/mcp/contextwatch` `internal/mcp/evidence` `internal/mcp/merge` `internal/mcp/preedit` `internal/mcp/prompt` `internal/mcp/transform`|
| `internal/mcpmeta` | `internal/mcpmeta` | `internal/mcpcatalog` `internal/mcp/mcpargs` `internal/metaroute`|
| `internal/mcpclient` | `internal/mcpclient` | |
| `internal/mcpgate` | `internal/mcpgate` | `internal/config` `internal/domain` `internal/governance`|
| `internal/mcpguide` | `internal/mcpguide` | |
| `internal/mcphints` | `internal/mcphints` | |
| `internal/mcpserve` | `internal/mcpserve` | `internal/config` `internal/mcp/mcpargs` `internal/optimize` `internal/stats` `internal/tokenize`|
| `internal/memory` | `internal/memory` | `internal/cache` `internal/domain` `internal/fsutil` `internal/metrics` `internal/storage`|
| `internal/metaroute` | `internal/metaroute` | `internal/mcpcatalog` `internal/skills`|
| `internal/metrics` | `internal/metrics` | |
| `internal/modernization` | `internal/modernization` | `internal/index` `internal/intel` `internal/topology`|
| `internal/mutation` | `internal/mutation` | `internal/execution` `internal/fsutil`|
| `internal/optimize` | `internal/optimize` | `internal/cache` `internal/compress` `internal/config` `internal/llm` `internal/memory` `internal/pii` `internal/semcache` `internal/stats` `internal/tokenize`|
| `internal/ownership` | `internal/ownership` | |
| `internal/pack` | `internal/pack` | `internal/budget` `internal/code` `internal/ignore` `internal/index` `internal/sec` `internal/secscan` `internal/tokenize`|
| `internal/pii` | `internal/pii` | |
| `internal/planner` | `internal/planner` | `internal/agent` `internal/agents` `internal/llm` `internal/pii`|
| `internal/policy` | `internal/policy` | |
| `internal/pluginhash` | `internal/pluginhash` | |
| `internal/precache` | `internal/precache` | `internal/brief` `internal/cache` `internal/code` `internal/docsearch` `internal/index` `internal/langparse` `internal/semcache`|
| `internal/processgroup` | `internal/processgroup` | |
| `internal/profiles` | `internal/profiles` | `internal/terse` `internal/tokenize`|
| `internal/project` | `internal/project` | `internal/index` `internal/intel` `internal/stats` `internal/topology`|
| `internal/prompt` | `internal/prompt` | |
| `internal/prprovider` | `internal/prprovider` | |
| `internal/relay` | `internal/relay` | `internal/eventbus`|
| `internal/rename` | `internal/rename` | `internal/filecache` `internal/index`|
| `internal/refactor` | `internal/refactor` | `internal/diff` `internal/fsutil`|
| `internal/repair` | `internal/repair` | |
| `internal/retrieval` | `internal/retrieval` | `internal/budget` `internal/context` `internal/evidence` `internal/index` `internal/intel` `internal/tokenize`|
| `internal/rulesblock` | `internal/rulesblock` | |
| `internal/runtime` | `internal/runtime` | `internal/config` `internal/domain`|
| `internal/sandbox` | `internal/sandbox` | `internal/cliflags` `internal/cliout` `internal/fsutil` `internal/governance` `internal/index` `internal/intel` `internal/processgroup`|
| `internal/sandbox/landlock` | `internal/sandbox/landlock` | |
| `internal/schema` | `internal/schema` | |
| `internal/script` | `internal/script` | `internal/governance` `internal/processgroup`|
| `internal/sdk` | `internal/sdk` | `internal/domain` `internal/mcp` `internal/mcpcatalog`|
| `internal/sec` | `internal/sec` | `internal/index` `internal/secscan`|
| `internal/secscan` | `internal/secscan` | `internal/ignore` `internal/index` `internal/langparse` `internal/pii`|
| `internal/semcache` | `internal/semcache` | `internal/cache`|
| `internal/servedledger` | `internal/servedledger` | |
| `internal/session` | `internal/session` | `internal/project`|
| `internal/setup` | `internal/setup` | `internal/agentwire` `internal/cliflags` `internal/configstrip` `internal/gitblocks` `internal/mcp/toolsurface` `internal/mcpcatalog` `internal/skills` `internal/strutil` `internal/version`|
| `internal/skills` | `internal/skills` | `internal/governance`|
| `internal/stats` | `internal/stats` | `internal/cache` `internal/context`|
| `internal/storage` | `internal/storage` | |
| `internal/strutil` | `internal/strutil` | |
| `internal/swap` | `internal/swap` | `internal/budget` `internal/code` `internal/tokenize`|
| `internal/tasklife` | `internal/tasklife` | `internal/agent` `internal/agents` `internal/cache` `internal/calibrate` `internal/coder` `internal/context` `internal/deployment` `internal/domain` `internal/eventbus` `internal/execution` `internal/flight` `internal/fsutil` `internal/governance` `internal/incident` `internal/index` `internal/intel` `internal/learning` `internal/lenses` `internal/loop` `internal/memory` `internal/modernization` `internal/orgapprovals` `internal/planner` `internal/prprovider` `internal/runtime` `internal/storage` `internal/twin` `internal/verdict` `internal/verification` `internal/whatif`|
| `internal/synthtest` | `internal/synthtest` | `internal/diff` `internal/fsutil` `internal/index` `internal/intel`|
| `internal/terse` | `internal/terse` | `internal/tokenize`|
| `internal/testfixture` | `internal/testfixture` | |
| `internal/tokenize` | `internal/tokenize` | `internal/config`|
| `internal/tokstats` | `internal/tokstats` | `internal/tokenize`|
| `internal/topology` | `internal/topology` | `internal/code` `internal/index`|
| `internal/transform` | `internal/transform` | `internal/diff` `internal/fsutil` `internal/index`|
| `internal/twin` | `internal/twin` | `internal/domain` `internal/index` `internal/intel` `internal/runtime` `internal/twinindex`|
| `internal/twinindex` | `internal/twinindex` | `internal/domain` `internal/index` `internal/intel` `internal/twin`|
| `internal/validate` | `internal/validate` | `internal/index` `internal/processgroup`|
| `internal/verdict` | `internal/verdict` | `internal/domain`|
| `internal/verifycmd` | `internal/verifycmd` | `internal/app` `internal/cliflags` `internal/cliout` `internal/compress` `internal/config` `internal/eval` `internal/governance` `internal/index` `internal/mcp/mcpargs` `internal/mcpcatalog` `internal/metrics` `internal/optimize` `internal/skills` `internal/storage` `internal/tasklife` `internal/verdict` `internal/verification` `internal/version` `internal/gitrun`|
| `internal/verification` | `internal/verification` | `internal/budget` `internal/calibrate` `internal/ci` `internal/config` `internal/context` `internal/domain` `internal/eval` `internal/eventbus` `internal/evidence` `internal/governance` `internal/guard` `internal/host` `internal/index` `internal/intel` `internal/langparse` `internal/memory` `internal/metrics` `internal/retrieval` `internal/sandbox` `internal/secscan` `internal/tokenize` `internal/validate` `internal/verdict` `internal/version` `internal/gitrun` `internal/duplication`|
| `internal/version` | `internal/version` | |
| | | `internal/agent` `internal/agents` `internal/app` `internal/architecture` `internal/domain` `internal/eventbus` `internal/governance` `internal/govrbac` `internal/guard` `internal/incident` `internal/index` `internal/intel` `internal/learning` `internal/loop` `internal/memory` `internal/metrics` `internal/modernization` `internal/orgapprovals` `internal/relay` `internal/runtime` `internal/tasklife` `internal/topology` `internal/verification` `internal/version` `internal/whatif` |
| `internal/webhook` | `internal/webhook` | `internal/eventbus`|
| `internal/whatif` | `internal/whatif` | `internal/domain` `internal/evidence` `internal/index` `internal/intel`|

