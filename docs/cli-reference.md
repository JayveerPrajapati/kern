# kern CLI Reference

The full command reference for the `kern` CLI. The README keeps a compact
summary and links here. This list mirrors the shipped dispatch table
(`cmd/kern/dispatch_table.go`) — when the CLI grows, this file grows with it.
`kern --all` prints the authoritative catalog; this document is the
curated one-liner reference for the same 65 commands.

## Command list

```bash
# meta
kern version [flags]                         print version
kern --version [flags]                       (alias of version)
kern -v [flags]                              (alias of version)
kern meta "<request>" [--root ROOT]          NL request router ("show me the architecture")
kern ask "<question>" [--root ROOT]          NL request router (alias of meta)
kern exitcode                                print kern's documented exit-code conventions (0 ok, 1 error, 2 usage, 3 decided-state/policy)
kern bench [--root ROOT] [--json]            deterministic zero-network latency harness (cold/warm index load + fixed query set; writes .kern/bench.json);
                                             --check verifies the run against cmd/kern/bench_thresholds.json and exits 1 on hard violations

# wiring
kern setup [flags]                           wire agents/MCP/hooks (--agents, --agents-md thin|full, --all-agents, --check, --detect, --dry-run,
                                             --global, --global-only, --global-rules, --json, --plain, --remove, --root, --verify, --yes)
kern buddy [root]                            session onboarding digest
kern project-map [root]                      full per-file project map (the same renderer buddy --map appends)
kern onboard [flags]                         register+index+wired status for the repo
kern update [--dry-run] [--force] [--pin <tag>] [--channel <name>]   update the installed binaries (via install.sh)
kern doctor [flags]                          self-diagnostics (--arch-drift, --calibration, --json, --root)
kern config [flags]                          show effective configuration (env > .kern/config.json > default)

# compression
kern optimize <prompt> [--attach FILE] [--cache] [--fewshot] [--kind prompt|log] [--llm MODEL] [--mask] [--model NAME] [--names a,b] [--session ID]
                                             compress a prompt/log/output
kern compact <file> [--root ROOT] [--tier summary|folded|full] [--etag E]   symbolic file summary
kern log <file|text|-> [flags]                compress log output (wrapper over optimize --kind log; --context-before/--context-after windows, --profile)
kern terse <file|text|-> [--max N]            line-level tersification of text (blank/comment/filler stripping, token budget)
kern fit-context [flags]                     adaptive context-window token compressor using tiered AST folding
kern mask [flags]                            mask secrets/PII locally (--names a,b,c)
kern semcache <list|sim|stats|clear>         semantic cache inspection
kern stats [--days N] [--session ID] [--json] [--by-tool] [--by-agent] [--by-root] [--root R] [--net]   token/cost savings
kern swap [FILE|-] [--max N] [--mode fit|summary|expand]   budget-swap path-tagged fenced code blocks

# exec
kern exec "<code>" [--json] [--lang LANG] [--list] [--max N] [--stdin file|-] [--timeout s]   isolated local runtime, stdout only
kern build "<command>" [--dir DIR] [--root ROOT] [--session ID] [--timeout s]   run a build command in the governed sandbox

# analysis
kern plan <change> [--root ROOT] [--json]    analyze a proposed change (blast-radius impact + surgical refactor planning)
kern apply <edits-file|-> [--root ROOT] [--test] [--dry-run] [--json]    apply edits transactionally, verify, roll back on failure (edits = JSON array of {path, content}; exit 0 applied+verified, 1 failed/rolled back, 2 usage)
kern impact <change> [kind] [new-target] [--root ROOT] [--json] [--precision] [--risk] [--runtime]   blast radius of a change

# autonomy
kern run <intent> [--root ROOT]              intent through the task pipeline

# governance
kern approve <id> | kern approve list        resolve an approval gate (interactive by design: type the id on the terminal); --scope once (default) or prefix with --prefix P records a live prefix grant — same-prefix commands run without re-asking for the session
kern approval ask <id> [--root R]           answer a pending approval at the terminal: [1] accept once / [2] accept prefix this session / [3] reject
kern approval wait <id> [--timeout 30m] [--root R]   block until a pending approval is decided (exit 0 approved / 3 rejected / 1 timed out)
kern approval list [--root R]               list pending approvals and active prefix grants (uses, expiry)
kern approval clear <id> [--approver] [--reason]     remove a blocking approval record (operator escape hatch)
kern policy <set|get|apply> [--file F] [--merge] [--json] [--root R]   org-level governance policy distribution
kern reject <id> [--approver ID] [--reason TEXT]     reject a pending approval request
kern audit [flags] | kern audit append | kern audit repair   governance audit log (tamper-evident chain; bounded display: 50 most recent by default, --limit N / --tail N, 0 = all)

# security
kern sec [root] [--engine internal|gosec|all|none] [--json] [--max N] [--severity ...]   security scan (exit 1 on error findings)

# verification
kern verify [<types>|<file|->] [flags]       unified verification engine (types: build,test,security,architecture,dependency,reuse,e2e,static-analysis,performance,cve,license,secrets,ci,arch; --types T);
                                             --command CMD --output summary|failures|tail:N|lines:A-B|full; --anchor ID re-slices a kept run;
                                             --short/--fast/--changed/--changed-since/--staged/--full; compliance: --cve --license --secrets
kern heal "<task>" [--llm MODEL] [--force] [--yes] [--task]   self-correct failing files (snapshot-based)

# docs
kern docs <query> [root] | kern docs fetch <url> [name] [root] | kern docs index/clear   local doc search (indexes repos with a docs tree)

# search
kern index [root] [--force] [--json] [--status] [--strict] [--update]   (re)build the symbol index
kern ast <pattern> [root] [--all]            AST symbol search (wildcards, kind prefixes)
kern search <query> [root] [--limit N] [--repos] [--json] [--semantic] [--etag E]   ranked free-text symbol search

# graph
kern graph <symbol> [root] [--one-line] [--mermaid] [--json] [--graphml] [--cypher] [--html] [--out FILE] [--limit N] [--etag E]
                                             adaptive context-min slice (definition + callers/callees); graph exports
kern explore <symbol> [root] [--depth N] [--max N] [--max-tokens N] [--explain] [--etag E]   symbol source + blast radius
kern path <from-symbol> <to-symbol> [root] (or --from S --to S)   shortest call path
kern probe "<task text>" [root] [--max N] [--json]   task-driven context bundle
kern why <symbol> [root] [--json] [--etag E]   rationale: doc comment + dependents
kern arch [flags]                            architecture overview
kern project [root]                          compact project map

# context
kern context <symbol> [root] [--lines N] [--lens] [--profile] [--etag E]   minimal source slice
kern authorize-context -agent <id> -task <task> [--deny-path ...] [--symbol SYM] [--root ROOT] [--json]   compute authorized context
kern explain-context --task "<change or intent>" [--root ROOT] [--budget N] [--json]   explainable deterministic context plan
kern orchestrate "<intent>" [--root ROOT] [--mode full|envelope|plan|fix|review|architecture|incident|explain]   silent context pipeline over an intent (JSON)

# review
kern review [--file F] [--lens] [--max N] [--range a..b] [--root R] [--runtime]   review context for changed files (exits 1 when changed files carry risk — findings)

# memory
kern memory <subcommand>                     engineering memory ops (add/list/recall/ranked/remove/clear)
kern remember <lesson> [--auto]              store a lesson ('memory add' alias)
kern memory-ranked <prompt> [--k 5] [--half-life 7.0] [--root ROOT]   decay-weighted memory retrieval

# evidence
kern evidence [flags]                        evidence store: export/verify/explain signed bundles, full-state dump (--full-state, --restore)
kern evidence-anchor [--claim TEXT] [--file F] [--line N] [--symbol NAME] [--root ROOT]   verify citations and cryptographic proof
kern anchor <anchor-id>                      fetch raw uncompressed anchor content
kern health [--root ROOT] [--json]           read-only JSON health snapshot (version, index, catalog)

# locks
kern guard <check|init|allow|classify|hook|grant|pause|paused|resume> [root] [--file f1,f2] [--range a..b] [--json|--sarif] [--threshold N] [--precision default|strict] [--agent-id ID --task DESC]
                                             architecture guardrails (reject boundary violations, exit 3 when count > threshold); classify <cmd> [--json] prints the risk-axis verdict (allow|gate|serve|governed + tier) for one command — the Go source the plugin's bash arm shells out to; grant [--check] [--root R] <command> is the Ask-Protocol bridge for GATE commands: exit 0 when an active approval covers the command (consumed; retry byte-identical), exit 3 with --check when not granted (nothing minted), otherwise mints approval appr-<id> and prints the ask-wait-retry instructions the operator answers via kern approval ask
kern fingerprint [root] [--file f1,f2] [--json]   repo fingerprint

# git
kern hook <install|uninstall|diff|store|claude-post|claude-prompt|gemini-after|gemini-prompt> [root] [--range a..b] [--global]   install/uninstall git hooks
kern commit [--all] [--dry-run] [--message M]   stage+commit

# servers
kern mcp [tools [category] [--json] [--category X]] [flags]   run the MCP server ('kern mcp tools' lists the catalog)
kern mcp-client <add|list|rm|call> [args...]   manage external MCP servers and call their tools (config: .kern/mcp-servers.json; see docs/mcp-client.md)

# docgen
kern gen-catalog [--root ROOT] [--check]     regenerate (or verify without writing) docs/tool-catalog.md
kern gen-contracts [--root ROOT] [--check]   regenerate (or verify without writing) docs/mcp/tool-contracts.md
kern gen-docs --doc catalog|contracts|schemas|site|selection [--root ROOT] [--check]   regenerate or verify docs (tool catalog, contracts, schemas, tool-selection guide, or the mechanical file-tree block of docs/index.md)
kern gen-plugin [--root ROOT] [--check]   sync the opencode plugin's canonical .opencode/plugins/kern.ts into the embedded asset (byte-identical; --check verifies parity without writing)
```

