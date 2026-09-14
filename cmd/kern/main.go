// Command kern is the local context optimizer: prompt compression, log
// stripping, project mapping, compact build runs and token savings reports.

package main

import (
	"fmt"
	"os"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	kversion "github.com/JayveerPrajapati/kern/internal/version"
)

// version is the build-stamped release version, initialized from the shared
// internal/version.Version so every kern binary reports the same value.
// It starts as the literal "dev" (not a copy of kversion.Version) because
// the legacy -ldflags "-X main.version=..." only rewrites a variable whose
// initializer is a compile-time constant: a runtime copy from another global
// aliases the read and silently defeats -X. When unstamped, init() adopts
// the shared internal/version.Version (default "dev", or the newer
// "-X github.com/JayveerPrajapati/kern/internal/version.Version=..." form).
var version = "dev"

func init() {
	version = kversion.Adopt(version)
}

func usage() {
	fmt.Fprintf(os.Stderr, `kern - local context, blast-radius & governance engine for AI agents & developers.

Core Workflows (The 5 Essential Verbs):
  kern meta "<request>"                           Natural language intent router (dispatches to any tool)
  kern explore <symbol|file> [--arch] [--json]    Deep symbol/file exploration (source, call flow, blast radius)
  kern search <query> [--repos] [--semantic]      AST symbol & full-text search across codebase
  kern plan <symbol|task> [--json]                Blast-radius impact & surgical refactor planning
  kern mutate | kern rename | kern refactor       Safe AST transformations, renames & test gap sensitivity
  kern verify [types] [--types build,test,sec]    Unified build/test/firewall gate validation (G0-G39)

Context & Token Optimization:
  kern optimize <prompt> [--fewshot] [--mask]     Compress & fine-tune prompts with project memory
  kern fit-context <symbol|file> [--budget N]     Multi-tier adaptive token window compressor
  kern pack [root] [--max-tokens N] [--graph]     Token-dense context bundle for LLMs
  kern compact <file>                             Symbolic signature summary of a file

Agent & Session Setup:
  kern onboard [root]                             Session start: register, index & wire agents
  kern buddy [root]                               Session digest & conventions for incoming agents
  kern setup [--detect] [--global]                Wire kern-first tools into AI agents (MCP/OpenCode/Claude)
  kern doctor [root] [--json]                     Diagnostic report (binary, index, wiring, freshness)

Tip: Run 'kern --all' or 'kern help --all' to list all 140+ specialized micro-commands.
`)
}

