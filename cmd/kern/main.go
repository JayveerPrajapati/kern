// Command kern is the local context optimizer: prompt compression, log
// stripping, project mapping, compact build runs and token savings reports.
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/JayveerPrajapati/kern/internal/brief"
	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/doctor"
	"github.com/JayveerPrajapati/kern/internal/hooks"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/lock"
	"github.com/JayveerPrajapati/kern/internal/mcp"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/prompt"
	"github.com/JayveerPrajapati/kern/internal/setup"
	"github.com/JayveerPrajapati/kern/internal/stats"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
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
  kern build "<command>" [--dir DIR]              run build, compact output
  kern log <file>                                 compress a log file
  kern index [root]                               build/refresh the AST index
  kern watch [root]                               daemon: auto re-index on change
  kern ast <pattern> [--all]                      AST symbol search (wildcards, kind prefixes)
  kern search <query> [--limit N] [--repos] [--json]
                                 ranked free-text symbol search (--repos: across registered repos)
  kern repos (list|add <path> [name]|remove <name>)
                                 multi-repo registry for cross-repo search
  kern graph <symbol> [--mermaid] [--json] [--graphml] [--html] [--out FILE]
                                 definition + callers + what it calls; export as JSON/GraphML/HTML
  kern context <symbolRegex> [--lines N]          minimal source slice for a symbol
  kern why <symbol> [--json]                      rationale: doc comment + who depends on it and why
  kern wiki [root] [--out DIR]                    export a markdown wiki (one page per package)
  kern stats [--days N] [--session ID] [--json]
  kern diff [--session ID]                        recent before/after entries
  kern export --csv                               export stats to CSV
  kern tokens [--bpe] "<text>"                    token count (estimator or exact BPE)
  kern setup [--root DIR] [--agents mcp,opencode,claude]   wire kern into agents (idempotent)
  kern setup --check                              show wiring status
  kern buddy [root]                               session onboarding digest for any agent
  kern prompt <template> [--file PATH] [--task TEXT]   fine-tuned prompt template
  kern prompt list                                list templates
  kern remember "<lesson>"                        record a lesson in project memory
  kern memory [--clear]                           show project memory
  kern budget "<text>" --max N                    fit text into a token budget
  kern doctor [root]                              diagnostics report
  kern hook install                               install post-commit diff->memory hook
  kern hook diff [range]                          compressed git diff (default HEAD~1..HEAD)
  kern hook store [range]                         store compressed diff in project memory
  kern changes [root] [--range a..b] [--file F] [--json]   change-impact: blast radius, risk, test gaps
  kern review [root] [--range a..b] [--max N]     token-optimised review context for changed files
  kern hubs [root] [--limit N] [--json]           most depended-on symbols + cross-package bridges
  kern testgaps [root] [--limit N] [--json]       test coverage + untested hotspots
  kern flows [root] [--limit N] [--json]          execution flows from entry points
  kern communities [root] [--json]                call-graph communities (label propagation)
  kern path <from> <to> [root] [--json]           shortest call path between two symbols
  kern dead [root] [--limit N] [--json]           dead code: symbols with no in-project callers
  kern larges [root] [--lines N] [--limit N] [--json]   largest declarations by source lines
  kern arch [root] [--json]                       architecture overview + coupling warnings
  kern churn [root] [--range a..b] [--json]       change-frequency risk (most-churned files)
  kern near <symbol> [root] [--depth N] [--max N] [--json]   dependency tree N hops away (walk-graph)
  kern walk <symbol> [root] [--depth N] [--max N]             alias of kern near
  kern probe "<task text>" [root] [--max N] [--json]         task -> budget-capped micro-context bundle
  kern trace <file|- for stdin> [root] [--limit N] [--json]  overlay pprof/stack trace on call graph
  kern lock <scope> [root]                       acquire a workspace lock (held until interrupted)
  kern unlock <scope> [root]                     remove a stale lock file
  kern status [root] [--json]                    list workspace locks (held/free)
  kern guard init [root]                         scaffold .kern/boundaries.json
  kern guard check [root] [--file F] [--range a..b] [--json]  reject boundary violations (exit 2)
  kern version                                    show version
  kern mcp                                        run MCP server on stdio
