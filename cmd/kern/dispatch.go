package main

import (
	"fmt"
	"os"
	"strings"

	bpcli "github.com/JayveerPrajapati/kern/internal/blueprint/cli"
)

// resolveCommandAndFlags extracts the subcommand and its remaining arguments
// from os.Args. It also handles the two pre-dispatch exits: no command at all
// (prints usage, exit 2) and a `--help`/`-h` request (prints usage, exit 0).
func resolveCommandAndFlags() (cmd string, rest []string) {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd = os.Args[1]
	rest = os.Args[2:]

	// `--help`/`-h` on any subcommand (or on kern itself) prints the
	// per-command help and exits 0 instead of being dispatched to a
	// subcommand handler.
	if cmd == "--help" || cmd == "-h" || hasFlag(rest, "--help") || hasFlag(rest, "-h") {
		printCommandHelp(cmd)
	}
	return cmd, rest
}

// commandHelp holds a one-line description per subcommand, used by
// printCommandHelp for `kern <cmd> --help`. Aliases share their primary
// command's description.
var commandHelp = map[string]string{
	"ops":               "governed autonomous engineering cockpit",
	"kernops":           "governed autonomous engineering cockpit",
	"optimize":          "compress a prompt/log/output",
	"preview":           "compress a prompt/log/output",
	"compact":           "symbolic file summary",
	"check":             "validate staged changes against policy (boundaries, secrets, tests)",
	"fix":               "validate agent-proposed fixes in an isolated worktree",
	"ci":                "CI change-governance validation (base vs head)",
	"verify-receipt":    "verify a tamper-evident CI receipt",
	"project":           "project map",
	"pack":              "paste-ready project bundle",
	"log":               "compress noisy logs",
	"tokens":            "token counts",
	"budget":            "fit text to a token budget",
	"terse":             "terser output",
	"swap":              "budget-swap fenced code blocks",
	"semcache":          "semantic cache stats",
	"stats":             "token savings",
	"exec":              "run code in an isolated sandbox",
	"doctor":            "self-diagnostics",
	"cache":             "cache stats + G-7 maintain (archive/evict)",
	"calibrate":         "measure how well blast-radius prediction matches git history (F1)",
	"mask":              "mask secrets/PII",
	"precache":          "warm caches",
	"index":             "(re)build the symbol index",
	"watch":             "watch & reindex",
	"ast":               "AST symbol search",
	"search":            "ranked symbol search",
	"repos":             "multi-repo search",
	"fts":               "FTS5 search",
	"graph":             "call-graph context",
	"inherits":          "class hierarchy",
	"context":           "symbol context slice",
	"why":               "rationale/doc report",
	"wiki":              "repo digest",
	"near":              "dependency-tree walk",
	"walk":              "dependency-tree walk",
	"probe":             "task-driven context bundle",
	"trace":             "runtime-impact overlay",
	"explore":           "symbol source + blast radius",
	"path":              "shortest call path",
	"dead":              "dead-code detection",
	"larges":            "god functions",
	"arch":              "architecture overview",
	"hubs":              "hotspots",
	"bridges":           "coupling points",
	"communities":       "subsystem clusters",
	"churn":             "change-frequency risk",
	"cochange":          "co-change coupling",
	"twin":              "software twin",
	"flows":             "call flows",
	"entries":           "entry points",
	"analyze":           "analyze a proposed change",
	"plan":              "analyze a proposed change",
	"risk":              "change risk",
	"what-if":           "simulate a change's impact",
	"simulate":          "simulate a change's impact",
	"impact":            "blast radius of a change",
	"execute":           "apply a patch in a sandbox",
	"verify":            "verify a change",
	"check-draft":       "validate draft code against the index",
	"taint":             "taint-lite: flag security sinks reachable from sources",
	"changes":           "review context for changed files",
	"review":            "review context for changed files",
	"heal":              "self-correct failing files",
	"udiff":             "unified diff between files",
	"validate":          "auto-validate",
	"sandbox":           "run a command with snapshot rollback",
	"run":               "intent through the task pipeline",
	"loop":              "closed autonomy loop",
	"autonomy":          "closed autonomy loop",
	"workflow":          "agent-team workflow",
	"do":                "autonomous task",
	"incident":          "incident investigation",
	"audit":             "governance audit log",
	"approve":           "resolve an approval gate",
	"evidence":          "evidence store",
	"guard":             "architecture guardrails",
	"fingerprint":       "repo fingerprint",
	"authorize-context": "compute authorized context",
	"lock":              "acquire workspace lock",
	"unlock":            "release workspace lock",
	"events":            "serve/watch/emit system events (relay)",
	"status":            "workspace lock status",
	"sec":               "security scan",
	"delete":            "safe symbol deletion",
	"rename":            "structural rename",
	"schema":            "validate JSON against schema",
	"setup":             "wire agents/MCP/hooks",
	"buddy":             "session onboarding digest",
	"onboard":           "register+index+wired status for the repo",
	"hook":              "install hooks",
	"commitmsg":         "conventional commit message",
	"commit":            "stage+commit",
	"remember":          "store a lesson",
	"memory":            "engineering memory ops",
	"recall":            "recall lessons",
	"learn":             "extract recurring patterns",
	"modernize":         "monolith modernization plan",
	"correlate":         "alert→evidence correlation",
	"task":              "task lifecycle ops",
	"efficiency":        "efficiency metrics",
	"artifacts":         "inspect task artifacts",
	"docs":              "local doc search",
	"doc_fetch":         "fetch a doc page into the index",
	"doc_search":        "search local docs",
	"fw":                "detect frameworks",
	"frameworks":        "detect frameworks",
	"entry-points":      "list framework entry points",
	"entrypoints":       "list framework entry points",
	"guide":             "usage guide",
	"meta":              "NL request router",
	"mcp":               "run the MCP server",
	"lsp":               "run the LSP server over stdio",
	"serve":             "run the web console",
	"web":               "run the web console",
	"version":           "print version",
}

