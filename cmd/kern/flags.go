package main

import (
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
)

// flags holds the parsed command-line flags shared across subcommands.
// Each subcommand handler reads only the fields it needs. Keeping the struct
// and its parser in their own file (SRP/OCP) means adding a flag never
// requires touching main.go — subcommands and the dispatcher stay decoupled.
//
// This struct is the SINGLE flag surface of the CLI: the legacy
// flag.NewFlagSet parsers (29 instances across 6 files) were migrated here,
// so every subcommand flag — including the MCP-tool mirror commands
// (compose, pre-edit, prompt-fill, ...), evidence, runtime, flight,
// authorize-context and register-host-sampler — parses through parseFlags.
// The migrated flag contracts (name, default, help) are preserved verbatim
// in the parseFlags() comment block below.
type flags struct {
	attach               string
	session              string
	model                string
	days                 int
	json                 bool
	dir                  string
	csv                  bool
	byTool               bool // --by-tool (stats: per-tool token ledger table)
	byAgent              bool // --by-agent (stats: per-agent token ledger table)
	byRoot               bool // --by-root (stats: per-project usage diagnostics)
	llm                  string
	bpe                  bool
	root                 string
	level                string
	taskType             string
	check                bool
	ci                   bool // --ci (check: machine-readable CI verdict on stdout, exit 0/1)
	verify               bool
	detect               bool
	allAgents            bool // --all-agents (setup: wire every supported agent, not only the detected ones)
	plain                bool // --plain (setup: legacy plain-text output, no boxes/glyphs/colour)
	globalOnly           bool // --global-only (setup: wire user-level MCP/hooks/rules only; write no file in any repo)
	remove               bool // --remove (setup: undo the machine-wide wiring; kern-owned entries only)
	global               bool
	globalRules          bool   // --global-rules (setup: manage kern rules in each host's GLOBAL instructions slot)
	indexOnly            bool   // --index-only (onboard: skip ALL agent wiring, build the index only)
	agentsMD             string // --agents-md (setup: repo AGENTS.md variant, thin|full; persisted in .kern/config.json)
	apply                bool
	runtime              bool
	agents               string
	file                 string
	task                 string
	budget               int
	hostUninstall        bool
	agentID              string
	mermaid              bool
	all                  bool
	clear                bool
	max                  int
	limit                int
	lines                int
	depth                int
	range_               string
	commits              int
	thresholds           string
	graphml              bool
	cypher               bool
	html                 bool
	out                  string
	repos                bool
	mask                 bool
	names                string
	cache                bool
	schema               string
	cmd                  string
	timeout              int
	timeoutSet           bool
	timeoutDur           string // --timeout duration-string form (30m, 500ms); integer-seconds values stay in .timeout
	wait                 int
	fewshot              bool
	mode                 string
	schedule             string // --schedule (loop: cron expression for scheduled closed-loop runs; "" = one-shot)
	withSkill            string
	once                 bool
	interval             int
	http                 string
	tlsCert              string
	tlsKey               string
	hold                 bool
	sarif                bool
	threshold            int
	severity             string
	engine               string
	namespace            string
	textA                string
	textB                string // --engine (sec: internal|gosec|all|none; default all = internal + gosec when available)
	semantic             bool
	lang                 string
	stdin                string
	execOutput           string // --output (exec: summary|tail:N|lines:A-B|full; the kern_verify contract)
	execAnchor           string // --anchor (exec: re-slice a kept run without re-executing)
	bridgesOnly          bool
	noinstructions       bool
	maxTokens            int
	maxFiles             int
	tier                 string
	graph                bool
	precision            string
	minConfidence        string
	fold                 bool
	explain              bool
	staged               bool
	compact              bool
	subject              bool
	message              string
	dryRun               bool
	reset                bool
	net                  bool // --net (stats): report net savings after rules-injection + slice re-read costs
	name                 string
	pattern              string
	query                string
	symbol               string
	change               string
	fresh                bool
	from                 string
	to                   string
	full                 bool
	generate             bool
	help                 bool
	approver             string
	reject               bool
	reason               string
	version              string
	status               bool
	strict               bool
	update               bool
	force                bool
	pin                  string // --pin (update: pin the target release tag, e.g. v0.9.9.1; the deliberate-downgrade consent)
	preflight            string // --preflight (update: hidden decision-only mode for install.sh — not in any help text)
	preflightSet         bool   // true when --preflight was given (even with an empty value)
	yes                  bool
	addr                 string
	enterprise           bool
	projects             []string
	terseCode            bool
	lens                 string
	profile              string
	evalDir              string
	skillDir             string
	verifyPipeline       bool
	verifySilent         bool
	verifyTokenReduction bool
	scanPath             string
	types                string
	cve                  bool   // --cve (verify: govulncheck vulnerability check)
	license              bool   // --license (verify: deterministic license classifier)
	secrets              bool   // --secrets (verify: committed-secret history scan)
	short                bool   // --short (verify: run the test step with `go test -short`; the default)
	changed              bool   // --changed (verify: scope the test step to packages with changes vs HEAD)
	changedSince         string // --changed-since (verify: scope the test step to packages changed since a git ref)
	fast                 bool   // --fast (verify: pre-commit tier — build + changed-package tests only, keeps -short)
	action               string
	column               int
	compilerOutput       string
	edits                string
	serverCmd            string
	target               string
	minFixes             int
	contextBefore        int    // --context-before (optimize log adaptive windowing)
	contextAfter         int    // --context-after  (optimize log adaptive windowing)
	kind                 string // --kind (optimize: prompt|log)
	etag                 string // --etag (context/compact/retrieve/explore: conditional-fetch etag from a previous response)
	oneLine              bool   // --one-line (graph: single-line call-graph neighbourhood)
	entities             bool   // --entities (graph: render the digital-twin entity nodes connected to the symbol, or the repo entity inventory without a symbol)
	risk                 bool   // --risk (impact: render the governance risk assessment)
	sinks                string // --sinks (synthesize-test: comma-separated security sink rule ids)
	archDrift            bool   // --arch-drift (doctor: report ARCHITECTURE.md LOC/deps drift section)
	calibration          bool   // --calibration (doctor: report prediction-vs-reality calibration health section)
	correlate            bool   // --correlate (incident: run the incident→twin→code correlation engine)
	runbook              string // --runbook (incident: JSON of a heal playbook to add)
	listPlaybooks        bool   // --list-playbooks (incident: list stored heal playbooks)
	code                 bool   // --code (correlate: include the incident→twin→code correlation section)
	merge                bool   // --merge (policy set: merge by policy ID instead of replacing the whole set)

	// ---- FlagSet-migrated flags (unified parser). Each field backs one or
	// more subcommand flags that previously used stdlib flag.FlagSet; names,
	// defaults and help text are preserved exactly (see parseFlags doc). ----
	agent             string   // --agent (authorize-context, agent-fingerprint, agent-coordination, agent-role-rbac)
	autoGap           bool     // --auto-gap (synthesize-test)
	auto              bool     // --auto (memory remember/add: tag the lesson as an automatic capture; memory prune: confirm removing auto captures)
	base              string   // --base (semantic-merge)
	body              string   // --body (ast-transform)
	budgetSet         bool     // true when --budget was given (context-watch string form)
	channel           string   // --channel (stream; update: release channel — stable|latest|regex, forwarded as KERN_CHANNEL; --pin overrides)
	chunkSize         string   // --chunk-size (stream)
	claim             string   // --claim (evidence-anchor)
	denyPaths         []string // --deny-path (authorize-context, repeatable)
	diff              string   // --diff (policy-dsl)
	expectFingerprint string   // --expect-fingerprint (evidence verify)
	field             string   // --field (ast-transform)
	fieldType         string   // --field-type (ast-transform)
	files             []string // --file repeatable accumulation (policy-dsl); single-use readers use .file (last value)
	format            string   // --format (context-watch, agent-fingerprint)
	halfLife          string   // --half-life (memory-ranked)
	iface             string   // --interface (ast-transform; "interface" is a Go keyword)
	injectMemory      string   // --inject-memory (prompt-fill)
	jsonSet           bool     // true when --json was given (authorize-context default-true)
	k                 string   // --k / -k (memory-ranked)
	keepTasks         int      // --keep-tasks (flight gc)
	key               string   // --key (register-host-sampler)
	lineRaw           string   // --line raw value (evidence-anchor string form)
	local             string   // --local (semantic-merge)
	notes             string   // --notes (agent-coordination)
	olderThan         string   // --older-than (flight gc)
	payload           string   // --payload (stream)
	percent           string   // --percent (stream)
	pipeline          string   // --pipeline (compose)
	policy            string   // --policy (policy-dsl)
	progressToken     string   // --progress-token (stream)
	prompt            string   // --prompt (memory-ranked)
	remote            string   // --remote (semantic-merge)
	repoPaths         []string // --repo (cross-repo-impact, repeatable)
	resource          string   // --resource (agent-coordination)
	role              string   // --role (agent-role-rbac)
	sign              bool     // --sign (evidence export)
	fullState         bool     // --full-state (evidence: export the full evidence store state)
	restore           string   // --restore (evidence: restore the store from a full-state bundle file)
	sig               string   // --sig (ast-transform)
	statusFilter      string   // --status string form (flight list); .status stays the bool form (index)
	tag               string   // --tag (ast-transform)
	template          string   // --template (prompt-fill)
	text              string   // --text (context-watch)
	tool              string   // --tool (agent-role-rbac)
	ttl               string   // --ttl (agent-coordination)
	url               string   // --url (evidence verify/explain)
}

