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
type flags struct {
	attach               string
	session              string
	model                string
	days                 int
	json                 bool
	dir                  string
	csv                  bool
	llm                  string
	bpe                  bool
	root                 string
	level                string
	taskType             string
	check                bool
	verify               bool
	detect               bool
	global               bool
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
	fewshot              bool
	mode                 string
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
	action               string
	column               int
	compilerOutput       string
	edits                string
	serverCmd            string
	target               string
	minFixes             int
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

	// take advances the parser past the current token and returns the next
	// one; ok=false at end of args (the historical behavior: a trailing
	// value flag is silently ignored).
	take := func(i *int) (string, bool) {
		*i++
		if *i < len(args) {
			return args[*i], true
		}
		return "", false
	}
	// setStr consumes the value of a string flag.
	setStr := func(i *int, dst *string) {
		if v, ok := take(i); ok {
			*dst = v
		}
	}
	// setIntFlag consumes the value of an int flag through setInt (error
	// sticky via parseErr).
	setIntFlag := func(i *int, dst *int, name string) {
		if v, ok := take(i); ok {
			setInt(dst, v, name)
		}
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--attach":
			setStr(&i, &f.attach)
		case "--session":
			setStr(&i, &f.session)
		case "--model":
			setStr(&i, &f.model)
		case "--days":
			setIntFlag(&i, &f.days, "--days")
		case "--dir":
			setStr(&i, &f.dir)
		case "--llm":
			setStr(&i, &f.llm)
		case "--json":
			f.json = true
		case "--status":
			f.status = true
		case "--strict":
			f.strict = true
		case "--update":
			f.update = true
		case "--force":
			f.force = true
		case "--terse-code", "-terse-code":
			f.terseCode = true
		case "--reset":
			f.reset = true
		case "--sarif":
			f.sarif = true
		case "--threshold":
			setIntFlag(&i, &f.threshold, "--threshold")
		case "--commits":
			setIntFlag(&i, &f.commits, "--commits")
		case "--thresholds":
			setStr(&i, &f.thresholds)
		case "--csv":
			f.csv = true
		case "--bpe":
			f.bpe = true
		case "--root":
			setStr(&i, &f.root)
		case "--addr":
			setStr(&i, &f.addr)
		case "--enterprise":
			f.enterprise = true
		case "--project":
			if v, ok := take(&i); ok {
				f.projects = append(f.projects, v)
			}
		case "--pattern":
			setStr(&i, &f.pattern)
		case "--query":
			setStr(&i, &f.query)
		case "--symbol":
			setStr(&i, &f.symbol)
		case "--change":
			setStr(&i, &f.change)
		case "--fresh":
			f.fresh = true
		case "--level":
			setStr(&i, &f.level)
		case "--task-type":
			setStr(&i, &f.taskType)
		case "--lens":
			setStr(&i, &f.lens)
		case "--profile":
			setStr(&i, &f.profile)
		case "--eval":
			setStr(&i, &f.evalDir)
		case "--skill":
			setStr(&i, &f.skillDir)
		case "--check":
			f.check = true
		case "--verify":
			f.verify = true
		case "--verify-pipeline":
			f.verifyPipeline = true
		case "--verify-silent":
			f.verifySilent = true
		case "--verify-token-reduction":
			f.verifyTokenReduction = true
		case "--scan":
			setStr(&i, &f.scanPath)
		case "--types":
			setStr(&i, &f.types)
		case "--detect":
			f.detect = true
		case "--global":
			f.global = true
		case "--apply":
			f.apply = true
		case "--runtime":
			f.runtime = true
		case "--agents":
			setStr(&i, &f.agents)
		case "--file":
			setStr(&i, &f.file)
		case "--task":
			setStr(&i, &f.task)
		case "--budget":
			setIntFlag(&i, &f.budget, "--budget")
		case "--uninstall":
			f.hostUninstall = true
		case "--agent-id":
			setStr(&i, &f.agentID)
		case "--mermaid":
			f.mermaid = true
		case "--repos":
			f.repos = true
		case "--mask":
			f.mask = true
		case "--names":
			setStr(&i, &f.names)
		case "--schema":
			setStr(&i, &f.schema)
		case "--cmd":
			setStr(&i, &f.cmd)
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
			setStr(&i, &f.mode)
		case "--with-skill":
			setStr(&i, &f.withSkill)
		case "--once":
			f.once = true
		case "--semantic":
			f.semantic = true
		case "--interval":
			setIntFlag(&i, &f.interval, "--interval")
		case "--http":
			setStr(&i, &f.http)
		case "--tls-cert":
			setStr(&i, &f.tlsCert)
		case "--tls-key":
			setStr(&i, &f.tlsKey)
		case "--hold":
			f.hold = true
		case "--graphml":
			f.graphml = true
		case "--cypher":
			f.cypher = true
		case "--html":
			f.html = true
		case "--out":
			setStr(&i, &f.out)
		case "--compact":
			f.compact = true
		case "--all":
			f.all = true
		case "--clear":
			f.clear = true
		case "--max":
			setIntFlag(&i, &f.max, "--max")
		case "--limit":
			setIntFlag(&i, &f.limit, "--limit")
		case "--range":
			setStr(&i, &f.range_)
		case "--lines":
			setIntFlag(&i, &f.lines, "--lines")
		case "--depth":
			setIntFlag(&i, &f.depth, "--depth")
		case "--full":
			f.full = true
		case "--from":
			setStr(&i, &f.from)
		case "--to":
			setStr(&i, &f.to)
		case "--severity":
			setStr(&i, &f.severity)
		case "--lang":
			setStr(&i, &f.lang)
		case "--stdin":
			setStr(&i, &f.stdin)
		case "--no-instructions":
			f.noinstructions = true
		case "--max-tokens":
			setIntFlag(&i, &f.maxTokens, "--max-tokens")
		case "--max-files":
			setIntFlag(&i, &f.maxFiles, "--max-files")
		case "--tier":
			setStr(&i, &f.tier)
		case "--precision":
			setStr(&i, &f.precision)
		case "--min-confidence":
			setStr(&i, &f.minConfidence)
		case "--fold":
			f.fold = true
		case "--explain":
			f.explain = true
		case "--graph":
			f.graph = true
		case "--generate":
			f.generate = true
		case "--staged":
			f.staged = true
		case "--subject":
			f.subject = true
		case "--message":
			setStr(&i, &f.message)
		case "--name":
			setStr(&i, &f.name)
		case "--dry-run":
			f.dryRun = true
		case "--approver":
			setStr(&i, &f.approver)
		case "--version":
			setStr(&i, &f.version)
		case "--reject":
			f.reject = true
		case "--reason":
			setStr(&i, &f.reason)
		case "--action":
			setStr(&i, &f.action)
		case "--column", "--col":
			setIntFlag(&i, &f.column, "--column")
		case "--compiler-output":
			setStr(&i, &f.compilerOutput)
		case "--edits":
			setStr(&i, &f.edits)
		case "--server-cmd":
			setStr(&i, &f.serverCmd)
		case "--target":
			setStr(&i, &f.target)
		case "--min-fixes":
			setIntFlag(&i, &f.minFixes, "--min-fixes")
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