// mcpCLIAlias maps every MCP tool whose CLI command name differs from its
// kern_* suffix to the dispatch case that serves it. Tools whose suffix IS
// a dispatch case (kern_search -> search) need no entry here. The parity
// test TestMCPToolsReachableFromCLI enforces that every registered MCP tool
// is covered either by a same-name dispatch case or by an alias entry, and
// that every alias target is a real dispatch case.
var mcpCLIAlias = map[string]string{
	"kern_agents":            "team",
	"kern_ast_search":        "ast",
	"kern_authorize_context": "authorize-context",
	"kern_code_graph":        "graph",
	"kern_compact_file":      "compact",
	"kern_context_budget":    "budget",
	"kern_diff_files":        "udiff",
	"kern_doc_index":         "docs",
	"kern_entry_points":      "entries",
	"kern_fts_search":        "fts",
	"kern_guard_check":       "guard",
	"kern_lock_status":       "status",
	"kern_mask_pii":          "mask",
	"kern_memory_add":        "remember",
	"kern_memory_list":       "memory",
	"kern_memory_recall":     "recall",
	"kern_optimize_log":      "log",
	"kern_optimize_output":   "terse",
	"kern_optimize_prompt":   "optimize",
	"kern_project_map":       "project",
	"kern_repo_search":       "repos",
	"kern_run_build":         "build",
	"kern_safe_delete":       "delete",
	"kern_schema_validate":   "schema",
	"kern_security":          "sec",
	"kern_test_gaps":         "testgaps",
	"kern_usage_guide":       "guide",
	"kern_verify_output":     "verify",
	"kern_what_if":           "what-if",
	"kern_check_draft":       "check-draft",
	"kern_taint":             "taint",
}

// printCommandHelp prints the one-line help for a subcommand and exits 0.
// Unknown commands (including bare `kern --help`) fall back to the global
// usage text.
func printCommandHelp(cmd string) {
	if desc, ok := commandHelp[cmd]; ok {
		fmt.Printf("kern %s — %s\n", cmd, desc)
		os.Exit(0)
	}
	usage()
	os.Exit(0)
}