func usageAll() {
	fmt.Fprintf(os.Stderr, `kern - kern your context. Complete CLI catalog.

Usage:
  kern optimize <prompt> [--attach FILE] [--session ID] [--model NAME] [--llm MODEL]
  kern preview  <prompt> [--attach FILE]          (dry-run, no stats recorded)
  kern compact <file>                             symbolic summary of a file
  kern project [root]                             compact project map
  kern pack [root] [--max-tokens N] [--out FILE]  single paste-ready file: tree + instructions + contents
  kern pack --graph [--symbol dispatchCommand] [--out FILE]  graph-snapshot pack: adjacency + signatures + fingerprint (~1-5 pct of file tokens)
  kern check [--staged] [--repo DIR] [--source agent|ide|human]  validate staged changes against policy
  kern fix [--repo DIR] [--file FILE] [--content ...]             validate agent-proposed fixes in an isolated worktree
  kern ci [--repo DIR] [--base main] [--head HEAD]                CI change-governance validation (base vs head)
  kern verify-receipt [--repo DIR] [FILE]                       verify a tamper-evident CI receipt (or its CI artifact)
  kern build "<command>" [--dir DIR]              run build, compact output
  kern log <file|->                                 compress a log file
  kern index [root] [--status] [--json] [--force] build/refresh the AST index
  kern index ensure-fresh [root] [--json]        probe freshness, update/rebuild when stale, strictly re-verify
  kern watch [root]                               daemon: auto re-index on change
  kern ast <pattern> [--all]                      AST symbol search (wildcards, kind prefixes; --all: search ALL cached projects)
  kern search <query> [--limit N] [--repos] [--json] [--semantic]
                                  ranked free-text symbol search (--semantic: Ollama re-rank; --repos: across registered repos)
  kern repos (list|add <path> [name]|remove <name>)
                                 multi-repo registry for cross-repo search
  kern graph <symbol> [--mermaid] [--json] [--graphml] [--html] [--out FILE] [--limit N]
                                 definition + callers + what it calls; export as JSON/GraphML/HTML; --html with no symbol renders a whole-repo explorer
  kern inherits <symbol> [root] [--json]           supertypes + subtypes (extends/implements/embeds)
  kern context <symbolRegex> [--lines N]          minimal source slice for a symbol
  kern why <symbol> [--json]                      rationale: doc comment + who depends on it and why
  kern wiki [root] [--out DIR] [--obsidian]       export a markdown wiki (one page per package; --obsidian: wikilinks + frontmatter)
  kern stats [--days N] [--session ID] [--json]
  kern semcache [stats|clear [NS]|list <NS>|sim <A> <B>]   semantic cache inspection (similar query -> instant)
  kern diff [--session ID]                        recent before/after entries
  kern export --csv                               export stats to CSV
  kern tokens [--bpe] "<text>"                    token count (estimator or exact BPE)
  kern setup [--root DIR] [--agents mcp,opencode,claude] [--detect] [--global]   wire kern into agents (idempotent)
  kern setup --check                                    show wiring status
  kern setup --verify                                   spawn the configured kern-mcp and check it answers the MCP initialize handshake
  kern setup --detect                                   auto-detect present agents and wire only those
  kern setup --global                                   wire kern-first instructions globally for all known agents
  kern buddy [root]                               session onboarding digest for any agent
  kern onboard [root]                             ensure repo is registered, indexed and wired (session start)
  kern prompt <template> [--file PATH] [--task TEXT]   fine-tuned prompt template
  kern prompt list                                list templates
  kern remember "<lesson>"                        record a lesson in project memory
  kern memory [--clear]                           show project memory
  kern recall "<prompt>" [root] [--limit N]        recall up-to-N relevant past lessons for a prompt
  kern learn                                      extract recurring patterns from engineering memory
  kern budget "<text>" --max N                    fit text into a token budget
  kern terse "<text>"|-                            compress an LLM's output: strip filler, keep code
  kern exec "<code>" [--lang LANG] [--timeout s] [--max bytes] [--stdin file|-]
                                                run a script in an isolated local runtime
  kern doctor [root] [--json]                     diagnostics report (binary, wiring, index, freshness, LLM)
  kern mask [file|-] [--names a,b,c]              mask secrets/PII locally with [MASKED_*] placeholders
  kern sec [root] [--severity error,warning,info] [--max N] [--json]
                                                security scan: hardcoded secrets, dynamic SQL, command injection
  kern delete <symbol> [root] [--apply] [--json]   safe-delete check: callers, exported/entry point verdict
  kern rename <old> <new> [root] [--apply] [--json]
                                                structural rename (Go, AST-precise) with transactional rollback
  kern flight (list|show <task-id>|tasks|gc) [root] [--json]
                                                replay agent flight records
  kern runtime (status|drift) [root] [--json]    production intelligence: status & drift
  kern docs <query> [root] [--limit N]            local vector search over documents (md/txt/rst)
  kern docs index [root] [--semantic]             pre-index documents
  kern docs fetch <url> [name] [root] [--semantic]  fetch a public doc page into the local index + cache
  kern doc_fetch <url> [--name N] [--root ROOT]     fetch a public doc page into the local index + cache
  kern doc_search <query> [--root ROOT] [--limit N]  local vector search over indexed documents
  kern verify <file|- for stdin> [root] [--json]  cross-check file:line/symbol/route claims in agent output
  kern schema <data.json|-> --schema <schema.json>
                                                deterministically validate JSON output against a JSON schema
  kern prompt <template> --schema <schema.json>  append strict schema formatting block to a rendered prompt
  kern validate [root] [--cmd "custom"] [--timeout s] [--json]
                                                auto-detect and run the project's build/test/syntax check
  kern heal [root] [--llm model] [--task TEXT] [--max N] [--timeout s]
                                                on failure, have the local LLM fix files in a snapshot
  kern udiff <file-a> <file-b> [--out patch]    unified line diff between two files (pure Go, no deps)
  kern sandbox [root] -- <command...> [--timeout s] [--json]
                                                run a risky command in an isolated sandbox snapshot
  kern swap <file|-> [root] [--max N] [--mode summary|expand]
                                                swap tagged code blocks for per-file signatures
  kern precache [root] [--interval s] [--once]  watch daemon: pre-warm code-summary and doc-search caches
  kern fw [root] [--catalog [lang]]               detect frameworks
  kern entry-points [root] [--limit N] [--pattern GLOB]  list framework entry points (handlers, routes)
  kern hook install                               install post-commit diff->memory hook
  kern hook diff [range]                          compressed git diff (default HEAD~1..HEAD)
  kern hook store [range]                         store compressed diff in project memory
  kern changes [root] [--range a..b] [--file F] [--json]   change-impact: blast radius, risk, test gaps
  kern review [root] [--range a..b] [--max N]     token-optimised review context for changed files
  kern hubs [root] [--limit N] [--json]           most depended-on symbols + cross-package bridges
  kern bridges [root] [--limit N] [--json]        cross-package bridge detection (coupling points)
  kern testgaps|test-gaps [root] [--limit N] [--json]   test coverage + untested hotspots
  kern impact <change> [kind] [new-target] [--root ROOT] [--json]
                                                deterministic 11-question ImpactReport (graph-driven, no LLM)
  kern what-if|simulate <change> [kind] [new-target] [--root ROOT] [--json]
                                                simulate a change on the graph
  kern entries [root] [--limit N] [--json]        framework entry points in the index
  kern flows [root] [--limit N] [--json]          execution flows from entry points, by reach
  kern communities [root] [--limit N] [--json] [--full]     call-graph communities (label propagation)
  kern path <from> <to> [root] [--json]           shortest call path between two symbols
  kern dead [root] [--limit N] [--json]           dead code: symbols with no in-project callers
  kern larges [root] [--lines N] [--limit N] [--json]   largest declarations by source lines
  kern arch [root] [--json]                       architecture overview + coupling warnings
  kern twin [root] [--root ROOT]                  digital twin knowledge graph
  kern churn [root] [--range a..b] [--json]       change-frequency risk (most-churned files)
  kern cochange [root] [--range a..b] [--limit N] [--json]   co-change coupling (files edited in lockstep)
  kern explore <symbol> [root] [--depth N] [--max N] [--json]   source + call flow + blast radius in one call
  kern fts "<query>" [root] [--limit N] [--json]  full-text search over the sqlite index
  kern near <symbol> [root] [--depth N] [--max N] [--json]   dependency tree N hops away
  kern probe "<task text>" [root] [--max N] [--json]         task -> budget-capped micro-context bundle
  kern trace <file|- for stdin> [root] [--limit N] [--json]  overlay pprof/stack trace on call graph
  kern lock <scope> [root] [--hold]               acquire a workspace lock
  kern unlock <scope> [root]                     remove a stale lock file
  kern status [root] [--json]                    list workspace locks
  kern approve [id] [--reject --reason "..." --approver "..."]
                                                list/approve/reject pending approvals
  kern audit [task-id] [--root ROOT] [--json]   show the audit trail
  kern evidence export [--root ROOT] [--agent-id ID] [--task T] [--out FILE]
                                                signed, tamper-evident evidence bundle (SHA-256 sealed)
  kern evidence verify [--file FILE] [--root ROOT]
                                                validate a bundle's seal and the repo's audit chain
  kern guard init [root]                         scaffold .kern/boundaries.json
  kern guard check [root] [--file F] [--range a..b] [--json|--sarif] [--threshold N]  reject boundary violations
  kern commitmsg [--staged|--range a..b] [--subject]   deterministic conventional commit message from the diff
  kern commit [--staged] [--all] [--message TEXT] [--dry-run]   stage + commit with a generated conventional message
  kern version                                    show version
  kern guide                                      categorized tool usage guide (performance tiers)
  kern completion <bash|zsh|fish>                 generate shell completion scripts
  kern mcp                                        run MCP server on stdio
  kern meta "<request>"                           single entry point: describe what you need, kern picks the tool
  kern fit-context <symbol|file> [--budget N]     context-adaptive token window compressor
  kern lsp-bridge <symbol|file> [--action def]    zero-weight LSP bridge for compiler types & definitions
  kern fw-trace <symbol|route> [--framework name] framework dependency injection & route tracer
  kern mutate [root] [--threshold N]              lightweight mutation testing for regression sensitivity
  kern fragility [root] [--limit N]               causal defect & fragility hotspot memory
  kern modernize [--root ROOT]                    phased monolith modernization plan
  kern verify <types> [--root ROOT]               unified verification engine
`)
}

