// Command kern is the local context optimizer: prompt compression, log
// stripping, project mapping, compact build runs and token savings reports.
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/JayveerPrajapati/kern/internal/brief"
	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/commitmsg"
	kdiff "github.com/JayveerPrajapati/kern/internal/diff"
	"github.com/JayveerPrajapati/kern/internal/docsearch"
	"github.com/JayveerPrajapati/kern/internal/doctor"
	"github.com/JayveerPrajapati/kern/internal/fetch"
	"github.com/JayveerPrajapati/kern/internal/fw"
	"github.com/JayveerPrajapati/kern/internal/heal"
	"github.com/JayveerPrajapati/kern/internal/hook"
	"github.com/JayveerPrajapati/kern/internal/hooks"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/lock"
	"github.com/JayveerPrajapati/kern/internal/mcp"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/pack"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/precache"
	"github.com/JayveerPrajapati/kern/internal/project"
	"github.com/JayveerPrajapati/kern/internal/prompt"
	"github.com/JayveerPrajapati/kern/internal/rename"
	"github.com/JayveerPrajapati/kern/internal/sandbox"
	"github.com/JayveerPrajapati/kern/internal/schema"
	"github.com/JayveerPrajapati/kern/internal/script"
	"github.com/JayveerPrajapati/kern/internal/sec"
	"github.com/JayveerPrajapati/kern/internal/semcache"
	"github.com/JayveerPrajapati/kern/internal/setup"
	"github.com/JayveerPrajapati/kern/internal/stats"
	"github.com/JayveerPrajapati/kern/internal/swap"
	"github.com/JayveerPrajapati/kern/internal/terse"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
	"github.com/JayveerPrajapati/kern/internal/validate"
	"github.com/JayveerPrajapati/kern/internal/verify"
)

// version is stamped at build time via -ldflags "-X main.version=v1.2.3".
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
  kern log <file>                                 compress a log file
  kern index [root]                               build/refresh the AST index
  kern watch [root]                               daemon: auto re-index on change
  kern ast <pattern> [--all]                      AST symbol search (wildcards, kind prefixes)
  kern search <query> [--limit N] [--repos] [--json] [--semantic]
                                  ranked free-text symbol search (--semantic: Ollama re-rank; --repos: across registered repos)
  kern repos (list|add <path> [name]|remove <name>)
                                 multi-repo registry for cross-repo search
  kern graph <symbol> [--mermaid] [--json] [--graphml] [--html] [--out FILE]
                                 definition + callers + what it calls; export as JSON/GraphML/HTML
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
  kern hook install                               install post-commit diff->memory hook
  kern hook diff [range]                          compressed git diff (default HEAD~1..HEAD)
  kern hook store [range]                         store compressed diff in project memory
  kern changes [root] [--range a..b] [--file F] [--json]   change-impact: blast radius, risk, test gaps
  kern review [root] [--range a..b] [--max N]     token-optimised review context for changed files
  kern hubs [root] [--limit N] [--json]           most depended-on symbols + cross-package bridges
  kern bridges [root] [--limit N] [--json]        cross-package bridge detection (coupling points)
  kern testgaps [root] [--limit N] [--json]       test coverage + untested hotspots
  kern entries [root] [--limit N] [--json]        framework entry points in the index
  kern flows [root] [--limit N] [--json]          execution flows from entry points (still available)
  kern communities [root] [--json]                call-graph communities (label propagation)
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
  kern lock <scope> [root] [--hold]               acquire a workspace lock (held until interrupted, or --hold for non-blocking)
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
	staged         bool
	subject        bool
	message        string
	dryRun         bool
}

// mcpHTTPAddr resolves the address for `kern mcp`: a positional argument wins
// over the --http flag. An empty result means stdio mode.
func mcpHTTPAddr(args []string, f flags) string {
	if len(args) > 0 {
		return args[0]
	}
	return f.http
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
		case "--staged":
			f.staged = true
		case "--subject":
			f.subject = true
		case "--message":
			i++
			if i < len(args) {
				f.message = args[i]
			}
		case "--dry-run":
			f.dryRun = true
		default:
			rest = append(rest, args[i])
		}
	}
	return f, rest, parseErr
}

// maxStdinBytes caps piped input so an uncooperative pipe cannot exhaust
// memory. 64 MiB is far beyond any legitimate use (prompts, logs, source).
const maxStdinBytes = 64 << 20