## Conditional fetch: `--etag`

`kern explore`, `kern context`, `kern compact`, `kern search`, `kern why`
and `kern graph` mint an etag in each response. Pass the last-seen etag back
(`kern explore <sym> --etag <E>`; same-named `etag` param on the
`kern_explore`, `kern_context`, `kern_compact_file`, `kern_search`,
`kern_why` and `kern_graph` MCP tools) and, if nothing changed, you get
`unchanged (etag <E>)` instead of the full payload — re-reads of the same
symbol/file/context cost almost nothing. The unchanged reply is
self-describing: if its content is not already in your context (a fresh
subagent or a restarted session), call again without the etag to receive
the full payload.

## `kern exec` — think in code

```bash
kern exec "print(sum(range(101)))" --lang python3   # 5050 (stdout only)
kern exec './script.py'                             # shebang picks the runtime
kern exec 'core::panic!("x")' --lang rust           # compiles + runs
kern exec --list                                    # runtimes installed here
```

Runtimes resolve from PATH (python3/python, node/bun/deno, bash/sh, perl,
ruby, php, lua, julia, R, go, rust). Runs in a fresh temp dir with a hard
timeout (15s default) and a stdout byte cap — only stdout is returned.

The script runs with a sanitized environment (HOME/XDG pointed into the temp
dir, secrets stripped) and, when the platform's unprivileged user namespaces
allow it, in a private network namespace so network egress is blocked. On
platforms where that isolation is unavailable (e.g. macOS, some containers)
`kern exec` **fails closed**: it refuses to run unisolated rather than
silently degrading to full network egress. A local operator can explicitly
opt out of the isolation requirement per-machine with
`export KERN_ALLOW_UNISOLATED=1` (alias: `KERN_ALLOW_NET=1`).

