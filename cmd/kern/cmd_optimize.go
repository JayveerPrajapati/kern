package main

import (
	"encoding/csv"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/semcache"
	"github.com/JayveerPrajapati/kern/internal/stats"
	"github.com/JayveerPrajapati/kern/internal/terse"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func runOptimize(cmd string, rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	prompt := strings.Join(args, " ")
	if prompt == "" || prompt == "-" {
		b, err := readStdin()
		if err != nil {
			fatal("cannot read stdin: %v", err)
		}
		if len(b) > 0 {
			prompt = string(b)
		}
	}
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

}

func runCompact(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("usage: kern compact [--root DIR] <file>")
	}
	file := args[0]
	// Root-confinement: the MCP handler for kern_compact_file validates paths
	// against the workspace root. The CLI path must enforce the same policy so
	// a plugin invoking the binary directly cannot read arbitrary files.
	// Without an explicit --root, reject absolute paths and parent-relative
	// ("..") paths outright.
	if f.root == "" {
		if filepath.IsAbs(file) || strings.Contains(file, "..") {
			fatal("refusing to read %q: absolute or parent-relative paths require --root", file)
		}
	} else {
		resolved, err := confineToRoot(f.root, file)
		if err != nil {
			fatal("%v", err)
		}
		file = resolved
	}
	content, err := code.ReadFile(file)
	if err != nil {
		fatal("%v", err)
	}
	sum := code.Summarize(file, content, 200)
	fmt.Println(sum.Render())

}

// confineToRoot resolves file so it stays lexically inside root, mirroring the
// filepath.Rel + ".." containment check the MCP handler applies to kern_compact_file.
// It returns the resolved path to read.
func confineToRoot(root, file string) (string, error) {
	var abs string
	if filepath.IsAbs(file) {
		abs = filepath.Clean(file)
	} else {
		abs = filepath.Join(root, file)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", file, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %s escapes project root %s", abs, root)
	}
	return abs, nil
}

func runLog(rest []string) {
	var b []byte
	var err error
	if len(rest) < 1 || rest[0] == "-" {
		b, err = readStdin()
		if err != nil {
			fatal("%v", err)
		}
	} else {
		b, err = os.ReadFile(rest[0])
		if err != nil {
			fatal("%v", err)
		}
	}
	wireRecorder()
	res, err := optimize.Log(string(b), optimize.Options{})
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(res.Output)
	fmt.Fprintf(os.Stderr, "kern: %d -> %d tokens (saved %d, %.1f%%)\n", res.BeforeTokens, res.AfterTokens, res.SavedTokens, res.SavedPercent)

}

func runTokens(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	text := strings.Join(args, " ")
	if text == "" {
		fatalUsage("usage: kern tokens [--bpe] <text>")
	}
	if f.bpe {
		fmt.Println(tokenize.CountBPE(text))
		return
	}
	fmt.Println(tokenize.Count(text))

}

func runBudget(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
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
		fatalUsage("usage: kern budget \"<text>\" --max N  (or pipe stdin)")
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

}

func runTerse(rest []string) {
	_, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
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
		fatalUsage("usage: kern terse \"<text>\" [--max]  (or pipe stdin)")
	}
	out, dropped := terse.Compress(text)
	before := tokenize.Count(text)
	after := tokenize.Count(out)
	fmt.Fprintf(os.Stderr, "kern: %d -> %d tokens (saved %d, %.1f%%, %d filler lines dropped)\n", before, after, before-after, pct(before, after), dropped)
	fmt.Println(out)

}

func runSemcache(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
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
			fatalUsage("usage: kern semcache list <prompt|log>")
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
			fatalUsage("usage: kern semcache sim <textA> <textB>")
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

}

func runStats(cmd string, rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
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

}