// readStdin returns the full piped stdin, rejecting input larger than
// maxStdinBytes instead of buffering it all.
func readStdin() ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(os.Stdin, maxStdinBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxStdinBytes {
		return nil, fmt.Errorf("stdin exceeds %d bytes", maxStdinBytes)
	}
	return b, nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	cmd := os.Args[1]
	rest := os.Args[2:]

	switch cmd {
	case "version", "--version", "-v":
		fmt.Printf("kern %s\n", version)

	case "guide":
		fmt.Println(mcp.Guide())

	case "optimize", "preview":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		prompt := strings.Join(args, " ")
		if prompt == "" {
			fatal("prompt is required")
		}
		wireRecorder()
		var attach string
		if f.attach != "" && f.attach != "-" {
			b, err := os.ReadFile(f.attach)
			if err != nil {
				fatal("cannot read attach: %v", err)
			}
			attach = string(b)
		} else if f.attach == "-" {
			b, err := readStdin()
			if err != nil {
				fatal("cannot read stdin: %v", err)
			}
			attach = string(b)
		}
		if cmd == "preview" {
			old := optimize.Recorder
			optimize.Recorder = nil
			defer func() { optimize.Recorder = old }()
		}
		res, err := optimize.Prompt(prompt, attach, optimize.Options{Session: f.session, Model: f.model, Source: "cli", LLM: f.llm, Mask: f.mask, MaskNames: splitNames(f.names), Cache: f.cache, FewShot: f.fewshot})
		if err != nil {
			fatal("%v", err)
		}
		fmt.Println(res.Output)
		if res.FromCache {
			fmt.Fprintf(os.Stderr, "kern: served from cache\n")
		}
		fmt.Fprintf(os.Stderr, "kern: %d -> %d tokens (saved %d, %.1f%%)\n", res.BeforeTokens, res.AfterTokens, res.SavedTokens, res.SavedPercent)

	case "compact":
		if len(rest) < 1 {
			fatal("usage: kern compact <file>")
		}
		content, err := code.ReadFile(rest[0])
		if err != nil {
			fatal("%v", err)
		}
		sum := code.Summarize(rest[0], content, 200)
		fmt.Println(sum.Render())

	case "project":
		root := "."
		if len(rest) > 0 {
			root = rest[0]
		}
		p, err := code.BuildProject(root, 500, 200)
		if err != nil {
			fatal("%v", err)
		}
		fmt.Println(p.Render())

	case "pack":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		if len(args) > 0 {
			root = args[0]
		}
		b, err := pack.Build(root, pack.Options{
			MaxTokens:        f.maxTokens,
			SkipInstructions: f.noinstructions,
		})
		if err != nil {
			fatal("%v", err)
		}
		out := b.Render()
		if f.out != "" {
			if werr := os.WriteFile(f.out, []byte(out), 0o644); werr != nil {
				fatal("%v", werr)
			}
			fmt.Printf("kern: packed %d files (%d tokens) to %s\n", len(b.Files), b.TotalTokens, f.out)
		} else {
			fmt.Print(out)
		}

	case "build":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		cmdStr := strings.Join(args, " ")
		if cmdStr == "" {
			fatal("usage: kern build <command>")
		}
		wireRecorder()
		res, err := optimize.RunBuild(context.Background(), cmdStr, f.dir, optimize.Options{Session: f.session})
		if err != nil {
			fatal("%v", err)
		}
		fmt.Println(res.Output)

	case "log":
		if len(rest) < 1 {
			fatal("usage: kern log <file>")
		}
		b, err := os.ReadFile(rest[0])
		if err != nil {
			fatal("%v", err)
		}
		wireRecorder()
		res, err := optimize.Log(string(b), optimize.Options{})
		if err != nil {
			fatal("%v", err)
		}
		fmt.Println(res.Output)
		fmt.Fprintf(os.Stderr, "kern: %d -> %d tokens (saved %d, %.1f%%)\n", res.BeforeTokens, res.AfterTokens, res.SavedTokens, res.SavedPercent)

	case "tokens":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		text := strings.Join(args, " ")
		if text == "" {
			fatal("usage: kern tokens [--bpe] <text>")
		}
		if f.bpe {
			fmt.Println(tokenize.CountBPE(text))
			return
		}
		fmt.Println(tokenize.Count(text))

	case "setup":
		f, _, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := f.root
		if root == "" {
			root = "."
		}
		if f.check {
			for _, s := range setup.Check(root) {
				mark := "-"
				if s.Installed {
					mark = "x"
				}
				fmt.Printf("[%s] %-22s %s\n", mark, s.Agent, s.Note)
			}
			return
		}
		var agents []string
		if f.agents != "" {
			agents = strings.Split(f.agents, ",")
		}
		for _, s := range setup.Wire(root, agents, f.detect) {
			mark := "ok"
			if !s.Installed {
				mark = "!!"
			}
			fmt.Printf("[%s] %-32s %s\n", mark, s.Agent, s.Note)
		}
		if len(f.agents) == 0 && !f.detect {
			fmt.Println("\nWired all agents. Use --detect to wire only detected agents, or --agents to target specific ones.")
		} else if f.detect && len(f.agents) == 0 {
			detected := setup.DetectAgents(root)
			fmt.Printf("\nDetected agents: %v\n", detected)
		}
		fmt.Println("Restart your agent (opencode reload / claude) to pick up the MCP servers and kern-first instructions.")

	case "buddy":
		root := "."
		if len(rest) > 0 {
			root = rest[0]
		}
		out, err := brief.Build(root)
		if err != nil {
			fatal("%v", err)
		}
		fmt.Println(out)

	case "prompt":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if len(args) == 0 {
			fatal("usage: kern prompt <template> [--file PATH] [--task TEXT]")
		}
		if args[0] == "list" {
			names, err := prompt.List()
			if err != nil {
				fatal("%v", err)
			}
			for _, n := range names {
				fmt.Println(n)
			}
			return
		}
		vars := map[string]string{
			"ROOT":    ".",
			"LANG":    "",
			"MAP":     "",
			"FILE":    f.file,
			"TASK":    f.task,
			"SYMBOLS": "",
		}
		if abs, err := filepath.Abs("."); err == nil {
			vars["ROOT"] = abs
		}
		if p, err := code.BuildProject(".", 500, 200); err == nil {
			vars["MAP"] = p.Render()
			vars["LANG"] = projectLangs()
		}
		if f.file != "" {
			if ctx := fileContext(f.file); ctx != "" {
				vars["FILE"] = f.file + "\n" + ctx
			}
		}
		out, err := prompt.Render(args[0], vars)
		if err != nil {
			fatal("%v", err)
		}
		if f.schema != "" {
			sc, serr := loadSchema(f.schema)
			if serr != nil {
				fatal("%v", serr)
			}
			fmt.Print(out)
			fmt.Println()
			fmt.Print(sc.PromptBlock())
			return
		}
		fmt.Print(out)

	case "validate":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		if len(args) > 0 {
			root = args[0]
		}
		var c *validate.Command
		if f.cmd != "" {
			parts := strings.Fields(f.cmd)
			if len(parts) == 0 {
				fatal("empty --cmd")
			}
			c = &validate.Command{Name: parts[0], Cmd: parts[0], Args: parts[1:]}
		} else {
			var err error
			c, err = validate.Detect(root)
			if err != nil {
				fatal("%v", err)
			}
		}
		fmt.Printf("kern: running %s %s\n", c.Cmd, strings.Join(c.Args, " "))
		res := validate.Run(context.Background(), root, c, time.Duration(f.timeout)*time.Second)
		if res.Err != nil {
			fmt.Fprintf(os.Stderr, "kern: validation error: %v\n", res.Err)
		}
		fmt.Print(res.Output)
		if res.OK {
			fmt.Printf("kern: validation OK (%s, %s)\n", c.Name, res.Dur.Round(time.Millisecond))
			return
		}
		fmt.Printf("kern: validation FAILED (%s, exit %d)\n", c.Name, res.ExitCode)
		os.Exit(1)

	case "heal":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		if len(args) > 0 {
			root = args[0]
		}
		task := f.task
		if task == "" {
			task = "Fix the failing build/test/syntax errors in this project."
		}
		iters := f.max
		if iters == 0 {
			iters = 3
		}
		fmt.Printf("kern: heal %s (cmd=%s, rounds=%d)\n", root, f.llm, iters)
		res := heal.Run(context.Background(), root, task, f.llm, iters, time.Duration(f.timeout)*time.Second)
		if res.Err != nil {
			fatal("%v", res.Err)
		}
		if res.Validated {
			fmt.Printf("kern: validated OK after %d correction round(s)\n", res.Iterations)
			if len(res.Changes) > 0 {
				fmt.Printf("kern: changed files (review and apply in your tree):\n")
				for _, c := range res.Changes {
					fmt.Printf("  - %s\n", c)
				}
			}
			if res.Diff != "" {
				fmt.Println(res.Diff)
			}
			return
		}
		fmt.Printf("kern: still failing after %d round(s)\n", res.Iterations)
		fmt.Print(res.LastOutput)
		os.Exit(1)

	case "udiff":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if len(args) < 2 {
			fatal("usage: kern udiff <file-a> <file-b> [--out out.patch]")
		}
		ab, err := os.ReadFile(args[0])
		if err != nil {
			fatal("cannot read %s: %v", args[0], err)
		}
		bb, err := os.ReadFile(args[1])
		if err != nil {
			fatal("cannot read %s: %v", args[1], err)
		}
		u := kdiff.Unified(args[0], args[1], splitLines(string(ab)), splitLines(string(bb)))
		if u == "" {
			fmt.Println("files identical")
			return
		}
		if f.out != "" {
			if err := os.WriteFile(f.out, []byte(u), 0o644); err != nil {
				fatal("%v", err)
			}
			fmt.Printf("wrote diff to %s\n", f.out)
			return
		}
		fmt.Print(u)

	case "sandbox":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		cmdParts := args
		if len(args) > 0 && args[0] != "--" && isDir(args[0]) {
			root = args[0]
			cmdParts = args[1:]
		}
		if len(cmdParts) == 0 {
			fatal("usage: kern sandbox [root] -- <command...>")
		}
		// Strip a leading -- separator.
		if cmdParts[0] == "--" {
			cmdParts = cmdParts[1:]
		}
		if len(cmdParts) == 0 {
			fatal("usage: kern sandbox [root] -- <command...>")
		}
		fmt.Printf("kern: sandbox run in %s: %s\n", root, strings.Join(cmdParts, " "))
		res := sandbox.Run(context.Background(), root, cmdParts[0], cmdParts[1:], time.Duration(f.timeout)*time.Second)
		fmt.Print(res.Output)
		if res.OK {
			fmt.Printf("kern: succeeded (%s); changes kept\n", res.Duration.Round(time.Millisecond))
			return
		}
		if res.Restored {
			fmt.Printf("kern: FAILED (exit %d, %s); tree restored to snapshot (%d files)\n", res.ExitCode, res.Duration.Round(time.Millisecond), res.Snapshots)
		} else {
			fmt.Printf("kern: FAILED (exit %d)\n", res.ExitCode)
		}
		if res.Err != nil {
			fmt.Fprintf(os.Stderr, "kern: %v\n", res.Err)
		}
		os.Exit(1)

	case "swap":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		if len(args) > 0 && isDir(args[0]) {
			root = args[0]
			args = args[1:]
		}
		var b []byte
		if len(args) > 0 && args[0] == "-" {
			b, err = readStdin()
		} else if len(args) > 0 {
			b, err = os.ReadFile(args[0])
		} else {
			b, err = readStdin()
		}
		if err != nil {
			fatal("%v", err)
		}
		text := string(b)
		switch f.mode {
		case "expand":
			fmt.Print(swap.ExpandMode(text, root))
		case "summary":
			fmt.Print(swap.SummaryMode(text, root))
		default:
			out, fits := swap.Fit(text, root, f.max)
			if !fits {
				fmt.Fprintf(os.Stderr, "kern: warning: still over budget after summarization\n")
			}
			fmt.Print(out)
		}

	case "precache":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		if len(args) > 0 {
			root = args[0]
		}
		if f.once {
			rep := precache.Warm(root)
			fmt.Printf("kern: warmed %d summaries (%d cache hits), %d doc chunks, docs saved=%v in %s\n",
				rep.Warmed, rep.CacheHits, rep.DocChunks, rep.DocsSaved, rep.Dur.Round(time.Millisecond))
			return
		}
		interval := time.Duration(f.interval) * time.Second
		if interval <= 0 {
			interval = 60 * time.Second
		}
		stop := make(chan struct{})
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		go func() { <-sig; close(stop) }()
		fmt.Printf("kern: pre-caching %s every %s (Ctrl-C to stop)\n", root, interval)
		for rep := range precache.Watch(root, interval, stop) {
			if rep.SourceMiss {
				fmt.Printf("kern: no project at %s\n", root)
				return
			}
			fmt.Printf("kern: warmed %d (%d hits), %d doc chunks\n", rep.Warmed, rep.CacheHits, rep.DocChunks)
		}

	case "schema":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if f.schema == "" {
			fatal("usage: kern schema <data.json|- for stdin> --schema <schema.json>\n  or: kern prompt <template> --schema <schema.json> to inject the schema")
		}
		sc, err := loadSchema(f.schema)
		if err != nil {
			fatal("%v", err)
		}
		var b []byte
		if len(args) > 0 && args[0] == "-" {
			b, err = readStdin()
		} else if len(args) > 0 {
			b, err = os.ReadFile(args[0])
		} else {
			b, err = readStdin()
		}
		if err != nil {
			fatal("%v", err)
		}
		violations := sc.Validate(b)
		if len(violations) == 0 {
			fmt.Println("schema OK: output conforms")
			return
		}
		fmt.Printf("schema violations (%d):\n", len(violations))
		for _, v := range violations {
			fmt.Println("  - " + v)
		}
		os.Exit(1)

	case "remember":
		lesson := strings.Join(rest, " ")
		if lesson == "" {
			fatal("usage: kern remember <lesson>")
		}
		if err := memory.Add(".", lesson); err != nil {
			fatal("%v", err)
		}
		fmt.Println("remembered.")

	case "memory":
		f, _, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if f.clear {
			if err := memory.Clear("."); err != nil {
				fatal("%v", err)
			}
			fmt.Println("project memory cleared.")
			return
		}
		for _, e := range memory.List(".") {
			fmt.Printf("%s  %s\n", e.Time.UTC().Format("2006-01-02 15:04"), e.Text)
		}

	case "recall":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if len(args) < 1 || args[0] == "" {
			fatal("usage: kern recall \"<prompt>\" [root] [--limit N]")
		}
		root := "."
		if len(args) > 1 {
			root = args[1]
		}
		k := f.limit
		if k <= 0 {
			k = 5
		}
		for _, e := range memory.Recall(root, args[0], k) {
			fmt.Printf("%s  %s\n", e.Time.UTC().Format("2006-01-02 15:04"), e.Text)
		}

	case "budget":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		text := strings.Join(args, " ")
		if text == "" {
			b, err := readStdin()
			if err != nil {
				fatal("%v", err)
			}
			text = string(b)
		}
		if text == "" {
			fatal("usage: kern budget \"<text>\" --max N  (or pipe stdin)")
		}
		maxTokens := f.max
		if maxTokens <= 0 {
			maxTokens = 4000
		}
		out := budget.Fit(text, maxTokens)
		before := tokenize.Count(text)
		after := tokenize.Count(out)
		fmt.Fprintf(os.Stderr, "kern: %d -> %d tokens (saved %d, %.1f%%)\n", before, after, before-after, pct(before, after))
		fmt.Println(out)

	case "terse":
		_, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		text := ""
		if len(args) > 0 {
			if args[0] == "-" {
				b, err := readStdin()
				if err != nil {
					fatal("%v", err)
				}
				text = string(b)
			} else if fi, err := os.Stat(args[0]); err == nil && !fi.IsDir() {
				b, err := os.ReadFile(args[0])
				if err != nil {
					fatal("%v", err)
				}
				text = string(b)
			} else {
				text = strings.Join(args, " ")
			}
		}
		if text == "" {
			b, err := readStdin()
			if err != nil {
				fatal("%v", err)
			}
			text = string(b)
		}
		if text == "" {
			fatal("usage: kern terse \"<text>\" [--max]  (or pipe stdin)")
		}
		out, dropped := terse.Compress(text)
		before := tokenize.Count(text)
		after := tokenize.Count(out)
		fmt.Fprintf(os.Stderr, "kern: %d -> %d tokens (saved %d, %.1f%%, %d filler lines dropped)\n", before, after, before-after, pct(before, after), dropped)
		fmt.Println(out)

	case "exec":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if len(args) > 0 && args[0] == "--list" {
			fmt.Printf("kern exec: available runtimes: %s\n", strings.Join(script.Available(), ", "))
			fmt.Printf("  supported languages: %s\n", strings.Join(script.Languages(), ", "))
			return
		}
		// Script source: positional args win. A lone "-" or a piped stdin
		// reads the script from stdin; a path to an existing file runs that
		// file (language from extension); anything else is treated as inline
		// code.
		var code, path string
		switch {
		case len(args) > 0 && args[0] == "-":
			b, err := readStdin()
			if err != nil {
				fatal("%v", err)
			}
			code = string(b)
		case len(args) > 0:
			if fi, err := os.Stat(args[0]); err == nil && !fi.IsDir() {
				path = args[0]
			} else {
				code = strings.Join(args, " ")
			}
		default:
			if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
				b, rerr := readStdin()
				if rerr != nil {
					fatal("%v", rerr)
				}
				code = string(b)
			}
		}
		if strings.TrimSpace(code) == "" && path == "" {
			fatal("usage: kern exec \"<code>\" [--lang LANG] [--timeout s] [--max bytes] [--stdin file|-]\n       kern exec script.py | kern exec - | kern exec --list")
		}

		run := script.Run{Lang: f.lang, Code: code, Path: path}
		// 15s default so a runaway script can't hang an agent for the shared
		// --timeout default (120s); an explicit --timeout always wins — even
		// 120, which the old sentinel comparison misread as "unset" (W2-39).
		if f.timeoutSet {
			run.Timeout = time.Duration(f.timeout) * time.Second
		} else {
			run.Timeout = 15 * time.Second
		}
		run.MaxOut = f.max
		if f.stdin != "" {
			if f.stdin == "-" {
				b, err := readStdin()
				if err != nil {
					fatal("%v", err)
				}
				run.Stdin = string(b)
			} else {
				b, err := os.ReadFile(f.stdin)
				if err != nil {
					fatal("%v", err)
				}
				run.Stdin = string(b)
			}
		}
		res := script.RunScript(run)
		if f.json {
			printJSON(res)
			if res.Err != nil {
				os.Exit(1)
			}
			return
		}
		fmt.Print(res.Stdout)
		if !strings.HasSuffix(res.Stdout, "\n") && res.Stdout != "" {
			fmt.Println()
		}
		if res.Err != nil {
			fmt.Fprintln(os.Stderr, "kern exec: "+res.Err.Error())
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "kern exec: %s ok (%s, %d bytes stdout)\n", res.Runtime, res.Duration.Round(time.Millisecond), len(res.Stdout))

	case "doctor":
		root := "."
		if len(rest) > 0 {
			root = rest[0]
		}
		fmt.Println(doctor.Render(root, doctor.Run(root)))

	case "mask":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		var in string
		if len(args) > 0 && args[0] != "" {
			in = args[0]
		}
		var b []byte
		if in == "" || in == "-" {
			b, err = readStdin()
		} else {
			b, err = os.ReadFile(in)
		}
		if err != nil {
			fatal("%v", err)
		}
		res := pii.MaskNames(string(b), splitNames(f.names))
		fmt.Print(res.Text)
		if res.Replaced > 0 {
			fmt.Fprintf(os.Stderr, "\nkern: masked %d secrets: ", res.Replaced)
			var parts []string
			for k, v := range res.ByLabel {
				parts = append(parts, fmt.Sprintf("%s %d", k, v))
			}
			fmt.Fprint(os.Stderr, strings.Join(parts, ", "))
			fmt.Fprintln(os.Stderr)
		}

	case "verify":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		in := "-"
		if len(args) > 0 && args[0] != "" {
			in = args[0]
			args = args[1:]
		}
		if len(args) > 0 {
			root = args[0]
		}
		var b []byte
		if in == "-" {
			b, err = readStdin()
		} else {
			b, err = os.ReadFile(in)
		}
		if err != nil {
			fatal("%v", err)
		}
		ix, ierr := loadOrBuild(root)
		if ierr != nil {
			ix = nil
		}
		rep := verify.Sorted(verify.Verify(ix, root, string(b)))
		if f.json {
			printJSON(rep)
			return
		}
		fmt.Println(verify.Render(rep))

	case "docs":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		sub := ""
		if len(args) > 0 && (args[0] == "index" || args[0] == "clear" || args[0] == "fetch") {
			sub = args[0]
			args = args[1:]
		}
		root := "."
		query := ""
		if sub == "" && len(args) > 0 {
			query = args[0]
			args = args[1:]
		}
		if sub == "fetch" {
			if len(args) == 0 {
				fatal("usage: kern docs fetch <url> [name] [root]")
			}
			rawURL := args[0]
			name := ""
			if len(args) > 1 {
				name = args[1]
			}
			if len(args) > 2 {
				root = args[2]
			}
			res, err := fetch.Fetch(rawURL, 0)
			if err != nil {
				fatal("%v", err)
			}
			if name == "" {
				name = slugName(rawURL)
			} else {
				name = sanitizeDocName(name)
			}
			if err := os.MkdirAll(cache.Path("data", "docs-fetch"), 0o755); err != nil {
				fatal("%v", err)
			}
			if err := os.WriteFile(cache.Path("data", "docs-fetch", name+".md"), []byte(res.Text), 0o600); err != nil {
				fatal("%v", err)
			}
			added, err := docsearch.MergeFetched(root, name, res.Text)
			if err != nil {
				fatal("%v", err)
			}
			if f.semantic {
				client := llm.New("")
				if !client.HasEmbeddingModel() {
					fmt.Printf("note: semantic embeddings skipped (%s not installed; run: ollama pull %s)\n", llm.EmbedModel(), llm.EmbedModel())
				} else {
					embedded, eerr := docsearch.ReembedFetch(root, name, client)
					if eerr != nil {
						fatal("%v", eerr)
					}
					if embedded > 0 {
						fmt.Printf("semantic embeddings attached to %d fetched chunks (KERN_EMBED_MODEL=%s)\n", embedded, llm.EmbedModel())
					}
				}
			}
			fmt.Printf("fetched %s -> %s (%d chars, %d chunks indexed into %s doc index)\n", rawURL, name, len(res.Text), added, root)
			if res.Title != "" {
				fmt.Printf("# %s\n\n", res.Title)
			}
			fmt.Println(clipText(res.Text, 600))
			return
		}
		if len(args) > 0 {
			root = args[0]
		}
		if sub == "index" {
			var ix *docsearch.Index
			var err error
			if f.semantic {
				client := llm.New("")
				if !client.Available() {
					fatal("ollama not reachable (semantic index requires a local Ollama); run without --semantic for deterministic indexing")
				}
				if !client.HasEmbeddingModel() {
					fatal("embedding model %q not installed (run: ollama pull %s)", llm.EmbedModel(), llm.EmbedModel())
				}
				docsearch.SemanticEmbedder = client
				ix, err = docsearch.IndexDirSemantic(root, client)
			} else {
				ix, err = docsearch.IndexDir(root)
			}
			if err != nil {
				fatal("%v", err)
			}
			if err := ix.Save(); err != nil {
				fatal("%v", err)
			}
			fmt.Printf("indexed %d chunks from %s\n", len(ix.Docs), root)
		} else if sub == "clear" {
			_ = os.RemoveAll(cache.Path("data", "docs"))
			_ = os.RemoveAll(cache.Path("data", "docs-fetch"))
			fmt.Println("cleared document index and fetched-doc cache")
		} else {
			if query == "" {
				fatal("usage: kern docs <query> [root] [--limit N] | kern docs index [root] [--semantic] | kern docs clear")
			}
			ix := docsearch.Load(root)
			if ix == nil {
				var err error
				ix, err = docsearch.IndexDir(root)
				if err != nil {
					fatal("%v", err)
				}
				_ = ix.Save()
			}
			// If the persisted index carries dense vectors, re-attach the
			// local embedder so queries fuse the semantic signal too.
			if hasSemantic(ix) {
				client := llm.New("")
				if client.HasEmbeddingModel() {
					docsearch.SemanticEmbedder = client
				}
			}
			k := f.limit
			results := ix.Search(query, k)
			if len(results) == 0 {
				fmt.Println("no matching document fragments")
				return
			}
			for i, r := range results {
				fmt.Printf("#%d sim=%.3f %s:%d\n", i+1, r.Sim, r.Doc.Chunk.File, r.Doc.Chunk.Start)
				body := strings.ReplaceAll(r.Doc.Chunk.Text, "\n", " ")
				if len(body) > 300 {
					body = body[:300] + "…"
				}
				fmt.Printf("  %s\n", body)
			}
		}

	case "fw":
		args := rest
		var filter string
		if len(args) > 0 && args[0] == "--catalog" {
			args = args[1:]
			if len(args) > 0 {
				filter = args[0]
				args = args[1:]
			}
			langs := fw.Langs()
			if filter != "" {
				var fs []fw.Framework
				for _, fr := range fw.Catalog() {
					if fr.Lang == filter || strings.Contains(strings.ToLower(fr.Name), strings.ToLower(filter)) {
						fs = append(fs, fr)
					}
				}
				if len(fs) == 0 {
					fatal("no frameworks match %q (languages: %s)", filter, strings.Join(langs, ", "))
				}
				for _, fr := range fs {
					fmt.Printf("%-20s %s\n", fr.Name, fr.Summary)
				}
				return
			}
			for _, lang := range langs {
				fmt.Printf("%s\n", lang)
				for _, fr := range fw.ByLang(lang) {
					fmt.Printf("  %-20s %s\n", fr.Name, fr.Summary)
				}
			}
			return
		}
		root := "."
		if len(args) > 0 && args[0] != "" {
			root = args[0]
		}
		det, err := fw.Detect(root)
		if err != nil {
			fatal("%v", err)
		}
		fmt.Println(fw.Render(det))

	case "hook":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		sub := "store"
		if len(args) > 0 {
			sub = args[0]
		}
		from, to := "", ""
		if f.range_ != "" {
			if p := strings.SplitN(f.range_, "..", 2); len(p) == 2 {
				from, to = p[0], p[1]
			} else {
				// A single ref means the range ending at that ref (HEAD ->
				// HEAD~1..HEAD, matching the hook default); it must not be
				// silently dropped (W2-40).
				from, to = f.range_+"~1", f.range_
			}
		}
		switch sub {
		case "install":
			if err := hooks.Install("."); err != nil {
				fatal("%v", err)
			}
			fmt.Println("post-commit hook installed (compresses each commit diff into project memory).")
		case "diff":
			out, err := hooks.Diff(from, to)
			if err != nil {
				fatal("%v", err)
			}
			fmt.Println(out)
		case "store":
			if err := hooks.Store(".", from, to); err != nil {
				fatal("%v", err)
			}
			fmt.Println("diff stored in project memory.")
		case "claude-post", "claude-prompt", "gemini-after", "gemini-prompt":
			// Native hooks for non-opencode agents (wired by `kern setup`).
			// Reads the agent's hook JSON from stdin and responds in that
			// agent's framing. Failures never break the agent's tool call:
			// handlers swallow their own parse errors.
			root := "."
			if len(args) > 1 && args[1] != "" {
				root = args[1]
			}
			in, err := readStdin()
			if err != nil {
				fatal("%v", err)
			}
			switch sub {
			case "claude-post":
				out, err := hook.ClaudePost(root, in)
				if err != nil {
					fatal("%v", err)
				}
				fmt.Println(out)
			case "claude-prompt":
				if err := hook.ClaudePrompt(root, in); err != nil {
					fatal("%v", err)
				}
			case "gemini-after":
				repl, err := hook.GeminiAfter(root, in)
				if err != nil {
					fatal("%v", err)
				}
				if repl != "" {
					// Gemini: exit code 2 + stderr text hides the real tool
					// result and substitutes the stderr content.
					fmt.Fprintln(os.Stderr, repl)
					os.Exit(2)
				}
			case "gemini-prompt":
				if err := hook.GeminiPrompt(root, in); err != nil {
					fatal("%v", err)
				}
			}
		default:
			fatal("usage: kern hook <install|diff|store|claude-post|claude-prompt|gemini-after|gemini-prompt> [root] [--range a..b]")
		}

	case "commitmsg":
		f, _, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		var out []byte
		if f.staged {
			out, err = gitDiff("diff --cached")
		} else if f.range_ != "" {
			out, err = gitDiff("diff --unified=0 " + f.range_)
		} else {
			out, err = gitDiff("diff HEAD")
			if err != nil {
				out, err = gitDiff("diff")
			}
		}
		if err != nil {
			fatal("%v", err)
		}
		msg := commitmsg.Generate(string(out))
		if f.subject {
			fmt.Println(msg.Subject)
		} else {
			fmt.Println(msg.String())
		}

	case "commit":
		f, _, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if f.all {
			if _, err := gitOutput("add", "-A"); err != nil {
				fatal("staging failed: %v", err)
			}
		}
		diffOut, err := gitDiff("diff --cached")
		if err != nil {
			fatal("%v", err)
		}
		if len(strings.TrimSpace(string(diffOut))) == 0 {
			fatal("nothing staged to commit (use --all to stage tracked+untracked changes)")
		}
		msg := commitmsg.Generate(string(diffOut))
		subject := f.message
		if subject == "" {
			subject = msg.Subject
		}
		if f.dryRun {
			fmt.Println("kern: would commit with:")
			fmt.Println()
			fmt.Println(subject)
			for _, l := range msg.Body {
				fmt.Println(l)
			}
			return
		}
		body := strings.Join(msg.Body, "\n")
		full := subject
		if body != "" {
			full += "\n\n" + body
		}
		out, err := gitCommit(full)
		if err != nil {
			fatal("commit failed: %v\n%s", err, out)
		}
		short := shortHash()
		fmt.Printf("committed %s %s\n", short, subject)

	case "semcache":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		sub := ""
		if len(args) > 0 {
			sub = args[0]
			args = args[1:]
		}
		switch sub {
		case "clear":
			ns := ""
			if len(args) > 0 {
				ns = args[0]
			}
			if err := semcache.Clear(ns); err != nil {
				fatal("%v", err)
			}
			if f.json {
				printJSON(map[string]any{"cleared": ns})
				return
			}
			if ns == "" {
				fmt.Println("semcache: cleared all namespaces")
			} else {
				fmt.Printf("semcache: cleared %q\n", ns)
			}
		case "list":
			if len(args) == 0 {
				fatal("usage: kern semcache list <prompt|log>")
			}
			ns := args[0]
			entries, err := semcache.Entries(ns)
			if err != nil {
				fatal("%v", err)
			}
			if len(entries) == 0 {
				fmt.Printf("semcache %q: empty\n", ns)
				return
			}
			fmt.Printf("semcache %q: %d entries\n", ns, len(entries))
			for i, in := range entries {
				fmt.Printf("  %d. %s\n", i+1, in)
			}
		case "sim":
			if len(args) != 2 {
				fatal("usage: kern semcache sim <textA> <textB>")
			}
			fmt.Printf("similarity: %.3f\n", semcache.Similarity(args[0], args[1]))
		default:
			st, err := semcache.Stats()
			if err != nil {
				fatal("%v", err)
			}
			if f.json {
				printJSON(map[string]any{"namespaces": st})
				return
			}
			if len(st) == 0 {
				fmt.Println("semcache: empty")
				return
			}
			fmt.Println("semcache entries by namespace:")
			names := make([]string, 0, len(st))
			for ns := range st {
				names = append(names, ns)
			}
			sort.Strings(names)
			for _, ns := range names {
				fmt.Printf("  %-8s %d\n", ns, st[ns])
			}
		}

	case "stats", "diff", "export":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		_ = args
		rec, err := stats.NewRecorder()
		if err != nil {
			fatal("%v", err)
		}
		if cmd == "diff" {
			entries, err := rec.Entries(20)
			if err != nil {
				fatal("%v", err)
			}
			for _, e := range entries {
				if f.session != "" && e.Session != f.session {
					continue
				}
				fmt.Printf("%s %-16s %s %7d -> %7d  (-%7d, %5.1f%%)  $%.4f\n",
					e.Time.Local().Format("01-02 15:04"), e.Operation, e.Source, e.BeforeTokens, e.AfterTokens, e.SavedTokens, e.SavedPercent, e.CostSavedUSD)
			}
			return
		}
		if cmd == "export" {
			entries, err := rec.Entries(100000)
			if err != nil {
				fatal("%v", err)
			}
			w := csv.NewWriter(os.Stdout)
			_ = w.Write([]string{"time", "operation", "source", "session", "model", "before_tokens", "after_tokens", "saved_tokens", "cost_saved_usd"})
			for _, e := range entries {
				_ = w.Write([]string{
					e.Time.UTC().Format(time.RFC3339), string(e.Operation), e.Source, e.Session, e.Model,
					strconv.Itoa(e.BeforeTokens), strconv.Itoa(e.AfterTokens), strconv.Itoa(e.SavedTokens),
					fmt.Sprintf("%.4f", e.CostSavedUSD),
				})
			}
			w.Flush()
			return
		}
		sum, err := rec.Summarize(f.days, f.session)
		if err != nil {
			fatal("%v", err)
		}
		if f.json {
			out := struct {
				Operations  int     `json:"operations"`
				BeforeTotal int     `json:"before_tokens"`
				AfterTotal  int     `json:"after_tokens"`
				SavedTotal  int     `json:"saved_tokens"`
				SavedPct    float64 `json:"saved_percent"`
				CostSaved   float64 `json:"cost_saved_usd"`
			}{sum.Operations, sum.BeforeTotal, sum.AfterTotal, sum.SavedTotal, sum.SavedPct, sum.CostSaved}
			printJSON(out)
			return
		}
		fmt.Printf("kern stats (last %d days)\n", f.days)
		fmt.Printf("  operations   : %d\n", sum.Operations)
		fmt.Printf("  before tokens: %d\n", sum.BeforeTotal)
		fmt.Printf("  after tokens : %d\n", sum.AfterTotal)
		fmt.Printf("  saved tokens : %d (%.1f%%)\n", sum.SavedTotal, sum.SavedPct)
		fmt.Printf("  cost saved   : $%.4f\n", sum.CostSaved)

	case "mcp":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		httpAddr := mcpHTTPAddr(args, f)
		wireRecorder()
		mcp.SetServerVersion(version)
		if httpAddr != "" {
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			if err := mcp.ServeHTTPContext(ctx, httpAddr); err != nil {
				os.Exit(1)
			}
			return
		}
		srv := mcp.NewServer(os.Stdin, os.Stdout)
		// SIGINT/SIGTERM: cancel in-flight tool calls and release held locks
		// so slow tools don't hang the process until the OS force-kills it
		// (W2-41; mirrors cmd/kern-mcp). Closing os.Stdin from another
		// goroutine does not reliably unblock the scanner's read, so Serve()
		// alone may never return: after cancelling, wait for the in-flight
		// count to drain, then exit.
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		go func() {
			<-ctx.Done()
			srv.CancelAll()
			srv.Close()
			_ = os.Stdin.Close()
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if srv.Inflight() == 0 {
					time.Sleep(100 * time.Millisecond)
					os.Exit(0)
				}
				time.Sleep(50 * time.Millisecond)
			}
			os.Exit(1)
		}()
		if err := srv.Serve(); err != nil {
			os.Exit(1)
		}
		srv.CancelAll()
		srv.Close()

	case "index":
		root := "."
		if len(rest) > 0 {
			root = rest[0]
		}
		ix, err := index.Build(root)
		if err != nil {
			fatal("%v", err)
		}
		if err := ix.Save(); err != nil {
			fatal("%v", err)
		}
		store := index.StorePath(root)
		if index.SQLiteEnabled() {
			if err := index.SaveSQLite(root, ix); err != nil {
				fatal("%v", err)
			}
			store = index.SQLitePath(root)
		}
		fmt.Printf("indexed %d symbols in %d files (%d packages) -> %s\n",
			len(ix.Symbols), len(ix.FileHashes), len(ix.Pkgs), store)
		fmt.Printf("languages: %s\n", strings.Join(ix.Languages(), ", "))

	case "sec":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := f.root
		if root == "" {
			root = "."
			if len(args) > 0 {
				root = args[0]
			}
		}
		max := f.max
		var allow []string
		if f.severity != "" {
			allow = strings.Split(f.severity, ",")
		}
		findings, serr := sec.Scan(root)
		if serr != nil {
			fatal("kern sec: %v", serr)
		}
		findings = sec.FilterBySeverity(findings, allow)
		if f.json {
			if err := json.NewEncoder(os.Stdout).Encode(findings); err != nil {
				fatal("%v", err)
			}
			return
		}
		fmt.Print(sec.Render(findings, max))
		counts := sec.Counts(findings)
		fmt.Fprintf(os.Stderr, "kern sec: %d findings (%d error, %d warning, %d info)\n",
			len(findings), counts["error"], counts["warning"], counts["info"])
		if counts["error"] > 0 {
			os.Exit(1)
		}

	case "delete":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if len(args) < 1 {
			fatal("usage: kern delete <symbol> [root] [--json]")
		}
		sym := args[0]
		root := f.root
		if root == "" {
			root = "."
			if len(args) > 1 {
				root = args[1]
			}
		}
		ix, err := loadOrBuild(root)
		if err != nil {
			fatal("%v", err)
		}
		r := intel.DeleteCheck(ix, sym)
		if f.json {
			printJSON(r)
			return
		}
		fmt.Println(intel.RenderDelete(r))
		if !r.Safe {
			os.Exit(1)
		}

	case "rename":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if len(args) < 2 {
			fatal("usage: kern rename <old> <new> [root] [--apply] [--json]")
		}
		oldName, newName := args[0], args[1]
		root := f.root
		if root == "" {
			root = "."
			if len(args) > 2 {
				root = args[2]
			}
		}
		ix, err := loadOrBuild(root)
		if err != nil {
			fatal("%v", err)
		}
		rep, err := rename.Rename(ix, oldName, newName)
		if err != nil {
			fatal("%v", err)
		}
		if f.json {
			printJSON(rep)
			return
		}
		fmt.Println(rename.Render(rep))
		if f.apply {
			if _, err := rename.Apply(root, rep); err != nil {
				fatal("apply failed (files restored): %v", err)
			}
			fmt.Printf("kern rename: %d edits applied; index will rebuild automatically\n", len(rep.Edits))
			if rep.Backup != "" {
				fmt.Printf("kern rename: backup at %s\n", rep.Backup)
			}
		}

	case "watch":
		root := "."
		interval := 5
		if len(rest) > 0 {
			root = rest[0]
		}
		mode := "polling"
		if _, _, err := project.WatcherCommand(root); err == nil {
			mode = "file-event (inotifywait/fswatch)"
		}
		fmt.Printf("kern watch: monitoring %s (mode: %s)\n", root, mode)
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		err := project.Watch(ctx, root, time.Duration(interval)*time.Second, func(changes []index.Change, ix *index.Index) {
			for _, c := range changes {
				fmt.Printf("[kern] %-8s %s\n", c.Kind, c.File)
			}
			fmt.Printf("[kern] index updated: %d symbols, %d packages (%s)\n",
				len(ix.Symbols), len(ix.Pkgs), strings.Join(ix.Languages(), ", "))
		}, func(err error) {
			fmt.Fprintf(os.Stderr, "[kern] watch error: %v\n", err)
		})
		if err != nil && err != context.Canceled {
			fatal("%v", err)
		}

	case "ast":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if len(args) < 1 {
			fatal("usage: kern ast <pattern> [root] [--all]")
		}
		pattern := args[0]
		if f.all {
			files, err := os.ReadDir(cache.Path("index"))
			if err != nil {
				fatal("%v", err)
			}
			for _, e := range files {
				if !strings.HasSuffix(e.Name(), ".json") {
					continue
				}
				ix, err := index.LoadFile(filepath.Join(cache.Path("index"), e.Name()))
				if err != nil {
					continue
				}
				for _, m := range ix.Search(pattern, 50) {
					fmt.Printf("%-28s %-10s %-7s %-24s %s:%d\n", ix.Root, m.Kind, m.Lang, m.FullName(), m.File, m.Line)
				}
			}
			return
		}
		root := "."
		if len(args) > 1 {
			root = args[1]
		}
		ix, err := loadOrBuild(root)
		if err != nil {
			fatal("%v", err)
		}
		for _, m := range ix.Search(pattern, 50) {
			fmt.Printf("%-10s %-7s %-24s %s:%d\n", m.Kind, m.Lang, m.FullName(), m.File, m.Line)
		}

	case "repos":
		if len(rest) == 0 || rest[0] == "list" {
			reg, err := intel.LoadRepos()
			if err != nil {
				fatal("%v", err)
			}
			if len(reg.Repos) == 0 {
				fmt.Println("no repos registered (kern repos add <path> [name])")
				return
			}
			for _, r := range reg.Repos {
				fmt.Printf("%-16s %s\n", r.Name, r.Root)
			}
			return
		}
		switch rest[0] {
		case "add":
			if len(rest) < 2 {
				fatal("usage: kern repos add <path> [name]")
			}
			name := ""
			if len(rest) > 2 {
				name = rest[2]
			}
			reg, err := intel.LoadRepos()
			if err != nil {
				fatal("%v", err)
			}
			if err := reg.Add(rest[1], name); err != nil {
				fatal("%v", err)
			}
			if err := reg.Save(); err != nil {
				fatal("%v", err)
			}
			added, _ := reg.Get(name)
			if name == "" {
				added, _ = reg.Get(filepath.Base(rest[1]))
			}
			fmt.Printf("added %s -> %s\n", added.Name, added.Root)
		case "remove":
			if len(rest) < 2 {
				fatal("usage: kern repos remove <name>")
			}
			reg, err := intel.LoadRepos()
			if err != nil {
				fatal("%v", err)
			}
			if !reg.Remove(rest[1]) {
				fatal("no repo named: %s", rest[1])
			}
			if err := reg.Save(); err != nil {
				fatal("%v", err)
			}
			fmt.Printf("removed %s\n", rest[1])
		default:
			fatal("usage: kern repos (list|add <path> [name]|remove <name>)")
		}

	case "search":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if len(args) < 1 {
			fatal("usage: kern search <query> [root] [--limit N] [--repos] [--json] [--semantic]")
		}
		query := args[0]
		root := "."
		if len(args) > 1 {
			root = args[1]
		}
		limit := f.limit
		if limit <= 0 {
			limit = 20
		}
		if f.repos {
			hits := intel.SearchRepos(query, limit)
			if len(hits) == 0 {
				fmt.Printf("no symbols matched across repos: %s\n", query)
				return
			}
			if f.json {
				printJSON(hits)
				return
			}
			fmt.Println(intel.FormatRepoHits(hits))
			return
		}
		ix, err := loadOrBuild(root)
		if err != nil {
			fatal("%v", err)
		}
		var matches []index.Symbol
		if f.semantic {
			client := llm.New("")
			if !client.HasEmbeddingModel() {
				fatal("embedding model %q not installed (run: ollama pull %s)", llm.EmbedModel(), llm.EmbedModel())
			}
			matches = intel.SemanticSearch(ix, query, limit, client)
		} else {
			matches = intel.RankedSearch(ix, query, limit)
		}
		if len(matches) == 0 {
			fmt.Printf("no symbols matched: %s\n", query)
			return
		}
		if f.json {
			printJSON(matches)
			return
		}
		for _, m := range matches {
			fmt.Printf("%-10s %-7s %-24s %s:%d\n", m.Kind, m.Lang, m.FullName(), m.File, m.Line)
		}

	case "graph":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if len(args) < 1 {
			fatal("usage: kern graph <symbol> [root] [--mermaid] [--json] [--graphml] [--html] [--out FILE]")
		}
		symbol := args[0]
		root := "."
		if len(args) > 1 {
			root = args[1]
		}
		ix, err := loadOrBuild(root)
		if err != nil {
			fatal("%v", err)
		}
		if f.mermaid {
			fmt.Println(ix.Mermaid(symbol))
			return
		}
		if f.json || f.graphml || f.html {
			g, ok := ix.Neighborhood(symbol)
			if !ok {
				fatal("no symbol found: %s", symbol)
			}
			var out string
			switch {
			case f.json:
				out = g.GraphJSON()
			case f.graphml:
				out = g.GraphGraphML()
			default:
				out = g.GraphHTML()
			}
			if f.out != "" {
				if err := os.WriteFile(f.out, []byte(out), 0o644); err != nil {
					fatal("%v", err)
				}
				fmt.Printf("wrote %s (%d bytes)\n", f.out, len(out))
				return
			}
			fmt.Println(out)
			return
		}
		fmt.Println(ix.Graph(symbol))

	case "inherits":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if len(args) < 1 {
			fatal("usage: kern inherits <symbol> [root] [--json]")
		}
		symbol := args[0]
		root := "."
		if len(args) > 1 {
			root = args[1]
		}
		ix, err := loadOrBuild(root)
		if err != nil {
			fatal("%v", err)
		}
		sym, ok := ix.FindSymbol(symbol)
		if !ok {
			fatal("no symbol found: %s", symbol)
		}
		sup := ix.SupertypesOf(sym)
		sub := ix.SubtypesOf(sym)
		if f.json {
			b, _ := json.MarshalIndent(map[string]any{
				"symbol":     sym.FullName(),
				"supertypes": sup,
				"subtypes":   sub,
			}, "", "  ")
			fmt.Println(string(b))
			return
		}
		fmt.Printf("%s (%s)\n", sym.FullName(), sym.Kind)
		if len(sup) == 0 && len(sub) == 0 {
			fmt.Println("  no inheritance edges")
		}
		for _, s := range sup {
			fmt.Printf("  supertype: %s\n", s)
		}
		for _, s := range sub {
			fmt.Printf("  subtype:   %s\n", s)
		}

	case "context":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if len(args) < 1 {
			fatal("usage: kern context <symbol> [root] [--lines N]")
		}
		symbol := args[0]
		root := "."
		if len(args) > 1 {
			root = args[1]
		}
		ix, err := loadOrBuild(root)
		if err != nil {
			fatal("%v", err)
		}
		// --lines is documented and must be honored, not eaten as a symbol
		// (W2-36).
		lines := f.lines
		if lines <= 0 {
			lines = 12
		}
		ctxText := ix.Context(symbol, lines)
		if ctxText == "" {
			fatal("no symbol found: %s", symbol)
		}
		fmt.Println(ctxText)

	case "why":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if len(args) < 1 {
			fatal("usage: kern why <symbol> [root] [--json]")
		}
		symbol := args[0]
		root := "."
		if len(args) > 1 {
			root = args[1]
		}
		ix, err := loadOrBuild(root)
		if err != nil {
			fatal("%v", err)
		}
		info, ok := intel.Why(ix, symbol)
		if !ok {
			fatal("no symbol found: %s", symbol)
		}
		if f.json {
			printJSON(info)
			return
		}
		fmt.Println(intel.FormatWhy(info))

	case "wiki":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		if len(args) > 0 {
			root = args[0]
		}
		ix, err := loadOrBuild(root)
		if err != nil {
			fatal("%v", err)
		}
		outDir := f.out
		if outDir == "" {
			outDir = filepath.Join(root, ".kern", "wiki")
		}
		written, err := intel.WikiExport(ix, outDir)
		if err != nil {
			fatal("%v", err)
		}
		fmt.Printf("wrote %d pages to %s\n", len(written), outDir)

	case "changes", "review":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		if len(args) > 0 {
			root = args[0]
		}
		ix, err := intel.ReadIndex(root)
		if err != nil {
			fatal("%v", err)
		}
		var changes []intel.FileChange
		if f.file != "" {
			for _, p := range strings.Split(f.file, ",") {
				if p = strings.TrimSpace(p); p != "" {
					changes = append(changes, intel.FileChange{File: p})
				}
			}
		} else {
			from, to := splitRange(f.range_)
			changes, err = intel.FilesForRangeL(root, from, to)
			if err != nil {
				fatal("%v", err)
			}
		}
		if len(changes) == 0 {
			// Clean tree is a success for CI: nothing to report.
			fmt.Println("no changed files (clean)")
			return
		}
		if cmd == "review" {
			report := intel.AnalyzeChangesRanged(ix, changes)
			if f.json {
				// --json is honored here too (same shape as `kern changes
				// --json`), not silently dropped for the markdown view
				// (W2-23).
				printJSON(report)
				if report.TotalRisk > 0 {
					os.Exit(1)
				}
				return
			}
			fmt.Println(intel.ReviewRanged(ix, changes, f.max))
			if report.TotalRisk > 0 {
				fmt.Fprintf(os.Stderr, "kern: %d changed file(s) with risk (total %.1f); exit 1\n", len(report.Changes), report.TotalRisk)
				os.Exit(1)
			}
			return
		}
		report := intel.AnalyzeChangesRanged(ix, changes)
		if f.json {
			printJSON(report)
			if report.TotalRisk > 0 {
				os.Exit(1)
			}
			return
		}
		fmt.Println(intel.RenderChanges(report))
		if report.TotalRisk > 0 {
			fmt.Fprintf(os.Stderr, "kern: %d changed file(s) with risk (total %.1f); exit 1\n", len(report.Changes), report.TotalRisk)
			os.Exit(1)
		}

	case "hubs":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		if len(args) > 0 {
			root = args[0]
		}
		ix, err := intel.ReadIndex(root)
		if err != nil {
			fatal("%v", err)
		}
		limit := f.limit
		if limit <= 0 {
			limit = 10
		}
		if f.json {
			printJSON(map[string]any{
				"hubs":    intel.Hubs(ix, limit),
				"bridges": intel.Bridges(ix, 15),
			})
			return
		}
		fmt.Println(intel.RenderHubs(intel.Hubs(ix, limit)))
		fmt.Println()
		fmt.Println(intel.RenderBridges(intel.Bridges(ix, 15)))

	case "bridges":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		if len(args) > 0 {
			root = args[0]
		}
		ix, err := intel.ReadIndex(root)
		if err != nil {
			fatal("%v", err)
		}
		limit := f.limit
		if limit <= 0 {
			limit = 15
		}
		if f.json {
			printJSON(map[string]any{"bridges": intel.Bridges(ix, limit)})
			return
		}
		fmt.Println(intel.RenderBridges(intel.Bridges(ix, limit)))

	case "testgaps":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		if len(args) > 0 {
			root = args[0]
		}
		ix, err := intel.ReadIndex(root)
		if err != nil {
			fatal("%v", err)
		}
		cov := intel.AnalyzeCoverage(ix)
		if f.json {
			printJSON(map[string]any{
				"coverage": cov,
				"gaps":     intel.TestGaps(ix, f.limit),
			})
			return
		}
		fmt.Println(cov.Render())

	case "flows":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		if len(args) > 0 {
			root = args[0]
		}
		ix, err := intel.ReadIndex(root)
		if err != nil {
			fatal("%v", err)
		}
		flows := intel.Flows(ix, f.limit, 12)
		if f.json {
			printJSON(map[string]any{"flows": flows})
			return
		}
		fmt.Println(intel.RenderFlows(flows))

	case "entries":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		if len(args) > 0 {
			root = args[0]
		}
		ix, err := intel.ReadIndex(root)
		if err != nil {
			fatal("%v", err)
		}
		limit := f.limit
		if limit <= 0 {
			limit = 50
		}
		var b strings.Builder
		n := 0
		for _, s := range ix.Symbols {
			if !s.Entry || s.Framework == "" {
				continue
			}
			fmt.Fprintf(&b, "%s %s %s %s:%d\n", s.Framework, s.FullName(), s.Route, s.File, s.Line)
			n++
			if n >= limit {
				break
			}
		}
		if n == 0 {
			fmt.Println("no framework entry points in index (run kern index to populate)")
			return
		}
		fmt.Print(b.String())

	case "communities":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		if len(args) > 0 {
			root = args[0]
		}
		ix, err := intel.ReadIndex(root)
		if err != nil {
			fatal("%v", err)
		}
		comms := intel.Communities(ix)
		if f.json {
			printJSON(map[string]any{"communities": comms})
			return
		}
		fmt.Println(intel.RenderCommunities(comms))

	case "path":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		// `kern path` with no symbols keeps the legacy behaviour of printing
		// the cache directory.
		if len(args) < 2 {
			fmt.Println(cache.Dir())
			return
		}
		root := "."
		if len(args) > 2 {
			root = args[2]
		}
		ix, err := intel.ReadIndex(root)
		if err != nil {
			fatal("%v", err)
		}
		from, okFrom := intel.Resolve(ix, args[0])
		to, okTo := intel.Resolve(ix, args[1])
		if !okFrom {
			fatal("unknown symbol: %s", args[0])
		}
		if !okTo {
			fatal("unknown symbol: %s", args[1])
		}
		path := intel.ShortestPath(ix, from, to)
		if f.json {
			printJSON(map[string]any{
				"from": from, "to": to,
				"path": path,
			})
			return
		}
		fmt.Println(intel.RenderPath(ix, path))

	case "dead":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		if len(args) > 0 {
			root = args[0]
		}
		ix, err := intel.ReadIndex(root)
		if err != nil {
			fatal("%v", err)
		}
		dead := intel.DeadCode(ix)
		if f.limit > 0 && len(dead) > f.limit {
			dead = dead[:f.limit]
		}
		if f.json {
			printJSON(map[string]any{"dead": dead})
			return
		}
		fmt.Println(intel.RenderDead(dead))

	case "larges":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		if len(args) > 0 {
			root = args[0]
		}
		ix, err := intel.ReadIndex(root)
		if err != nil {
			fatal("%v", err)
		}
		minLines := f.lines
		if minLines <= 0 {
			minLines = 60
		}
		large := intel.LargeFunctions(ix, minLines)
		if f.json {
			printJSON(map[string]any{"min_lines": minLines, "large": large})
			return
		}
		fmt.Println(intel.RenderLarge(large))

	case "arch":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		if len(args) > 0 {
			root = args[0]
		}
		ix, err := intel.ReadIndex(root)
		if err != nil {
			fatal("%v", err)
		}
		a := intel.AnalyzeArchitecture(ix)
		if f.json {
			printJSON(a)
			return
		}
		fmt.Println(intel.RenderArch(a))

	case "churn":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		if len(args) > 0 {
			root = args[0]
		}
		from, to := splitRange(f.range_)
		report, err := intel.Churn(root, from, to)
		if err != nil {
			fatal("%v", err)
		}
		if f.json {
			printJSON(report)
			return
		}
		fmt.Println(intel.RenderChurn(report))

	case "cochange":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		if len(args) > 0 {
			root = args[0]
		}
		from, to := splitRange(f.range_)
		report, err := intel.CoChange(root, from, to)
		if err != nil {
			fatal("%v", err)
		}
		if f.json {
			printJSON(report)
			return
		}
		fmt.Println(intel.RenderCoChange(report, f.limit))

	case "explore":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if len(args) < 1 {
			fatal("usage: kern explore <symbol> [root] [--depth N] [--max N]")
		}
		root := "."
		if len(args) > 1 {
			root = args[1]
		}
		ix, err := intel.ReadIndex(root)
		if err != nil {
			fatal("%v", err)
		}
		depth := f.depth
		if depth < 0 {
			depth = 0
		}
		maxN := f.max
		if maxN < 0 {
			maxN = 0
		}
		rep, err := intel.Explore(ix, args[0], depth, maxN)
		if err != nil {
			fatal("%v", err)
		}
		if f.json {
			printJSON(rep)
			return
		}
		fmt.Println(intel.RenderExplore(rep))

	case "fts":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if len(args) < 1 {
			fatal("usage: kern fts \"<query>\" [root] [--limit N]")
		}
		root := "."
		if len(args) > 1 {
			root = args[1]
		}
		limit := f.limit
		if limit <= 0 {
			limit = 20
		}
		matches, err := index.FTS5Search(root, args[0], limit)
		if err != nil {
			fatal("%v", err)
		}
		if f.json {
			printJSON(matches)
			return
		}
		for _, m := range matches {
			fmt.Printf("%s %s %s:%d\n", m.Kind, m.FullName(), m.File, m.Line)
		}

	case "near", "walk":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if len(args) < 1 {
			fatal("usage: kern near <symbol> [root] [--depth N] [--max N]")
		}
		root := "."
		if len(args) > 1 {
			root = args[1]
		}
		ix, err := intel.ReadIndex(root)
		if err != nil {
			fatal("%v", err)
		}
		depth := f.depth
		if depth < 0 {
			depth = 2
		}
		maxN := f.max
		if maxN <= 0 {
			maxN = 100
		}
		nodes, err := intel.Near(ix, args[0], depth, maxN)
		if err != nil {
			fatal("%v", err)
		}
		if f.json {
			printJSON(map[string]any{"depth": depth, "max_nodes": maxN, "nodes": nodes})
			return
		}
		fmt.Println(intel.RenderNear(ix, nodes))

	case "probe":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if len(args) < 1 {
			fatal("usage: kern probe \"<task text>\" [root] [--max N]")
		}
		root := "."
		if len(args) > 1 {
			root = args[1]
		}
		ix, err := intel.ReadIndex(root)
		if err != nil {
			fatal("%v", err)
		}
		maxTokens := f.max
		if maxTokens <= 0 {
			maxTokens = 4000
		}
		report := intel.Probe(ix, args[0], maxTokens)
		if f.json {
			printJSON(report)
			return
		}
		text := intel.RenderProbe(report)
		if report.Truncated {
			text = intel.FitProbe(text, maxTokens)
		}
		fmt.Println(text)

	case "trace":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if len(args) < 1 {
			fatal("usage: kern trace <file|- for stdin> [root] [--limit N]")
		}
		sourceName := args[0]
		var src string
		if sourceName == "-" {
			b, err := readStdin()
			if err != nil {
				fatal("%v", err)
			}
			src = string(b)
		} else {
			b, err := os.ReadFile(sourceName)
			if err != nil {
				fatal("%v", err)
			}
			src = string(b)
		}
		root := "."
		if len(args) > 1 {
			root = args[1]
		}
		ix, err := intel.ReadIndex(root)
		if err != nil {
			fatal("%v", err)
		}
		report := intel.Trace(ix, src, sourceName, f.limit)
		if f.json {
			printJSON(report)
			return
		}
		fmt.Println(intel.RenderTrace(report))

	case "lock":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if len(args) < 1 {
			fatal("usage: kern lock <scope> [root]")
		}
		root := f.root
		if root == "" {
			root = "."
		}
		if len(args) > 1 {
			root = args[1]
		}
		scope := args[0]
		lk, err := lock.Acquire(root, scope)
		if err != nil {
			_, pid, _ := lock.Held(root, scope)
			fatal("lock %q is held (pid %d): %v", scope, pid, err)
		}
		defer lk.Release()
		if f.hold {
			// Non-blocking mode for tool/plugin callers: acquire, report, and
			// return immediately. The OS releases the flock on exit, so the
			// lock is only held for the duration of this process — use
			// `kern status` to verify and `kern unlock` to clear the marker.
			fmt.Printf("lock acquired: %s (pid %d). note: the lock marker persists until `kern unlock`; the flock releases when this process exits.\n", scope, os.Getpid())
			return
		}
		fmt.Printf("lock acquired: %s (pid %d). releasing on interrupt.\n", scope, os.Getpid())
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		<-ctx.Done()
		fmt.Printf("lock released: %s\n", scope)

	case "unlock":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		if len(args) < 1 {
			fatal("usage: kern unlock <scope> [root]")
		}
		root := f.root
		if root == "" {
			root = "."
		}
		if len(args) > 1 {
			root = args[1]
		}
		if err := lock.Remove(root, args[0]); err != nil {
			fatal("%v", err)
		}
		fmt.Printf("lock removed: %s\n", args[0])

	case "status":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		root := "."
		if len(args) > 0 {
			root = args[0]
		}
		sts, err := lock.List(root)
		if err != nil {
			fatal("%v", err)
		}
		if f.json {
			printJSON(map[string]any{"locks": sts})
			return
		}
		if len(sts) == 0 {
			fmt.Println("no locks in workspace")
			return
		}
		for _, s := range sts {
			state := "free"
			if s.Held {
				state = "HELD"
			}
			holder := ""
			if s.PID > 0 {
				holder = fmt.Sprintf(" (pid %d)", s.PID)
			}
			fmt.Printf("  %-24s %-6s%s\n", s.Scope, state, holder)
		}

	case "guard":
		f, args, err := parseFlags(rest)
		if err != nil {
			fatal("flags: %v", err)
		}
		sub := "check"
		if len(args) > 0 {
			sub = args[0]
		}
		root := "."
		if len(args) > 1 {
			root = args[1]
		}
		switch sub {
		case "init":
			if err := intel.InitBoundaries(root); err != nil {
				fatal("%v", err)
			}
			fmt.Printf("wrote %s (edit it to declare boundary rules)\n", intel.DefaultBoundariesPath(root))
		case "check":
			ix, err := intel.ReadIndex(root)
			if err != nil {
				fatal("%v", err)
			}
			b, err := intel.LoadBoundaries(root)
			if err != nil {
				fatal("%v", err)
			}
			var files []string
			if f.file != "" {
				for _, p := range strings.Split(f.file, ",") {
					if p = strings.TrimSpace(p); p != "" {
						files = append(files, p)
					}
				}
			} else {
				from, to := splitRange(f.range_)
				files, err = intel.FilesForRange(root, from, to)
				if err != nil {
					fatal("%v (or pass --file f1,f2 to check specific files)", err)
				}
			}
			if len(files) == 0 {
				fatal("no changed files (use --file f1,f2 or --range a..b, or make edits)")
			}
			for _, p := range files {
				if _, err := os.Stat(filepath.Join(root, p)); err != nil {
					if os.IsNotExist(err) {
						// Deleted in the diff: nothing to check — the file's
						// symbols are gone from the index too (W2-20).
						continue
					}
					fatal("file not found: %s", p)
				}
			}
			violations := intel.CheckBoundaries(ix, b, files)
			switch {
			case f.sarif:
				fmt.Println(intel.RenderViolationsSARIF(violations, version))
			case f.json:
				printJSON(map[string]any{"violations": violations})
			default:
				fmt.Println(intel.RenderViolations(violations))
			}
			if f.threshold >= 0 && len(violations) > f.threshold {
				os.Exit(2)
			}
		default:
			fatal("usage: kern guard <check|init> [root] [--file f1,f2] [--range a..b] [--json|--sarif] [--threshold N]")
		}

	default:
		usage()
		os.Exit(1)
	}
}