func main() {
	cmd, rest := resolveCommandAndFlags()

	// Load prior metrics snapshot from disk so CLI metrics accumulate across
	// invocations (F-46/F-47/F-56). The `stats performance --reset` command
	// clears the persisted file before rendering; all other commands load the
	// prior state on startup and save the updated state on exit.
	metricsPath := cache.Path("metrics.json")
	isStatsPerfReset := cmd == "stats" && len(rest) > 0 && rest[0] == "performance" && hasFlag(rest[1:], "--reset")
	if !isStatsPerfReset {
		_ = metrics.Default().Load(metricsPath) // best-effort; missing file is fine
	}

	// dispatchCommand is wrapped in a func literal that recovers the
	// exitError sentinel panicked by fatal/fatalUsage (and other handler
	// exits routed through it) and converts it back into the exit code. This
	// keeps every exit on the single path below so the metrics snapshot is
	// persisted before os.Exit runs.
	code := func() (c int) {
		defer func() {
			if r := recover(); r != nil {
				if e, ok := r.(exitError); ok {
					c = e.code
					return
				}
				panic(r) // re-panic non-sentinel
			}
		}()
		return dispatchCommand(cmd, rest)
	}()

	// Persist the updated snapshot before exiting. Best-effort: a write
	// failure is non-fatal (metrics are non-critical). This runs explicitly
	// (not via defer) because os.Exit below does not run deferred functions.
	// For `stats performance --reset`, runStatsPerformance handles persistence
	// (Reset + Save) in-place.
	if !isStatsPerfReset {
		_ = os.MkdirAll(cache.Dir(), 0o755)
		_ = metrics.Default().Save(metricsPath)
	}
	os.Exit(code)
}