`)
}

type flags struct {
	attach  string
	session string
	model   string
	days    int
	json    bool
	dir     string
	csv     bool
	llm     string
	bpe     bool
	root    string
	check   bool
	agents  string
	file    string
	task    string
	mermaid bool
	all     bool
	clear   bool
	max     int
	limit   int
	lines   int
	depth   int
	range_  string
	graphml bool
	html    bool
	out     string
	repos   bool
}

func parseFlags(args []string) (flags, []string) {
	var f flags
	f.days = 7
	var rest []string
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
				f.days, _ = strconv.Atoi(args[i])
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
				f.max, _ = strconv.Atoi(args[i])
			}
		case "--limit":
			i++
			if i < len(args) {
				f.limit, _ = strconv.Atoi(args[i])
			}
		case "--range":
			i++
			if i < len(args) {
				f.range_ = args[i]
			}
		case "--lines":
			i++
			if i < len(args) {
				f.lines, _ = strconv.Atoi(args[i])
			}
		case "--depth":
			i++
			if i < len(args) {
				f.depth, _ = strconv.Atoi(args[i])
			}
		default:
			rest = append(rest, args[i])
		}
	}
	return f, rest
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

	case "optimize", "preview":
		f, args := parseFlags(rest)
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
			b, err := io.ReadAll(os.Stdin)
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
		res, err := optimize.Prompt(prompt, attach, optimize.Options{Session: f.session, Model: f.model, Source: "cli", LLM: f.llm})
		if err != nil {
			fatal("%v", err)
		}
		fmt.Println(res.Output)
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

	case "build":
		f, args := parseFlags(rest)
		cmdStr := strings.Join(args, " ")
		if cmdStr == "" {
			fatal("usage: kern build <command>")
		}
		wireRecorder()
		res, err := optimize.RunBuild(cmdStr, f.dir, optimize.Options{Session: f.session})
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
		f, args := parseFlags(rest)
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
		f, _ := parseFlags(rest)
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
		for _, s := range setup.Wire(root, agents) {
			mark := "ok"
			if !s.Installed {
				mark = "!!"
			}
			fmt.Printf("[%s] %-22s %s\n", mark, s.Agent, s.Note)
		}
		fmt.Println("\nWiring complete. Restart your agent (opencode reload / claude) to pick up the MCP servers.")

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
		f, args := parseFlags(rest)
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
		fmt.Print(out)

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
		f, _ := parseFlags(rest)
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

	case "budget":
		f, args := parseFlags(rest)
		text := strings.Join(args, " ")
		if text == "" {
			b, err := io.ReadAll(os.Stdin)
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

	case "doctor":
		root := "."
		if len(rest) > 0 {
			root = rest[0]
		}
		fmt.Println(doctor.Render(root, doctor.Run(root)))

	case "hook":
		f, args := parseFlags(rest)
		sub := "store"
		if len(args) > 0 {
			sub = args[0]
		}
		from, to := "", ""
		if f.range_ != "" {
			if p := strings.SplitN(f.range_, "..", 2); len(p) == 2 {
				from, to = p[0], p[1]
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
		default:
			fatal("usage: kern hook <install|diff|store> [--range a..b]")
		}

	case "stats", "diff", "export":
		f, args := parseFlags(rest)
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
		wireRecorder()
		srv := mcp.NewServer(os.Stdin, os.Stdout)
		if err := srv.Serve(); err != nil {
			os.Exit(1)
		}

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
		fmt.Printf("indexed %d symbols in %d files (%d packages) -> %s\n",
			len(ix.Symbols), len(ix.FileHashes), len(ix.Pkgs), index.StorePath(root))
		fmt.Printf("languages: %s\n", strings.Join(ix.Languages(), ", "))

	case "watch":
		root := "."
		interval := 5
		if len(rest) > 0 {
			root = rest[0]
		}
		fmt.Printf("kern watch: monitoring %s (index updates automatically)\n", root)
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		err := index.Watch(ctx, root, time.Duration(interval)*time.Second, func(changes []index.Change, ix *index.Index) {
			for _, c := range changes {
				fmt.Printf("[kern] %-8s %s\n", c.Kind, c.File)
			}
			fmt.Printf("[kern] index updated: %d symbols, %d packages (%s)\n",
				len(ix.Symbols), len(ix.Pkgs), strings.Join(ix.Languages(), ", "))
		})
		if err != nil && err != context.Canceled {
			fatal("%v", err)
		}

	case "ast":
		if len(rest) < 1 {
			fatal("usage: kern ast <pattern> [root] [--all]")
		}
		f, args := parseFlags(rest)
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
		if len(rest) < 1 {
			fatal("usage: kern search <query> [root] [--limit N] [--repos] [--json]")
		}
		f, args := parseFlags(rest)
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
		matches := intel.RankedSearch(ix, query, limit)
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
		if len(rest) < 1 {
			fatal("usage: kern graph <symbol> [root] [--mermaid] [--json] [--graphml] [--html] [--out FILE]")
		}
		f, args := parseFlags(rest)
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

	case "context":
		if len(rest) < 1 {
			fatal("usage: kern context <symbol> [root]")
		}
		symbol := rest[0]
		root := "."
		if len(rest) > 1 {
			root = rest[1]
		}
		ix, err := loadOrBuild(root)
		if err != nil {
			fatal("%v", err)
		}
		ctxText := ix.Context(symbol, 12)
		if ctxText == "" {
			fatal("no symbol found: %s", symbol)
		}
		fmt.Println(ctxText)

	case "why":
		if len(rest) < 1 {
			fatal("usage: kern why <symbol> [root] [--json]")
		}
		f, args := parseFlags(rest)
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
		f, args := parseFlags(rest)
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
		f, args := parseFlags(rest)
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
			fatal("no changed files (use --range a..b, --file f1,f2, or make edits)")
		}
		if cmd == "review" {
			fmt.Println(intel.ReviewRanged(ix, changes, f.max))
			return
		}
		report := intel.AnalyzeChangesRanged(ix, changes)
		if f.json {
			printJSON(report)
			return
		}
		fmt.Println(intel.RenderChanges(report))

	case "hubs":
		f, args := parseFlags(rest)
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

	case "testgaps":
		f, args := parseFlags(rest)
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
		f, args := parseFlags(rest)
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

	case "communities":
		f, args := parseFlags(rest)
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
		f, args := parseFlags(rest)
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
		f, args := parseFlags(rest)
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
		f, args := parseFlags(rest)
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
		f, args := parseFlags(rest)
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
		f, args := parseFlags(rest)
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

	case "near", "walk":
		f, args := parseFlags(rest)
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
		f, args := parseFlags(rest)
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
		f, args := parseFlags(rest)
		if len(args) < 1 {
			fatal("usage: kern trace <file|- for stdin> [root] [--limit N]")
		}
		sourceName := args[0]
		var src string
		if sourceName == "-" {
			b, err := io.ReadAll(os.Stdin)
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
		f, args := parseFlags(rest)
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
		fmt.Printf("lock acquired: %s (pid %d). releasing on interrupt.\n", scope, os.Getpid())
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		<-ctx.Done()
		fmt.Printf("lock released: %s\n", scope)

	case "unlock":
		f, args := parseFlags(rest)
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
		f, args := parseFlags(rest)
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
		f, args := parseFlags(rest)
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
					fatal("file not found: %s", p)
				}
			}
			violations := intel.CheckBoundaries(ix, b, files)
			if f.json {
				printJSON(map[string]any{"violations": violations})
				if len(violations) > 0 {
					os.Exit(2)
				}
				return
			}
			fmt.Println(intel.RenderViolations(violations))
			if len(violations) > 0 {
				os.Exit(2)
			}
		default:
			fatal("usage: kern guard <check|init> [root] [--file f1,f2] [--range a..b]")
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

func pct(before, after int) float64 {
	if before <= 0 {
		return 0
	}
	return float64(before-after) / float64(before) * 100
}
