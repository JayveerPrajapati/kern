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
