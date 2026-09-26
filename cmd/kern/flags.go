package main

import (
	"fmt"
	"strconv"
	"strings"
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
	llm                  string
	bpe                  bool
	root                 string
	level                string
	taskType             string
	check                bool
	ci                   bool // --ci (check: machine-readable CI verdict on stdout, exit 0/1)
	verify               bool
	detect               bool
	global               bool
	globalRules          bool   // --global-rules (setup: manage kern rules in each host's GLOBAL instructions slot)
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
	changelog            string // --changelog (commitmsg: render a release-notes draft for a git range)
	changelogSet         bool   // true when --changelog was given (even with an empty value)
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
	semantic             bool
	lang                 string
	stdin                string
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
	cve                  bool // --cve (verify: govulncheck vulnerability check)
	license              bool // --license (verify: deterministic license classifier)
	secrets              bool // --secrets (verify: committed-secret history scan)
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
//	synthesize-test:                  --target "" "target function or method name"; --file "" "target file path"; --auto-gap false "auto-select top untested hotspot"; --apply false "write synthesized test to test file"; --json false "output JSON format"; --root "." "project root"
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
func parseFlags(args []string) (flags, []string, error) {
	var f flags
	f.days = 7
	f.timeout = 120
	f.depth = -1
	f.commits = 60
	f.thresholds = "2.0,4.0,6.0,8.0"
	// --cache defaults ON for optimize/preview, aligning with the MCP
	// kern_optimize_prompt tool (cacheOn=true): results compound in the
	// local exact + semantic caches unless the operator opts out with
	// --cache=false.
	f.cache = true
	// Migrated FlagSet defaults that are not zero-valued (per-command flags
	// whose default is only read by the owning command, so a shared default
	// cannot leak across commands).
	f.keepTasks = 20
	f.k = "5"
	f.halfLife = "7.0"
	f.format = "text"
	f.chunkSize = "1000"
	f.ttl = "300"
	f.injectMemory = "true"
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

	// take advances the parser past the current token and returns the next
	// one; ok=false at end of args (the documented accept-and-default
	// semantic: a trailing value-less flag is left at its default).
	take := func(i *int) (string, bool) {
		*i++
		if *i < len(args) {
			return args[*i], true
		}
		return "", false
	}
	// setStr consumes the value of a string flag: the inline --flag=value
	// form when present, else the next argument (accept-and-default when
	// none).
	setStr := func(i *int, dst *string, inline string, hasInline bool) {
		if hasInline {
			*dst = inline
			return
		}
		if v, ok := take(i); ok {
			*dst = v
		}
	}
	// setIntFlag consumes the value of an int flag through setInt (error
	// sticky via parseErr).
	setIntFlag := func(i *int, dst *int, name, inline string, hasInline bool) {
		if hasInline {
			setInt(dst, inline, name)
			return
		}
		if v, ok := take(i); ok {
			setInt(dst, v, name)
		}
	}
	// setBool sets a bool flag: --flag, --flag=true or --flag=false (the
	// stdlib flag package's bool forms, migrated verbatim).
	setBool := func(dst *bool, name, inline string, hasInline bool) {
		if hasInline {
			b, err := strconv.ParseBool(inline)
			if err != nil {
				parseErr = fmt.Errorf("%s: invalid boolean value %q", name, inline)
				return
			}
			*dst = b
		} else {
			*dst = true
		}
	}
	for i := 0; i < len(args); i++ {
		name, inline, hasInline := splitFlag(args[i])
		// Single-dash long flags (`-json`, `-root`, `-addr`) are accepted
		// by normalizing them to their double-dash form (`--json`, ...), so
		// both spellings work like Go tooling users expect. Only tokens
		// that look like a single-dash letter flag (isFlagToken logic) and
		// are not the short help form `-h` are rewritten; `-k`/`-terse-code`
		// normalize to their dual-form cases below, and negative numbers
		// (`-1`) or a bare `-` are untouched (still positionals).
		if len(name) > 1 && name[0] == '-' && name[1] != '-' && isAlpha(name[1]) && name != "-h" {
			name = "--" + name[1:]
		}
		switch name {
		case "--attach":
			setStr(&i, &f.attach, inline, hasInline)
		case "--session":
			setStr(&i, &f.session, inline, hasInline)
		case "--model":
			setStr(&i, &f.model, inline, hasInline)
		case "--days":
			setIntFlag(&i, &f.days, "--days", inline, hasInline)
		case "--dir":
			setStr(&i, &f.dir, inline, hasInline)
		case "--llm":
			setStr(&i, &f.llm, inline, hasInline)
		case "--json":
			setBool(&f.json, "--json", inline, hasInline)
			f.jsonSet = true
		case "--status":
			// Dual-form flag: bool (index: `kern index --status`) AND string
			// (flight list: `kern flight list --status ok|--status=ok`). The
			// next token is never consumed, so `kern index --status <root>`
			// keeps its positional root; flight list falls back to the first
			// positional when no inline value was given.
			// Quirk (documented, gate-8): an inline bool form like
			// `--status=false` sets the bool AND records "false" as the
			// string filter; the old parser rejected inline forms
			// entirely, so this is a new accepted form, not a regression.
			f.status = true
			if hasInline {
				f.statusFilter = inline
			}
		case "--strict":
			setBool(&f.strict, "--strict", inline, hasInline)
		case "--update":
			setBool(&f.update, "--update", inline, hasInline)
		case "--force":
			setBool(&f.force, "--force", inline, hasInline)
		case "--pin":
			setStr(&i, &f.pin, inline, hasInline)
		case "--preflight":
			setStr(&i, &f.preflight, inline, hasInline)
			f.preflightSet = true
		case "--yes":
			setBool(&f.yes, "--yes", inline, hasInline)
		case "--terse-code", "-terse-code":
			setBool(&f.terseCode, "--terse-code", inline, hasInline)
		case "--reset":
			setBool(&f.reset, "--reset", inline, hasInline)
		case "--sarif":
			setBool(&f.sarif, "--sarif", inline, hasInline)
		case "--threshold":
			setIntFlag(&i, &f.threshold, "--threshold", inline, hasInline)
		case "--commits":
			setIntFlag(&i, &f.commits, "--commits", inline, hasInline)
		case "--thresholds":
			setStr(&i, &f.thresholds, inline, hasInline)
		case "--csv":
			setBool(&f.csv, "--csv", inline, hasInline)
		case "--by-tool":
			setBool(&f.byTool, "--by-tool", inline, hasInline)
		case "--by-agent":
			setBool(&f.byAgent, "--by-agent", inline, hasInline)
		case "--bpe":
			setBool(&f.bpe, "--bpe", inline, hasInline)
		case "--root":
			setStr(&i, &f.root, inline, hasInline)
		case "--addr":
			setStr(&i, &f.addr, inline, hasInline)
		case "--enterprise":
			setBool(&f.enterprise, "--enterprise", inline, hasInline)
		case "--project":
			if hasInline {
				f.projects = append(f.projects, inline)
			} else if v, ok := take(&i); ok {
				f.projects = append(f.projects, v)
			}
		case "--pattern":
			setStr(&i, &f.pattern, inline, hasInline)
		case "--query":
			setStr(&i, &f.query, inline, hasInline)
		case "--symbol":
			setStr(&i, &f.symbol, inline, hasInline)
		case "--change":
			setStr(&i, &f.change, inline, hasInline)
		case "--fresh":
			setBool(&f.fresh, "--fresh", inline, hasInline)
		case "--level":
			setStr(&i, &f.level, inline, hasInline)
		case "--task-type":
			setStr(&i, &f.taskType, inline, hasInline)
		case "--lens":
			setStr(&i, &f.lens, inline, hasInline)
		case "--profile":
			setStr(&i, &f.profile, inline, hasInline)
		case "--eval":
			setStr(&i, &f.evalDir, inline, hasInline)
		case "--skill":
			setStr(&i, &f.skillDir, inline, hasInline)
		case "--check":
			setBool(&f.check, "--check", inline, hasInline)
		case "--ci":
			setBool(&f.ci, "--ci", inline, hasInline)
		case "--arch-drift":
			setBool(&f.archDrift, "--arch-drift", inline, hasInline)
		case "--calibration":
			setBool(&f.calibration, "--calibration", inline, hasInline)
		case "--correlate":
			setBool(&f.correlate, "--correlate", inline, hasInline)
		case "--runbook":
			setStr(&i, &f.runbook, inline, hasInline)
		case "--list-playbooks":
			setBool(&f.listPlaybooks, "--list-playbooks", inline, hasInline)
		case "--code":
			setBool(&f.code, "--code", inline, hasInline)
		case "--verify":
			setBool(&f.verify, "--verify", inline, hasInline)
		case "--verify-pipeline":
			setBool(&f.verifyPipeline, "--verify-pipeline", inline, hasInline)
		case "--verify-silent":
			setBool(&f.verifySilent, "--verify-silent", inline, hasInline)
		case "--verify-token-reduction":
			setBool(&f.verifyTokenReduction, "--verify-token-reduction", inline, hasInline)
		case "--scan":
			setStr(&i, &f.scanPath, inline, hasInline)
		case "--types":
			setStr(&i, &f.types, inline, hasInline)
		case "--cve":
			setBool(&f.cve, "--cve", inline, hasInline)
		case "--license":
			setBool(&f.license, "--license", inline, hasInline)
		case "--secrets":
			setBool(&f.secrets, "--secrets", inline, hasInline)
		case "--detect":
			setBool(&f.detect, "--detect", inline, hasInline)
		case "--global":
			setBool(&f.global, "--global", inline, hasInline)
		case "--global-rules":
			setBool(&f.globalRules, "--global-rules", inline, hasInline)
		case "--agents-md":
			setStr(&i, &f.agentsMD, inline, hasInline)
		case "--apply":
			setBool(&f.apply, "--apply", inline, hasInline)
		case "--runtime":
			setBool(&f.runtime, "--runtime", inline, hasInline)
		case "--agents":
			setStr(&i, &f.agents, inline, hasInline)
		case "--file":
			// Single-value readers use .file (last value); policy-dsl's
			// repeatable form accumulates every value in .files.
			if hasInline {
				f.file = inline
				f.files = append(f.files, inline)
			} else if v, ok := take(&i); ok {
				f.file = v
				f.files = append(f.files, v)
			}
		case "--task":
			setStr(&i, &f.task, inline, hasInline)
		case "--budget":
			if hasInline {
				setInt(&f.budget, inline, "--budget")
				f.budgetSet = true
			} else if v, ok := take(&i); ok {
				setInt(&f.budget, v, "--budget")
				f.budgetSet = true
			}
		case "--uninstall":
			setBool(&f.hostUninstall, "--uninstall", inline, hasInline)
		case "--agent-id":
			setStr(&i, &f.agentID, inline, hasInline)
		case "--mermaid":
			setBool(&f.mermaid, "--mermaid", inline, hasInline)
		case "--repos":
			setBool(&f.repos, "--repos", inline, hasInline)
		case "--mask":
			setBool(&f.mask, "--mask", inline, hasInline)
		case "--names":
			setStr(&i, &f.names, inline, hasInline)
		case "--schema":
			setStr(&i, &f.schema, inline, hasInline)
		case "--cmd":
			setStr(&i, &f.cmd, inline, hasInline)
		case "--timeout":
			if hasInline {
				setInt(&f.timeout, inline, "--timeout")
				f.timeoutSet = true
			} else if v, ok := take(&i); ok {
				setInt(&f.timeout, v, "--timeout")
				f.timeoutSet = true
			}
		case "--wait":
			if hasInline {
				setInt(&f.wait, inline, "--wait")
			} else if v, ok := take(&i); ok {
				setInt(&f.wait, v, "--wait")
			}
		case "--cache":
			setBool(&f.cache, "--cache", inline, hasInline)
		case "--fewshot":
			setBool(&f.fewshot, "--fewshot", inline, hasInline)
		case "--mode":
			setStr(&i, &f.mode, inline, hasInline)
		case "--merge":
			setBool(&f.merge, "--merge", inline, hasInline)
		case "--schedule":
			setStr(&i, &f.schedule, inline, hasInline)
		case "--with-skill":
			setStr(&i, &f.withSkill, inline, hasInline)
		case "--kind":
			setStr(&i, &f.kind, inline, hasInline)
		case "--one-line":
			setBool(&f.oneLine, "--one-line", inline, hasInline)
		case "--entities":
			setBool(&f.entities, "--entities", inline, hasInline)
		case "--risk":
			setBool(&f.risk, "--risk", inline, hasInline)
		case "--sinks":
			setStr(&i, &f.sinks, inline, hasInline)
		case "--once":
			setBool(&f.once, "--once", inline, hasInline)
		case "--semantic":
			setBool(&f.semantic, "--semantic", inline, hasInline)
		case "--bridges-only":
			setBool(&f.bridgesOnly, "--bridges-only", inline, hasInline)
		case "--interval":
			setIntFlag(&i, &f.interval, "--interval", inline, hasInline)
		case "--http":
			setStr(&i, &f.http, inline, hasInline)
		case "--tls-cert":
			setStr(&i, &f.tlsCert, inline, hasInline)
		case "--tls-key":
			setStr(&i, &f.tlsKey, inline, hasInline)
		case "--hold":
			setBool(&f.hold, "--hold", inline, hasInline)
		case "--graphml":
			setBool(&f.graphml, "--graphml", inline, hasInline)
		case "--cypher":
			setBool(&f.cypher, "--cypher", inline, hasInline)
		case "--html":
			setBool(&f.html, "--html", inline, hasInline)
		case "--out":
			setStr(&i, &f.out, inline, hasInline)
		case "--compact":
			setBool(&f.compact, "--compact", inline, hasInline)
		case "--all":
			setBool(&f.all, "--all", inline, hasInline)
		case "--clear":
			setBool(&f.clear, "--clear", inline, hasInline)
		case "--max":
			setIntFlag(&i, &f.max, "--max", inline, hasInline)
		case "--limit":
			setIntFlag(&i, &f.limit, "--limit", inline, hasInline)
		case "--range":
			setStr(&i, &f.range_, inline, hasInline)
		case "--changelog":
			setStr(&i, &f.changelog, inline, hasInline)
			f.changelogSet = true
		case "--lines", "--line":
			// --line is the documented singular form (lsp-bridge help); the
			// parser previously rejected it because only --lines existed.
			// evidence-anchor reads the same flag as a STRING (line number):
			// the raw value is captured in .lineRaw while .lines stays the
			// int form the lsp/context commands consume.
			if hasInline {
				setInt(&f.lines, inline, "--line")
				f.lineRaw = inline
			} else if v, ok := take(&i); ok {
				setInt(&f.lines, v, "--line")
				f.lineRaw = v
			}
		case "--depth":
			setIntFlag(&i, &f.depth, "--depth", inline, hasInline)
		case "--full":
			setBool(&f.full, "--full", inline, hasInline)
		case "--from":
			setStr(&i, &f.from, inline, hasInline)
		case "--to":
			setStr(&i, &f.to, inline, hasInline)
		case "--severity":
			setStr(&i, &f.severity, inline, hasInline)
		case "--lang":
			setStr(&i, &f.lang, inline, hasInline)
		case "--stdin":
			setStr(&i, &f.stdin, inline, hasInline)
		case "--no-instructions":
			setBool(&f.noinstructions, "--no-instructions", inline, hasInline)
		case "--max-tokens":
			setIntFlag(&i, &f.maxTokens, "--max-tokens", inline, hasInline)
		case "--max-files":
			setIntFlag(&i, &f.maxFiles, "--max-files", inline, hasInline)
		case "--context-before":
			setIntFlag(&i, &f.contextBefore, "--context-before", inline, hasInline)
		case "--context-after":
			setIntFlag(&i, &f.contextAfter, "--context-after", inline, hasInline)
		case "--tier":
			setStr(&i, &f.tier, inline, hasInline)
		case "--precision":
			setStr(&i, &f.precision, inline, hasInline)
		case "--min-confidence":
			setStr(&i, &f.minConfidence, inline, hasInline)
		case "--fold":
			setBool(&f.fold, "--fold", inline, hasInline)
		case "--explain":
			setBool(&f.explain, "--explain", inline, hasInline)
		case "--graph":
			setBool(&f.graph, "--graph", inline, hasInline)
		case "--generate":
			setBool(&f.generate, "--generate", inline, hasInline)
		case "--staged":
			setBool(&f.staged, "--staged", inline, hasInline)
		case "--subject":
			setBool(&f.subject, "--subject", inline, hasInline)
		case "--message":
			setStr(&i, &f.message, inline, hasInline)
		case "--name":
			setStr(&i, &f.name, inline, hasInline)
		case "--dry-run":
			setBool(&f.dryRun, "--dry-run", inline, hasInline)
		case "--approver":
			setStr(&i, &f.approver, inline, hasInline)
		case "--version":
			setStr(&i, &f.version, inline, hasInline)
		case "--reject":
			setBool(&f.reject, "--reject", inline, hasInline)
		case "--reason":
			setStr(&i, &f.reason, inline, hasInline)
		case "--action":
			setStr(&i, &f.action, inline, hasInline)
		case "--column", "--col":
			setIntFlag(&i, &f.column, "--column", inline, hasInline)
		case "--compiler-output":
			setStr(&i, &f.compilerOutput, inline, hasInline)
		case "--edits":
			setStr(&i, &f.edits, inline, hasInline)
		case "--server-cmd":
			setStr(&i, &f.serverCmd, inline, hasInline)
		case "--target":
			setStr(&i, &f.target, inline, hasInline)
		case "--min-fixes":
			setIntFlag(&i, &f.minFixes, "--min-fixes", inline, hasInline)
		// ---- Migrated FlagSet flags (unified parser) ----
		case "--agent":
			setStr(&i, &f.agent, inline, hasInline)
		case "--auto-gap":
			setBool(&f.autoGap, "--auto-gap", inline, hasInline)
		case "--base":
			setStr(&i, &f.base, inline, hasInline)
		case "--body":
			setStr(&i, &f.body, inline, hasInline)
		case "--channel":
			setStr(&i, &f.channel, inline, hasInline)
		case "--chunk-size":
			setStr(&i, &f.chunkSize, inline, hasInline)
		case "--claim":
			setStr(&i, &f.claim, inline, hasInline)
		case "--deny-path":
			if hasInline {
				f.denyPaths = append(f.denyPaths, inline)
			} else if v, ok := take(&i); ok {
				f.denyPaths = append(f.denyPaths, v)
			}
		case "--diff":
			setStr(&i, &f.diff, inline, hasInline)
		case "--expect-fingerprint":
			setStr(&i, &f.expectFingerprint, inline, hasInline)
		case "--field":
			setStr(&i, &f.field, inline, hasInline)
		case "--field-type":
			setStr(&i, &f.fieldType, inline, hasInline)
		case "--format":
			setStr(&i, &f.format, inline, hasInline)
		case "--half-life":
			setStr(&i, &f.halfLife, inline, hasInline)
		case "--interface":
			setStr(&i, &f.iface, inline, hasInline)
		case "--inject-memory":
			setStr(&i, &f.injectMemory, inline, hasInline)
		case "--k", "-k":
			setStr(&i, &f.k, inline, hasInline)
		case "--keep-tasks":
			setIntFlag(&i, &f.keepTasks, "--keep-tasks", inline, hasInline)
		case "--key":
			setStr(&i, &f.key, inline, hasInline)
		case "--local":
			setStr(&i, &f.local, inline, hasInline)
		case "--notes":
			setStr(&i, &f.notes, inline, hasInline)
		case "--older-than":
			setStr(&i, &f.olderThan, inline, hasInline)
		case "--payload":
			setStr(&i, &f.payload, inline, hasInline)
		case "--percent":
			setStr(&i, &f.percent, inline, hasInline)
		case "--pipeline":
			setStr(&i, &f.pipeline, inline, hasInline)
		case "--policy":
			setStr(&i, &f.policy, inline, hasInline)
		case "--progress-token":
			setStr(&i, &f.progressToken, inline, hasInline)
		case "--prompt":
			setStr(&i, &f.prompt, inline, hasInline)
		case "--remote":
			setStr(&i, &f.remote, inline, hasInline)
		case "--repo":
			if hasInline {
				f.repoPaths = append(f.repoPaths, inline)
			} else if v, ok := take(&i); ok {
				f.repoPaths = append(f.repoPaths, v)
			}
		case "--resource":
			setStr(&i, &f.resource, inline, hasInline)
		case "--role":
			setStr(&i, &f.role, inline, hasInline)
		case "--sign":
			setBool(&f.sign, "--sign", inline, hasInline)
		case "--full-state":
			setBool(&f.fullState, "--full-state", inline, hasInline)
		case "--restore":
			setStr(&i, &f.restore, inline, hasInline)
		case "--sig":
			setStr(&i, &f.sig, inline, hasInline)
		case "--tag":
			setStr(&i, &f.tag, inline, hasInline)
		case "--template":
			setStr(&i, &f.template, inline, hasInline)
		case "--text":
			setStr(&i, &f.text, inline, hasInline)
		case "--tool":
			setStr(&i, &f.tool, inline, hasInline)
		case "--ttl":
			setStr(&i, &f.ttl, inline, hasInline)
		case "--url":
			setStr(&i, &f.url, inline, hasInline)
		case "--help", "-h":
			f.help = true
		default:
			arg := args[i]
			// A token is treated as a flag if it is `--` followed by more
			// characters (e.g. `--bogus`) or a single `-` followed by a letter
			// (e.g. `-x`). Negative numbers (`-1`), a bare `-` (stdin), and
			// plain positionals are NOT flags and still go to rest.
			if isFlagToken(arg) {
				parseErr = fmt.Errorf("unknown flag: %s", name)
			} else {
				rest = append(rest, arg)
			}
		}
	}
	return f, rest, parseErr
}

// isFlagToken reports whether arg looks like a CLI flag: `--` followed by
// more characters, or a single `-` followed by an ASCII letter. Negative
// numbers, a bare "-" (stdin) and plain positionals are not flags.
func isFlagToken(arg string) bool {
	return len(arg) > 2 && strings.HasPrefix(arg, "--") ||
		len(arg) > 1 && arg[0] == '-' && isAlpha(arg[1])
}

// splitFlag splits a flag token into (name, inlineValue, hasInlineValue):
// `--flag=value` becomes ("--flag", "value", true). Non-flag tokens (and
// flag tokens without '=') pass through unsplit.
func splitFlag(arg string) (name, val string, hasVal bool) {
	if isFlagToken(arg) {
		if eq := strings.IndexByte(arg, '='); eq >= 0 {
			return arg[:eq], arg[eq+1:], true
		}
	}
	return arg, "", false
}

// isAlpha reports whether b is an ASCII letter.
func isAlpha(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// stdlibFlagErr renders a parseFlags error in the stdlib flag package's
// phrasing for the few handlers whose stderr message is pinned to that
// format (runSynthesizeTest: TestRunSynthesizeTestBadFlagExits2 asserts the
// "flag provided but not defined" wording). Parsing is still fully unified
// through parseFlags — only the error message rendering differs.
func stdlibFlagErr(err error) error {
	if err == nil {
		return nil
	}
	if rest, ok := strings.CutPrefix(err.Error(), "unknown flag: "); ok {
		return fmt.Errorf("flag provided but not defined: %s", rest)
	}
	return err
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
