# kern CLI Reference

The full command reference for the `kern` CLI. The README keeps a compact
summary and links here. This list mirrors the shipped dispatch table
(`cmd/kern/dispatch_table.go`) — when the CLI grows, this file grows with it.

## Command list

```bash
kern skills (list|show <name>|install)            bundled agent runbooks and automation scripts
kern optimize <prompt> [--attach FILE] [--session ID] [--model NAME] [--llm MODEL]
kern preview  <prompt> [--attach FILE]          (dry-run, no stats recorded)
kern compact <file>                             symbolic summary of a file
kern project [root]                             compact project map
kern pack [root] [--max-tokens N] [--out FILE]  one paste-ready bundle: tree + instructions + contents
kern build "<command>" [--dir DIR]              run build, compact output
kern log [<file>|-] [--context-before N] [--context-after M] [--profile NAME] [--root ROOT]   compress a log file with adaptive windowing & profiles
kern index [root]                               build/refresh the AST index
kern watch [root]                               daemon: auto re-index on change
kern ast <pattern> [--all]                      AST symbol search (wildcards, kind prefixes)
kern search <query> [--limit N] [--repos] [--json] [--semantic]
                                ranked free-text symbol search
kern repos (list|add <path> [name]|remove <name>)   multi-repo registry
kern graph <symbol> [--mermaid] [--json] [--graphml] [--html] [--out FILE] [--limit N]
                                 definition + callers + what it calls; graph exports;
                                 --html with no symbol renders a whole-repo explorer (community bands + search)
kern inherits <symbol> [root] [--json]           supertypes + subtypes
kern context <symbolRegex> [--lines N]           minimal source slice
kern why <symbol> [--json]                       rationale: doc comment + dependents
kern wiki [root] [--out DIR]                     export a markdown wiki, one page per package
kern stats [--days N] [--session ID] [--json]    token/cost savings
kern semcache [stats|clear [NS]|list <NS>|sim <A> <B>]   semantic cache inspection
kern diff [--session ID]                         recent before/after entries
kern export --csv                                export stats to CSV
kern tokens [--bpe] "<text>"                     token count (estimator or byte-level BPE counter)
kern setup [--root ROOT] [--agents opencode,claude,codex]   wire kern into agents
kern setup --check                               show wiring status
kern buddy [root]                                session onboarding digest
kern onboard [root]                              register + index + wire a repo for kern (session-start)
kern artifacts [task-id]                         inspect task artifacts (ContextPacket → ImpactReport → VerificationReport chain)
kern correlate <alert-json> [--root ROOT]        correlate a production alert to evidence (alert → service → commit → symbol)
kern learn [threshold]                           extract recurring patterns from engineering memory
kern modernize [root]                            phased monolith modernization plan
kern prompt <template> [--file <file>] [--task TEXT]   fine-tuned prompt templates
kern prompt list                                 list templates
kern remember "<lesson>" / kern memory / kern recall "<prompt>"   project memory
kern budget "<text>" --max N                     fit text to a token budget
kern terse "<text>"|-                            compress an LLM's output
kern exec "<code>" [--lang LANG] [--timeout s] [--max bytes] [--stdin file|-]
                                                isolated local runtime, stdout only
kern doctor [root]                              diagnostics report
kern mask [file|-] [--names a,b,c]              mask secrets/PII locally
kern sec [root] [--severity ...] [--max N] [--json]   security scan (exit 1 on errors)
kern taint [root] [--file F] [--range a..b] [--generate]   taint-lite scan; --range scopes to files changed in a git range; --generate emits test scaffolds (Go + pytest)
kern delete <symbol> [root] [--apply] [--json]   safe-delete check (exit 1 when unsafe); --apply removes
                                                the symbol + its test-only callers (backed up, rollback)
kern rename <old> <new> [root] [--apply] [--json]    structural rename (AST-scoped)
kern flight (list|show <task-id>|tasks|gc) [root]    replay agent flight records; tasks = trail linkage;
                                                gc = retention (--keep-tasks N, --older-than 30d)
kern runtime (status|drift) [root] [--json]      production intelligence: status = wired adapter + service
                                                profiles (discovery wizard when none); drift = runtime
                                                routes vs code routes; kern review --runtime overlays them
kern guide                                          categorized tool usage guide (performance tiers)
kern udiff <file-a> <file-b> [--out patch]          unified line diff between two files (pure Go)
kern hook install / hook diff [range] / hook store [range]
                                post-commit diff → project memory
kern lock <scope> [root] / unlock / status          workspace locks for concurrent agents
kern precache [root] [--interval s] [--once]        watch daemon: pre-warm code/doc caches
kern changes / review / hubs / testgaps / entries / flows / communities / path / dead
       / larges / arch / churn / cochange / near / walk / probe / trace / explore
                                change impact and code-intelligence analyses
kern fts "<query>" [root] [--limit N]               full-text search over the SQLite index
                                (sqlite store compiled in by default; disable with -tags nosqlite)
kern cache [root] [--dry-run]                           cache GC: gzip-archive dormant entries, TTL-evict stale ones
kern lsp [root]                                     LSP over stdio: hover/definition/references from the index
kern guard init [root]                              scaffold .kern/boundaries.json
kern guard check [root] [--file F] [--range a..b] [--json|--sarif] [--threshold N]
                                reject boundary violations (exit 2 when count > N)
kern check [--staged|--repo R] [--format F]          run change-firewall gates (secrets, boundaries, duplication)
kern fix [--file F] [--content C] [--repo R]         validate fix in isolated git worktree; auto-repair loop
kern ci --base <sha> --head <sha> [--repo R]         pre-merge CI gate; emits tamper-evident receipt
kern verify-receipt <id> [--sarif] [--in-toto] [--check-diff]   verify receipt signature, export SARIF/in-toto, detect git tamper
kern ops "<intent>" [--level L0-L5] [--non-interactive] [--json]   KernOps terminal cockpit: governed execution in ephemeral sandboxes
kern ops triage --log <path|-> [--non-interactive] [--json]         Auto-SRE incident triage: squeeze log, AST correlation, sandbox repro, auto-repair
kern fw [root] [--catalog]                       framework detection
kern verify [<types>|<file|->] [root] [--types T]   hallucination check for file claims, or unified verification engine (types: build,test,security,architecture,dependency)
kern validate [root]                             run the project's build/test, compact
kern heal "<task>" [--llm MODEL] [--max N] [--force]  snapshot-based LLM auto-fix
kern analyze <change> [--root ROOT]          analyze a proposed change against the whole system (ADR)
kern team [--root ROOT]                      build the standard specialist team; list roles + task states
kern risk <change> [--root ROOT]             deterministic risk report for a proposed change
kern bridges [root] [--limit N] [--json]     cross-package bridge detection (coupling points)
kern sandbox "<command>"                         run with filesystem snapshot + rollback (NOT isolation: full user privileges, out-of-root writes never rolled back, ~/.ssh & ~/.aws readable); every run reports its network policy (posture + network-error hits from output); the command runs argv-only — NO shell expansion, so ~ and $VAR are literal (use absolute paths) and shell operators (|, >, &&) are not interpreted
kern pre-edit <file|symbol>                       predictive blast-radius, untested hotspot detection
kern compose '<pipeline-json>'                    multi-tool deterministic pipeline runner (inline JSON)
kern prompt-fill <template>                       compile dynamic prompt with injected project context
kern semantic-diff <file-a> <file-b>              AST-level functional diff of modified symbols
kern evidence-anchor "<claim-text>"               SHA-256 evidence certificate verification for claim
kern context-watch                                conversation context bloat monitor & compaction
kern agent-fingerprint                            agent tool sequence hashing and loop detection
kern explain <symbol>                             graph-backed architectural narrative synthesis
kern cross-repo-impact <symbol> --repo <path>...  multi-repo contract compatibility and blast radius
kern memory-ranked <query>                        decay-weighted memory recall with half-life scoring
kern policy-dsl --policy <policy.json>            declarative policy-as-code evaluation
kern agent-coordination (claim|release|list)      multi-agent workspace claims with TTL and handoffs
kern agent-role-rbac                              role-based tool access control
kern stream                                       response token chunking and streaming
kern ast-transform <file> [--add-field|...]       deterministic AST-level transformations
kern semantic-merge --base <b> --local <l> --remote <r>   AST-aware 3-way semantic merge
kern synthesize-test <symbol> [--file <file>]     synthesize table-driven unit tests from signatures
kern health                                       MCP server health and index freshness report
kern schema ...                                  JSON-schema validation
kern docs index/clear/fetch                       local docs index (index|clear|fetch)
kern version                                     print the installed version
kern serve [--root PATH] [--addr ADDR] [--enterprise] [--project NAME=PATH]...
                                                start the REST API + dashboard server
                                                (single-project, or --enterprise multi-project
                                                with shared org audit/memory/policies)
```

