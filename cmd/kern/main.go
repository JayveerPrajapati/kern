// Command kern is the local context optimizer: prompt compression, log
// stripping, project mapping, compact build runs and token savings reports.

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/metrics"
)

var version = "dev"

func usage() {
	fmt.Fprintf(os.Stderr, `kern - kern your context. Local-only token optimizer for AI agents.

Usage:
  kern optimize <prompt> [--attach FILE] [--session ID] [--model NAME] [--llm MODEL]
  kern preview  <prompt> [--attach FILE]          (dry-run, no stats recorded)
  kern compact <file>                             symbolic summary of a file
  kern project [root]                             compact project map
  kern pack [root] [--max-tokens N] [--out FILE]  single paste-ready file: tree + instructions + contents
  kern build "<command>" [--dir DIR]              run build, compact output
  kern log <file|->                                 compress a log file
  kern index [root]                               build/refresh the AST index
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
  kern wiki [root] [--out DIR]                    export a markdown wiki (one page per package)
  kern stats [--days N] [--session ID] [--json]
  kern semcache [stats|clear [NS]|list <NS>|sim <A> <B>]   semantic cache inspection (similar query -> instant)
  kern diff [--session ID]                        recent before/after entries
  kern export --csv                               export stats to CSV
  kern tokens [--bpe] "<text>"                    token count (estimator or exact BPE)
  kern setup [--root DIR] [--agents mcp,opencode,claude] [--detect]   wire kern into agents (idempotent)
  kern setup --check                                    show wiring status
  kern setup --detect                                   auto-detect present agents and wire only those
  kern buddy [root]                               session onboarding digest for any agent
  kern prompt <template> [--file PATH] [--task TEXT]   fine-tuned prompt template
  kern prompt list                                list templates
  kern remember "<lesson>"                        record a lesson in project memory
  kern memory [--clear]                           show project memory
  kern recall "<prompt>" [root] [--limit N]        recall up-to-N relevant past lessons for a prompt
   kern budget "<text>" --max N                    fit text into a token budget
   kern terse "<text>"|-                            compress an LLM's output: strip filler, keep code
   kern exec "<code>" [--lang LANG] [--timeout s] [--max bytes] [--stdin file|-]
                                                 run a script in an isolated local runtime (python3,
                                                 node, go, bash, ...) and return ONLY stdout; shebang
                                                 or --lang selects the runtime; --list shows installed
  kern doctor [root]                              diagnostics report
  kern mask [file|-] [--names a,b,c]              mask secrets/PII locally with [MASKED_*] placeholders
  kern sec [root] [--severity error,warning,info] [--max N] [--json]
                                                 security scan: hardcoded secrets, dynamic SQL, command
                                                 injection, weak crypto, unsafe deserialization (exit 1 on errors)
  kern delete <symbol> [root] [--json]           safe-delete check: callers (prod vs test), exported/entry
                                                 point, SAFE/NOT SAFE verdict (exit 1 when unsafe)
  kern rename <old> <new> [root] [--apply] [--json]
                                                 structural rename (Go, AST-precise): previews every
                                                 definition/reference first; --apply commits with backups
                                                 under .kern/rename-backup/ and transactional rollback
   kern docs <query> [root] [--limit N]            local vector search over documents (md/txt/rst)
   kern docs index [root] [--semantic]             pre-index documents; --semantic adds Ollama embeddings;
                                                   kern docs clear resets
   kern docs fetch <url> [name] [root] [--semantic]  fetch a public doc page into the local index + cache
   kern doc_fetch <url> [--name N] [--root ROOT]     fetch a public doc page into the local index + cache
   kern doc_search <query> [--root ROOT] [--limit N]  local vector search over indexed documents
  kern verify <file|- for stdin> [root] [--json]  cross-check file:line/symbol/route claims in agent output
  kern schema <data.json|-> --schema <schema.json>
                                                deterministically validate JSON output against a JSON schema
  kern prompt <template> --schema <schema.json>  append strict schema formatting block to a rendered prompt
  kern validate [root] [--cmd "custom"] [--timeout s]
                                                auto-detect and run the project's build/test/syntax check
  kern heal [root] [--llm model] [--task TEXT] [--max N] [--timeout s]
                                                on failure, have the local LLM fix files in a snapshot,
                                                re-validate there, and show a diff to review (never edits your tree)
  kern optimize <prompt> [--fewshot]           inject top recalled lessons from project memory as baselines
  kern udiff <file-a> <file-b> [--out patch]    unified line diff between two files (pure Go, no deps)
  kern sandbox [root] -- <command...> [--timeout s]
                                                run a risky command; on failure the tree is restored to a
                                                snapshot (success keeps changes)
  kern swap <file|-> [root] [--max N] [--mode summary|expand]
                                                swap tagged code blocks (fenced lang:path blocks) for
                                                per-file signatures to fit a token budget, or expand back
  kern precache [root] [--interval s] [--once]  watch daemon: pre-warm code-summary and doc-search caches
  kern optimize ... [--mask] [--names a,b,c]      also strip secrets from the prompt (restored in output)
  kern optimize ... [--cache]                     serve identical requests from the local response cache
  kern fw [root] [--catalog [lang]]               detect frameworks (default: --catalog shows the built-in list)
  kern frameworks [root]                          alias for kern fw
  kern entry-points [root] [--limit N] [--pattern GLOB]  list framework entry points (handlers, routes)
  kern hook install                               install post-commit diff->memory hook
  kern hook diff [range]                          compressed git diff (default HEAD~1..HEAD)
  kern hook store [range]                         store compressed diff in project memory
  kern changes [root] [--range a..b] [--file F] [--json]   change-impact: blast radius, risk, test gaps
  kern review [root] [--range a..b] [--max N]     token-optimised review context for changed files
  kern hubs [root] [--limit N] [--json]           most depended-on symbols + cross-package bridges
  kern bridges [root] [--limit N] [--json]        cross-package bridge detection (coupling points)
  kern testgaps [root] [--limit N] [--json]       test coverage + untested hotspots
  kern entries [root] [--limit N] [--json]        framework entry points in the index
  kern flows [root] [--limit N] [--json]          execution flows from entry points, by reach
  kern communities [root] [--limit N] [--json] [--full]     call-graph communities (label propagation)
  kern path <from> <to> [root] [--json]           shortest call path between two symbols
  kern dead [root] [--limit N] [--json]           dead code: symbols with no in-project callers
  kern larges [root] [--lines N] [--limit N] [--json]   largest declarations by source lines
  kern arch [root] [--json]                       architecture overview + coupling warnings
  kern churn [root] [--range a..b] [--json]       change-frequency risk (most-churned files)
  kern cochange [root] [--range a..b] [--limit N] [--json]   co-change coupling (files edited in lockstep)
  kern explore <symbol> [root] [--depth N] [--max N] [--json]   source + call flow + blast radius in one call
  kern fts "<query>" [root] [--limit N] [--json]  full-text search over the sqlite index (requires -tags sqlite)
  kern near <symbol> [root] [--depth N] [--max N] [--json]   dependency tree N hops away (walk-graph)
  kern walk <symbol> [root] [--depth N] [--max N]             alias of kern near
  kern probe "<task text>" [root] [--max N] [--json]         task -> budget-capped micro-context bundle
  kern trace <file|- for stdin> [root] [--limit N] [--json]  overlay pprof/stack trace on call graph
  kern lock <scope> [root] [--hold]               acquire a workspace lock (held until interrupted, or --hold for non-blocking; lock is per-process — unlock runs in the same process)
  kern unlock <scope> [root]                     remove a stale lock file
  kern status [root] [--json]                    list workspace locks (held/free)
  kern guard init [root]                         scaffold .kern/boundaries.json
   kern guard check [root] [--file F] [--range a..b] [--json|--sarif] [--threshold N]  reject boundary violations (exit 2 when count > N)
   kern commitmsg [--staged|--range a..b] [--subject]   deterministic conventional commit message from the diff
   kern commit [--staged] [--all] [--message TEXT] [--dry-run]   stage + commit with a generated conventional message
   kern version                                    show version
  kern guide                                      categorized tool usage guide (performance tiers)
  kern hook <install|diff|store|claude-post|claude-prompt|gemini-after|gemini-prompt>   git hooks (install/diff/store) or agent hooks (read hook JSON on stdin)
  kern mcp                                        run MCP server on stdio
   kern analyze <change> [--root ROOT]       analyze a proposed change against the whole system
   kern plan <change> [--root ROOT]          implementation plan for a proposed change
   kern execute <patch|patch-file> [--root]  apply a diff in an isolated worktree and verify build
   kern verify <types> [--root ROOT]         unified verification engine (types: build,test,security,architecture,dependency; default build,test)
   kern incident <alert-json> [snapshot] [--root]  end-to-end incident investigation
   kern team [--root ROOT]          build the standard specialist team; list roles + task states
   kern loop <intent> [--level L0..L5] [--root]    run the closed loop (default L0 read-only) and show the stage timeline
   kern autonomy <intent> [--level L0..L5]         alias of kern loop
`)
}

