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
	// usage is optional extended help printed after the one-liner when the
	// command has real flags worth documenting (e.g. semantic-merge's
	// --base/--local/--remote). Empty for commands whose one-liner suffices.
	usage string
}

// commandTable fuses the dispatchCommand switch (E3 refactor) with the
// one-line help map: every subcommand and alias routes through one
// table, so a command can no longer exist in the switch without help
// or vice versa. See dispatchCommand for the lookup.
var commandTable = map[string]commandEntry{
	"version": {run: func(cmd string, rest []string) int {
		runVersion(rest)
		return 0
	}, help: "print version", usage: "usage: kern version [flags]"},
	"--version": {run: func(cmd string, rest []string) int {
		runVersion(rest)
		return 0
	}, help: "", usage: "usage: kern --version [flags]  (alias of version)"},
	"-v": {run: func(cmd string, rest []string) int {
		runVersion(rest)
		return 0
	}, help: "", usage: "usage: kern -v [flags]  (alias of version)"},
	"guide": {run: func(cmd string, rest []string) int {
		runGuide(rest)
		return 0
	}, help: "usage guide", usage: "usage: kern guide [flags]"},
	"completion": {run: func(cmd string, rest []string) int {
		return runCompletion(rest)
	}, help: "generate shell completion scripts", usage: "usage: kern completion <bash|zsh|fish>\n  supported shells: bash, zsh, fish"},
	"optimize": {run: func(cmd string, rest []string) int {
		runOptimize(cmd, rest)
		return 0
	}, help: "compress a prompt/log/output", usage: "usage: kern optimize [flags]\n  options:\n    --attach           attach a file\n    --cache            use the semantic cache\n    --fewshot          few-shot mode\n    --llm              LLM backend name\n    --mask             mask PII/secrets in output\n    --model            model name\n    --names            names to mask (comma-separated)\n    --session          session id"},
	"preview": {run: func(cmd string, rest []string) int {
		runOptimize(cmd, rest)
		return 0
	}, help: "compress a prompt/log/output", usage: "usage: kern preview [flags]  (alias of optimize)\n  options:\n    --attach           attach a file\n    --cache            use the semantic cache\n    --fewshot          few-shot mode\n    --llm              LLM backend name\n    --mask             mask PII/secrets in output\n    --model            model name\n    --names            names to mask (comma-separated)\n    --session          session id"},
	"compact": {run: func(cmd string, rest []string) int {
		runCompact(rest)
		return 0
	}, help: "symbolic file summary", usage: "usage: kern compact [--root DIR] <file>\n  options:\n    --root             project root (default: .)\n    --tier             summary|folded|full"},
	"project": {run: func(cmd string, rest []string) int {
		runProject(rest)
		return 0
	}, help: "project map", usage: "usage: kern project [flags]"},
	"pack": {run: func(cmd string, rest []string) int {
		runPack(rest)
		return 0
	}, help: "paste-ready project bundle (--graph: graph-snapshot pack)", usage: "usage: kern pack [flags]\n  options:\n    --fold             fold function bodies\n    --graph            graph mode / graph-snapshot pack\n    --no-instructions  omit generated instructions\n    --out              write output to FILE\n    --tier             summary|folded|full"},
	"build": {run: func(cmd string, rest []string) int {
		runBuild(rest)
		return 0
	}, help: "", usage: "usage: kern build <command>\n  options:\n    --dir              working directory\n    --session          session id"},
	"log": {run: func(cmd string, rest []string) int {
		runLog(rest)
		return 0
	}, help: "compress noisy logs", usage: "usage: kern log [flags]"},
	"tokens": {run: func(cmd string, rest []string) int {
		runTokens(rest)
		return 0
	}, help: "token counts", usage: "usage: kern tokens [--bpe] <text>\n  options:\n    --bpe              count tokens with BPE"},
	"setup": {run: func(cmd string, rest []string) int {
		runSetup(rest)
		return 0
	}, help: "wire agents/MCP/hooks", usage: "usage: kern setup [flags]\n  options:\n    --agents\n    --check            check mode\n    --detect           detect mode\n    --global           apply at global/user scope\n    --root             project root (default: .)\n    --verify           verify mode"},
	"buddy": {run: func(cmd string, rest []string) int {
		runBuddy(rest)
		return 0
	}, help: "session onboarding digest", usage: "usage: kern buddy [flags]"},
	"onboard": {run: func(cmd string, rest []string) int {
		runOnboard(rest)
		return 0
	}, help: "register+index+wired status for the repo", usage: "usage: kern onboard [flags]\n  options:\n    --root             project root (default: .)"},
	"skills": {run: func(cmd string, rest []string) int {
		runSkills(rest)
		return 0
	}, help: "list, show, or install agent skills", usage: "usage: kern skills show <skill-name>"},
	"prompt": {run: func(cmd string, rest []string) int {
		runPrompt(rest)
		return 0
	}, help: "", usage: "usage: kern prompt <template> [--file PATH] [--task TEXT]\n  options:\n    --file             file path\n    --schema           schema file/JSON\n    --task"},
	"validate": {run: func(cmd string, rest []string) int {
		runValidate(rest)
		return 0
	}, help: "auto-validate", usage: "usage: kern validate [flags]\n  options:\n    --cmd              command string\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"validate-proposed": {run: func(cmd string, rest []string) int {
		runValidateProposed(rest)
		return 0
	}, help: "blueprint gate on a proposed (not-on-disk) change", usage: "usage: kern validate-proposed --files <json> [--root ROOT] [--source SRC]\n  options:\n    --files            JSON array of proposed changes [{\"path\",\"content\",\"op\"}]\n    --root             repository root (default: .)\n    --source           agent identity (default: agent)"},
	"explain-finding": {run: func(cmd string, rest []string) int {
		runExplainFinding(rest)
		return 0
	}, help: "plain-language explanation of a blueprint gate finding", usage: "usage: kern explain-finding --finding <json> [--root ROOT]"},
	"repair-guidance": {run: func(cmd string, rest []string) int {
		runRepairGuidance(rest)
		return 0
	}, help: "repair guidance for a blueprint gate finding", usage: "usage: kern repair-guidance --finding <json> [--root ROOT]"},
	"heal": {run: func(cmd string, rest []string) int {
		runHeal(rest)
		return 0
	}, help: "self-correct failing files", usage: "usage: kern heal [flags]\n  options:\n    --force            override the HIGH-risk repair gate\n    --llm              LLM backend name\n    --task"},
	"udiff": {run: func(cmd string, rest []string) int {
		runUdiff(rest)
		return 0
	}, help: "unified diff between files", usage: "usage: kern udiff <file-a> <file-b> [--out out.patch] [--compact]\n  options:\n    --compact          compact output\n    --out              write output to FILE\n    --root             project root (default: .)"},
	"sandbox": {run: func(cmd string, rest []string) int {
		runSandbox(rest)
		return 0
	}, help: "run a command with snapshot rollback", usage: "usage: kern sandbox [root] -- <command...>\n  options:\n    --force            keep changes even when touched files carry a HIGH pre-edit verdict\n    --json             emit JSON output"},
	"swap": {run: func(cmd string, rest []string) int {
		runSwap(rest)
		return 0
	}, help: "budget-swap fenced code blocks", usage: "usage: kern swap [flags]\n  options:\n    --mode             mode selector"},
	"precache": {run: func(cmd string, rest []string) int {
		runPrecache(rest)
		return 0
	}, help: "warm caches", usage: "usage: kern precache [flags]\n  options:\n    --once             single pass, no watch"},
	"schema": {run: func(cmd string, rest []string) int {
		runSchema(rest)
		return 0
	}, help: "validate JSON against schema", usage: "usage: kern schema <data.json|- for stdin> --schema <schema.json>\\n or: kern prompt <template> --schema <schema.json> to inject the schema\n  options:\n    --schema           schema file/JSON"},
	"remember": {run: func(cmd string, rest []string) int {
		runRemember(rest)
		return 0
	}, help: "store a lesson", usage: "usage: kern remember <lesson>"},
	"memory": {run: func(cmd string, rest []string) int {
		runMemory(rest)
		return 0
	}, help: "engineering memory ops", usage: "usage: kern memory add <lesson>\n  options:\n    --clear            clear/empty the store\n    --json             emit JSON output\n    --limit            cap results at N\n    --root             project root (default: .)"},
	"recall": {run: func(cmd string, rest []string) int {
		runRecall(rest)
		return 0
	}, help: "recall lessons", usage: "usage: kern recall \n  options:\n    --limit            cap results at N"},
	"budget": {run: func(cmd string, rest []string) int {
		runBudget(rest)
		return 0
	}, help: "fit text to a token budget", usage: "usage: kern budget \n  options:\n    --max              maximum count/threshold"},
	"terse": {run: func(cmd string, rest []string) int {
		runTerse(rest)
		return 0
	}, help: "terser output", usage: "usage: kern terse \n  options:\n    --max              maximum count/threshold"},
	"exec": {run: func(cmd string, rest []string) int {
		runExec(rest)
		return 0
	}, help: "run code in an isolated sandbox", usage: "usage: kern exec \n  options:\n    --json             emit JSON output\n    --lang             language\n    --list\n    --max              maximum count/threshold\n    --stdin            stdin content\n    --timeout          timeout (seconds or ms per command)"},
	"doctor": {run: func(cmd string, rest []string) int {
		return runDoctor(rest)
	}, help: "self-diagnostics", usage: "usage: kern doctor [flags]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"agents": {run: func(cmd string, rest []string) int {
		runAgents(rest)
		return 0
	}, help: "wired agents + LLM provider priority", usage: "usage: kern agents [--probe] [--json] [--root ROOT]\n  options:\n    --probe            live-test each installed LLM provider (may take minutes)\n    --json             emit JSON\n    --root             project root (default: .)"},
	"calibrate": {run: func(cmd string, rest []string) int {
		runCalibrate(rest)
		return 0
	}, help: "measure how well blast-radius prediction matches git history (F1)", usage: "usage: kern calibrate [flags]\n  options:\n    --root             project root (default: .)\n    --thresholds       threshold values (comma-separated)"},
	"mask": {run: func(cmd string, rest []string) int {
		runMask(rest)
		return 0
	}, help: "mask secrets/PII", usage: "usage: kern mask [flags]\n  options:\n    --names            names to mask (comma-separated)"},
	"analyze": {run: func(cmd string, rest []string) int {
		runAnalyze(cmd, rest)
		return 0
	}, help: "analyze a proposed change", usage: "usage: kern analyze <change> [--root ROOT]\n  options:\n    --lens             analysis lens\n    --profile          profile name\n    --root             project root (default: .)\n    --task"},
	"plan": {run: func(cmd string, rest []string) int {
		runAnalyze(cmd, rest)
		return 0
	}, help: "analyze a proposed change", usage: "usage: kern plan <change> [--root ROOT]\n  options:\n    --lens             analysis lens\n    --profile          profile name\n    --root             project root (default: .)\n    --task"},
	"team": {run: func(cmd string, rest []string) int {
		runTeam(rest)
		return 0
	}, help: "", usage: "usage: kern team [flags]\n  options:\n    --root             project root (default: .)"},
	"workflow": {run: func(cmd string, rest []string) int {
		runWorkflow(rest)
		return 0
	}, help: "agent-team workflow", usage: "usage: kern workflow <intent> [--task TASK_ID] [--root ROOT]\n  options:\n    --root             project root (default: .)\n    --task"},
	"ops": {run: func(cmd string, rest []string) int {
		return runOps(rest)
	}, help: "governed autonomous engineering cockpit", usage: "usage: kern ops [flags]"},
	"kernops": {run: func(cmd string, rest []string) int {
		return runOps(rest)
	}, help: "governed autonomous engineering cockpit", usage: "usage: kern kernops [flags]  (alias of ops)"},
	"loop": {run: func(cmd string, rest []string) int {
		runLoop(cmd, rest)
		return 0
	}, help: "closed autonomy loop", usage: "usage: kern loop <intent> [--level L0..L5] [--root ROOT]\n  options:\n    --level            autonomy level (L0-L5)\n    --root             project root (default: .)"},
	"autonomy": {run: func(cmd string, rest []string) int {
		runLoop(cmd, rest)
		return 0
	}, help: "closed autonomy loop", usage: "usage: kern autonomy <intent> [--level L0..L5] [--root ROOT]\n  options:\n    --level            autonomy level (L0-L5)\n    --root             project root (default: .)"},
	"risk": {run: func(cmd string, rest []string) int {
		runRisk(rest)
		return 0
	}, help: "change risk", usage: "usage: kern risk <change> [--root ROOT]\n  options:\n    --root             project root (default: .)"},
	"execute": {run: func(cmd string, rest []string) int {
		runExecute(rest)
		return 0
	}, help: "apply a patch in a sandbox", usage: "usage: kern execute <patch|patch-file> [--root ROOT]\n  options:\n    --root             project root (default: .)"},
	"incident": {run: func(cmd string, rest []string) int {
		runIncident(rest)
		return 0
	}, help: "incident investigation", usage: "usage: kern incident <alert-json> [snapshot-json] [--root ROOT]\n  options:\n    --root             project root (default: .)"},
	"run": {run: func(cmd string, rest []string) int {
		runRun(rest)
		return 0
	}, help: "intent through the task pipeline", usage: "usage: kern run <intent> [--root ROOT]\n  options:\n    --root             project root (default: .)"},
	"what-if": {run: func(cmd string, rest []string) int {
		runWhatIf(cmd, rest)
		return 0
	}, help: "simulate a change's impact", usage: "usage: kern what-if <change> [kind] [new-target] [--root ROOT]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"simulate": {run: func(cmd string, rest []string) int {
		runWhatIf(cmd, rest)
		return 0
	}, help: "simulate a change's impact", usage: "usage: kern simulate <change> [kind] [new-target] [--root ROOT]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"impact": {run: func(cmd string, rest []string) int {
		runImpact(rest)
		return 0
	}, help: "blast radius of a change", usage: "usage: kern impact <change> [kind] [new-target] [--root ROOT]\n  options:\n    --json             emit JSON output\n    --precision        precision mode\n    --root             project root (default: .)"},
	"correlate": {run: func(cmd string, rest []string) int {
		runCorrelate(rest)
		return 0
	}, help: "alert→evidence correlation", usage: "usage: kern correlate <alert-json> [--root ROOT]\n  options:\n    --root             project root (default: .)"},
	"learn": {run: func(cmd string, rest []string) int {
		runLearn(rest)
		return 0
	}, help: "extract recurring patterns", usage: "usage: kern learn [flags]\n  options:\n    --root             project root (default: .)"},
	"modernize": {run: func(cmd string, rest []string) int {
		runModernize(rest)
		return 0
	}, help: "monolith modernization plan", usage: "usage: kern modernize [flags]\n  options:\n    --root             project root (default: .)"},
	"task": {run: func(cmd string, rest []string) int {
		runTask(rest)
		return 0
	}, help: "task lifecycle ops", usage: "usage: kern task <id> [--root ROOT]\n  options:\n    --root             project root (default: .)"},
	"tasks": {run: func(cmd string, rest []string) int {
		runTasks(rest)
		return 0
	}, help: "list tasks (id/state/intent/updated)", usage: "usage: kern tasks [--root ROOT]\n  options:\n    --root             project root (default: .)"},
	"efficiency": {run: func(cmd string, rest []string) int {
		runEfficiency(rest)
		return 0
	}, help: "efficiency metrics", usage: "usage: kern efficiency <id> [--root ROOT]\n  options:\n    --root             project root (default: .)"},
	"approve": {run: func(cmd string, rest []string) int {
		runApprove(rest)
		return 0
	}, help: "resolve an approval gate", usage: "usage: kern approve [flags]\n  options:\n    --approver         approver identity\n    --reason           reason text\n    --reject           reject instead of approve\n    --root             project root (default: .)"},
	"deploy": {run: func(cmd string, rest []string) int {
		runDeploy(rest)
		return 0
	}, help: "deploy a task (real deploys require approval)", usage: "usage: kern deploy <task-id> [--version V]\n  options:\n    --root             project root (default: .)\n    --version          version string"},
	"audit": {run: func(cmd string, rest []string) int {
		runAudit(rest)
		return 0
	}, help: "governance audit log", usage: "usage: kern audit [flags]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"evidence": {run: func(cmd string, rest []string) int {
		return runEvidence(rest)
	}, help: "evidence store", usage: "usage: kern evidence [flags]"},
	"artifacts": {run: func(cmd string, rest []string) int {
		runArtifacts(rest)
		return 0
	}, help: "inspect task artifacts", usage: "usage: kern artifacts [flags]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"verify": {run: func(cmd string, rest []string) int {
		runVerify(rest)
		return 0
	}, help: "verify a change", usage: "usage: kern verify [<types>|<file|->] [flags]\n  high-level: kern verify [build,test,security,architecture,dependency] [--types X] (default build,test; needs KERN_ALLOW_EXEC=1)\n  claims: kern verify <file|-> [root]\n  options:\n    --types            explicit check types (alias for positional <types>; matches MCP kern_verify)\n    --eval             evaluate a directory of cases\n    --json             emit JSON output\n    --root             project root (default: .)\n    --scan             scan path\n    --skill            skill directory\n    --verify-pipeline  silent-orchestrator pipeline verify\n    --verify-silent    silent verify\n    --verify-token-reduction token-reduction verify"},
	"check-draft": {run: func(cmd string, rest []string) int {
		runCheckDraft(rest)
		return 0
	}, help: "validate draft code against the index", usage: "usage: kern check-draft [flags]\n  options:\n    --lang             language\n    --root             project root (default: .)"},
	"taint": {run: func(cmd string, rest []string) int {
		runTaint(rest)
		return 0
	}, help: "taint-lite: flag security sinks reachable from sources", usage: "usage: kern taint [flags]\n  options:\n    --file             file path\n    --generate         generate a scaffold/test\n    --range            line range a..b\n    --root             project root (default: .)"},
	"docs": {run: func(cmd string, rest []string) int {
		runDocs(rest)
		return 0
	}, help: "local doc search", usage: "usage: kern docs fetch <url> [name] [root]\n  options:\n    --limit            cap results at N\n    --root             project root (default: .)\n    --semantic         semantic matching"},
	"doc_fetch": {run: func(cmd string, rest []string) int {
		runDocFetch(rest)
		return 0
	}, help: "fetch a doc page into the index", usage: "usage: kern doc_fetch <url> [--name N] [--root ROOT]\n  options:\n    --name             name (memory key / item name)\n    --root             project root (default: .)"},
	"doc_search": {run: func(cmd string, rest []string) int {
		runDocSearch(rest)
		return 0
	}, help: "search local docs", usage: "usage: kern doc_search <query> [--root ROOT] [--limit N]\n  options:\n    --limit            cap results at N\n    --root             project root (default: .)"},
	"check": {run: func(cmd string, rest []string) int {
		return bpcli.RunCheck(rest)
	}, help: "validate staged changes against policy (boundaries, secrets, tests)", usage: "usage: kern check [flags]"},
	"diff-gate": {run: func(cmd string, rest []string) int {
		return bpcli.RunDiffGate(rest)
	}, help: "deterministic diff gate: advisory local checks on the working-tree diff (gofmt, vulnerabilities, schema drift, unsafe exec, changelog, MCP catalog) — --blocking for CI", usage: "usage: kern diff-gate [flags]"},
	"fix": {run: func(cmd string, rest []string) int {
		return bpcli.RunFix(rest)
	}, help: "validate agent-proposed fixes in an isolated worktree", usage: "usage: kern fix [flags]"},
	"metrics": {run: func(cmd string, rest []string) int {
		return bpcli.RunMetrics(rest)
	}, help: "show local change-governance validation metrics", usage: "usage: kern metrics [flags]"},
	"request-approval": {run: func(cmd string, rest []string) int {
		return bpcli.RunRequestApproval(rest)
	}, help: "request human approval for a high-risk change (two-person rule)", usage: "usage: kern request-approval [flags]"},
	"reject": {run: func(cmd string, rest []string) int {
		return bpcli.RunApprovalDecision("reject", rest)
	}, help: "reject a pending approval request: reject <id> [--reason ...]", usage: "usage: kern reject [flags]"},
	"verify-receipt": {run: func(cmd string, rest []string) int {
		return bpcli.RunVerifyReceipt(rest)
	}, help: "verify a tamper-evident CI receipt", usage: "usage: kern verify-receipt [flags]"},
	"ci": {run: func(cmd string, rest []string) int {
		return bpcli.RunCI(rest)
	}, help: "CI change-governance validation (base vs head)", usage: "usage: kern ci [flags]"},
	"install": {run: func(cmd string, rest []string) int {
		// Blueprint change-governance git hooks (pre-commit/pre-push). The
		// Blueprint CLI lives inside kern (kern check / kern ci / kern sec),
		// so `kern install hook` replaces the standalone `blueprint install hook`.
		return bpcli.RunInstall(rest)
	}, help: "install Blueprint change-governance git hooks (pre-commit/pre-push)", usage: "usage: kern install [flags]"},
	"fw": {run: func(cmd string, rest []string) int {
		runFw(rest)
		return 0
	}, help: "detect frameworks", usage: "usage: kern fw [flags]"},
	"frameworks": {run: func(cmd string, rest []string) int {
		runFw(rest)
		return 0
	}, help: "detect frameworks", usage: "usage: kern frameworks [flags]  (alias of fw)"},
	"entry-points": {run: func(cmd string, rest []string) int {
		runEntryPoints(rest)
		return 0
	}, help: "list framework entry points", usage: "usage: kern entry-points [flags]\n  options:\n    --pattern          search pattern\n    --root             project root (default: .)"},
	"entrypoints": {run: func(cmd string, rest []string) int {
		runEntryPoints(rest)
		return 0
	}, help: "list framework entry points", usage: "usage: kern entrypoints [flags]  (alias of entry-points)\n  options:\n    --pattern          search pattern\n    --root             project root (default: .)"},
	"hook": {run: func(cmd string, rest []string) int {
		runHook(rest)
		return 0
	}, help: "install hooks", usage: "usage: kern hook <install|diff|store|claude-post|claude-prompt|gemini-after|gemini-prompt> [root] [--range a..b]\n  options:\n    --range            line range a..b"},
	"commitmsg": {run: func(cmd string, rest []string) int {
		runCommitmsg(rest)
		return 0
	}, help: "conventional commit message", usage: "usage: kern commitmsg [flags]\n  options:\n    --range            line range a..b\n    --root             project root (default: .)\n    --staged           operate on staged changes (git diff --cached)\n    --subject          subject line only"},
	"commit": {run: func(cmd string, rest []string) int {
		runCommit(rest)
		return 0
	}, help: "stage+commit", usage: "usage: kern commit [flags]\n  options:\n    --all              include all entries\n    --dry-run          preview without applying\n    --message          message text"},
	"semcache": {run: func(cmd string, rest []string) int {
		runSemcache(rest)
		return 0
	}, help: "semantic cache stats", usage: "usage: kern semcache list <prompt|log>\n  options:\n    --json             emit JSON output"},
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
	}, help: "token savings", usage: "usage: kern stats [flags]"},
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
	}, help: "", usage: "usage: kern diff [flags]  (alias of stats)"},
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
	}, help: "", usage: "usage: kern export [flags]  (alias of stats)"},
	"mcp": {run: func(cmd string, rest []string) int {
		runMCP(rest)
		return 0
	}, help: "run the MCP server", usage: "usage: kern mcp [flags]\n  options:\n    --tls-cert\n    --tls-key"},
	"lsp": {run: func(cmd string, rest []string) int {
		runLSP(rest)
		return 0
	}, help: "run the LSP server over stdio", usage: "usage: kern lsp [flags]\n  options:\n    --root             project root (default: .)"},
	"meta": {run: func(cmd string, rest []string) int {
		runMeta(rest)
		return 0
	}, help: "NL request router", usage: "usage: kern meta"},
	"serve": {run: func(cmd string, rest []string) int {
		runServe(rest)
		return 0
	}, help: "run the web console", usage: "usage: kern serve [flags]\n  options:\n    --addr             listen address\n    --root             project root (default: .)"},
	"org": {run: func(cmd string, rest []string) int {
		runOrg(rest)
		return 0
	}, help: "enterprise org admin (projects/agents/teams/memory/audit/search)", usage: "usage: kern org [flags]\n  options:\n    --project          project name"},
	"web": {run: func(cmd string, rest []string) int {
		runServe(rest)
		return 0
	}, help: "run the web console", usage: "usage: kern web [flags]  (alias of serve)\n  options:\n    --addr             listen address\n    --root             project root (default: .)"},
	"index": {run: func(cmd string, rest []string) int {
		runIndex(rest)
		return 0
	}, help: "(re)build the symbol index; --status reports health; ensure-fresh probes+rebuilds+re-verifies in one subprocess", usage: "usage: kern index [flags]\n  options:\n    --force            force overwrite\n    --json             emit JSON output\n    --root             project root (default: .)\n    --status           status mode\n    --strict           strict mode\n    --update           update mode"},
	"sec": {run: func(cmd string, rest []string) int {
		runSec(rest)
		return 0
	}, help: "security scan", usage: "usage: kern sec [flags]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)\n    --severity         severity filter (comma-separated, default error)"},
	"delete": {run: func(cmd string, rest []string) int {
		runDelete(rest)
		return 0
	}, help: "safe symbol deletion", usage: "usage: kern delete <symbol> [root] [--apply] [--json]\n  options:\n    --apply            apply the change (HIGH pre-edit verdict blocks without --force)\n    --force            override the HIGH-risk mutation gate\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"rename": {run: func(cmd string, rest []string) int {
		runRename(rest)
		return 0
	}, help: "structural rename", usage: "usage: kern rename <old> <new> [root] [--apply] [--json]\n  options:\n    --apply            apply the change (HIGH pre-edit verdict blocks without --force)\n    --force            override the HIGH-risk mutation gate\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"watch": {run: func(cmd string, rest []string) int {
		runWatch(rest)
		return 0
	}, help: "watch & reindex", usage: "usage: kern watch [flags]"},
	"ast": {run: func(cmd string, rest []string) int {
		runAst(rest)
		return 0
	}, help: "AST symbol search", usage: "usage: kern ast <pattern> [root] [--all]\n  options:\n    --all              include all entries\n    --root             project root (default: .)"},
	"repos": {run: func(cmd string, rest []string) int {
		runRepos(rest)
		return 0
	}, help: "multi-repo search", usage: "usage: kern repos (list|add <path> [name]|search <query>|remove <name>)"},
	"search": {run: func(cmd string, rest []string) int {
		runSearch(rest)
		return 0
	}, help: "ranked symbol search", usage: "usage: kern search <query> [root] [--limit N] [--repos] [--json] [--semantic]\n  options:\n    --json             emit JSON output\n    --limit            cap results at N\n    --repos            scan across repositories\n    --root             project root (default: .)\n    --semantic         semantic matching"},
	"prose": {run: func(cmd string, rest []string) int {
		runProse(rest)
		return 0
	}, help: "map prose <words> to symbol candidates", usage: "usage: kern prose \"<words>\" [root] [--limit N]\n  options:\n    --limit            cap results at N\n    --root             project root (default: .)"},
	"graph": {run: func(cmd string, rest []string) int {
		runGraph(rest)
		return 0
	}, help: "call-graph context", usage: "usage: kern graph <symbol> [root] [--mermaid] [--json] [--graphml] [--cypher] [--html] [--out FILE] [--max-tokens N] [--limit N]\n  options:\n    --cypher\n    --graphml\n    --html\n    --json             emit JSON output\n    --limit            cap results at N\n    --max-tokens       token cap\n    --mermaid          Mermaid output\n    --min-confidence   minimum confidence\n    --out              write output to FILE\n    --root             project root (default: .)"},
	"inherits": {run: func(cmd string, rest []string) int {
		runInherits(rest)
		return 0
	}, help: "class hierarchy", usage: "usage: kern inherits <symbol> [root] [--json]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"context": {run: func(cmd string, rest []string) int {
		runContext(rest)
		return 0
	}, help: "symbol context slice", usage: "usage: kern context <symbol> [root] [--lines N]\n  options:\n    --lens             analysis lens\n    --lines            context line count\n    --profile          profile name\n    --root             project root (default: .)"},
	"why": {run: func(cmd string, rest []string) int {
		runWhy(rest)
		return 0
	}, help: "rationale/doc report", usage: "usage: kern why <symbol> [root] [--json]\n  options:\n    --json             emit JSON output\n    --min-confidence   minimum confidence\n    --root             project root (default: .)"},
	"wiki": {run: func(cmd string, rest []string) int {
		runWiki(rest)
		return 0
	}, help: "repo digest", usage: "usage: kern wiki [flags]\n  options:\n    --out              write output to FILE\n    --root             project root (default: .)"},
	"changes": {run: func(cmd string, rest []string) int {
		runChanges(cmd, rest)
		return 0
	}, help: "review context for changed files", usage: "usage: kern changes [flags]\n  options:\n    --file             file path\n    --json             emit JSON output\n    --lens             analysis lens\n    --profile          profile name\n    --range            line range a..b\n    --root             project root (default: .)\n    --runtime"},
	"review": {run: func(cmd string, rest []string) int {
		runChanges(cmd, rest)
		return 0
	}, help: "review context for changed files", usage: "usage: kern review [flags]  (alias of changes)\n  options:\n    --file             file path\n    --json             emit JSON output\n    --lens             analysis lens\n    --profile          profile name\n    --range            line range a..b\n    --root             project root (default: .)\n    --runtime"},
	"hubs": {run: func(cmd string, rest []string) int {
		runHubs(rest)
		return 0
	}, help: "hotspots", usage: "usage: kern hubs [flags]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"bridges": {run: func(cmd string, rest []string) int {
		runBridges(rest)
		return 0
	}, help: "coupling points", usage: "usage: kern bridges [flags]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"testgaps": {run: func(cmd string, rest []string) int {
		runTestgaps(rest)
		return 0
	}, help: "", usage: "usage: kern testgaps [flags]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"test-gaps": {run: func(cmd string, rest []string) int {
		runTestgaps(rest)
		return 0
	}, help: "", usage: "usage: kern test-gaps [flags]  (alias of testgaps)\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"flows": {run: func(cmd string, rest []string) int {
		runFlows(rest)
		return 0
	}, help: "call flows", usage: "usage: kern flows [flags]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"entries": {run: func(cmd string, rest []string) int {
		runEntries(rest)
		return 0
	}, help: "entry points", usage: "usage: kern entries [flags]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"communities": {run: func(cmd string, rest []string) int {
		runCommunities(rest)
		return 0
	}, help: "subsystem clusters", usage: "usage: kern communities [flags]\n  options:\n    --full             full (unfolded) output\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"path": {run: func(cmd string, rest []string) int {
		runPath(rest)
		return 0
	}, help: "shortest call path", usage: "usage: kern path <from-symbol> <to-symbol> [root] (or --from S --to S)\n  options:\n    --from             git range start (ref/sha)\n    --json             emit JSON output\n    --min-confidence   minimum confidence\n    --root             project root (default: .)\n    --to               git range end (ref/sha)"},
	"dead": {run: func(cmd string, rest []string) int {
		runDead(rest)
		return 0
	}, help: "dead-code detection", usage: "usage: kern dead [flags]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"surprising": {run: func(cmd string, rest []string) int {
		runSurprising(rest)
		return 0
	}, help: "cross-community call edges ranked by community distance x rarity", usage: "usage: kern surprising [flags]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"cycles": {run: func(cmd string, rest []string) int {
		runCycles(rest)
		return 0
	}, help: "package-level import cycles (Tarjan SCC)", usage: "usage: kern cycles [flags]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"snapshot": {run: func(cmd string, rest []string) int {
		if len(rest) > 0 && rest[0] == "verify" {
			return runSnapshotVerify(rest[1:])
		}
		return runSnapshot(rest)
	}, help: "canonical graph snapshot for multi-agent share", usage: "usage: kern snapshot [root] [--out FILE] [--symbol X] [--limit N] [--verify <file>] [--strict]\n  options:\n    --out FILE      write the snapshot JSON to FILE (default: stdout)\n    --symbol X      snapshot only the neighbourhood of symbol X\n    --limit N       cap nodes/edges in the snapshot\n    --verify FILE   load FILE and verify it against the current repo (fresh/stale/unknown)\n    --strict        force a content walk during verification\n    --root DIR      project root (default: .)"},
	"larges": {run: func(cmd string, rest []string) int {
		runLarges(rest)
		return 0
	}, help: "god functions", usage: "usage: kern larges [flags]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"arch": {run: func(cmd string, rest []string) int {
		runArch(rest)
		return 0
	}, help: "architecture overview", usage: "usage: kern arch [flags]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"churn": {run: func(cmd string, rest []string) int {
		runChurn(rest)
		return 0
	}, help: "change-frequency risk", usage: "usage: kern churn [flags]\n  options:\n    --json             emit JSON output\n    --range            line range a..b\n    --root             project root (default: .)"},
	"cochange": {run: func(cmd string, rest []string) int {
		runCochange(rest)
		return 0
	}, help: "co-change coupling", usage: "usage: kern cochange [flags]\n  options:\n    --json             emit JSON output\n    --range            line range a..b\n    --root             project root (default: .)"},
	"explore": {run: func(cmd string, rest []string) int {
		runExplore(rest)
		return 0
	}, help: "symbol source + blast radius", usage: "usage: kern explore <symbol> [root] [--depth N] [--max N] [--explain]\n  options:\n    --depth            traversal depth (default 2, 0 = uncapped)\n    --explain          append the why-rationale section (what it is, who depends on it and why)\n    --json             emit JSON output\n    --max              maximum count/threshold (default 30)\n    --min-confidence   minimum confidence\n    --root             project root (default: .)"},
	"fts": {run: func(cmd string, rest []string) int {
		runFts(rest)
		return 0
	}, help: "FTS5 search", usage: "usage: kern fts \n  options:\n    --json             emit JSON output\n    --limit            cap results at N\n    --root             project root (default: .)"},
	"near": {run: func(cmd string, rest []string) int {
		runNear(rest)
		return 0
	}, help: "dependency-tree walk", usage: "usage: kern near <symbol> [root] [--depth N] [--max N]\n  options:\n    --depth            traversal depth\n    --json             emit JSON output\n    --max              maximum count/threshold\n    --root             project root (default: .)"},
	"walk": {run: func(cmd string, rest []string) int {
		runNear(rest)
		return 0
	}, help: "dependency-tree walk", usage: "usage: kern near <symbol> [root] [--depth N] [--max N]\n  options:\n    --depth            traversal depth\n    --json             emit JSON output\n    --max              maximum count/threshold\n    --root             project root (default: .)"},
	"probe": {run: func(cmd string, rest []string) int {
		runProbe(rest)
		return 0
	}, help: "task-driven context bundle", usage: "usage: kern probe \n  options:\n    --json             emit JSON output\n    --max              maximum count/threshold\n    --min-confidence   minimum confidence"},
	"retrieve": {run: func(cmd string, rest []string) int {
		runRetrieve(rest)
		return 0
	}, help: "progressive disclosure retrieval (l1|l2|l3)", usage: "usage: kern retrieve --task-type <type> --symbol <name> [root] [--max-tokens N]\n  options:\n    --depth            traversal depth\n    --json             emit JSON output\n    --level            autonomy level (L0-L5)\n    --limit            cap results at N\n    --lines            context line count\n    --max              maximum count/threshold\n    --max-tokens       token cap\n    --query            search query\n    --root             project root (default: .)\n    --symbol           target symbol name\n    --task-type"},
	"resolve": {run: func(cmd string, rest []string) int {
		runResolve(rest)
		return 0
	}, help: "resolve a retrieval handle to l2|l3 content", usage: "usage: kern resolve <handle-id> [root] [--level l2|l3] [--max-tokens N]\n  options:\n    --json             emit JSON output\n    --level            autonomy level (L0-L5)\n    --max-tokens       token cap\n    --root             project root (default: .)"},
	"context-envelope": {run: func(cmd string, rest []string) int {
		runContextEnvelope(rest)
		return 0
	}, help: "context envelope as versioned JSON", usage: "usage: kern context-envelope --change \n  options:\n    --change\n    --max-tokens       token cap\n    --root             project root (default: .)"},
	"explain-context": {run: func(cmd string, rest []string) int {
		runExplainContext(rest)
		return 0
	}, help: "explainable deterministic context plan", usage: "usage: kern explain-context --task \n  options:\n    --budget           token budget\n    --json             emit JSON output\n    --root             project root (default: .)\n    --task"},
	"orchestrate": {run: func(cmd string, rest []string) int {
		runOrchestrate(rest)
		return 0
	}, help: "silent context pipeline (classify -> plan -> evidence -> budget -> envelope)", usage: "usage: kern orchestrate \n  options:\n    --change\n    --max-tokens       token cap\n    --mode             mode selector\n    --root             project root (default: .)\n    --with-skill       attach a skill by name"},
	"eval": {run: func(cmd string, rest []string) int {
		runEval(rest)
		return 0
	}, help: "context-quality evaluation: run/compare/report", usage: "usage: kern eval <run|compare|report> [DIR] [--root ROOT] [--max-tokens N] [--mode MODE]\n  options:\n    --max-tokens       token cap\n    --mode             mode selector\n    --root             project root (default: .)"},
	"gen-catalog": {run: func(cmd string, rest []string) int {
		runGenCatalog(rest)
		return 0
	}, help: "regenerate docs/tool-catalog.md from the live MCP catalog", usage: "usage: kern gen-catalog [--root <dir>]\n  options:\n    --root             project root (default: .)"},
	"note": {run: func(cmd string, rest []string) int {
		runNote(rest)
		return 0
	}, help: "governed decision records (new/list/status/validate)", usage: "usage: kern note [flags]"},
	"agent": {run: func(cmd string, rest []string) int {
		runAgent(rest)
		return 0
	}, help: "agent control surfaces (message/interrupt are MCP-tool surfaces)", usage: "usage: kern agent <message|interrupt> [args] — agent control is an MCP-tool surface; use the kern_agent_message / kern_agent_interrupt MCP tools, or the CLI mirrors 'kern agent-message' / 'kern agent-interrupt'"},
	"agent-message": {run: func(cmd string, rest []string) int {
		runAgentMessage(rest)
		return 0
	}, help: "send a message to an agent's coordination inbox", usage: "usage: kern agent-message --to <agent> [--from <agent>] [--task <id>] \n  options:\n    --from             git range start (ref/sha)\n    --task\n    --to               git range end (ref/sha)"},
	"agent-interrupt": {run: func(cmd string, rest []string) int {
		runAgentInterrupt(rest)
		return 0
	}, help: "cancel a running task through the TaskService", usage: "usage: kern agent-interrupt <task-id> [reason words...]"},
	"mcp-client": {run: func(cmd string, rest []string) int {
		runMcpClient(rest)
		return 0
	}, help: "external MCP servers: add/list/rm/call", usage: "usage: kern mcp-client [flags]"},
	"review-pack": {run: func(cmd string, rest []string) int {
		runReviewPack(rest)
		return 0
	}, help: "immutable deterministic review pack (P2-001)", usage: "usage: kern review-pack [flags]"},
	"review-consensus": {run: func(cmd string, rest []string) int {
		runReviewConsensus(rest)
		return 0
	}, help: "normalize review packs into consensus/divergence (P2-002)", usage: "usage: kern review-consensus [flags]"},
	"host": {run: func(cmd string, rest []string) int {
		runHost(rest)
		return 0
	}, help: "silent host-adapter context injection (dry-run|check|uninstall)", usage: "usage: kern host [flags]\n  options:\n    --check            check mode\n    --dry-run          preview without applying\n    --root             project root (default: .)\n    --task\n    --uninstall        uninstall"},
	"trace": {run: func(cmd string, rest []string) int {
		runTrace(rest)
		return 0
	}, help: "runtime-impact overlay", usage: "usage: kern trace <file|- for stdin> [root] [--limit N]\n  options:\n    --json             emit JSON output\n    --limit            cap results at N"},
	"twin": {run: func(cmd string, rest []string) int {
		runTwin(rest)
		return 0
	}, help: "software twin", usage: "usage: kern twin [flags]\n  options:\n    --root             project root (default: .)"},
	"lock": {run: func(cmd string, rest []string) int {
		runLock(rest)
		return 0
	}, help: "acquire workspace lock", usage: "usage: kern lock <scope> [root]\n  options:\n    --hold             hold the gate open\n    --root             project root (default: .)"},
	"unlock": {run: func(cmd string, rest []string) int {
		runUnlock(rest)
		return 0
	}, help: "release workspace lock", usage: "usage: kern unlock <scope> [root]\n  options:\n    --root             project root (default: .)"},
	"events": {run: func(cmd string, rest []string) int {
		return runEvents(rest)
	}, help: "serve/watch/emit system events (relay)", usage: "usage: kern events [flags]"},
	"flight": {run: func(cmd string, rest []string) int {
		return runFlight(rest)
	}, help: "replay agent flight records (list|show)", usage: "usage: kern flight [flags]"},
	"status": {run: func(cmd string, rest []string) int {
		runStatus(rest)
		return 0
	}, help: "workspace lock status", usage: "usage: kern status [flags]\n  options:\n    --json             emit JSON output"},
	"runtime": {run: func(cmd string, rest []string) int {
		return runRuntime(rest)
	}, help: "production-intelligence adapter status (discovery wizard)", usage: "usage: kern runtime [flags]"},
	"guard": {run: func(cmd string, rest []string) int {
		runGuard(rest)
		return 0
	}, help: "architecture guardrails", usage: "usage: kern guard <check|init> [root] [--file f1,f2] [--range a..b] [--json|--sarif] [--threshold N] [--precision default|strict] [--agent-id ID --task DESC]\n  options:\n    --agent-id         agent identity\n    --file             file path\n    --json             emit JSON output\n    --precision        precision mode\n    --range            line range a..b\n    --sarif            SARIF 2.1.0 report\n    --task\n    --threshold        score threshold"},
	"fingerprint": {run: func(cmd string, rest []string) int {
		runFingerprint(rest)
		return 0
	}, help: "repo fingerprint", usage: "usage: kern fingerprint [flags]\n  options:\n    --file             file path\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"authorize-context": {run: func(cmd string, rest []string) int {
		runAuthorizeContext(rest)
		return 0
	}, help: "compute authorized context", usage: "usage: kern authorize-context [flags]"},
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
	}, help: "autonomous task", usage: "usage: kern do [flags]"},
	"health": {run: func(cmd string, rest []string) int {
		runHealth(rest)
		return 0
	}, help: "MCP server health and index freshness", usage: "usage: kern health [flags]"},
	"compose": {run: func(cmd string, rest []string) int {
		runCompose(rest)
		return 0
	}, help: "multi-tool pipeline runner", usage: "usage: kern compose --pipeline '[{\"tool\": \"kern_search\", \"args\": {\"query\": \"Index.Search\"}}]'\n  options:\n    --pipeline JSON  deterministic multi-tool pipeline spec with variable interpolation"},
	"pre_edit": {run: func(cmd string, rest []string) int {
		runPreEdit(rest)
		return 0
	}, help: "", usage: "usage: kern pre_edit [flags]"},
	"pre-edit": {run: func(cmd string, rest []string) int {
		runPreEdit(rest)
		return 0
	}, help: "predictive blast-radius and edit risk", usage: "usage: kern pre-edit [flags]  (alias of pre_edit)"},
	"prompt_fill": {run: func(cmd string, rest []string) int {
		runPromptFill(rest)
		return 0
	}, help: "", usage: "usage: kern prompt-fill --template <name> [--task <desc>] [--file <path>]\n  options:\n    --file             file path\n    --task\n    --template"},
	"prompt-fill": {run: func(cmd string, rest []string) int {
		runPromptFill(rest)
		return 0
	}, help: "dynamic prompt template compilation", usage: "usage: kern prompt-fill --template <name> [--task <desc>] [--file <path>]\n  options:\n    --file             file path\n    --task\n    --template"},
	"semantic_diff": {run: func(cmd string, rest []string) int {
		runSemanticDiff(rest)
		return 0
	}, help: "", usage: "usage: kern semantic_diff [flags]"},
	"semantic-diff": {run: func(cmd string, rest []string) int {
		runSemanticDiff(rest)
		return 0
	}, help: "AST functional symbol diff", usage: "usage: kern semantic-diff [flags]  (alias of semantic_diff)"},
	"evidence_anchor": {run: func(cmd string, rest []string) int {
		runEvidenceAnchor(rest)
		return 0
	}, help: "", usage: "usage: kern evidence_anchor [flags]"},
	"evidence-anchor": {run: func(cmd string, rest []string) int {
		runEvidenceAnchor(rest)
		return 0
	}, help: "verify citations and cryptographic proof", usage: "usage: kern evidence-anchor [flags]  (alias of evidence_anchor)"},
	"context_watch": {run: func(cmd string, rest []string) int {
		runContextWatch(rest)
		return 0
	}, help: "", usage: "usage: kern context-watch [--budget NUM] [--format text|json] <text>\n  options:\n    --budget           token budget\n    --format           output format: json|terminal"},
	"context-watch": {run: func(cmd string, rest []string) int {
		runContextWatch(rest)
		return 0
	}, help: "context token budget bloat audit", usage: "usage: kern context-watch [--budget NUM] [--format text|json] <text>\n  options:\n    --budget           token budget\n    --format           output format: json|terminal"},
	"agent_fingerprint": {run: func(cmd string, rest []string) int {
		runAgentFingerprint(rest)
		return 0
	}, help: "", usage: "usage: kern agent_fingerprint [flags]"},
	"agent-fingerprint": {run: func(cmd string, rest []string) int {
		runAgentFingerprint(rest)
		return 0
	}, help: "agent loop and drift detection", usage: "usage: kern agent-fingerprint [flags]  (alias of agent_fingerprint)"},
	"explain": {run: func(cmd string, rest []string) int {
		runExplain(rest)
		return 0
	}, help: "architectural narrative synthesis", usage: "usage: kern explain <target-symbol-or-file> [--root DIR]\n  options:\n    --root             project root (default: .)"},
	"cross_repo_impact": {run: func(cmd string, rest []string) int {
		runCrossRepoImpact(rest)
		return 0
	}, help: "", usage: "usage: kern cross-repo-impact <symbol> [--repo <path>]... [--root DIR]\n  options:\n    --repo             repository root (default: current directory)\n    --root             project root (default: .)"},
	"cross-repo-impact": {run: func(cmd string, rest []string) int {
		runCrossRepoImpact(rest)
		return 0
	}, help: "cross-repo blast radius", usage: "usage: kern cross-repo-impact <symbol> [--repo <path>]... [--root DIR]\n  options:\n    --repo             repository root (default: current directory)\n    --root             project root (default: .)"},
	"memory_ranked": {run: func(cmd string, rest []string) int {
		runMemoryRanked(rest)
		return 0
	}, help: "", usage: "usage: kern memory-ranked <prompt> [-k 5] [--half-life 7.0] [--root DIR]\n  options:\n    --half-life\n    --root             project root (default: .)"},
	"memory-ranked": {run: func(cmd string, rest []string) int {
		runMemoryRanked(rest)
		return 0
	}, help: "decay-weighted memory retrieval", usage: "usage: kern memory-ranked <prompt> [-k 5] [--half-life 7.0] [--root DIR]\n  options:\n    --half-life\n    --root             project root (default: .)"},
	"policy_dsl": {run: func(cmd string, rest []string) int {
		runPolicyDSL(rest)
		return 0
	}, help: "", usage: "usage: kern policy_dsl [flags]"},
	"policy-dsl": {run: func(cmd string, rest []string) int {
		runPolicyDSL(rest)
		return 0
	}, help: "policy-as-code evaluation", usage: "usage: kern policy-dsl [flags]  (alias of policy_dsl)"},
	"agent_coordination": {run: func(cmd string, rest []string) int {
		runAgentCoordination(rest)
		return 0
	}, help: "", usage: "usage: kern agent_coordination [flags]"},
	"agent-coordination": {run: func(cmd string, rest []string) int {
		runAgentCoordination(rest)
		return 0
	}, help: "multi-agent handoffs and claims", usage: "usage: kern agent-coordination [flags]  (alias of agent_coordination)"},
	"agent_role_rbac": {run: func(cmd string, rest []string) int {
		runAgentRoleRBAC(rest)
		return 0
	}, help: "", usage: "usage: kern agent_role_rbac [flags]"},
	"agent-role-rbac": {run: func(cmd string, rest []string) int {
		runAgentRoleRBAC(rest)
		return 0
	}, help: "role-based tool access control", usage: "usage: kern agent-role-rbac [flags]  (alias of agent_role_rbac)"},
	"stream": {run: func(cmd string, rest []string) int {
		runStream(rest)
		return 0
	}, help: "chunking and stream progress", usage: "usage: kern stream [flags]"},
	"ast_transform": {run: func(cmd string, rest []string) int {
		runAstTransform(rest)
		return 0
	}, help: "", usage: "usage: kern ast_transform [flags]"},
	"ast-transform": {run: func(cmd string, rest []string) int {
		runAstTransform(rest)
		return 0
	}, help: "deterministic AST-level transformations and scaffolding", usage: "usage: kern ast-transform [flags]  (alias of ast_transform)"},
	"semantic_merge": {run: func(cmd string, rest []string) int {
		runSemanticMerge(rest)
		return 0
	}, help: "", usage: "usage: kern semantic_merge [flags]"},
	"semantic-merge": {run: func(cmd string, rest []string) int {
		runSemanticMerge(rest)
		return 0
	}, help: "AST-aware 3-way semantic merge and conflict detection", usage: "usage: kern semantic-merge [--file FILE] [--base CODE] [--local CODE] [--remote CODE] [--apply] [--json] [--root DIR]\n  options:\n    --base CODE    base version: code string, or a path to a file containing it\n    --local CODE   local version: code string or file path\n    --remote CODE  remote version: code string or file path\n    --file FILE    target file path (required with --apply)\n    --apply        write a clean 3-way merge into the target file\n    --json         emit the merge/conflict result as JSON\n    --root DIR     project root for AST context (default: .)"},
	"synthesize_test": {run: func(cmd string, rest []string) int {
		runSynthesizeTest(rest)
		return 0
	}, help: "", usage: "usage: kern synthesize_test [flags]"},
	"synthesize-test": {run: func(cmd string, rest []string) int {
		runSynthesizeTest(rest)
		return 0
	}, help: "automatically synthesize table-driven unit tests from AST signatures", usage: "usage: kern synthesize-test [flags]  (alias of synthesize_test)"},
	"cache": {run: func(cmd string, rest []string) int {
		runCache(rest)
		return 0
	}, help: "cache stats + G-7 maintain (archive/evict)", usage: "usage: kern cache [flags]\n  options:\n    --dry-run          preview without applying"},
	"config": {run: func(cmd string, rest []string) int {
		runConfig(rest)
		return 0
	}, help: "show effective configuration (env > .kern/config.json > default)", usage: "usage: kern config [flags]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},
}
