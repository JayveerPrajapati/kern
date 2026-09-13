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
kern log <file>                                 compress a log file
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
kern setup [--root DIR] [--agents opencode,claude,codex]   wire kern into agents
kern setup --check                               show wiring status
kern buddy [root]                                session onboarding digest
kern onboard [root]                              register + index + wire a repo for kern (session-start)
kern artifacts [task-id]                         inspect task artifacts (ContextPacket → ImpactReport → VerificationReport chain)
kern correlate <alert-json> [--root ROOT]        correlate a production alert to evidence (alert → service → commit → symbol)
kern learn [threshold]                           extract recurring patterns from engineering memory
kern modernize [root]                            phased monolith modernization plan
kern prompt <template> [--file PATH] [--task TEXT]   fine-tuned prompt templates
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
                                (requires -tags sqlite)
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
kern sandbox "<command>"                         run with filesystem snapshot + rollback; every run reports its network policy (posture + network-error hits from output)
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
kern synthesize-test <symbol> [--file <path>]     synthesize table-driven unit tests from signatures
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
kern note <new|list|status|validate> ...            governed decision records (new/list/status/validate)
kern status [--json]                                workspace lock status
kern eval <run|compare|report> [DIR] [--root ROOT] [--max-tokens N] [--mode MODE]   context-quality evaluation
kern workflow <intent> [--task TASK_ID] [--root ROOT]     agent-team workflow
kern request-approval                                request human approval for a high-risk change (two-person rule)
kern resolve <handle-id> [root] [--level l2|l3] [--max-tokens N]   resolve a retrieval handle to l2|l3 content
kern autonomy <intent> [--level L0..L5] [--root ROOT]      closed autonomy loop
kern authorize-context [-agent ID -task DESC] [--root .] [--json]   compute the exact set of symbols/call edges an
                                                                agent may read for a task + auditable authorization proof
kern gen-catalog [--root <dir>]                     regenerate docs/tool-catalog.md from the live MCP catalog
kern metrics                                        show local change-governance validation metrics
kern efficiency <id> [--root ROOT]                  efficiency metrics
kern cycles [--json] [--root ROOT]                  package-level import cycles (Tarjan SCC)
kern surprising [--json] [--root ROOT]              cross-community call edges ranked by community distance x rarity
kern twin                                           software twin (live map of the repo)
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