func wireRecorder() {
	rec, err := stats.NewRecorder()
	if err == nil {
		optimize.Recorder = rec
	}
}

func loadOrBuild(root string) (*index.Index, error) {
	if ix, err := index.Load(root); err == nil && ix != nil && !ix.Stale() {
		return ix, nil
	}
	ix, err := index.Build(root)
	if err != nil {
		return nil, err
	}
	_ = ix.Save()
	return ix, nil
}

// splitRange parses a git range "a..b" into (from, to). A single ref is kept
// as "from" with "to" empty, which git treats as "compare to working tree".
func splitRange(r string) (string, string) {
	if r == "" {
		return "", ""
	}
	if p := strings.SplitN(r, "..", 2); len(p) == 2 {
		return p[0], p[1]
	}
	return r, ""
}

func printJSON(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(string(b))
}

func projectLangs() string {
	ix, err := loadOrBuild(".")
	if err != nil {
		return ""
	}
	return strings.Join(ix.Languages(), ", ")
}

func fileContext(path string) string {
	content, err := code.ReadFile(path)
	if err != nil {
		return ""
	}
	return code.Summarize(path, content, 200).Render()
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	os.Exit(1)
}

func splitNames(s string) []string {
	var out []string
	for _, n := range strings.Split(s, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func loadSchema(spec string) (*schema.Schema, error) {
	if strings.HasPrefix(spec, "{") {
		return schema.Parse(spec)
	}
	b, err := os.ReadFile(spec)
	if err != nil {
		return nil, fmt.Errorf("cannot read schema: %w", err)
	}
	return schema.Parse(string(b))
}

func pct(before, after int) float64 {
	if before <= 0 {
		return 0
	}
	return float64(before-after) / float64(before) * 100
}

// hasSemantic reports whether any indexed doc carries a dense embedding.
func hasSemantic(ix *docsearch.Index) bool {
	for _, d := range ix.Docs {
		if len(d.Semantic) > 0 {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// gitDiff runs a git diff subcommand in the current directory and returns its
// stdout.
func gitDiff(args string) ([]byte, error) {
	cmd := exec.Command("git", strings.Split(args, " ")...)
	return cmd.Output()
}

// gitOutput runs a git subcommand and returns its combined output.
func gitOutput(args ...string) ([]byte, error) {
	return exec.Command("git", args...).CombinedOutput()
}

// gitCommit creates a commit with the given message fed over stdin, so no
// message ever appears in a shell argument or the process table.
func gitCommit(message string) ([]byte, error) {
	cmd := exec.Command("git", "commit", "-F", "-")
	cmd.Stdin = strings.NewReader(message)
	return cmd.CombinedOutput()
}

// shortHash returns the current HEAD's short hash, or "" if none exists yet.
func shortHash() string {
	out, err := gitDiff("rev-parse --short HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// slugName derives a filesystem-safe document name from a URL, e.g.
// https://react.dev/reference/usestate -> react.dev-reference-usestate.
func slugName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "doc"
	}
	name := u.Hostname() + u.Path
	name = strings.TrimSuffix(name, "/")
	return sanitizeDocName(name)
}

// sanitizeDocName constrains a doc name to a safe cache filename:
// lowercase alphanumerics and dashes only, so path separators, "../" or
// absolute paths can never escape the cache root. Falls back to "doc".
func sanitizeDocName(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.TrimSpace(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastDash = false
		} else if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + ('a' - 'A'))
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "doc"
	}
	return out
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