// dispatchCommand routes a parsed subcommand to its handler and returns the
// process exit code. main() converts that into a single os.Exit call so every
// command shares the same shutdown path.
func dispatchCommand(cmd string, rest []string) int {
	switch cmd {
	case "version", "--version", "-v":
		runVersion(rest)
		return 0

	case "guide":
		runGuide(rest)
		return 0

	case "optimize", "preview":
		runOptimize(cmd, rest)
		return 0

	case "compact":
		runCompact(rest)
		return 0

	case "project":
		runProject(rest)
		return 0

	case "pack":
		runPack(rest)
		return 0

	case "build":
		runBuild(rest)
		return 0

	case "log":
		runLog(rest)
		return 0

	case "tokens":
		runTokens(rest)
		return 0

	case "setup":
		runSetup(rest)
		return 0

	case "buddy":
		runBuddy(rest)
		return 0

	case "onboard":
		runOnboard(rest)
		return 0

	case "prompt":
		runPrompt(rest)
		return 0

	case "validate":
		runValidate(rest)
		return 0

	case "heal":
		runHeal(rest)
		return 0

	case "udiff":
		runUdiff(rest)
		return 0

	case "sandbox":
		runSandbox(rest)
		return 0

	case "swap":
		runSwap(rest)
		return 0

	case "precache":
		runPrecache(rest)
		return 0

	case "schema":
		runSchema(rest)
		return 0

	case "remember":
		runRemember(rest)
		return 0

	case "memory":
		runMemory(rest)
		return 0

	case "recall":
		runRecall(rest)
		return 0

	case "budget":
		runBudget(rest)
		return 0

	case "terse":
		runTerse(rest)
		return 0

	case "exec":
		runExec(rest)
		return 0

	case "doctor":
		runDoctor(rest)
		return 0

	case "calibrate":
		runCalibrate(rest)
		return 0

	case "mask":
		runMask(rest)
		return 0

	case "analyze", "plan":
		runAnalyze(cmd, rest)
		return 0

	case "team":
		runTeam(rest)
		return 0

	case "workflow":
		runWorkflow(rest)
		return 0

	case "ops", "kernops":
		return runOps(rest)

	case "loop", "autonomy":
		runLoop(cmd, rest)
		return 0

	case "risk":
		runRisk(rest)
		return 0

	case "execute":
		runExecute(rest)
		return 0

	case "incident":
		runIncident(rest)
		return 0

	case "run":
		runRun(rest)
		return 0

	case "what-if", "simulate":
		runWhatIf(cmd, rest)
		return 0

	case "impact":
		runImpact(rest)
		return 0

	case "correlate":
		runCorrelate(rest)
		return 0

	case "learn":
		runLearn(rest)
		return 0

	case "modernize":
		runModernize(rest)
		return 0

	case "task":
		runTask(rest)
		return 0

	case "efficiency":
		runEfficiency(rest)
		return 0

	case "approve":
		runApprove(rest)
		return 0

	case "audit":
		runAudit(rest)
		return 0

	case "evidence":
		return runEvidence(rest)

	case "artifacts":
		runArtifacts(rest)
		return 0

	case "verify":
		runVerify(rest)
		return 0

	case "check-draft":
		runCheckDraft(rest)
		return 0
	case "taint":
		runTaint(rest)
		return 0

	case "docs":
		runDocs(rest)
		return 0

	case "doc_fetch":
		runDocFetch(rest)
		return 0

	case "doc_search":
		runDocSearch(rest)
		return 0

	case "check":
		return bpcli.RunCheck(rest)

	case "fix":
		return bpcli.RunFix(rest)

	case "ci":
		return bpcli.RunCI(rest)

	case "verify-receipt":
		return bpcli.RunVerifyReceipt(rest)

	case "fw", "frameworks":
		runFw(rest)
		return 0

	case "entry-points", "entrypoints":
		runEntryPoints(rest)
		return 0

	case "hook":
		runHook(rest)
		return 0

	case "commitmsg":
		runCommitmsg(rest)
		return 0

	case "commit":
		runCommit(rest)
		return 0

	case "semcache":
		runSemcache(rest)
		return 0

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
			return 0
		}
		runStats(cmd, rest)
		return 0

	case "mcp":
		runMCP(rest)
		return 0
	case "lsp":
		runLSP(rest)
		return 0
	case "meta":
		runMeta(rest)
		return 0

	case "serve", "web":
		runServe(rest)
		return 0

	case "index":
		runIndex(rest)
		return 0

	case "sec":
		runSec(rest)
		return 0

	case "delete":
		runDelete(rest)
		return 0

	case "rename":
		runRename(rest)
		return 0

	case "watch":
		runWatch(rest)
		return 0

	case "ast":
		runAst(rest)
		return 0

	case "repos":
		runRepos(rest)
		return 0

	case "search":
		runSearch(rest)
		return 0

	case "graph":
		runGraph(rest)
		return 0

	case "inherits":
		runInherits(rest)
		return 0

	case "context":
		runContext(rest)
		return 0

	case "why":
		runWhy(rest)
		return 0

	case "wiki":
		runWiki(rest)
		return 0

	case "changes", "review":
		runChanges(cmd, rest)
		return 0

	case "hubs":
		runHubs(rest)
		return 0

	case "bridges":
		runBridges(rest)
		return 0

	case "testgaps", "test-gaps":
		runTestgaps(rest)
		return 0

	case "flows":
		runFlows(rest)
		return 0

	case "entries":
		runEntries(rest)
		return 0

	case "communities":
		runCommunities(rest)
		return 0

	case "path":
		runPath(rest)
		return 0

	case "dead":
		runDead(rest)
		return 0

	case "larges":
		runLarges(rest)
		return 0

	case "arch":
		runArch(rest)
		return 0

	case "churn":
		runChurn(rest)
		return 0

	case "cochange":
		runCochange(rest)
		return 0

	case "explore":
		runExplore(rest)
		return 0

	case "fts":
		runFts(rest)
		return 0

	case "near", "walk":
		runNear(rest)
		return 0

	case "probe":
		runProbe(rest)
		return 0

	case "trace":
		runTrace(rest)
		return 0
	case "twin":
		runTwin(rest)
		return 0
	case "lock":
		runLock(rest)
		return 0

	case "unlock":
		runUnlock(rest)
		return 0

	case "events":
		return runEvents(rest)

	case "status":
		runStatus(rest)
		return 0

	case "guard":
		runGuard(rest)
		return 0

	case "fingerprint":
		runFingerprint(rest)
		return 0

	case "authorize-context":
		runAuthorizeContext(rest)
		return 0

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
		return 0

	case "cache":
		runCache(rest)
		return 0
	default:
		usage()
		return 2
	}
}
