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
	if version == "dev" {
		version = kversion.Version
	}
}

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
  kern index [root] [--status] [--json]           build/refresh the AST index; --status reports cached
                                                index health (symbols/files/stale) without rebuilding
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
  kern setup [--root DIR] [--agents mcp,opencode,claude] [--detect] [--global]   wire kern into agents (idempotent); --global also writes kern-first instructions to global agent config (~/AGENTS.md, ~/.claude/CLAUDE.md, ~/.config/opencode/plugins/)
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
                                                 run a script in an isolated local runtime (python3,
                                                 node, go, bash, ...) and return ONLY stdout; shebang
                                                 or --lang selects the runtime; --list shows installed
  kern doctor [root] [--json]                     diagnostics report (binary, wiring, index, freshness, LLM)
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
  kern validate [root] [--cmd "custom"] [--timeout s] [--json]
                                                auto-detect and run the project's build/test/syntax check
  kern heal [root] [--llm model] [--task TEXT] [--max N] [--timeout s]
                                                on failure, have the local LLM fix files in a snapshot,
                                                re-validate there, and show a diff to review (never edits your tree)
  kern optimize <prompt> [--fewshot]           inject top recalled lessons from project memory as baselines
  kern udiff <file-a> <file-b> [--out patch]    unified line diff between two files (pure Go, no deps)
  kern sandbox [root] -- <command...> [--timeout s] [--json]
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
  kern testgaps|test-gaps [root] [--limit N] [--json]   test coverage + untested hotspots
  kern impact <change> [kind] [new-target] [--root ROOT] [--json]
                                                deterministic 11-question ImpactReport (graph-driven, no LLM)
  kern what-if|simulate <change> [kind] [new-target] [--root ROOT] [--json]
                                                simulate a change on the graph; JSON emits the full
                                                impact report (affected, risk, claims, mitigations)
  kern entries [root] [--limit N] [--json]        framework entry points in the index
  kern flows [root] [--limit N] [--json]          execution flows from entry points, by reach
  kern communities [root] [--limit N] [--json] [--full]     call-graph communities (label propagation)
  kern path <from> <to> [root] [--json]           shortest call path between two symbols
  kern dead [root] [--limit N] [--json]           dead code: symbols with no in-project callers
  kern larges [root] [--lines N] [--limit N] [--json]   largest declarations by source lines
  kern arch [root] [--json]                       architecture overview + coupling warnings