// parseFlags is the SINGLE CLI flag parser. Every subcommand — including the
// 29 migrated stdlib flag.FlagSet parsers — routes through here. It returns
// the parsed flags, the positional arguments (rest), and the first parse
// error (sticky: only the first error is reported).
//
// Documented semantics (behavioral contract, review-pinned):
//
//  1. TRAILING VALUE-LESS FLAGS ARE ACCEPT-AND-DEFAULT. A value flag at the
//     end of the line with no value (e.g. `kern pack --out`) is accepted and
//     left at its default — it is NOT an error and NOT silently swallowed as
//     a positional. This is the deliberate unified semantic (chosen over
//     reject-with-usage because the stdlib flag.FlagSet contract it replaces
//     varies per command and the pinned tests assert accept-and-default);
//     pinned by TestParseFlagsTrailingValueLessFlag and
//     TestParseFlagsHelpAndMissingValue.
//
//  2. --flag=value AND --flag value FORMS ARE BOTH ACCEPTED for value flags;
//     bool flags accept --flag, --flag=true and --flag=false.
//
//  3. Unknown flags (any `--x`/`-x` token not in the switch) are rejected
//     with "unknown flag: X" (fail loud, usage error, exit 2 via the
//     handler's fatalUsage). Negative numbers and a bare "-" (stdin) are
//     positionals, not flags.
//
//  4. Invalid integer values fail loud: "FLAG: invalid integer \"V\"".
//     First error wins; later values are not evaluated.
//
//  5. Positionals keep their order in rest; the parser does NOT stop at the
//     first positional (unlike stdlib flag), so flags may appear before or
//     after positionals.
//
// Migrated flag.FlagSet contracts, preserved verbatim (name / default /
// help text) from the six files that previously owned FlagSets:
//
//	health (cmd_mcp_tools.go):        --root "." "project root"; --json false "emit JSON (default; health output is always JSON)"
//	compose:                          --root "." "project root"; --pipeline "" "pipeline JSON string"; --timeout "60" "per-step timeout in seconds"
//	pre-edit:                         --file "" "file path"; --lines 0 "line number (integer; invalid values exit 2)"; --symbol "" "symbol name"; --root "." "project root"; --json false "emit JSON output"
//	prompt-fill:                      --template "" "template name"; --task "" "task description"; --file "" "target file path"; --inject-memory "true" "inject memory"; --root "." "project root"
//	semantic-diff:                    --from "" "from revision"; --to "" "to revision"; --range "" "git revision range"; --root "." "project root"
//	evidence-anchor:                  --claim "" "claim or citation"; --file "" "file path"; --line "" "line number"; --symbol "" "symbol name"; --root "." "project root"
//	context-watch:                    --budget "32000" "token budget"; --format "text" "output format"; --text "" "context text"
//	agent-fingerprint:                --agent "" "agent id"; --format "text" "output format"
//	explain:                          --target "" "target symbol or file"; --root "." "project root"
//	cross-repo-impact:                --target "" "target symbol"; --symbol "" "target symbol (alias for --target)"; --root "." "project root"; --repo (repeatable) "linked repo path (repeatable)"
//	memory-ranked:                    --prompt "" "task prompt"; --k "5" "max lessons"; --half-life "7.0" "half-life in days"; --root "." "project root"
//	policy-dsl:                       --policy "" "policy YAML/JSON or file path"; --diff "" "diff string"; --root "." "project root"; --file (repeatable) "changed file (repeatable)"
//	agent-coordination:               --action "status" "action: handoff, claim, release, inbox, status"; --agent "" "agent id"; --from "" "from agent"; --to "" "to agent"; --task "" "task id"; --resource "" "resource name"; --ttl "300" "TTL in seconds"; --notes "" "notes"; --root "." "project root"
//	agent-role-rbac:                  --action "evaluate" "action: evaluate, roles, assign, check"; --agent "" "agent id"; --role "" "role name"; --tool "" "tool name"; --root "." "project root"
//	stream:                           --action "status" "action: status, chunk, channels, emit"; --channel "" "channel name"; --payload "" "payload string"; --chunk-size "1000" "chunk size"; --progress-token "" "progress token"; --percent "" "percent"; --message "" "message"
//	ast-transform:                    --action "implement_interface" "action: implement_interface, add_field, add_method"; --file "" "target file path"; --target "" "target symbol/struct name"; --interface "" "interface to implement"; --field "" "field name"; --field-type "" "field type"; --tag "" "struct field tag"; --sig "" "method signature"; --body "" "method body"; --apply false "apply edits to file"; --root "." "project root"
//	semantic-merge:                   --file "" "target file path"; --base "" "base version code or file path"; --local "" "local version code or file path"; --remote "" "remote version code or file path"; --apply false "apply clean merge to file"; --json false "output JSON format"; --root "." "project root"
//	synthesize-test:                  a source is required: a positional file (or --file), or --auto-gap; --target "" "target function or method name"; --file "" "target file path"; --auto-gap false "auto-select top untested hotspot"; --apply false "write synthesized test to test file"; --json false "output JSON format"; --root "." "project root"
//	runtime drift/status:             --root "." "project root"; --json false "emit JSON"
//	evidence export:                  --root "." "project root"; --agent-id "default" "agent ID the authorization is scoped to"; --task "" "task ID the authorization is scoped to"; --out "-" "output path (\"-\" = stdout)"; --json true "emit JSON (the only form; default true)"; --sign false "sign the bundle with the project key (.kern/keys/, created on first use)"
//	evidence verify:                  --file "" "bundle JSON file (default: read from stdin)"; --url "" "bundle URL to fetch and verify without cloning"; --root "" "repo root to verify the audit chain against (default: bundle's repo_root)"; --expect-fingerprint "" "require the bundle to be signed by this key fingerprint (the trust anchor)"
//	evidence explain:                 --file "" "bundle JSON file (default: read from stdin)"; --url "" "bundle URL to fetch and explain without cloning"
//	evidence full-state:              --root "." "project root holding .kern/evidence"; --out "-" "output path (\"-\" = stdout)"
//	evidence restore:                 --root "." "project root holding .kern/evidence"; --restore "" "full-state bundle file to restore into the store"
//	authorize-context:                --agent "" "agent ID to authorize (required)"; --task "" "task ID the authorization is scoped to (required)"; --root "." "project root"; --symbol "" "optional substring filter applied to allowed symbols"; --deny-path (repeatable) "path prefix denied by the task scope (repeatable)"; --json true "emit JSON (default true)"
//	flight list:                      --root "." "project root holding .kern/flight"; --agent "" "filter by agent id"; --task "" "filter by task id"; --status "" "filter by record status (ok|error|blocked|denied)"; --json false "emit JSON lines"
//	flight show:                      --root "." "project root holding .kern/flight"
//	flight tasks:                     --root "." "project root holding .kern/flight"; --json false "emit JSON"
//	flight gc:                        --root "." "project root holding .kern/flight"; --keep-tasks 20 "retain the N most recently active task trails (0 disables)"; --older-than "" "retain tasks active within this duration (e.g. 30d, 720h)"
//	register-host-sampler:            --key "" "registration key (default: this connection's slot)"; --timeout "" "per-call timeout in seconds (default 180)"; --model "" "optional model label"
//
// parseFlags is the SINGLE CLI flag parser. Every subcommand — including the
// 29 migrated stdlib flag.FlagSet parsers — routes through here. It returns
// the parsed flags, the positional arguments (rest), and the first parse
// error (sticky: only the first error is reported).
//
// Documented semantics (behavioral contract, review-pinned):
//
//  1. TRAILING VALUE-LESS FLAGS ARE ACCEPT-AND-DEFAULT. A value flag at the
//     end of the line with no value (e.g. `kern pack --out`) is accepted and
//     left at its default — it is NOT an error and NOT silently swallowed as
//     a positional. This is the deliberate unified semantic (chosen over
//     reject-with-usage because the stdlib flag.FlagSet contract it replaces
//     varies per command and the pinned tests assert accept-and-default);
//     pinned by TestParseFlagsTrailingValueLessFlag and
//     TestParseFlagsHelpAndMissingValue.
//
//  2. --flag=value AND --flag value FORMS ARE BOTH ACCEPTED for value flags;
//     bool flags accept --flag, --flag=true and --flag=false.
//
//  3. Unknown flags (any `--x`/`-x` token not in the switch) are rejected
//     with "unknown flag: X" (fail loud, usage error, exit 2 via the
//     handler's fatalUsage). Negative numbers and a bare "-" (stdin) are
//     positionals, not flags.
//
//  4. Invalid integer values fail loud: "FLAG: invalid integer \"V\"".
//     First error wins; later values are not evaluated.
//
//  5. Positionals keep their order in rest; the parser does NOT stop at the
//     first positional (unlike stdlib flag), so flags may appear before or
//     after positionals.
//
// The case tables moved to flags_parse.go: parseCoreFlag owns every flag
// before the migrated FlagSet marker in the old single switch, and
// parseMigratedFlag owns the 29 migrated FlagSet flags. The loop machinery
// (setInt/take/setStr/setIntFlag/setTimeout/setBool) lives on flagParser in
// flags_parse.go as well.
func parseFlags(args []string) (flags, []string, error) {
	p := &flagParser{}
	p.f.days = 7
	p.f.timeout = 120
	p.f.depth = -1
	p.f.commits = 60
	p.f.thresholds = "2.0,4.0,6.0,8.0"
	// --cache defaults ON for optimize/preview, aligning with the MCP
	// kern_optimize tool (cacheOn=true): results compound in the
	// local exact + semantic caches unless the operator opts out with
	// --cache=false.
	p.f.cache = true
	// Migrated FlagSet defaults that are not zero-valued (per-command flags
	// whose default is only read by the owning command, so a shared default
	// cannot leak across commands).
	p.f.keepTasks = 20
	p.f.k = "5"
	p.f.halfLife = "7.0"
	p.f.format = "text"
	p.f.chunkSize = "1000"
	p.f.ttl = "300"
	p.f.injectMemory = "true"
	for i := 0; i < len(args); i++ {
		name, inline, hasInline := cliflags.SplitFlag(args[i])
		// Single-dash long flags (`-json`, `-root`, `-addr`) are accepted
		// by normalizing them to their double-dash form (`--json`, ...), so
		// both spellings work like Go tooling users expect. Only tokens
		// that look like a single-dash letter flag (cliflags.IsFlagToken logic) and
		// are not the short help form `-h` are rewritten; `-k`/`-terse-code`
		// normalize to their dual-form cases below, and negative numbers
		// (`-1`) or a bare `-` are untouched (still positionals).
		if len(name) > 1 && name[0] == '-' && name[1] != '-' && cliflags.IsAlpha(name[1]) && name != "-h" {
			name = "--" + name[1:]
		}
		if p.parseCoreFlag(name, inline, hasInline, &i, args) {
			continue
		}
		if p.parseMigratedFlag(name, inline, hasInline, &i, args) {
			continue
		}
		switch name {
		case "--help", "-h":
			p.f.help = true
		default:
			arg := args[i]
			// A token is treated as a flag if it is `--` followed by more
			// characters (e.g. `--bogus`) or a single `-` followed by a letter
			// (e.g. `-x`). Negative numbers (`-1`), a bare `-` (stdin), and
			// plain positionals are NOT flags and still go to rest.
			if cliflags.IsFlagToken(arg) {
				p.parseErr = fmt.Errorf("unknown flag: %s", name)
			} else {
				p.rest = append(p.rest, arg)
			}
		}
	}
	return p.f, p.rest, p.parseErr
}
