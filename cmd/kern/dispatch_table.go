package main

import (
	"fmt"
	approvalcli "github.com/JayveerPrajapati/kern/internal/approvalcli"
	graphcli "github.com/JayveerPrajapati/kern/internal/graphcli"
	"os"
	"strconv"
	"strings"
)

type commandEntry struct {
	run  func(cmd string, rest []string) int
	help string

	usage string

	category string

	alias bool
}

const (
	evidenceAnchorHelp = "verify citations and cryptographic proof"
	memoryRankedHelp   = "decay-weighted memory retrieval"
)

var commandTable = map[string]commandEntry{
	"version": {category: "meta", run: func(cmd string, rest []string) int {
		runVersion(rest)
		return 0
	}, help: "print version", usage: "usage: kern version [flags]"},
	"--version": {category: "meta", run: func(cmd string, rest []string) int {
		runVersion(rest)
		return 0
	}, help: "", usage: "usage: kern `--version` [flags]  (alias of version)"},
	"-v": {category: "meta", run: func(cmd string, rest []string) int {
		runVersion(rest)
		return 0
	}, help: "", usage: "usage: kern -v [flags]  (alias of version)"},

	"anchor": {category: "evidence", run: func(cmd string, rest []string) int {
		return runFetchRawAnchor(rest)
	}, help: "fetch raw uncompressed anchor content", usage: "usage: kern anchor <anchor-id>"},

	"optimize": {category: "compression", run: func(cmd string, rest []string) int {
		runOptimize(cmd, rest)
		return 0
	}, help: "compress a prompt/log/output", usage: "usage: kern optimize [flags]\n  options:\n    --attach           attach a file\n    --cache            cache optimize results (masked prompt previews + results stored under ~/.cache/kern; default: on)\n    --fewshot          few-shot mode\n    --kind             prompt (default) | log\n    --llm              LLM backend name\n    --mask             mask PII/secrets in output\n    --model            model name\n    --names            names to mask (comma-separated)\n    --session          session id"},

	"compact": {category: "compression", run: func(cmd string, rest []string) int {
		runCompact(rest)
		return 0
	}, help: "symbolic file summary", usage: "usage: kern compact [--root ROOT] <file>\n  options:\n    --root             project root (default: .)\n    --tier             summary|folded|full\n    --etag             conditional-fetch etag from a previous response"},
	"log": {category: "compression", run: func(cmd string, rest []string) int {
		// Thin wrapper over `kern optimize --kind log` (surface consolidation
		// T2b) — restored because the opencode plugin's tool.execute.after
		// compression hook and the kern_optimize action=log MCP tool invoke
		// this spelling; the dispatch row was dropped in a CLI slim while
		// every caller (and runOptimize's own comment) still assumed it.
		runLog(rest)
		return 0
	}, help: "compress log output (wrapper over optimize --kind log)", usage: "usage: kern log <file|text|-> [flags]\n  options:\n    --cache            cache optimize results (default: on)\n    --context-after    lines after each error event to keep\n    --context-before   lines before each error event to keep\n    --mask             mask PII/secrets in output\n    --names            names to mask (comma-separated)\n    --profile          log profile\n    --root             project root (default: .)"},
	"terse": {category: "compression", run: func(cmd string, rest []string) int {
		// Thin wrapper over the budget --mode terse core (T2b) — restored for
		// the same reason as "log": the plugin's kern_optimize action=output
		// tool invokes this spelling.
		runTerse(rest)
		return 0
	}, help: "line-level tersification of text (wrapper over the terse core)", usage: "usage: kern terse <file|text|-> [--max N]"},
	"project": {category: "graph", run: func(cmd string, rest []string) int {
		runProject(rest)
		return 0
	}, help: "project map", usage: "usage: kern project [flags]"},

	"setup": {category: "wiring", run: func(cmd string, rest []string) int {
		return runSetup(rest)
	}, help: "wire agents/MCP/hooks", usage: "usage: kern setup [flags]\n  options:\n    --agents\n    --agents-md        repo AGENTS.md variant: thin (default, wiring-only; full rules then live in global instructions) or full\n    --all-agents       wire every supported agent, not only the detected ones (the old no-flag behaviour)\n    --check            check mode\n    --detect           wire only the agents detected on this machine (the default when no agent flag is given) — writes the managed files, not a read-only scan (pair with --dry-run to preview)\n    --dry-run          preview mode: report what would be written, change nothing\n    --global           apply at global/user scope (wires every agent, machine-wide)\n    --global-only      wire user-level MCP + hooks for every agent and write NO file in any repo (repos initialize themselves on first agent use)\n    --global-rules     manage kern rules in each host's GLOBAL instructions (~/.claude/CLAUDE.md, ~/.codex/AGENTS.md, ~/.config/opencode/AGENTS.md)\n    --json             machine-readable JSON output; pairs with check or dry-run mode (one document, no human rendering)\n    --plain            legacy plain-text output (no boxes, glyphs or colour; also used automatically when output is piped)\n    --remove           undo the machine-wide wiring: remove only kern's own entries and blocks from user-level config (backs each edited file up first; --agents limits it; pair with --dry-run to preview; never touches ~/.kern data)\n    --root             project root (default: .)\n    --verify           verify mode\n    --yes              skip the interactive confirmation; REQUIRED to apply from a non-interactive run (without it setup previews in dry-run mode; json/check/verify/remove modes are explicit and never prompted)\n  on an interactive terminal setup shows a plan and asks before writing; run kern setup --dry-run first for a read-only preview of what wiring would write"},
	"buddy": {category: "wiring", run: func(cmd string, rest []string) int {
		runBrief(rest)
		return 0
	}, help: "session onboarding digest", usage: "usage: kern buddy [root]\n  options:\n    --map              include the full per-file project map\n    --root             project root (default: .)"},
	"project-map": {category: "wiring", run: func(cmd string, rest []string) int {
		runProjectMap(rest)
		return 0
	}, help: "full per-file project map (the same renderer buddy --map appends)", usage: "usage: kern project-map [root]\n  options:\n    --root             project root (default: .)"},

	"onboard": {category: "wiring", run: func(cmd string, rest []string) int {
		runOnboard(rest)
		return 0
	}, help: "register+index+wired status for the repo", usage: "usage: kern onboard [flags]\n  options:\n    --root             project root (default: .)"},
	"update": {category: "wiring", run: func(cmd string, rest []string) int {
		runUpdate(rest)
		return 0
	}, help: "update the installed binaries (via install.sh)", usage: "usage: kern update [--dry-run] [--force] [--pin <tag>] [--channel <name>]\n  delegates to the canonical install.sh: compares the installed version\n  against the latest release and swaps the binaries (with backup) when older.\n  a local-build installed version (Makefile hash stamp / dev) is refused\n  before any network access — rebuild via 'make build && make install'.\n    --dry-run          check-only status report, nothing changes\n    --force            override a refused update (the only override)\n    --pin <tag>        update to a specific release tag (e.g. v0.9.9.1);\n                       also the deliberate-downgrade path refused versions name\n    --channel <name>   release channel for latest: stable (newest 3-component\n                       tag, 4-component hotfixes excluded) or a regex over\n                       tag names (default: latest); --pin overrides it\n  (the one kern command that intentionally touches the network)"},

	"heal": {category: "verification", run: func(cmd string, rest []string) int {
		return runHeal(rest)
	}, help: "self-correct failing files", usage: "usage: kern heal [flags]\n  options:\n    --force            override the HIGH-risk repair gate (requires interactive y/N confirmation)\n    --yes              confirm the --force gate override without prompting (required for non-interactive runs)\n    --llm              Ollama model name\n    --task"},

	"swap": {category: "compression", run: func(cmd string, rest []string) int {
		runSwap(rest)
		return 0
	}, help: "budget-swap path-tagged fenced code blocks", usage: "usage: kern swap [FILE|-] [--max N] [--mode fit|summary|expand]\n  swappable fences carry a file path tag: ```lang:path (e.g. ```go:internal/foo.go)\n  options:\n    --max              token budget (default: 4000)\n    --mode             fit (default) | summary | expand"},

	"memory": {category: "memory", run: func(cmd string, rest []string) int {
		runMemory(rest)
		return 0
	}, help: "engineering memory ops", usage: memoryUsage},

	"exec": {category: "exec", run: func(cmd string, rest []string) int {
		return runExec(rest)
	}, help: "run code in a network-isolated sandbox (sensitive reads and writes outside the ephemeral run dir blocked)", usage: "usage: kern exec \n  options:\n    --json             emit JSON output\n    --lang             language\n    --readonly         skip the KERN_TOOLS/KERN_ALLOW_EXEC gate (read-only bash fallbacks)\n    --list\n    --max              maximum count/threshold\n    --stdin            stdin content\n    --timeout          timeout (seconds or ms per command)"},
	"build": {category: "exec", run: func(cmd string, rest []string) int {
		return runBuild(rest)
	}, help: "run a build command in the governed sandbox", usage: "usage: kern build <command> [flags]\n  exits with the CHILD command's exit code when the command itself fails (make/sh -c convention);\n  the wrapper's own failures stay 1 (runtime) / 2 (usage) / 3 (policy denial)\n  options:\n    --dir              working directory\n    --session          session id\n    --root             project root (default: .)\n    --timeout          timeout (seconds or ms per command)"},
	"exitcode": {category: "meta", run: func(cmd string, rest []string) int {
		runExitcode(rest)
		return 0
	}, help: "print kern's documented exit-code conventions", usage: "usage: kern exitcode\n  prints the exit-code conventions table (0 ok, 1 error, 2 usage, 3 decided-state/policy)"},
	"bench": {category: "meta", run: func(cmd string, rest []string) int {
		runBench(rest)
		return 0
	}, help: "deterministic zero-network latency harness (cold/warm index load + fixed query set; writes .kern/bench.json)", usage: "usage: kern bench [--root ROOT] [--json]\n  measures the two load paths every kern command pays — cold index build vs warm\n  persisted-store load — plus a fixed set of query latencies (symbol search,\n  ranked search, one-hop callers, transitive blast radius, hub ranking),\n  mirroring the cold-vs-warm methodology of docs/benchmarks/graph-latency.md.\n  Output: a human table on stdout AND the canonical JSON document at\n  <root>/.kern/bench.json (the path the web console's /benchmarks page reads).\n  No network, no LLM calls, no external tools.\n  options:\n    --root             project root (default: .)\n    --json             also emit the JSON document on stdout"},
	"doctor": {category: "wiring", run: func(cmd string, rest []string) int {
		return runDoctor(rest)
	}, help: "self-diagnostics", usage: "usage: kern doctor [flags]\n  options:\n    --arch-drift      report ARCHITECTURE.md LOC/deps drift (drift rows are non-fatal warnings; exits 1 only when the parity run itself fails, e.g. missing/broken ARCHITECTURE.md)\n    --calibration     report prediction-vs-reality calibration-health section (non-fatal)\n        --json             emit JSON output\n    --root             project root (default: .)"},

	"mask": {category: "compression", run: func(cmd string, rest []string) int {
		runMask(rest)
		return 0
	}, help: "mask secrets/PII", usage: "usage: kern mask [flags]\n  options:\n    --names            names to mask (comma-separated)"},

	"plan": {category: "analysis", run: func(cmd string, rest []string) int {
		runAnalyze(cmd, rest)
		return 0
	}, help: "analyze a proposed change", usage: "usage: kern plan <change> [--root ROOT]\n  options:\n    --json             emit the structured plan as JSON\n    --root             project root (default: .)\n    --task"},

	"apply": {category: "verification", run: func(cmd string, rest []string) int {
		return runApply(rest)
	}, help: "apply edits transactionally, verify, roll back on failure", usage: "usage: kern apply <edits-file|-> [--root ROOT] [--test] [--dry-run] [--json]\n  the edits file is a JSON array of {path, content} objects (content = the NEW COMPLETE file; same shape as kern_refactor_transaction's edits)\n  pipeline: transactional apply (sandbox compile gate) -> scoped `go test` on the modified packages (--test) -> live-tree rollback when verify fails\n  exit codes: 0 applied+verified (or dry-run preview), 1 apply/verify FAILED (findings), 2 usage, 3 denied\n  options:\n    --root             project root (default: .)\n    --test             run `go test` on the packages of every modified .go file after the build gate (rollback on failure)\n    --dry-run          preview the unified diff without touching the live tree\n    --json             emit the full result (diff, outputs, receipt) as JSON"},

	"run": {category: "autonomy", run: func(cmd string, rest []string) int {
		runRun(rest)
		return 0
	}, help: "intent through the task pipeline", usage: "usage: kern run <intent> [--root ROOT]\n  single-shot classify+run workflow\n  options:\n    --root             project root (default: .)"},

	"impact": {category: "analysis", run: func(cmd string, rest []string) int {
		runImpact(rest)
		return 0
	}, help: "blast radius of a change", usage: "usage: kern impact <change> [kind] [new-target] [--root ROOT]\n  options:\n    --json             emit JSON output\n    --precision        precision mode\n    --risk             render the governance risk assessment (analyze --lens risk output)\n    --root             project root (default: .)\n    --runtime          fold runtime evidence (data stores, incidents, architecture rules) into the report"},

	"approve": {category: "governance", run: func(cmd string, rest []string) int {
		return approvalcli.RunApprove(rest)
	}, help: "resolve an approval gate", usage: "usage: kern approve <id> | kern approve list [flags]\n  the approval gate is interactive by design: the human must TYPE the\n  approval id on the terminal to confirm (30s confirm timeout, then a\n  refusal); there is no non-interactive override. `kern approve list` shows\n  every decidable pending approval (governance store).\n  options:\n    --approver         approver identity\n    --prefix           prefix for the prefix scope (default: the command's first two tokens)\n    --reason           reason text\n    --reject           reject instead of approve (non-interactive)\n    --root             project root (default: .)\n    --scope            once (default) or prefix — prefix widens the decision into a session-scoped grant: commands starting with the prefix run without re-asking (use-capped, expiry-bounded, audit-logged)"},
	"approval": {category: "governance", run: func(cmd string, rest []string) int {
		return approvalcli.RunApproval(rest)
	}, help: "observe a pending approval's decision (wait) or clear a blocking record (clear)", usage: "usage: kern approval <subcommand> [flags]\n  subcommands:\n    wait               block until the approval is decided — prints \"pending: <id>\" once at start, then the decision when it lands\n                       (exit 0 approved / 3 rejected / 1 timed out)\n    clear              remove an approval record by id (operator escape hatch): a stale REJECTED record stops blocking\n                       byte-identical retries of its command; the next attempt mints a fresh approval a human still decides\n  options:\n    --approver         operator identity recorded for the clear (clear)\n    --reason           reason text (clear)\n    --timeout          how long to wait for the decision (duration, e.g. 30m, 90s, 500ms; default 30m; wait)\n    --root             project root (default: .)"},

	"evidence": {category: "evidence", run: func(cmd string, rest []string) int {
		return runEvidence(rest)
	}, help: "evidence store", usage: "usage: kern evidence [flags]\n  subcommands:\n    export             build a signed evidence bundle for the repo\n    verify             verify a bundle's seal and audit chain (--file|--url|positional)\n    explain            plain-language explanation of a bundle (--file|--url|positional)\n  full-state operations:\n    --full-state       export the COMPLETE evidence store state (every record + per-record sha256 checksums + bundle digest) as one deterministic JSON document\n    --restore FILE     restore the store from a full-state bundle (bundle digest and every checksum verified BEFORE any write; tampered bundles fail cleanly)\n  options:\n    --root             project root (default: .)\n    --out              output path for --full-state (default \"-\" = stdout)\n    --agent-id         agent ID the authorization is scoped to (export; default \"default\")\n    --task             task ID the authorization is scoped to (export)\n    --sign             sign the bundle with the project key (.kern/keys/, created on first use) (export)\n    --file             bundle JSON file (verify/explain; default: read from stdin)\n    --url              bundle URL to fetch and verify/explain without cloning\n    --expect-fingerprint require the bundle to be signed by this key fingerprint (verify; the trust anchor)"},

	"verify": {category: "verification", run: func(cmd string, rest []string) int {
		return runVerify(rest)
	}, help: "verify a change", usage: "usage: kern verify [<types>|<file|->] [flags]\n  high-level: kern verify [build,test,security,architecture,dependency,reuse,e2e,static-analysis,performance,cve,license,secrets,ci,arch,claims] [--types X] (default build,test; needs KERN_ALLOW_EXEC=1)\n  compliance: kern verify --cve --license --secrets (opt-in; each flag adds its check to the run)\n  claims gate: kern verify claims (prove the public product claims: blast-radius reference evidence, audit-chain integrity, guard gate corpus, tool-catalog contract; runs on its own)\n  claims file: kern verify <file|-> [root] (verify a file of agent output/references against the index)\n  command mode: kern verify --command `go test ./...` --output summary (run YOUR gate behind the governed allowlist; the full run is kept — re-slice it via the --anchor flag)\n  incremental: kern verify test --changed (scope the test step to packages with uncommitted changes)\n  options:\n    --types            explicit check types (alias for positional <types>; matches MCP kern_verify)\n    --short            run the test step with `go test -short` (DEFAULT; fast agent-safe suite, ~1min)\n    --fast             pre-commit tier: build + tests scoped to uncommitted-changed packages only (build-only when nothing changed; skips security/arch/dependency/e2e)\n    --changed-since    scope the test step to packages changed since a git ref (CI: build+vet+test on the changed surface; a clean diff prints NO-GO-CHANGES and exits 0)\n    --changed          scope the test step to packages with uncommitted changes vs HEAD (falls back to the default scope when none)\n    --staged           scope the test step to staged changes vs HEAD (the pre-commit hook tier)\n    --command CMD      run YOUR build/test/lint command instead of the built-in checks (governed allowlist)\n    --output           command-mode slice: summary|failures|tail:N|lines:A-B|full (default summary)\n    --anchor ID        re-slice a previously saved full run by its anchor id, no rerun\n    --timeout          command-mode timeout in seconds (default 120)\n    --full             run the COMPLETE test suite `go test -v ./...` (slower, ~4min; overrides the short and fast modes)\n    --cve              run the govulncheck vulnerability scan (SKIPPED when govulncheck is not installed)\n    --license          classify module licenses from go.mod/vendor (deterministic, no network)\n    --secrets          scan git history for committed secrets (masked snippets only)\n    --eval             evaluate a directory of cases\n    --json             emit JSON output\n    --root             project root (default: .)\n    --scan             scan path\n    --skill            skill directory\n    --verify-pipeline  silent-orchestrator pipeline verify\n    --verify-silent    silent verify\n    --verify-token-reduction  token-reduction verify\n  test-step override: KERN_VERIFY_TEST env or verify.test in .kern/config.json replaces the test command verbatim (wins over both --short and --full runs)\n  note: the full report prints to stdout by design (a FAIL verdict exits 1 as the CI signal)"},

	"docs": {category: "docs", run: func(cmd string, rest []string) int {
		runDocs(rest)
		return 0
	}, help: "local doc search (indexes repos with a docs tree)", usage: "usage: kern docs <query> [root] | kern docs fetch <url> [name] [root]\n  searches only indexed repo docs — repos without a docs tree report that instead of a bare no-match\n  options:\n    --limit            cap results at N\n    --root             project root (default: .)\n    --semantic         semantic matching"},

	// Blueprint bridge commands (restored 2026-10-18, P7 reconciliation):
	// the standalone blueprint repo consumes kern via these binary commands
	// (adapters/kern client + the G2/G3/G7/G17/G27 integration harness). The
	// pivot CLI slim removed the dispatch rows while kern kept the engines;
	// the commands are thin shells over internal/guard + internal/sec +
	// internal/secscan + approvalcli, exactly the "keep kern guard, audit"
	// surface the pivot inventory named.
	"guard": {category: "locks", run: func(cmd string, rest []string) int {
		return runGuard(rest)
	}, help: "architecture guardrails", usage: "usage: kern guard <check|init|allow|classify|parity|hook|grant|pause|paused|resume> [root] [--file f1,f2] [--range a..b] [--json|--sarif] [--threshold N] [--precision default|strict] [--agent-id ID --task DESC]\n  classify <command> [--json]: risk-axis verdict (allow|gate|serve|governed + tier) for one command — the Go source the plugin's bash arm shells out to\n  parity [--json] [--root R]: cross-oracle check — every corpus command must be dangerous in BOTH the bash-guard (guardrule) and governed-exec (execclassify) classifiers; a divergence is a bypass (exit 1)\n  grant [--check] [--root R] <command>: Ask-Protocol bridge for GATE commands — without --check the mint form first PEEKS for an active approval covering the command and exits 0 (nothing consumed — the guard consumes it when the command runs; retry byte-identical); otherwise it MINTS approval appr-<id> and prints the ask-wait-retry instructions (the operator answers kern approval ask appr-<id>). with --check the plugin/script claim form CONSUMES the covering approval (exit 0 — then run the command) or exits 3 when not granted (nothing minted)\n  hook: the pure-Go PreToolUse guard hook — reads the host payload on stdin (tool_name/tool_input JSON), emits allow/serve/deny (deny exits 2) with kern steering; the single-process replacement for the kern-guard.sh shell script\n  pause [root] [--scope repo] [--duration 30m] --reason TEXT: audited, typed, duration-bounded repo pause for read/grep/glob steering only (exec governance stays enforced)\n  paused [root]: print active|inactive\n  resume [root]: remove the active repo pause\n  options:\n    --agent-id         agent identity\n    --check            grant; consult without minting (exit 3 when not granted)\n    --duration         pause duration (default 30m, max 2h)\n    --file             file path\n    --force            overwrite an existing boundaries file (init)\n    --json             emit JSON output\n    --precision        precision mode\n    --range            line range a..b\n    --reason           operator reason (pause; required)\n    --require-boundaries\n                       strict mode: fail when .kern/boundaries.json is missing\n    --root             project root (grant; default: cwd)\n    --sarif            SARIF 2.1.0 report\n    --scope            repo (pause; the only supported scope)\n    --task\n    --threshold        score threshold"},
	"sec": {category: "security", run: func(cmd string, rest []string) int {
		runSec(rest)
		return 0
	}, help: "security scan (regex + optional gosec)", usage: "usage: kern sec [root] [flags]\n  options:\n    --engine           scan engines: internal|gosec|all|none (default all: internal + gosec when installed)\n    --json             emit JSON output\n    --max              cap rendered findings (text mode)\n    --root             project root (default: .)\n    --severity         severity filter (comma-separated, default error)"},
	"fingerprint": {category: "locks", run: func(cmd string, rest []string) int {
		runFingerprint(rest)
		return 0
	}, help: "repo fingerprint", usage: "usage: kern fingerprint [root] [--file f1,f2] [--json]\n  options:\n    --file             file path\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"audit": {category: "governance", run: func(cmd string, rest []string) int {
		return approvalcli.RunAudit(rest)
	}, help: "governance audit log", usage: "usage: kern audit [flags] | kern audit append | kern audit repair | kern audit rotate\n  subcommands:\n    append             append an AuditEntry JSON from stdin or --file into the tamper-evident chain (external writers)\n    repair             re-chain the tamper-evident chain from the first broken link\n    rotate             archive the active chain under .kern/audit/archive-<timestamp>/ and start a fresh keyed chain (bounded retention)\n  options:\n    --file             entry JSON file (append; default stdin)\n    --json             emit JSON output\n    --keep N           rotate: keep the newest N archives (default 3; 0 keeps all)\n    --limit N          show the N most recent entries (default 50; 0 = all)\n    --tail N           alias for --limit\n    --root             project root (default: .)"},

	"reject": {category: "governance", run: func(cmd string, rest []string) int {
		return approvalcli.RunReject(rest)
	}, help: "reject a pending approval request: reject <id> [--reason ...]", usage: "usage: kern reject [flags]\n  options:\n    --approver         approver identity\n    --reason           reason text\n    --root             project root (default: .)"},

	"policy": {category: "governance", run: func(cmd string, rest []string) int {
		return approvalcli.RunPolicy(rest)
	}, help: "org-level governance policy distribution (set|get|apply)", usage: "usage: kern policy <set|get|apply> [flags]\n  manages the org policy document at <org-root>/.kern/org-policy.json — the\n  single source of truth enterprise project firewalls are built from. The org\n  root resolves from the root flag, else $KERN_ORG_ROOT; no org root means no\n  org governance (each project's own .kern store stays authoritative).\n  subcommands:\n    set                write/merge the org policy (--file JSON array of policies, or {\"policies\": [...]}; --merge merges by policy ID with the existing document)\n    get                print the org policy (+ recorded/content hashes + drift report)\n    apply              reload the org policy document into canonical state (re-records the content hash when the file was edited out-of-band) and report drift\n  options:\n    --file             policy JSON file for set (array or {\"policies\": [...]})\n    --merge            merge by policy ID instead of replacing the whole set (set)\n    --json             emit JSON output (get)\n    --root             org root (default: $KERN_ORG_ROOT)"},

	"hook": {category: "git", run: func(cmd string, rest []string) int {
		return runHook(rest)
	}, help: "install/uninstall git hooks", usage: "usage: kern hook <install|uninstall|diff|store|claude-post|claude-prompt|gemini-after|gemini-prompt> [root] [--range a..b] [--global]\n  options:\n    --range            line range a..b\n    --global           hook uninstall: unset global core.hooksPath (kern's ~/.kern/git-hooks only)"},
	"commit": {category: "git", run: func(cmd string, rest []string) int {
		return runCommit(rest)
	}, help: "stage+commit", usage: "usage: kern commit [flags]\n  options:\n    --all              include all entries\n    --dry-run          preview without applying\n    --message          message text"},
	"semcache": {category: "compression", run: func(cmd string, rest []string) int {
		runSemcache(rest)
		return 0
	}, help: "semantic cache stats", usage: "usage: kern semcache <list|sim|stats|clear>\n  subcommands:\n    list [<prompt|log>] show stored inputs\n    sim <textA> <textB> show Jaccard similarity\n    stats               show per-namespace hit/miss/eviction/savings accounting\n    clear [namespace]   wipe a namespace (or all)\n  options:\n    --a A --b B        texts for sim (mirrors the MCP a/b args)\n    --json             emit JSON output\n    --namespace N      namespace for list/clear (mirrors the MCP namespace arg)"},
	"stats": {category: "compression", run: runStatsEntry, help: "token savings", usage: "usage: kern stats [flags]\n  options:\n    --days             days back (default 7)\n    --session          filter by session id\n    --json             emit JSON output\n    --by-tool          per-tool ledger: calls, tokens returned, tokens saved, cost saved\n    --by-agent         per-agent ledger: calls, tokens returned, tokens saved, cost saved (agent_id from MCP calls; others under (unattributed))\n    --by-root          per-project diagnostics: rooted projects only (legacy rootless entries are excluded)\n    --root             scope --by-tool to an exact project root\n    --net              net savings: gross saved tokens minus rules-injection cost (loaded AGENTS.md/CLAUDE.md) minus slice re-read call costs"},

	"mcp": {category: "servers", run: func(cmd string, rest []string) int {
		runMCP(rest)
		return 0
	}, help: "run the MCP server ('kern mcp tools' lists the catalog)", usage: "usage: kern mcp [tools [category] [--json] [--category X]] [flags]\n  subcommands:\n    tools              list the full MCP tool catalog (grouped by category)\n  options:\n    --http ADDR        serve over HTTP: an address (:8080, unix:/path) or 'auto' for a 0600 unix socket\n    --tls-cert\n    --tls-key"},

	"mcp-client": {category: "servers", run: func(cmd string, rest []string) int {
		// Thin shell over the external-MCP-server client (cmd_mcp_client.go):
		// manage and call OUTSIDE MCP servers whose config lives at
		// .kern/mcp-servers.json. Restored — the pivot CLI slim dropped the
		// dispatch row while docs/mcp-client.md and the tested client SDK
		// (internal/mcpclient) remained, so `kern mcp-client` silently
		// no-oped ("unknown command") despite being a documented feature.
		runMcpClient(rest)
		return 0
	}, help: "manage external MCP servers and call their tools (config: .kern/mcp-servers.json)", usage: "usage: kern mcp-client <add|list|rm|call> [args...]\n  subcommands:\n    add <name> <command...>  register an external stdio MCP server (or --url for a remote HTTP server)\n    list                    list registered external servers\n    rm <name>               unregister an external server\n    call <name> <tool> [args]  call a tool on a registered server\n  options:\n    --url              register a remote streamable-http server (add)\n    --root             project root (default: .)\n  see docs/mcp-client.md for the full feature"},

	"meta": {category: "meta", run: func(cmd string, rest []string) int {
		runMeta(rest)
		return 0
	}, help: "NL request router", usage: "usage: kern meta \"<request>\" [--root ROOT] [--pipeline JSON]\n  example: kern meta \"show me the architecture\"\n  --pipeline JSON runs the deterministic multi-tool pipeline instead of NL routing"},
	"ask": {category: "meta", run: func(cmd string, rest []string) int {
		runMeta(rest)
		return 0
	}, help: "NL request router (alias of meta)", usage: "usage: kern ask \"<question>\" [--root ROOT]\n  example: kern ask \"how does dispatch work\""},
	"index": {category: "search", run: func(cmd string, rest []string) int {
		return graphcli.RunIndex(rest)
	}, help: "(re)build the symbol index; --status reports health; ensure-fresh probes+rebuilds+re-verifies in one subprocess", usage: "usage: kern index [root] [flags]\n  options:\n    --force            force overwrite\n    --json             emit JSON output\n    --root             project root (default: .)\n    --status           status mode\n    --strict           strict mode\n    --update           update mode"},

	"ast": {category: "search", run: func(cmd string, rest []string) int {
		return graphcli.RunAst(rest)
	}, help: "AST symbol search", usage: "usage: kern ast <pattern> [root] [--all]\n  options:\n    --all              include all entries\n    --root             project root (default: .)"},

	"fts": {category: "search", run: func(cmd string, rest []string) int {
		return graphcli.RunFts(rest)
	}, help: "direct SQLite FTS5 symbol search", usage: "usage: kern fts \"<query>\" [root] [--limit N] [--json]\n  options:\n    --json             emit JSON output\n    --limit            cap results at N (default 20)\n    --root             project root (default: .)"},

	"search": {category: "search", run: func(cmd string, rest []string) int {
		return graphcli.RunSearch(rest)
	}, help: "ranked symbol search", usage: "usage: kern search <query> [root] [--limit N] [--path DIR] [--repos] [--json] [--semantic]\n  options:\n    --json             emit JSON output\n    --limit            cap results at N\n    --path             scope results to a repo-relative file or directory\n    --repos            scan across repositories\n    --root             project root (default: .)\n    --semantic         semantic matching\n    --etag             conditional-fetch etag from a previous response"},

	"graph": {category: "graph", run: func(cmd string, rest []string) int {
		return graphcli.RunGraph(rest)
	}, help: "call-graph context", usage: "usage: kern graph <symbol> [root] [--mermaid] [--one-line] [--entities] [--json] [--graphml] [--cypher] [--html] [--out FILE] [--max-tokens N] [--limit N]\n  options:\n    --cypher\n    --entities         render the digital-twin entity nodes connected to the symbol (or the repo entity inventory without a symbol)\n    --graphml\n    --html\n    --json             emit JSON output\n    --limit            cap results at N\n    --max-tokens       token cap\n    --mermaid          Mermaid output\n    --min-confidence   minimum confidence\n    --one-line         single-line call-graph neighbourhood (definition, callers, callees)\n    --out              write output to FILE\n    --root             project root (default: .)\n    --etag             conditional-fetch etag from a previous response"},

	"context": {category: "context", run: func(cmd string, rest []string) int {
		return runContext(rest)
	}, help: "symbol context slice", usage: "usage: kern context <symbol> [root] [--lines N]\n  options:\n    --lens             analysis lens\n    --lines            context line count\n    --profile          profile name\n    --root             project root (default: .)\n    --etag             conditional-fetch etag from a previous response"},
	"refs": {category: "graph", run: func(cmd string, rest []string) int {
		return runRefs(rest)
	}, help: "AST-verified references (types/consts/vars/fields included)", usage: "usage: kern refs <symbol> [root]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},

	"why": {category: "graph", run: func(cmd string, rest []string) int {
		return graphcli.RunWhy(rest)
	}, help: "rationale/doc report", usage: "usage: kern why <symbol> [root] [--json]\n  options:\n    --json             emit JSON output\n    --min-confidence   minimum confidence\n    --root             project root (default: .)\n    --etag             conditional-fetch etag from a previous response"},

	"review": {category: "review", run: func(cmd string, rest []string) int {
		runChanges(cmd, rest)
		return 0
	}, help: "review context for changed files", usage: "usage: kern review [flags]\n  exits 1 when changed files carry risk (findings; the report is still on stdout)\n  options:\n    --file             file path\n    --json             emit JSON output\n    --lens             analysis lens\n    --max              token budget for the review output\n    --profile          profile name\n    --range            line range a..b\n    --root             project root (default: .)\n    --runtime"},

	"path": {category: "graph", run: func(cmd string, rest []string) int {
		return graphcli.RunPath(rest)
	}, help: "shortest call path", usage: "usage: kern path <from-symbol> <to-symbol> [root] (or --from S --to S)\n  options:\n    --from             start symbol\n    --json             emit JSON output\n    --min-confidence   minimum confidence\n    --root             project root (default: .)\n    --to               end symbol"},

	"arch": {category: "graph", run: func(cmd string, rest []string) int {
		return graphcli.RunArch(rest)
	}, help: "architecture overview", usage: "usage: kern arch [flags]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},

	"explore": {category: "graph", run: func(cmd string, rest []string) int {
		return graphcli.RunExplore(rest)
	}, help: "symbol source + blast radius", usage: "usage: kern explore <symbol> [root] [--depth N] [--max N] [--max-tokens N] [--explain]\n  options:\n    --depth            traversal depth (default 2, 0 = uncapped)\n    --explain          append the why-rationale section (what it is, who depends on it and why)\n    --json             emit JSON output\n    --max              maximum count/threshold (default 30)\n    --max-tokens       cap the assembled response (trims lists with a '+N more (raise max_tokens)' marker)\n    --min-confidence   minimum confidence\n    --root             project root (default: .)\n    --etag             conditional-fetch etag from a previous response"},

	"probe": {category: "graph", run: func(cmd string, rest []string) int {
		return graphcli.RunProbe(rest)
	}, help: "task-driven context bundle", usage: "usage: kern probe \"<task text>\" [root] [--max N]\n  options:\n    --json             emit JSON output\n    --max              maximum count/threshold\n    --min-confidence   minimum confidence"},

	"explain-context": {category: "context", run: func(cmd string, rest []string) int {

		runOrchestrate(append([]string{"--mode", "plan"}, rest...))
		return 0
	}, help: "explainable deterministic context plan", usage: "usage: kern explain-context --task \"<change or intent>\" [--root ROOT] [--budget N] [--json]\n  options:\n    --budget           token budget\n    --json             emit JSON output\n    --root             project root (default: .)\n    --task"},

	"orchestrate": {category: "context", run: func(cmd string, rest []string) int {
		runOrchestrate(rest)
		return 0
	}, help: "silent context pipeline over an intent (JSON)", usage: "usage: kern orchestrate \"<intent>\" [--root ROOT] [--mode full|envelope|plan|fix|review|architecture|incident|explain]\n  options:\n    --budget           token budget (plan mode)\n    --json             emit JSON output (plan mode)\n    --max-tokens       token budget\n    --mode             pipeline mode\n    --root             project root (default: .)\n    --task             intent (alias of the positional)\n    --with-skill       skill name"},

	"remember": {category: "memory", run: func(cmd string, rest []string) int {
		runRemember(rest)
		return 0
	}, help: "store a lesson ('memory add' alias)", usage: "usage: kern remember <lesson> [--auto]\n  options:\n    --auto             tag the lesson as an automatic capture\n    --root             project root (default: .)"},

	"memory-ranked": {category: "memory", run: func(cmd string, rest []string) int {
		runMemoryRanked(rest)
		return 0
	}, help: "decay-weighted memory retrieval", usage: "usage: kern memory-ranked <prompt> [--k 5] [--half-life 7.0] [--root ROOT]\n  options:\n    --half-life        half-life in days\n    --k                max lessons (alias -k)\n    --root             project root (default: .)"},

	"evidence-anchor": {category: "evidence", run: func(cmd string, rest []string) int {
		return runEvidenceAnchor(rest)
	}, help: "verify citations and cryptographic proof", usage: "usage: kern evidence-anchor [--claim TEXT] [--file F] [--line N] [--symbol NAME] [--root ROOT]\n  options:\n    --claim            claim or citation\n    --file             file path\n    --line             line number\n    --root             project root (default: .)\n    --symbol           symbol name"},

	"health": {category: "meta", run: func(cmd string, rest []string) int {
		runHealth(rest)
		return 0
	}, help: "read-only JSON health snapshot (version, index, catalog)", usage: "usage: kern health [--root ROOT] [--json]\n  options:\n    --json             emit JSON output (default; health output is always JSON)\n    --root             project root (default: .)"},

	"authorize-context": {category: "context", run: func(cmd string, rest []string) int {
		return runAuthorizeContext(rest)
	}, help: "compute authorized context", usage: "usage: kern authorize-context -agent <id> -task <task> [--deny-path ...] [--symbol SYM] [--root ROOT] [--json]\n  options:\n    --agent            agent ID to authorize (required)\n    --task             task ID the authorization is scoped to (required)\n    --deny-path        path prefix denied by the task scope (repeatable)\n    --symbol           optional substring filter applied to allowed symbols\n    --root             project root (default: .)\n    --json             emit JSON (default: true)"},

	"config": {category: "wiring", run: func(cmd string, rest []string) int {
		runConfig(rest)
		return 0
	}, help: "show effective configuration (env > .kern/config.json > default)", usage: "usage: kern config [flags]\n  options:\n    --json             emit JSON output\n    --root             project root (default: .)"},
	"fit-context": {category: "compression", run: func(cmd string, rest []string) int {

		runBudget(append([]string{"--mode", "fit"}, rest...))
		return 0
	}, help: "adaptive context-window token compressor using tiered AST folding", usage: "usage: kern fit-context [flags]\n  options:\n    --file             comma-separated target files\n    --symbol           comma-separated symbol names\n    --query            relevance search query\n    --max-tokens       token budget (default 8000)\n    --root             project root (default: .)\n    --json             emit result as JSON"},

	"gen-docs": {category: "docgen", run: func(cmd string, rest []string) int {
		runGenDocs(rest)
		return 0
	}, help: "regenerate docs (tool catalog, tool contracts, schemas, tool-selection guide, or site index) from the live MCP catalog", usage: "usage: kern gen-docs --doc catalog|contracts|schemas|site|selection [--root ROOT] [--check]\n  options:\n    --doc              which doc to regenerate: catalog (default), contracts, schemas, selection, or site (the mechanical file-tree block of docs/index.md)\n    --root             project root (default: .)\n    --check            verify the generated doc is fresh without writing it"},

	"gen-catalog": {category: "docgen", run: func(cmd string, rest []string) int {
		runGenCatalog(rest)
		return 0
	}, help: "regenerate docs/tool-catalog.md from the live MCP catalog", usage: "usage: kern gen-catalog [--root ROOT] [--check]\n  options:\n    --root             project root (default: .)\n    --check            verify the generated catalog doc is fresh without writing it"},

	"gen-contracts": {category: "docgen", run: func(cmd string, rest []string) int {
		runGenContracts(rest)
		return 0
	}, help: "regenerate docs/mcp/tool-contracts.md from the live MCP catalog", usage: "usage: kern gen-contracts [--root ROOT] [--check]\n  options:\n    --root             project root (default: .)\n    --check            verify the generated contracts doc is fresh without writing it"},
	"gen-plugin": {category: "docgen", run: func(cmd string, rest []string) int {
		runGenPlugin(rest)
		return 0
	}, help: "sync the opencode plugin's canonical copy into the embedded asset AND register its sha256 (--check verifies both)", usage: "usage: kern gen-plugin [--root ROOT] [--check]\n  copies .opencode/plugins/kern.ts (canonical, hand-edited) into the embedded\n  internal/agentwire/assets/plugin/kern.ts so `kern setup` deploys the same\n  bytes everywhere, and registers the new sha256 in internal/pluginhash (the\n  step the shipped-hash test used to demand); idempotent.\n  options:\n    --root             project root (default: .)\n    --check            verify asset parity + registry freshness without writing"},

	"daemon": {category: "servers", run: func(cmd string, rest []string) int {
		return runDaemon(rest)
	}, help: "warm read-only index daemon (auto-started; speeds up search/context/explore/graph/impact)", usage: "usage: kern daemon [serve|status|stop] [--root ROOT]\n  serve   run the daemon in the foreground (normally auto-started by the CLI)\n  status  report the running daemon's pid and version (default)\n  stop    shut the daemon down\n  The daemon answers read-only index commands (search, context, compact,\n  explore, graph, impact, dependencies, why) from one hydrated index, so the\n  second call onward skips index hydration. Set KERN_DAEMON=0 to disable it."},
}

var commandFlags = map[string][]string{

	"version": {}, "--version": {}, "-v": {}, "exitcode": {},

	"bench": {"check", "json", "root"},

	"setup":       {"agents", "agents-md", "all-agents", "check", "detect", "dry-run", "global", "global-only", "global-rules", "json", "plain", "remove", "root", "verify", "yes"},
	"buddy":       {"map", "root"},
	"project-map": {"root"},
	"onboard":     {"root"},

	"update": {"channel", "dry-run", "force", "pin", "preflight"},

	"doctor": {"arch-drift", "calibration", "json", "root"},

	"config": {"json", "root"},

	"optimize": {"attach", "cache", "fewshot", "kind", "llm", "mask", "model", "names", "session"},

	"compact": {"etag", "root", "tier"},
	"log":     {"cache", "context-after", "context-before", "mask", "names", "profile", "root"},
	"terse":   {"max"},
	"project": {"max-files"},

	"fit-context": {"file", "json", "max-tokens", "mode", "query", "root", "symbol"},
	"mask":        {"names"},
	"swap":        {"max", "mode"},
	"semcache":    {"a", "b", "json", "namespace"},
	"stats":       {"by-agent", "by-root", "by-tool", "days", "json", "net", "reset", "root", "session"},

	"memory": {"auto", "clear", "json", "limit", "root"},

	"exec":  {"anchor", "json", "lang", "list", "max", "output", "readonly", "root", "stdin", "timeout"},
	"build": {"dir", "root", "session", "timeout"},

	"plan": {"json", "root", "task"},

	"apply": {"dry-run", "json", "root", "test"},

	"impact": {"json", "lens", "precision", "risk", "root", "runtime"},

	"refs": {"json", "root"},

	"why": {"etag", "json", "min-confidence", "root"},

	"graph": {"cypher", "entities", "etag", "graphml", "html", "json", "limit", "max-tokens", "mermaid", "min-confidence", "one-line", "out", "root"},

	"context": {"etag", "lens", "lines", "profile", "root"},

	"review": {"file", "json", "lens", "max", "profile", "range", "root", "runtime"},

	"path": {"from", "json", "min-confidence", "root", "to"},

	"arch": {"json", "root"},

	"explore": {"depth", "etag", "explain", "json", "max", "max-tokens", "min-confidence", "root"},

	"probe": {"json", "max", "min-confidence"},

	"index": {"force", "json", "root", "status", "strict", "update"},

	"ast":    {"all", "root"},
	"fts":    {"json", "limit", "root"},
	"search": {"etag", "json", "limit", "path", "repos", "root", "semantic"},

	"docs": {"limit", "root", "semantic"},

	"explain-context": {"budget", "json", "mode", "root", "task"},

	"orchestrate":     {"budget", "json", "max-tokens", "mode", "root", "task", "with-skill"},
	"remember":        {"auto", "root"},
	"memory-ranked":   {"half-life", "k", "root"},
	"evidence-anchor": {"claim", "file", "line", "root", "symbol"},
	"health":          {"json", "root"},

	"authorize-context": {"agent", "deny-path", "json", "root", "symbol", "task"},

	"run": {"level", "root"},

	"approve":  {"approver", "prefix", "reason", "reject", "root", "scope"},
	"approval": {"approver", "reason", "root", "timeout"},
	"policy":   {"file", "json", "merge", "root"},

	"evidence": {"agent-id", "expect-fingerprint", "file", "full-state", "out", "restore", "root", "sign", "task", "url"},

	"reject": {"approver", "reason", "root"},

	"verify": {"anchor", "changed", "changed-since", "command", "cve", "eval", "fast", "full", "json", "license", "output", "root", "scan", "secrets", "short", "skill", "staged", "timeout", "types", "verify-pipeline", "verify-silent", "verify-token-reduction"},

	"guard":       {"agent-id", "check", "duration", "file", "force", "json", "precision", "range", "reason", "require-boundaries", "root", "sarif", "scope", "task", "threshold"},
	"sec":         {"engine", "json", "max", "root", "severity"},
	"fingerprint": {"file", "json", "root"},
	"audit":       {"file", "json", "keep", "limit", "root", "tail"},

	// docgen
	"gen-contracts": {"check", "root"},
	"gen-docs":      {"check", "doc", "root"},
	"gen-catalog":   {"check", "root"},
	"gen-plugin":    {"check", "root"},

	"daemon": {"root"},

	"heal": {"force", "llm", "task", "yes"},

	"mcp":        {"category", "http", "json", "project", "tls-cert", "tls-key"},
	"mcp-client": {"root", "url"},

	"meta": {"pipeline", "root"},
	"ask":  {"pipeline", "root"},

	"hook":   {"global", "range"},
	"commit": {"all", "dry-run", "message"},
}

var commandRawArgs = map[string]bool{}

func runStatsEntry(cmd string, rest []string) int {
	if cmd == "stats" && len(rest) > 0 && rest[0] == "performance" {
		f, _ := parseFlagsOrDie(rest[1:])
		out, err := runStatsPerformance(f.reset, f.json)
		if err != nil {
			fatal("dispatchCommand: %v", err)
		}
		fmt.Println(out)
		return 0
	}
	runStats(cmd, rest)
	return 0
}

const packDefaultMaxTokens = 250_000

func packArgs(rest []string) ([]string, bool) {
	for _, a := range rest {
		if a == "--max-tokens" || strings.HasPrefix(a, "--max-tokens=") {
			return rest, false
		}
	}
	return append(rest, "--max-tokens", strconv.Itoa(packDefaultMaxTokens)), true
}

func runPackCapped(rest []string) {
	args, injected := packArgs(rest)
	runPack(args)
	if injected {
		fmt.Fprintf(os.Stderr, "kern: pack budget defaults to %d tokens; rerun with --max-tokens N to raise it (--max-tokens 0 for unlimited)\n", packDefaultMaxTokens)
	}
}
