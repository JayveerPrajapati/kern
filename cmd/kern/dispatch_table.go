package main

import (
	"fmt"
	bpcli "github.com/JayveerPrajapati/kern/internal/blueprint/cli"
	"strings"
)

// commandEntry binds a subcommand (or alias) to its handler and one-line
// help text. run receives the invoked command name (so alias-shared
// handlers like runOptimize can tell which spelling was used) and the
// remaining arguments.
type commandEntry struct {
	run  func(cmd string, rest []string) int
	help string
}

// commandTable fuses the dispatchCommand switch (E3 refactor) with the
// one-line help map: every subcommand and alias routes through one
// table, so a command can no longer exist in the switch without help
// or vice versa. See dispatchCommand for the lookup.
var commandTable = map[string]commandEntry{
	"version": {run: func(cmd string, rest []string) int {
		runVersion(rest)
		return 0
	}, help: "print version"},
	"--version": {run: func(cmd string, rest []string) int {
		runVersion(rest)
		return 0
	}, help: ""},
	"-v": {run: func(cmd string, rest []string) int {
		runVersion(rest)
		return 0
	}, help: ""},
	"guide": {run: func(cmd string, rest []string) int {
		runGuide(rest)
		return 0
	}, help: "usage guide"},
	"optimize": {run: func(cmd string, rest []string) int {
		runOptimize(cmd, rest)
		return 0
	}, help: "compress a prompt/log/output"},
	"preview": {run: func(cmd string, rest []string) int {
		runOptimize(cmd, rest)
		return 0
	}, help: "compress a prompt/log/output"},
	"compact": {run: func(cmd string, rest []string) int {
		runCompact(rest)
		return 0
	}, help: "symbolic file summary"},
	"project": {run: func(cmd string, rest []string) int {
		runProject(rest)
		return 0
	}, help: "project map"},
	"pack": {run: func(cmd string, rest []string) int {
		runPack(rest)
		return 0
	}, help: "paste-ready project bundle"},
	"build": {run: func(cmd string, rest []string) int {
		runBuild(rest)
		return 0
	}, help: ""},
	"log": {run: func(cmd string, rest []string) int {
		runLog(rest)
		return 0
	}, help: "compress noisy logs"},
	"tokens": {run: func(cmd string, rest []string) int {
		runTokens(rest)
		return 0
	}, help: "token counts"},
	"setup": {run: func(cmd string, rest []string) int {
		runSetup(rest)
		return 0
	}, help: "wire agents/MCP/hooks"},
	"buddy": {run: func(cmd string, rest []string) int {
		runBuddy(rest)
		return 0
	}, help: "session onboarding digest"},
	"onboard": {run: func(cmd string, rest []string) int {
		runOnboard(rest)
		return 0
	}, help: "register+index+wired status for the repo"},
	"skills": {run: func(cmd string, rest []string) int {
		runSkills(rest)
		return 0
	}, help: "list, show, or install agent skills"},
	"prompt": {run: func(cmd string, rest []string) int {
		runPrompt(rest)
		return 0
	}, help: ""},
	"validate": {run: func(cmd string, rest []string) int {
		runValidate(rest)
		return 0
	}, help: "auto-validate"},
	"heal": {run: func(cmd string, rest []string) int {
		runHeal(rest)
		return 0
	}, help: "self-correct failing files"},
	"udiff": {run: func(cmd string, rest []string) int {
		runUdiff(rest)
		return 0
	}, help: "unified diff between files"},
	"sandbox": {run: func(cmd string, rest []string) int {
		runSandbox(rest)
		return 0
	}, help: "run a command with snapshot rollback"},
	"swap": {run: func(cmd string, rest []string) int {
		runSwap(rest)
		return 0
	}, help: "budget-swap fenced code blocks"},
	"precache": {run: func(cmd string, rest []string) int {
		runPrecache(rest)
		return 0
	}, help: "warm caches"},
	"schema": {run: func(cmd string, rest []string) int {
		runSchema(rest)
		return 0
	}, help: "validate JSON against schema"},
	"remember": {run: func(cmd string, rest []string) int {
		runRemember(rest)
		return 0
	}, help: "store a lesson"},
	"memory": {run: func(cmd string, rest []string) int {
		runMemory(rest)
		return 0
	}, help: "engineering memory ops"},
	"recall": {run: func(cmd string, rest []string) int {
		runRecall(rest)
		return 0
	}, help: "recall lessons"},
	"budget": {run: func(cmd string, rest []string) int {
		runBudget(rest)
		return 0
	}, help: "fit text to a token budget"},
	"terse": {run: func(cmd string, rest []string) int {
		runTerse(rest)
		return 0
	}, help: "terser output"},
	"exec": {run: func(cmd string, rest []string) int {
		runExec(rest)
		return 0
	}, help: "run code in an isolated sandbox"},
	"doctor": {run: func(cmd string, rest []string) int {
		runDoctor(rest)
		return 0
	}, help: "self-diagnostics"},
	"calibrate": {run: func(cmd string, rest []string) int {
		runCalibrate(rest)
		return 0
	}, help: "measure how well blast-radius prediction matches git history (F1)"},
	"mask": {run: func(cmd string, rest []string) int {
		runMask(rest)
		return 0
	}, help: "mask secrets/PII"},
	"analyze": {run: func(cmd string, rest []string) int {
		runAnalyze(cmd, rest)
		return 0
	}, help: "analyze a proposed change"},
	"plan": {run: func(cmd string, rest []string) int {
		runAnalyze(cmd, rest)
		return 0
	}, help: "analyze a proposed change"},
	"team": {run: func(cmd string, rest []string) int {
		runTeam(rest)
		return 0
	}, help: ""},
	"workflow": {run: func(cmd string, rest []string) int {
		runWorkflow(rest)
		return 0
	}, help: "agent-team workflow"},
	"ops": {run: func(cmd string, rest []string) int {
		return runOps(rest)
	}, help: "governed autonomous engineering cockpit"},
	"kernops": {run: func(cmd string, rest []string) int {
		return runOps(rest)
	}, help: "governed autonomous engineering cockpit"},
	"loop": {run: func(cmd string, rest []string) int {
		runLoop(cmd, rest)
		return 0
	}, help: "closed autonomy loop"},
	"autonomy": {run: func(cmd string, rest []string) int {
		runLoop(cmd, rest)
		return 0
	}, help: "closed autonomy loop"},
	"risk": {run: func(cmd string, rest []string) int {
		runRisk(rest)
		return 0
	}, help: "change risk"},
	"execute": {run: func(cmd string, rest []string) int {
		runExecute(rest)
		return 0
	}, help: "apply a patch in a sandbox"},
	"incident": {run: func(cmd string, rest []string) int {
		runIncident(rest)
		return 0
	}, help: "incident investigation"},
	"run": {run: func(cmd string, rest []string) int {
		runRun(rest)
		return 0
	}, help: "intent through the task pipeline"},
	"what-if": {run: func(cmd string, rest []string) int {
		runWhatIf(cmd, rest)
		return 0
	}, help: "simulate a change's impact"},
	"simulate": {run: func(cmd string, rest []string) int {
		runWhatIf(cmd, rest)
		return 0
	}, help: "simulate a change's impact"},
	"impact": {run: func(cmd string, rest []string) int {
		runImpact(rest)
		return 0
	}, help: "blast radius of a change"},
	"correlate": {run: func(cmd string, rest []string) int {
		runCorrelate(rest)
		return 0
	}, help: "alert→evidence correlation"},
	"learn": {run: func(cmd string, rest []string) int {
		runLearn(rest)
		return 0
	}, help: "extract recurring patterns"},
	"modernize": {run: func(cmd string, rest []string) int {
		runModernize(rest)
		return 0
	}, help: "monolith modernization plan"},
	"task": {run: func(cmd string, rest []string) int {
		runTask(rest)
		return 0
	}, help: "task lifecycle ops"},
	"efficiency": {run: func(cmd string, rest []string) int {
		runEfficiency(rest)
		return 0
	}, help: "efficiency metrics"},
	"approve": {run: func(cmd string, rest []string) int {
		runApprove(rest)
		return 0
	}, help: "resolve an approval gate"},
	"deploy": {run: func(cmd string, rest []string) int {
		runDeploy(rest)
		return 0
	}, help: "deploy a task (real deploys require approval)"},
	"audit": {run: func(cmd string, rest []string) int {
		runAudit(rest)
		return 0
	}, help: "governance audit log"},
	"evidence": {run: func(cmd string, rest []string) int {
		return runEvidence(rest)
	}, help: "evidence store"},
	"artifacts": {run: func(cmd string, rest []string) int {
		runArtifacts(rest)
		return 0
	}, help: "inspect task artifacts"},
	"verify": {run: func(cmd string, rest []string) int {
		runVerify(rest)
		return 0
	}, help: "verify a change"},
	"check-draft": {run: func(cmd string, rest []string) int {
		runCheckDraft(rest)
		return 0
	}, help: "validate draft code against the index"},
	"taint": {run: func(cmd string, rest []string) int {
		runTaint(rest)
		return 0
	}, help: "taint-lite: flag security sinks reachable from sources"},
	"docs": {run: func(cmd string, rest []string) int {
		runDocs(rest)
		return 0
	}, help: "local doc search"},
	"doc_fetch": {run: func(cmd string, rest []string) int {
		runDocFetch(rest)
		return 0
	}, help: "fetch a doc page into the index"},
	"doc_search": {run: func(cmd string, rest []string) int {
		runDocSearch(rest)
		return 0
	}, help: "search local docs"},
	"check": {run: func(cmd string, rest []string) int {
		return bpcli.RunCheck(rest)
	}, help: "validate staged changes against policy (boundaries, secrets, tests)"},
	"diff-gate": {run: func(cmd string, rest []string) int {
		return bpcli.RunDiffGate(rest)
	}, help: "deterministic diff gate: advisory local checks on the working-tree diff (gofmt, vulnerabilities, schema drift, unsafe exec, changelog, MCP catalog) — --blocking for CI"},
	"fix": {run: func(cmd string, rest []string) int {
		return bpcli.RunFix(rest)
	}, help: "validate agent-proposed fixes in an isolated worktree"},
	"metrics": {run: func(cmd string, rest []string) int {
		return bpcli.RunMetrics(rest)
	}, help: "show local change-governance validation metrics"},
	"request-approval": {run: func(cmd string, rest []string) int {
		return bpcli.RunRequestApproval(rest)
	}, help: "request human approval for a high-risk change (two-person rule)"},
	"reject": {run: func(cmd string, rest []string) int {
		return bpcli.RunApprovalDecision("reject", rest)
	}, help: "reject a pending approval request: reject <id> [--reason ...]"},
	"verify-receipt": {run: func(cmd string, rest []string) int {
		return bpcli.RunVerifyReceipt(rest)
	}, help: "verify a tamper-evident CI receipt"},
	"ci": {run: func(cmd string, rest []string) int {
		return bpcli.RunCI(rest)
	}, help: "CI change-governance validation (base vs head)"},
	"install": {run: func(cmd string, rest []string) int {
		// Blueprint change-governance git hooks (pre-commit/pre-push). The
		// Blueprint CLI lives inside kern (kern check / kern ci / kern sec),
		// so `kern install hook` replaces the standalone `blueprint install hook`.
		return bpcli.RunInstall(rest)
	}, help: "install Blueprint change-governance git hooks (pre-commit/pre-push)"},
	"fw": {run: func(cmd string, rest []string) int {
		runFw(rest)
		return 0
	}, help: "detect frameworks"},
	"frameworks": {run: func(cmd string, rest []string) int {
		runFw(rest)
		return 0
	}, help: "detect frameworks"},
	"entry-points": {run: func(cmd string, rest []string) int {
		runEntryPoints(rest)
		return 0
	}, help: "list framework entry points"},
	"entrypoints": {run: func(cmd string, rest []string) int {
		runEntryPoints(rest)
		return 0
	}, help: "list framework entry points"},
	"hook": {run: func(cmd string, rest []string) int {
		runHook(rest)
		return 0
	}, help: "install hooks"},
	"commitmsg": {run: func(cmd string, rest []string) int {
		runCommitmsg(rest)
		return 0
	}, help: "conventional commit message"},
	"commit": {run: func(cmd string, rest []string) int {
		runCommit(rest)
		return 0
	}, help: "stage+commit"},
	"semcache": {run: func(cmd string, rest []string) int {
		runSemcache(rest)
		return 0
	}, help: "semantic cache stats"},
	"stats": {run: func(cmd string, rest []string) int {
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
				fatal("dispatchCommand: %v", err)
			}
			fmt.Println(out)
			return 0
		}
		runStats(cmd, rest)
		return 0
	}, help: "token savings"},
	"diff": {run: func(cmd string, rest []string) int {
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
				fatal("dispatchCommand: %v", err)
			}
			fmt.Println(out)
			return 0
		}
		runStats(cmd, rest)
		return 0
	}, help: ""},
	"export": {run: func(cmd string, rest []string) int {
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
				fatal("dispatchCommand: %v", err)
			}
			fmt.Println(out)
			return 0
		}
		runStats(cmd, rest)
		return 0
	}, help: ""},
	"mcp": {run: func(cmd string, rest []string) int {
		runMCP(rest)
		return 0
	}, help: "run the MCP server"},
	"lsp": {run: func(cmd string, rest []string) int {
		runLSP(rest)
		return 0
	}, help: "run the LSP server over stdio"},
	"meta": {run: func(cmd string, rest []string) int {
		runMeta(rest)
		return 0
	}, help: "NL request router"},
	"serve": {run: func(cmd string, rest []string) int {
		runServe(rest)
		return 0
	}, help: "run the web console"},
	"org": {run: func(cmd string, rest []string) int {
		runOrg(rest)
		return 0
	}, help: "enterprise org admin (projects/agents/teams/memory/audit/search)"},
	"web": {run: func(cmd string, rest []string) int {
		runServe(rest)
		return 0
	}, help: "run the web console"},
	"index": {run: func(cmd string, rest []string) int {
		runIndex(rest)
		return 0
	}, help: "(re)build the symbol index"},
	"sec": {run: func(cmd string, rest []string) int {
		runSec(rest)
		return 0
	}, help: "security scan"},
	"delete": {run: func(cmd string, rest []string) int {
		runDelete(rest)
		return 0
	}, help: "safe symbol deletion"},
	"rename": {run: func(cmd string, rest []string) int {
		runRename(rest)
		return 0
	}, help: "structural rename"},
	"watch": {run: func(cmd string, rest []string) int {
		runWatch(rest)
		return 0
	}, help: "watch & reindex"},
	"ast": {run: func(cmd string, rest []string) int {
		runAst(rest)
		return 0
	}, help: "AST symbol search"},
	"repos": {run: func(cmd string, rest []string) int {
		runRepos(rest)
		return 0
	}, help: "multi-repo search"},
	"search": {run: func(cmd string, rest []string) int {
		runSearch(rest)
		return 0
	}, help: "ranked symbol search"},
	"graph": {run: func(cmd string, rest []string) int {
		runGraph(rest)
		return 0
	}, help: "call-graph context"},
	"inherits": {run: func(cmd string, rest []string) int {
		runInherits(rest)
		return 0
	}, help: "class hierarchy"},
	"context": {run: func(cmd string, rest []string) int {
		runContext(rest)
		return 0
	}, help: "symbol context slice"},
	"why": {run: func(cmd string, rest []string) int {
		runWhy(rest)
		return 0
	}, help: "rationale/doc report"},
	"wiki": {run: func(cmd string, rest []string) int {
		runWiki(rest)
		return 0
	}, help: "repo digest"},
	"changes": {run: func(cmd string, rest []string) int {
		runChanges(cmd, rest)
		return 0
	}, help: "review context for changed files"},
	"review": {run: func(cmd string, rest []string) int {
		runChanges(cmd, rest)
		return 0
	}, help: "review context for changed files"},
	"hubs": {run: func(cmd string, rest []string) int {
		runHubs(rest)
		return 0
	}, help: "hotspots"},
	"bridges": {run: func(cmd string, rest []string) int {
		runBridges(rest)
		return 0
	}, help: "coupling points"},
	"testgaps": {run: func(cmd string, rest []string) int {
		runTestgaps(rest)
		return 0
	}, help: ""},
	"test-gaps": {run: func(cmd string, rest []string) int {
		runTestgaps(rest)
		return 0
	}, help: ""},
	"flows": {run: func(cmd string, rest []string) int {
		runFlows(rest)
		return 0
	}, help: "call flows"},
	"entries": {run: func(cmd string, rest []string) int {
		runEntries(rest)
		return 0
	}, help: "entry points"},
	"communities": {run: func(cmd string, rest []string) int {
		runCommunities(rest)
		return 0
	}, help: "subsystem clusters"},
	"path": {run: func(cmd string, rest []string) int {
		runPath(rest)
		return 0
	}, help: "shortest call path"},
	"dead": {run: func(cmd string, rest []string) int {
		runDead(rest)
		return 0
	}, help: "dead-code detection"},
	"larges": {run: func(cmd string, rest []string) int {
		runLarges(rest)
		return 0
	}, help: "god functions"},
	"arch": {run: func(cmd string, rest []string) int {
		runArch(rest)
		return 0
	}, help: "architecture overview"},
	"churn": {run: func(cmd string, rest []string) int {
		runChurn(rest)
		return 0
	}, help: "change-frequency risk"},
	"cochange": {run: func(cmd string, rest []string) int {
		runCochange(rest)
		return 0
	}, help: "co-change coupling"},
	"explore": {run: func(cmd string, rest []string) int {
		runExplore(rest)
		return 0
	}, help: "symbol source + blast radius"},
	"fts": {run: func(cmd string, rest []string) int {
		runFts(rest)
		return 0
	}, help: "FTS5 search"},
	"near": {run: func(cmd string, rest []string) int {
		runNear(rest)
		return 0
	}, help: "dependency-tree walk"},
	"walk": {run: func(cmd string, rest []string) int {
		runNear(rest)
		return 0
	}, help: "dependency-tree walk"},
	"probe": {run: func(cmd string, rest []string) int {
		runProbe(rest)
		return 0
	}, help: "task-driven context bundle"},
	"retrieve": {run: func(cmd string, rest []string) int {
		runRetrieve(rest)
		return 0
	}, help: "progressive disclosure retrieval (l1|l2|l3)"},
	"resolve": {run: func(cmd string, rest []string) int {
		runResolve(rest)
		return 0
	}, help: "resolve a retrieval handle to l2|l3 content"},
	"context-envelope": {run: func(cmd string, rest []string) int {
		runContextEnvelope(rest)
		return 0
	}, help: "context envelope as versioned JSON"},
	"explain-context": {run: func(cmd string, rest []string) int {
		runExplainContext(rest)
		return 0
	}, help: "explainable deterministic context plan"},
	"review-pack": {run: func(cmd string, rest []string) int {
		runReviewPack(rest)
		return 0
	}, help: "immutable deterministic review pack (P2-001)"},
	"review-consensus": {run: func(cmd string, rest []string) int {
		runReviewConsensus(rest)
		return 0
	}, help: "normalize review packs into consensus/divergence (P2-002)"},
	"host": {run: func(cmd string, rest []string) int {
		runHost(rest)
		return 0
	}, help: "silent host-adapter context injection (dry-run|check|uninstall)"},
	"trace": {run: func(cmd string, rest []string) int {
		runTrace(rest)
		return 0
	}, help: "runtime-impact overlay"},
	"twin": {run: func(cmd string, rest []string) int {
		runTwin(rest)
		return 0
	}, help: "software twin"},
	"lock": {run: func(cmd string, rest []string) int {
		runLock(rest)
		return 0
	}, help: "acquire workspace lock"},
	"unlock": {run: func(cmd string, rest []string) int {
		runUnlock(rest)
		return 0
	}, help: "release workspace lock"},
	"events": {run: func(cmd string, rest []string) int {
		return runEvents(rest)
	}, help: "serve/watch/emit system events (relay)"},
	"flight": {run: func(cmd string, rest []string) int {
		return runFlight(rest)
	}, help: "replay agent flight records (list|show)"},
	"status": {run: func(cmd string, rest []string) int {
		runStatus(rest)
		return 0
	}, help: "workspace lock status"},
	"runtime": {run: func(cmd string, rest []string) int {
		return runRuntime(rest)
	}, help: "production-intelligence adapter status (discovery wizard)"},
	"guard": {run: func(cmd string, rest []string) int {
		runGuard(rest)
		return 0
	}, help: "architecture guardrails"},
	"fingerprint": {run: func(cmd string, rest []string) int {
		runFingerprint(rest)
		return 0
	}, help: "repo fingerprint"},
	"authorize-context": {run: func(cmd string, rest []string) int {
		runAuthorizeContext(rest)
		return 0
	}, help: "compute authorized context"},
	"do": {run: func(cmd string, rest []string) int {
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
			fatal("dispatchCommand: %v", err)
		}
		fmt.Print(out)
		return 0
	}, help: "autonomous task"},
	"health": {run: func(cmd string, rest []string) int {
		runHealth(rest)
		return 0
	}, help: "MCP server health and index freshness"},
	"compose": {run: func(cmd string, rest []string) int {
		runCompose(rest)
		return 0
	}, help: "multi-tool pipeline runner"},
	"pre_edit": {run: func(cmd string, rest []string) int {
		runPreEdit(rest)
		return 0
	}, help: ""},
	"pre-edit": {run: func(cmd string, rest []string) int {
		runPreEdit(rest)
		return 0
	}, help: "predictive blast-radius and edit risk"},
	"prompt_fill": {run: func(cmd string, rest []string) int {
		runPromptFill(rest)
		return 0
	}, help: ""},
	"prompt-fill": {run: func(cmd string, rest []string) int {
		runPromptFill(rest)
		return 0
	}, help: "dynamic prompt template compilation"},
	"semantic_diff": {run: func(cmd string, rest []string) int {
		runSemanticDiff(rest)
		return 0
	}, help: ""},
	"semantic-diff": {run: func(cmd string, rest []string) int {
		runSemanticDiff(rest)
		return 0
	}, help: "AST functional symbol diff"},
	"evidence_anchor": {run: func(cmd string, rest []string) int {
		runEvidenceAnchor(rest)
		return 0
	}, help: ""},
	"evidence-anchor": {run: func(cmd string, rest []string) int {
		runEvidenceAnchor(rest)
		return 0
	}, help: "verify citations and cryptographic proof"},
	"context_watch": {run: func(cmd string, rest []string) int {
		runContextWatch(rest)
		return 0
	}, help: ""},
	"context-watch": {run: func(cmd string, rest []string) int {
		runContextWatch(rest)
		return 0
	}, help: "context token budget bloat audit"},
	"agent_fingerprint": {run: func(cmd string, rest []string) int {
		runAgentFingerprint(rest)
		return 0
	}, help: ""},
	"agent-fingerprint": {run: func(cmd string, rest []string) int {
		runAgentFingerprint(rest)
		return 0
	}, help: "agent loop and drift detection"},
	"explain": {run: func(cmd string, rest []string) int {
		runExplain(rest)
		return 0
	}, help: "architectural narrative synthesis"},
	"cross_repo_impact": {run: func(cmd string, rest []string) int {
		runCrossRepoImpact(rest)
		return 0
	}, help: ""},
	"cross-repo-impact": {run: func(cmd string, rest []string) int {
		runCrossRepoImpact(rest)
		return 0
	}, help: "cross-repo blast radius"},
	"memory_ranked": {run: func(cmd string, rest []string) int {
		runMemoryRanked(rest)
		return 0
	}, help: ""},
	"memory-ranked": {run: func(cmd string, rest []string) int {
		runMemoryRanked(rest)
		return 0
	}, help: "decay-weighted memory retrieval"},
	"policy_dsl": {run: func(cmd string, rest []string) int {
		runPolicyDSL(rest)
		return 0
	}, help: ""},
	"policy-dsl": {run: func(cmd string, rest []string) int {
		runPolicyDSL(rest)
		return 0
	}, help: "policy-as-code evaluation"},
	"agent_coordination": {run: func(cmd string, rest []string) int {
		runAgentCoordination(rest)
		return 0
	}, help: ""},
	"agent-coordination": {run: func(cmd string, rest []string) int {
		runAgentCoordination(rest)
		return 0
	}, help: "multi-agent handoffs and claims"},
	"agent_role_rbac": {run: func(cmd string, rest []string) int {
		runAgentRoleRBAC(rest)
		return 0
	}, help: ""},
	"agent-role-rbac": {run: func(cmd string, rest []string) int {
		runAgentRoleRBAC(rest)
		return 0
	}, help: "role-based tool access control"},
	"stream": {run: func(cmd string, rest []string) int {
		runStream(rest)
		return 0
	}, help: "chunking and stream progress"},
	"ast_transform": {run: func(cmd string, rest []string) int {
		runAstTransform(rest)
		return 0
	}, help: ""},
	"ast-transform": {run: func(cmd string, rest []string) int {
		runAstTransform(rest)
		return 0
	}, help: "deterministic AST-level transformations and scaffolding"},
	"semantic_merge": {run: func(cmd string, rest []string) int {
		runSemanticMerge(rest)
		return 0
	}, help: ""},
	"semantic-merge": {run: func(cmd string, rest []string) int {
		runSemanticMerge(rest)
		return 0
	}, help: "AST-aware 3-way semantic merge and conflict detection"},
	"synthesize_test": {run: func(cmd string, rest []string) int {
		runSynthesizeTest(rest)
		return 0
	}, help: ""},
	"synthesize-test": {run: func(cmd string, rest []string) int {
		runSynthesizeTest(rest)
		return 0
	}, help: "automatically synthesize table-driven unit tests from AST signatures"},
	"cache": {run: func(cmd string, rest []string) int {
		runCache(rest)
		return 0
	}, help: "cache stats + G-7 maintain (archive/evict)"},
	"config": {run: func(cmd string, rest []string) int {
		runConfig(rest)
		return 0
	}, help: "show effective configuration (env > .kern/config.json > default)"},
}
