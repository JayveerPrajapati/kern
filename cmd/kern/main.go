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
	range_  string
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
		case "--all":
			f.all = true
		case "--clear":
			f.clear = true
		case "--max":
			i++
			if i < len(args) {
				f.max, _ = strconv.Atoi(args[i])
			}
		case "--range":
			i++
			if i < len(args) {
				f.range_ = args[i]
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
				Operations  int `json:"operations"`
				BeforeTotal int `json:"before_tokens"`
				AfterTotal  int `json:"after_tokens"`
				SavedTotal  int `json:"saved_tokens"`
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

	case "graph":
		if len(rest) < 1 {
			fatal("usage: kern graph <symbol> [root] [--mermaid]")
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

	case "path":
		fmt.Println(cache.Dir())

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
	if ix, err := index.Load(root); err == nil && ix != nil {
		return ix, nil
	}
	ix, err := index.Build(root)
	if err != nil {
		return nil, err
	}
	_ = ix.Save()
	return ix, nil
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