## More commands (shipped, previously undocumented here)

One-liners straight from the dispatch table's own help text:

```bash
kern approve [--approver ID] [--reason TEXT] [--reject]   resolve an approval gate (list pending with no args)
kern deploy <task-id> [--version V]                deploy a task (real deploys require approval)
kern org [--project NAME=PATH]... <subcommand>     enterprise org admin (projects/agents/teams/memory/audit/search)
kern policy <set|get|apply> [--root R] [--file F] [--merge]   org policy distribution: write/merge the org policy document, print it (+hash + drift), or re-apply it
kern note <new|list|status|validate> ...            governed decision records (new/list/status/validate)
kern status [--json]                                workspace lock status
kern eval <run|compare|report> [root] [--root ROOT] [--max-tokens N] [--mode MODE]   context-quality evaluation
kern workflow <intent> [--task TASK_ID] [--root ROOT]     agent-team workflow
kern request-approval                                request human approval for a high-risk change (two-person rule)
kern resolve <handle-id> [root] [--level l2|l3] [--max-tokens N]   resolve a retrieval handle to l2|l3 content
kern autonomy <intent> [--level L0..L5] [--root ROOT]      closed autonomy loop
kern authorize-context [-agent ID -task DESC] [--root .] [--json]   compute the exact set of symbols/call edges an
                                                                agent may read for a task + auditable authorization proof
kern gen-catalog [--root ROOT]                     regenerate docs/tool-catalog.md from the live MCP catalog
kern metrics                                        show local change-governance validation metrics
kern cycles [--json] [--root ROOT]                  package-level import cycles (Tarjan SCC)
kern surprising [--json] [--root ROOT]              cross-community call edges ranked by community distance x rarity
kern twin                                           software twin (live map of the repo)
kern fit-context <symbol|file> [--budget N]         context-adaptive token window compressor
kern lsp-bridge <symbol|file> [--action def]        zero-weight LSP bridge for compiler types & definitions
kern fw-trace <symbol|route> [--framework name]     framework dependency injection & route tracer
kern mutate [root] [--threshold N]                  lightweight mutation testing for regression sensitivity
kern fragility [root] [--limit N]                   causal defect & fragility hotspot memory
kern refactor-transaction <plan-json>               multi-file transactional AST refactor sandbox
kern repair-diagnostics <error-text>                compiler diagnostic-to-AST auto-repair engine
kern agent-interrupt <task-id> [reason words...]    cancel a running task through the TaskService
kern agent-message --to <agent> [--from <agent>] [--task <id>]   send a message to an agent's coordination inbox
kern agents [--probe] [--json] [--root ROOT]        wired agents + LLM provider priority
kern anchor <anchor-id>                             fetch raw uncompressed anchor content
kern arch [flags]                                   architecture overview
kern ask "<question>" [--root ROOT]                  NL request router (alias of meta)
kern audit [flags]                                  governance audit log
kern bench [--root ROOT] [--json]                   deterministic latency harness: cold/warm index load + fixed query set (writes .kern/bench.json)
kern blueprint <subcommand> [args]                  blueprint change-governance suite (check/diff-gate/fix/metrics/request-approval/reject/verify-receipt/ci/install)
kern brief [root]                                   print the repo onboarding brief (project map, index, hubs, entry points, stats, memory)
kern calibrate [flags]                              measure how well blast-radius prediction matches git history (F1)
kern check-draft <file|-> [root] [--lang LANG] [--file F]   validate draft code against the index
kern churn [flags]                                  change-frequency risk
kern cochange [flags]                               co-change coupling
kern commit [flags]                                 stage+commit
kern commitmsg [flags]                              conventional commit message
kern communities [flags]                            subsystem clusters
kern completion <bash|zsh|fish>                     generate shell completion scripts
kern config [flags]                                 show effective configuration (env > .kern/config.json > default)
kern context-envelope --change "<change>" [--root ROOT] [--max-tokens N]   context envelope as versioned JSON (alias of orchestrate --mode envelope)
kern dead [flags]                                   dead-code detection
kern diff-gate [flags]                              deterministic diff gate: advisory local checks on the working-tree diff (gofmt, vulnerabilities, schema drift, unsafe exec, changelog, MCP catalog) — --blocking for CI
kern do "<intent>" [--level L0..L5]                 autonomous task (wired LLM coder + planner)
kern doc-fetch <url> [--name N] [--root ROOT]       fetch a doc page into the index
kern doc-search <query> [--root ROOT] [--limit N]   search local docs
kern efficiency <id> [--root ROOT]                  efficiency metrics (alias of task efficiency)
kern entries [flags]                                entry points (alias of entry-points)
kern entry-points [flags]                           list framework entry points
kern entrypoints [flags]                            list framework entry points (alias of entry-points)
kern events [flags]                                 serve/watch/emit system events (relay)
kern evidence [flags]                               evidence store: export/verify/explain signed bundles, full-state dump
kern execute <patch|patch-file> [--root ROOT]       apply a patch in a sandbox
kern exitcode                                       print kern's documented exit-code conventions (0 ok, 1 error, 2 usage, 3 decided-state/policy)
kern explain-context --task "<change or intent>" [--root ROOT] [--budget N] [--json]   explainable deterministic context plan (alias of orchestrate --mode plan)
kern explain-finding --finding <json> [--root ROOT] plain-language explanation of a blueprint gate finding
kern explore <symbol> [root] [--depth N] [--max N] [--explain]   symbol source + blast radius
kern fetch-raw-anchor <anchor-id>                   fetch raw uncompressed anchor content (alias of anchor)
kern fingerprint [flags]                            repo fingerprint
kern flows [flags]                                  call flows
kern frameworks [flags]                             detect frameworks (alias of fw)
kern gen-contracts [--root ROOT]                   regenerate docs/mcp/tool-contracts.md from the live MCP catalog
kern gen-docs --doc catalog|contracts|site [--root ROOT]   regenerate docs (tool catalog, tool contracts, or the mechanical file-tree block of docs/index.md) from the live MCP catalog
kern host [flags]                                   silent host-adapter context injection (dry-run|check|uninstall)
kern hubs [flags]                                   hotspots
kern impact <change> [kind] [new-target] [--root ROOT]   blast radius of a change
kern incident list | <alert-json> [snapshot-json] [--root ROOT]   incident investigation; 'kern incident list' browses history
kern install [flags]                                install Blueprint change-governance git hooks (pre-commit/pre-push)
kern kernops [flags]                                governed autonomous engineering cockpit (alias of ops)
kern larges [flags]                                 god functions
kern loop <intent> [--level L0..L5] [--mode observe|autonomous] [--schedule cron] [--root ROOT]   closed autonomy loop
kern mcp [tools [category] [--json] [--category X]] [flags]   run the MCP server ('kern mcp tools' lists the catalog)
kern mcp-client [flags]                             external MCP servers: add/list/rm/call
kern meta "<request>" [--root ROOT] [--pipeline JSON]  NL request router
kern near <symbol> [root] [--depth N] [--max N]     dependency-tree walk
kern orchestrate "<intent>" [--root ROOT] [--max-tokens N] [--mode fix|review|architecture|incident|explain|envelope|plan|full] [--with-skill NAME]   silent context pipeline (classify -> plan -> evidence -> budget -> envelope)
kern path <from-symbol> <to-symbol> [root]          shortest call path
kern plan <change> [--root ROOT]                    analyze a proposed change
kern probe "<task text>" [root] [--max N]           task-driven context bundle
kern prose "<words>" [root] [--limit N]             map prose <words> to symbol candidates
kern refactor [flags]                               multi-file transactional AST refactoring engine with sandbox compilation and rollback (alias of refactor-transaction)
kern register-host-sampler [command] [--key K] [--timeout S] [--model M]   register/unregister a host sampler command for LLM delegation (hosts that do not announce MCP sampling)
kern reject [flags]                                 reject a pending approval request: reject <id> [--reason ...]
kern repair-guidance --finding <json> [--root ROOT] repair guidance for a blueprint gate finding
kern retrieve                                       progressive disclosure retrieval (l1|l2|l3)
kern review-consensus [flags]                       normalize review packs into consensus/divergence (P2-002)
kern review-pack [flags]                            immutable deterministic review pack (P2-001)
kern run <intent> [--root ROOT]                     intent through the task pipeline
kern security [flags]                               security scan (alias of sec)
kern simulate <change> [kind] [new-target] [--root ROOT]   simulate a change's impact (alias of impact)
kern snapshot [root] [--out FILE] [--symbol X] [--limit N] [--verify <file>] [--strict] [--format pack] [--max-tokens N]   canonical graph snapshot for multi-agent share
kern swap [FILE|-] [--max N] [--mode fit|summary|expand]   budget-swap path-tagged fenced code blocks
kern task <id> [--root ROOT]                        task lifecycle ops
kern tasks [--root ROOT]                            list tasks (id/state/intent/updated) (alias of task list)
kern test-gaps [flags]                              analyze test coverage gaps (alias of testgaps)
kern testgaps [flags]                               analyze test coverage gaps
kern trace <file|- for stdin> [root] [--limit N]    runtime-impact overlay
kern ui [flags]                                     run the web console (alias of serve)
kern unlock <scope> [root]                          release workspace lock
kern update [--dry-run] [--force] [--pin <tag>] [--channel <name>]   update the installed binaries (via install.sh)
kern validate-proposed --files <json> [--root ROOT] [--source SRC]   blueprint gate on a proposed (not-on-disk) change
kern walk <symbol> [root] [--depth N] [--max N]     dependency-tree walk
kern web [flags]                                    run the web console (alias of serve)
kern what-if <change> [kind] [new-target] [--root ROOT]   simulate a change's impact (alias of impact)
```
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

Host command execution is gated by a governance firewall (fail-closed): set
`KERN_ALLOW_EXEC=1` or allowlist tools via `KERN_TOOLS` to opt in. `kern build`
and `kern validate` share the same gate.