Filesystem confinement is platform-split and honest about it: sensitive-path
reads (~/.ssh, ~/.aws, ~/.gnupg, ~/.kube, ~/.netrc, cloud credentials) are
blocked on both macOS (Seatbelt deny-list) and Linux (Landlock), but
**filesystem writes are not contained on macOS** — the profile is a deny-list
over `(allow default)`, so a script can still write outside its temp dir.
On Linux with Landlock (kernel ≥ 5.13) writes are confined to the workspace,
temp and cache dirs (`KERN_SANDBOX_WRITE_CONFINEMENT`, default on); where
Landlock is unavailable the behavior degrades safely to the macOS-equivalent
posture. Set expectations accordingly: `kern exec` is a network isolation
boundary, not a full filesystem sandbox.

Host command execution is gated by a governance firewall (fail-closed): set
`KERN_ALLOW_EXEC=1` or allowlist tools via `KERN_TOOLS` to opt in. `kern build`
shares the same gate.

## Exit codes

`kern exitcode` prints the authoritative conventions (0 ok, 1 error, 2 usage,
3 denied/decided-state). One boundary is worth stating explicitly: **an
invalid VALUE to a valid flag or argument is a runtime error → exit 1** (e.g.
`kern impact` with an unknown change kind, `kern guard check` with a malformed
boundaries file). Exit 2 is reserved for structurally bad usage: unknown
flags, missing required arguments, and unknown commands.