kern twin [root] [--root ROOT]                  digital twin knowledge graph: node counts per kind + api endpoints
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
   kern approve [id] [--reject --reason "..." --approver "..."]
                                                 list pending approvals; with an id, approve (or --reject) it
   kern audit [task-id] [--root ROOT] [--json]   show the audit trail (all entries, or for one task)
   kern audit append [--root ROOT] [--file PATH]  link an external entry into the tamper-evident hash chain (entry JSON from stdin or --file)
   kern evidence export [--root ROOT] [--agent-id ID] [--task T] [--out FILE]
                                                 signed, tamper-evident evidence bundle (authorization +
                                                 freshness + lineage + audit trail snapshot, SHA-256 sealed)
                                                 for SOC 2 / ISO 42001 / EU AI Act review; --out "-" = stdout
   kern evidence verify [--file FILE] [--root ROOT]
                                                 validate a bundle's seal and the repo's audit chain;
                                                 exit 0 = valid, 2 = tampered/broken, 1 = parse error
    kern efficiency <id> [--root ROOT]    efficiency report (17.6) for a task
  kern guard init [root]                         scaffold .kern/boundaries.json
   kern guard check [root] [--file F] [--range a..b] [--json|--sarif] [--threshold N]  reject boundary violations (exit 2 when count > N)
   kern fingerprint [root] [--file f1,f2] [--json]   structural fingerprints of Go functions (data command; never a gate)
   kern commitmsg [--staged|--range a..b] [--subject]   deterministic conventional commit message from the diff
   kern commit [--staged] [--all] [--message TEXT] [--dry-run]   stage + commit with a generated conventional message
   kern version                                    show version
  kern guide                                      categorized tool usage guide (performance tiers)
  kern hook <install|diff|store|claude-post|claude-prompt|gemini-after|gemini-prompt>   git hooks (install/diff/store) or agent hooks (read hook JSON on stdin)
  kern mcp                                        run MCP server on stdio
  kern meta "<request>"                           single entry point: describe what you need, kern picks the tool
   kern analyze <change> [--root ROOT]       analyze a proposed change against the whole system
   kern plan <change> [--root ROOT]          implementation plan for a proposed change
   kern modernize [--root ROOT]              phased monolith modernization plan
   kern execute <patch|patch-file> [--root]  apply a diff in an isolated worktree and verify build
   kern verify <types> [--root ROOT]         unified verification engine (types: build,test,security,architecture,dependency; default build,test)
   kern incident <alert-json> [snapshot] [--root]  end-to-end incident investigation
   kern correlate <alert-json> [--root ROOT]       correlate an alert to evidence (alert→service→commit→symbol)
   kern team [--root ROOT]          build the standard specialist team; list roles + task states
   kern artifacts [task-id] [--root ROOT]          inspect task artifacts
   kern loop <intent> [--level L0..L5] [--root]    run the closed loop (default L0 read-only) and show the stage timeline
   kern autonomy <intent> [--level L0..L5]         alias of kern loop
  kern serve [--root PATH] [--addr ADDR] [--enterprise] [--project NAME=PATH]...
                                 start the kern REST API + dashboard server
                                 (single-project, or --enterprise multi-project
                                 with shared org audit/memory/policies)
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
	verify         bool
	detect         bool
	global         bool
	apply          bool
	agents         string
	file           string
	task           string
	agentID        string
	mermaid        bool
	all            bool
	clear          bool
	max            int
	limit          int
	lines          int
	depth          int
	range_         string
	commits        int
	thresholds     string
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
	tier           string
	precision      string
	fold           bool
	staged         bool
	compact        bool
	subject        bool
	message        string
	dryRun         bool
	reset          bool
	name           string
	pattern        string
	full           bool
	generate       bool
	help           bool
	approver       string
	reject         bool
	reason         string
	status         bool
	strict         bool
	addr           string
	enterprise     bool
	projects       []string
	terseCode      bool
}

func parseFlags(args []string) (flags, []string, error) {
	var f flags
	f.days = 7
	f.timeout = 120
	f.depth = -1
	f.commits = 60
	f.thresholds = "2.0,4.0,6.0,8.0"
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
		case "--status":
			f.status = true
		case "--strict":
			f.strict = true
		case "--terse-code", "-terse-code":
			f.terseCode = true
		case "--reset":
			f.reset = true
		case "--sarif":
			f.sarif = true
		case "--threshold":
			i++
			if i < len(args) {
				setInt(&f.threshold, args[i], "--threshold")
			}
		case "--commits":
			i++
			if i < len(args) {
				setInt(&f.commits, args[i], "--commits")
			}
		case "--thresholds":
			i++
			if i < len(args) {
				f.thresholds = args[i]
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
		case "--addr":
			i++
			if i < len(args) {
				f.addr = args[i]
			}
		case "--enterprise":
			f.enterprise = true
		case "--project":
			i++
			if i < len(args) {
				f.projects = append(f.projects, args[i])
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
		case "--verify":
			f.verify = true
		case "--detect":
			f.detect = true
		case "--global":
			f.global = true
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
		case "--agent-id":
			i++
			if i < len(args) {
				f.agentID = args[i]
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
		case "--compact":
			f.compact = true
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
		case "--tier":
			i++
			if i < len(args) {
				f.tier = args[i]
			}
		case "--precision":
			i++
			if i < len(args) {
				f.precision = args[i]
			}
		case "--fold":
			f.fold = true
		case "--generate":
			f.generate = true
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
		case "--approver":
			i++
			if i < len(args) {
				f.approver = args[i]
			}
		case "--reject":
			f.reject = true
		case "--reason":
			i++
			if i < len(args) {
				f.reason = args[i]
			}
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