type flags struct {
	attach         string
	session        string
	model          string
	days           int
	json           bool
	dir            string
	csv            bool
	llm            string
	bpe            bool
	root           string
	level          string
	check          bool
	detect         bool
	apply          bool
	agents         string
	file           string
	task           string
	mermaid        bool
	all            bool
	clear          bool
	max            int
	limit          int
	lines          int
	depth          int
	range_         string
	graphml        bool
	html           bool
	out            string
	repos          bool
	mask           bool
	names          string
	cache          bool
	schema         string
	cmd            string
	timeout        int
	timeoutSet     bool
	fewshot        bool
	mode           string
	once           bool
	interval       int
	http           string
	hold           bool
	sarif          bool
	threshold      int
	severity       string
	semantic       bool
	lang           string
	stdin          string
	noinstructions bool
	maxTokens      int
	maxFiles       int
	staged         bool
	subject        bool
	message        string
	dryRun         bool
	reset          bool
	name           string
	pattern        string
	full           bool
	help           bool
}

func parseFlags(args []string) (flags, []string, error) {
	var f flags
	f.days = 7
	f.timeout = 120
	f.depth = -1
	var rest []string
	var parseErr error
	setInt := func(dst *int, val, flag string) {
		if parseErr != nil {
			return
		}
		n, err := strconv.Atoi(val)
		if err != nil {
			parseErr = fmt.Errorf("%s: invalid integer %q", flag, val)
			return
		}
		*dst = n
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--attach":
			i++
			if i < len(args) {
				f.attach = args[i]
			}
		case "--session":
			i++
			if i < len(args) {
				f.session = args[i]
			}
		case "--model":
			i++
			if i < len(args) {
				f.model = args[i]
			}
		case "--days":
			i++
			if i < len(args) {
				setInt(&f.days, args[i], "--days")
			}
		case "--dir":
			i++
			if i < len(args) {
				f.dir = args[i]
			}
		case "--llm":
			i++
			if i < len(args) {
				f.llm = args[i]
			}
		case "--json":
			f.json = true
		case "--reset":
			f.reset = true
		case "--sarif":
			f.sarif = true
		case "--threshold":
			i++
			if i < len(args) {
				setInt(&f.threshold, args[i], "--threshold")
			}
		case "--csv":
			f.csv = true
		case "--bpe":
			f.bpe = true
		case "--root":
			i++
			if i < len(args) {
				f.root = args[i]
			}
		case "--pattern":
			i++
			if i < len(args) {
				f.pattern = args[i]
			}
		case "--level":
			i++
			if i < len(args) {
				f.level = args[i]
			}
		case "--check":
			f.check = true
		case "--detect":
			f.detect = true
		case "--apply":
			f.apply = true
		case "--agents":
			i++
			if i < len(args) {
				f.agents = args[i]
			}
		case "--file":
			i++
			if i < len(args) {
				f.file = args[i]
			}
		case "--task":
			i++
			if i < len(args) {
				f.task = args[i]
			}
		case "--mermaid":
			f.mermaid = true
		case "--repos":
			f.repos = true
		case "--mask":
			f.mask = true
		case "--names":
			i++
			if i < len(args) {
				f.names = args[i]
			}
		case "--schema":
			i++
			if i < len(args) {
				f.schema = args[i]
			}
		case "--cmd":
			i++
			if i < len(args) {
				f.cmd = args[i]
			}
		case "--timeout":
			i++
			if i < len(args) {
				setInt(&f.timeout, args[i], "--timeout")
				f.timeoutSet = true
			}
		case "--cache":
			f.cache = true
		case "--fewshot":
			f.fewshot = true
		case "--mode":
			i++
			if i < len(args) {
				f.mode = args[i]
			}
		case "--once":
			f.once = true
		case "--semantic":
			f.semantic = true
		case "--interval":
			i++
			if i < len(args) {
				setInt(&f.interval, args[i], "--interval")
			}
		case "--http":
			i++
			if i < len(args) {
				f.http = args[i]
			}
		case "--hold":
			f.hold = true
		case "--graphml":
			f.graphml = true
		case "--html":
			f.html = true
		case "--out":
			i++
			if i < len(args) {
				f.out = args[i]
			}
		case "--all":
			f.all = true
		case "--clear":
			f.clear = true
		case "--max":
			i++
			if i < len(args) {
				setInt(&f.max, args[i], "--max")
			}
		case "--limit":
			i++
			if i < len(args) {
				setInt(&f.limit, args[i], "--limit")
			}
		case "--range":
			i++
			if i < len(args) {
				f.range_ = args[i]
			}
		case "--lines":
			i++
			if i < len(args) {
				setInt(&f.lines, args[i], "--lines")
			}
		case "--depth":
			i++
			if i < len(args) {
				setInt(&f.depth, args[i], "--depth")
			}
		case "--full":
			f.full = true
		case "--severity":
			i++
			if i < len(args) {
				f.severity = args[i]
			}
		case "--lang":
			i++
			if i < len(args) {
				f.lang = args[i]
			}
		case "--stdin":
			i++
			if i < len(args) {
				f.stdin = args[i]
			}
		case "--no-instructions":
			f.noinstructions = true
		case "--max-tokens":
			i++
			if i < len(args) {
				setInt(&f.maxTokens, args[i], "--max-tokens")
			}
		case "--max-files":
			i++
			if i < len(args) {
				setInt(&f.maxFiles, args[i], "--max-files")
			}
		case "--staged":
			f.staged = true
		case "--subject":
			f.subject = true
		case "--message":
			i++
			if i < len(args) {
				f.message = args[i]
			}
		case "--name":
			i++
			if i < len(args) {
				f.name = args[i]
			}
		case "--dry-run":
			f.dryRun = true
		case "--help", "-h":
			f.help = true
		default:
			arg := args[i]
			// A token is treated as a flag if it is `--` followed by more
			// characters (e.g. `--bogus`) or a single `-` followed by a letter
			// (e.g. `-x`). Negative numbers (`-1`), a bare `-` (stdin), and
			// plain positionals are NOT flags and still go to rest.
			isFlag := len(arg) > 2 && strings.HasPrefix(arg, "--") ||
				len(arg) > 1 && arg[0] == '-' && isAlpha(arg[1])
			if isFlag {
				parseErr = fmt.Errorf("unknown flag: %s", arg)
			} else {
				rest = append(rest, arg)
			}
		}
	}
	return f, rest, parseErr
}

// isAlpha reports whether b is an ASCII letter.
func isAlpha(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// hasFlag reports whether the given flag appears in args. Used for the
// `stats performance --reset` early-check in main() before parseFlags runs.
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	rest := os.Args[2:]

	// `--help`/`-h` on any subcommand (or on kern itself) prints the global
	// usage and exits 0 instead of being dispatched to a subcommand handler.
	if cmd == "--help" || cmd == "-h" || hasFlag(rest, "--help") || hasFlag(rest, "-h") {
		usage()
		os.Exit(0)
	}

	// Load prior metrics snapshot from disk so CLI metrics accumulate across
	// invocations (F-46/F-47/F-56). The `stats performance --reset` command
	// clears the persisted file before rendering; all other commands load the
	// prior state on startup and save the updated state on exit.
	metricsPath := cache.Path("metrics.json")
	isStatsPerfReset := cmd == "stats" && len(rest) > 0 && rest[0] == "performance" && hasFlag(rest[1:], "--reset")
	if !isStatsPerfReset {
		_ = metrics.Default().Load(metricsPath) // best-effort; missing file is fine
	}
	// Persist the updated snapshot on exit. Best-effort: a write failure is
	// non-fatal (metrics are non-critical). For `stats performance --reset`,
	// runStatsPerformance handles persistence (Reset + Save) in-place.
	defer func() {
		if !isStatsPerfReset {
			_ = os.MkdirAll(cache.Dir(), 0o755)
			_ = metrics.Default().Save(metricsPath)
		}
	}()

	switch cmd {
	case "version", "--version", "-v":
		runVersion(rest)

	case "guide":
		runGuide(rest)

	case "optimize", "preview":
		runOptimize(cmd, rest)

	case "compact":
		runCompact(rest)

	case "project":
		runProject(rest)

	case "pack":
		runPack(rest)

	case "build":
		runBuild(rest)

	case "log":
		runLog(rest)

	case "tokens":
		runTokens(rest)

	case "setup":
		runSetup(rest)

	case "buddy":
		runBuddy(rest)

	case "prompt":
		runPrompt(rest)

	case "validate":
		runValidate(rest)

	case "heal":
		runHeal(rest)

	case "udiff":
		runUdiff(rest)

	case "sandbox":
		runSandbox(rest)

	case "swap":
		runSwap(rest)

	case "precache":
		runPrecache(rest)

	case "schema":
		runSchema(rest)

	case "remember":
		runRemember(rest)

	case "memory":
		runMemory(rest)

	case "recall":
		runRecall(rest)

	case "budget":
		runBudget(rest)

	case "terse":
		runTerse(rest)

	case "exec":
		runExec(rest)

	case "doctor":
		runDoctor(rest)

	case "mask":
		runMask(rest)

	case "analyze", "plan":
		runAnalyze(cmd, rest)

	case "team":
		runTeam(rest)

	case "loop", "autonomy":
		runLoop(cmd, rest)

	case "risk":
		runRisk(rest)

	case "execute":
		runExecute(rest)

	case "incident":
		runIncident(rest)

	case "what-if", "simulate":
		runWhatIf(cmd, rest)

	case "impact":
		runImpact(rest)

	case "verify":
		runVerify(rest)

	case "docs":
		runDocs(rest)

	case "doc_fetch":
		runDocFetch(rest)

	case "doc_search":
		runDocSearch(rest)

	case "fw", "frameworks":
		runFw(rest)

	case "entry-points", "entrypoints":
		runEntryPoints(rest)

	case "hook":
		runHook(rest)

	case "commitmsg":
		runCommitmsg(rest)

	case "commit":
		runCommit(rest)

	case "semcache":
		runSemcache(rest)

	case "stats", "diff", "export":
		// `kern stats performance` routes to the metrics snapshot (F-41/F-46/
		// F-47/F-56) instead of the token-savings stats. `--reset` clears the
		// process-wide recorder first; `--json` emits the structured snapshot.
		if cmd == "stats" && len(rest) > 0 && rest[0] == "performance" {
			f, _, err := parseFlags(rest[1:])
			if err != nil {
				fatalUsage("flags: %v", err)
			}
			out, err := runStatsPerformance(f.reset, f.json)
			if err != nil {
				fatal("%v", err)
			}
			fmt.Println(out)
			return
		}
		runStats(cmd, rest)

	case "mcp":
		runMCP(rest)

	case "index":
		runIndex(rest)

	case "sec":
		runSec(rest)

	case "delete":
		runDelete(rest)

	case "rename":
		runRename(rest)

	case "watch":
		runWatch(rest)

	case "ast":
		runAst(rest)

	case "repos":
		runRepos(rest)

	case "search":
		runSearch(rest)

	case "graph":
		runGraph(rest)

	case "inherits":
		runInherits(rest)

	case "context":
		runContext(rest)

	case "why":
		runWhy(rest)

	case "wiki":
		runWiki(rest)

	case "changes", "review":
		runChanges(cmd, rest)

	case "hubs":
		runHubs(rest)

	case "bridges":
		runBridges(rest)

	case "testgaps":
		runTestgaps(rest)

	case "flows":
		runFlows(rest)

	case "entries":
		runEntries(rest)

	case "communities":
		runCommunities(rest)

	case "path":
		runPath(rest)

	case "dead":
		runDead(rest)

	case "larges":
		runLarges(rest)

	case "arch":
		runArch(rest)

	case "churn":
		runChurn(rest)

	case "cochange":
		runCochange(rest)

	case "explore":
		runExplore(rest)

	case "fts":
		runFts(rest)

	case "near", "walk":
		runNear(rest)

	case "probe":
		runProbe(rest)

	case "trace":
		runTrace(rest)

	case "lock":
		runLock(rest)

	case "unlock":
		runUnlock(rest)

	case "status":
		runStatus(rest)

	case "guard":
		runGuard(rest)

	case "do":
		// `kern do "<intent>"` — single-entry autonomous coding (F-12/F-36/F-50).
		// Runs the closed loop at L2 (sandbox modifications) with the autonomous
		// coder wired as the default code-stage handler. Optional --level L0..L5
		// overrides the autonomy gate.
		f, dargs, err := parseFlags(rest)
		if err != nil {
			fatalUsage("flags: %v", err)
		}
		intent := strings.Join(dargs, " ")
		if intent == "" {
			if b, berr := readStdin(); berr == nil {
				intent = strings.TrimSpace(string(b))
			}
		}
		if intent == "" {
			fatal("do: intent required (pass as args or stdin)")
		}
		root := f.root
		if root == "" {
			root = "."
		}
		out, err := runDo(root, f.level, intent)
		if err != nil {
			fmt.Print(out)
			fatal("do: %v", err)
		}
		fmt.Print(out)

	default:
		usage()
		os.Exit(2)
	}
}
