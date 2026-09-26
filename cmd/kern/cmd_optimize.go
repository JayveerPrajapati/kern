package main

import (
	"encoding/csv"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/code"
	kernctx "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/semcache"
	"github.com/JayveerPrajapati/kern/internal/stats"
	"github.com/JayveerPrajapati/kern/internal/strutil"
	"github.com/JayveerPrajapati/kern/internal/terse"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

func runOptimize(cmd string, rest []string) {
	f, args := parseFlagsOrDie(rest)
	// --kind log routes to the log-compression path (the former `kern log`;
	// `kern log` is now a thin wrapper presetting this kind). It honors the
	// log-specific flags (--context-before/--context-after/--profile/--root)
	// and reproduces the log output byte-for-byte (surface consolidation
	// T2b).
	if f.kind == "log" {
		runLog(rest)
		return
	}
	if f.kind != "" && f.kind != "prompt" {
		fatalUsage("optimize: unknown --kind %q (valid kinds: prompt, log)", f.kind)
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
		fatalUsage("prompt is required (kern %s \"<prompt>\" or pipe stdin)", cmd)
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
		fatal("optimize: %v", err)
	}
	out := res.Output
	// Deterministic (no-LLM) path: optimize.Prompt unmasks its internal
	// placeholders before returning even when no LLM ran, so a --mask run
	// without an LLM backend would echo the original secrets verbatim. Re-apply
	// the deterministic pii.Mask to the output before printing so --mask never
	// echoes known secret patterns (sk-proj-…, emails, …). The LLM path is
	// left unchanged — its output is intentionally unmasked and readable.
	if f.mask && (f.llm == "" || res.LLMSkipped != "") {
		out = pii.Mask(out).Text
	}
	fmt.Println(out)
	if res.FromCache {
		fmt.Fprintf(os.Stderr, "kern: served from cache\n")
	}
	fmt.Fprintf(os.Stderr, "kern: %d -> %d tokens (saved %d, %.1f%%)\n", res.BeforeTokens, res.AfterTokens, res.SavedTokens, res.SavedPercent)
	if res.SavedTokens == 0 && res.BeforeTokens > 0 {
		fmt.Fprintln(os.Stderr, "kern: nothing to compress — try --mask, --attach, or a longer input")
	}
	if res.LLMSkipped != "" {
		fmt.Fprintf(os.Stderr, "kern: warning: %s\n", res.LLMSkipped)
	}

}

func runCompact(rest []string) {
	f, args := parseFlagsOrDie(rest)
	if len(args) < 1 {
		fatalUsage("usage: kern compact [--root ROOT] <file>")
	}
	file := args[0]
	// Root-confinement: the MCP handler for kern_compact_file validates paths
	// against the workspace root. The CLI path must enforce the same policy so
	// a plugin invoking the binary directly cannot read arbitrary files.
	// Without an explicit --root, reject absolute paths and parent-relative
	// ("..") paths outright.
	if f.root == "" {
		if filepath.IsAbs(file) {
			cwd, err := os.Getwd()
			if err != nil {
				fatal("compact: %v", err)
			}
			resolved, err := confineToRoot(cwd, file)
			if err != nil {
				fatal("refusing to read %q: absolute path outside current working directory requires --root", file)
			}
			file = resolved
		} else if strings.Contains(file, "..") {
			fatal("refusing to read %q: parent-relative paths require --root", file)
		}
	} else {
		resolved, err := confineToRoot(f.root, file)
		if err != nil {
			fatal("compact: %v", err)
		}
		file = resolved
	}
	content, err := code.ReadFile(file)
	if err != nil {
		fatal("compact: %v", err)
	}
	// The default tier preserves the historical behavior: a symbolic summary.
	// --tier full returns the whole file and --tier folded returns signatures
	// with bodies elided (each elision counts the lines removed).
	tier := code.TierSummary
	if f.tier != "" {
		t, terr := code.ParseTier(f.tier)
		if terr != nil {
			fatalUsage("%v", terr)
		}
		tier = t
	}
	if f.terseCode {
		content = kernctx.PruneCode(file, content, true)
	}
	rendered := code.RenderTier(file, content, tier)
	fmt.Println(rendered)
	before := tokenize.Count(string(content))
	after := tokenize.Count(rendered)
	printSavingsFooter(os.Stderr, before, after, kernctx.CostPerToken())
	// Record to the savings ledger like every other compression surface
	// (QA Pick #1, finding F-B): without this, `kern compact` under-reports
	// lifetime savings in `kern stats` / `kern diff`.
	if optimize.Recorder == nil {
		_ = optimize.EnsureRecorder()
	}
	if optimize.Recorder != nil {
		_ = optimize.Recorder.Record(stats.Entry{
			Operation:    stats.OpCompactFile,
			Tool:         stats.ToolForOperation(stats.OpCompactFile),
			Source:       file,
			Model:        stats.DefaultModel,
			BeforeTokens: before,
			AfterTokens:  after,
			BeforeBytes:  len(content),
			AfterBytes:   len(rendered),
		})
	}
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
	f, args := parseFlagsOrDie(rest)
	var b []byte
	var rerr error
	src := ""
	if len(args) < 1 || args[0] == "-" {
		b, rerr = readStdin()
		if rerr != nil {
			fatal("log: %v", rerr)
		}
	} else {
		src = args[0]
		b, rerr = os.ReadFile(src)
		if rerr != nil {
			fatal("log: %v", rerr)
		}
	}
	wireRecorder()
	res, err := optimize.Log(string(b), optimize.Options{
		ContextBefore: f.contextBefore,
		ContextAfter:  f.contextAfter,
		Profile:       f.profile,
		Root:          f.root,
	})
	if err != nil {
		fatal("log: %v", err)
	}
	fmt.Println(res.Output)
	fmt.Fprintf(os.Stderr, "kern: %d -> %d tokens (saved %d, %.1f%%)%s\n",
		res.BeforeTokens, res.AfterTokens, res.SavedTokens, res.SavedPercent,
		func() string {
			if f.contextBefore > 0 || f.contextAfter > 0 {
				return fmt.Sprintf(" [window -%d/+%d]", f.contextBefore, f.contextAfter)
			}
			return ""
		}())
	if res.LLMSkipped != "" {
		fmt.Fprintf(os.Stderr, "kern: warning: %s\n", res.LLMSkipped)
	}
	printSavingsFooter(os.Stderr, res.BeforeTokens, res.AfterTokens, kernctx.CostPerToken())
}

func runTokens(rest []string) {
	f, args := parseFlagsOrDie(rest)
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
	f, args := parseFlagsOrDie(rest)
	// --mode selects the budget family (surface consolidation T2b): code
	// (default) = the current FitCode path; terse = the former `kern terse`
	// (terse.Tersify); fit = the former `kern fit-context`
	// (fit.FitContext). Each mode honors its original flags; `kern terse`
	// and `kern fit-context` are now thin wrappers presetting their mode.
	switch f.mode {
	case "terse":
		runTerseCore(rest)
		return
	case "fit":
		runFitContextCore(rest)
		return
	case "", "code":
	default:
		fatalUsage("budget: unknown --mode %q (valid modes: code, terse, fit)", f.mode)
	}
	text := strings.Join(args, " ")
	if text == "" {
		b, err := readStdin()
		if err != nil {
			fatal("budget: %v", err)
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
	out := budget.FitCode(text, maxTokens)
	before := tokenize.Count(text)
	after := tokenize.Count(out)
	// Always state the applied budget so a silent default (4000 when --max is
	// omitted) can never be mistaken for a requested cap.
	fmt.Fprintf(os.Stderr, "kern: %d -> %d tokens (saved %d, %.1f%%, budget %d)\n", before, after, before-after, strutil.Pct(before, after), maxTokens)
	fmt.Println(out)
	printSavingsFooter(os.Stderr, before, after, kernctx.CostPerToken())
}

func runTerse(rest []string) {
	// `kern terse` is a thin wrapper over `kern budget --mode terse`
	// (surface consolidation T2b): the handler presets the mode and the
	// shared core reproduces the terse output byte-for-byte.
	runTerseCore(rest)
}

// runTerseCore is the deterministic line-level tersification shared by
// `kern terse` and `kern budget --mode terse`.
func runTerseCore(rest []string) {
	f, args := parseFlagsOrDie(rest)
	text := ""
	if len(args) > 0 {
		if args[0] == "-" {
			b, err := readStdin()
			if err != nil {
				fatal("terse: %v", err)
			}
			text = string(b)
		} else if fi, err := os.Stat(args[0]); err == nil && !fi.IsDir() {
			b, err := os.ReadFile(args[0])
			if err != nil {
				fatal("terse: %v", err)
			}
			text = string(b)
		} else {
			text = strings.Join(args, " ")
		}
	}
	if text == "" {
		b, err := readStdin()
		if err != nil {
			fatal("terse: %v", err)
		}
		text = string(b)
	}
	if text == "" {
		fatalUsage("usage: kern terse \"<text>\" [--max N]  (or pipe stdin)")
	}
	// Deterministic line-level tersification (internal/terse.Tersify): blank
	// and comment-only lines are stripped, repeated whitespace collapsed, and
	// filler dropped. --max is honored as a token ceiling: the head is kept
	// until the budget is met and the over-budget tail is reported as dropped.
	out, st := terse.Tersify(text, f.max)
	before, after := st.BeforeTokens, st.AfterTokens
	msg := fmt.Sprintf("kern: %d -> %d tokens (saved %d, %.1f%%, %d filler lines dropped", before, after, before-after, strutil.Pct(before, after), st.DroppedFiller)
	if st.DroppedBlank > 0 || st.DroppedComment > 0 {
		msg += fmt.Sprintf(", %d blank + %d comment lines stripped", st.DroppedBlank, st.DroppedComment)
	}
	if st.DroppedIssue > 0 {
		msg += fmt.Sprintf(", %d TODO/FIXME/XXX/HACK comment lines stripped", st.DroppedIssue)
	}
	if f.max > 0 {
		msg += fmt.Sprintf(", budget %d", f.max)
		if st.DroppedBudget > 0 {
			msg += fmt.Sprintf(" (%d lines dropped)", st.DroppedBudget)
		}
	}
	msg += ")"
	fmt.Fprintln(os.Stderr, msg)
	fmt.Println(out)
	printSavingsFooter(os.Stderr, before, after, kernctx.CostPerToken())
}

func runSemcache(rest []string) {
	f, args := parseFlagsOrDie(rest)
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
			fatal("semcache: %v", err)
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
			fatal("semcache: %v", err)
		}
		if f.json {
			printJSON(entries)
			return
		}
		if len(entries) == 0 {
			fmt.Printf("semcache %q: empty\n", ns)
			return
		}
		fmt.Printf("semcache %q: %d entries\n", ns, len(entries))
		for i, in := range entries {
			fmt.Printf("  %d. %s\n", i+1, in)
		}
	case "stats":
		st, err := semcache.Stats()
		if err != nil {
			fatal("semcache: %v", err)
		}
		if f.json {
			printJSON(map[string]any{"namespaces": st})
			return
		}
		if len(st) == 0 {
			fmt.Println("semcache: empty")
			return
		}
		var tH, tM, tE, tS int64
		var tN int
		fmt.Println("semcache accounting:")
		names := slices.Sorted(maps.Keys(st))
		for _, ns := range names {
			s := st[ns]
			tN += s.Entries
			tH += s.Hits
			tM += s.Misses
			tE += s.Evictions
			tS += s.SavedBytes
			fmt.Printf("  %-10s entries=%-4d hits=%-7d misses=%-7d evictions=%-4d saved=%.1f KB\n",
				ns, s.Entries, s.Hits, s.Misses, s.Evictions, float64(s.SavedBytes)/1024)
		}
		fmt.Printf("  %-10s entries=%-4d hits=%-7d misses=%-7d evictions=%-4d saved=%.1f KB\n",
			"total", tN, tH, tM, tE, float64(tS)/1024)
	case "sim":
		if len(args) != 2 {
			fatalUsage("usage: kern semcache sim <textA> <textB>")
		}
		if f.json {
			printJSON(map[string]any{"similarity": semcache.Similarity(args[0], args[1])})
			return
		}
		fmt.Printf("similarity: %.3f\n", semcache.Similarity(args[0], args[1]))
	default:
		st, err := semcache.Stats()
		if err != nil {
			fatal("semcache: %v", err)
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
		names := slices.Sorted(maps.Keys(st))
		for _, ns := range names {
			fmt.Printf("  %-8s %d\n", ns, st[ns].Entries)
		}
	}

}

func runStats(cmd string, rest []string) {
	// Positional args are unused by the stats subcommands; parseFlags still
	// validates unknown flags so typos fail loudly (rc=2).
	f, _ := parseFlagsOrDie(rest)
	rec, err := stats.NewRecorder()
	if err != nil {
		fatal("stats: %v", err)
	}
	if cmd == "diff" {
		limit := 20
		if f.limit > 0 {
			limit = f.limit
		}
		entries, err := rec.Entries(limit)
		if err != nil {
			fatal("stats: %v", err)
		}
		if f.limit <= 0 && len(entries) >= limit {
			// The default 20-entry cap is silent by design only if it does not
			// bind; say so when it might, so a truncated diff is not mistaken
			// for the full history.
			fmt.Fprintf(os.Stderr, "kern: showing up to %d entries (pass --limit to raise)\n", limit)
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
	// `kern export --csv` writes the CSV ledger; without the flag it falls
	// through to the same summary as `kern stats` (the usage line documents
	// --csv as the CSV switch, so the flag gates the format instead of being
	// a no-op).
	if cmd == "export" && f.csv {
		entries, err := rec.Entries(100000)
		if err != nil {
			fatal("stats: %v", err)
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
	// `kern stats --by-tool` renders the per-tool token ledger: for each tool
	// that recorded entries (MCP dispatch entries plus optimization entries
	// whose operation maps to a tool), its call count, tokens returned to the
	// agent, tokens saved and estimated cost saved, sorted by savings desc.
	// Tools that don't know their before/after contribute tokens returned only —
	// no savings are ever fabricated.
	if f.byTool {
		tools, err := rec.SummarizeByTool(f.days, f.session)
		if err != nil {
			fatal("stats: %v", err)
		}
		if f.json {
			printJSON(tools)
			return
		}
		fmt.Printf("kern stats --by-tool (last %d days)%s\n", f.days, statsRateSuffix())
		if len(tools) == 0 {
			fmt.Println("  no per-tool data yet (MCP tool calls and kern compact record entries)")
			return
		}
		fmt.Printf("  %-22s %6s %14s %13s %11s\n", "tool", "calls", "tokens ret", "tokens saved", "cost saved")
		for _, ts := range tools {
			fmt.Printf("  %-22s %6d %14d %13d $%10.4f\n", ts.Tool, ts.Calls, ts.TokensReturned, ts.TokensSaved, ts.CostSaved)
		}
		return
	}
	// `kern stats --by-agent` renders the per-agent token ledger, mirroring
	// --by-tool end-to-end: entries are grouped by the agent_id recorded at
	// MCP dispatch (entries without an attribution fall into the
	// "(unattributed)" bucket).
	if f.byAgent {
		agents, err := rec.SummarizeByAgent(f.days, f.session)
		if err != nil {
			fatal("stats: %v", err)
		}
		if f.json {
			printJSON(agents)
			return
		}
		fmt.Printf("kern stats --by-agent (last %d days)%s\n", f.days, statsRateSuffix())
		if len(agents) == 0 {
			fmt.Println("  no per-agent data yet (MCP tool calls with agent_id and kern compact record entries)")
			return
		}
		fmt.Printf("  %-22s %6s %14s %13s %11s\n", "agent", "calls", "tokens ret", "tokens saved", "cost saved")
		for _, as := range agents {
			fmt.Printf("  %-22s %6d %14d %13d $%10.4f\n", as.Agent, as.Calls, as.TokensReturned, as.TokensSaved, as.CostSaved)
		}
		return
	}
	sum, err := rec.Summarize(f.days, f.session)
	if err != nil {
		fatal("stats: %v", err)
	}
	if f.json {
		perMillion, model, kind := kernctx.CostRateInfo()
		out := struct {
			Operations         int     `json:"operations"`
			BeforeTotal        int     `json:"before_tokens"`
			AfterTotal         int     `json:"after_tokens"`
			SavedTotal         int     `json:"saved_tokens"`
			SavedPct           float64 `json:"saved_percent"`
			CostSaved          float64 `json:"cost_saved_usd"`
			CostRatePerMillion float64 `json:"cost_rate_per_million"`
			CostRateModel      string  `json:"cost_rate_model,omitempty"`
			CostRateKind       string  `json:"cost_rate_kind,omitempty"`
		}{sum.Operations, sum.BeforeTotal, sum.AfterTotal, sum.SavedTotal, sum.SavedPct, sum.CostSaved, perMillion, model, kind}
		printJSON(out)
		return
	}
	fmt.Printf("kern stats (last %d days)\n", f.days)
	fmt.Printf("  operations   : %d\n", sum.Operations)
	fmt.Printf("  before tokens: %d\n", sum.BeforeTotal)
	fmt.Printf("  after tokens : %d\n", sum.AfterTotal)
	fmt.Printf("  saved tokens : %d (%.1f%%)\n", sum.SavedTotal, sum.SavedPct)
	fmt.Printf("  cost saved   : $%.4f\n", sum.CostSaved)
	fmt.Printf("  cost model   : %s\n", costModelLabel())
	fmt.Println("  per-tool ledger: kern stats --by-tool · per-agent: kern stats --by-agent")

}

// costModelLabel renders the self-explaining "cost model" line value for
// `kern stats`: the engaged per-model table rate, the explicit operator
// override, or the labeled flat assumption — so a fresh install's 1e-05
// default is never presented as a confident figure.
func costModelLabel() string {
	perMillion, model, kind := kernctx.CostRateInfo()
	switch kind {
	case "table":
		return fmt.Sprintf("$%.4g/1M (%s)", perMillion, model)
	case "override":
		return fmt.Sprintf("$%.6f/token (operator override)", perMillion/1e6)
	default: // "flat": the 1e-05 default applies — an assumption
		if model != "" {
			return fmt.Sprintf("$%.6f/token (assumed — %q has no rate in the per-model table; set cost_per_token)", perMillion/1e6, model)
		}
		return fmt.Sprintf("$%.6f/token (assumed — set llm.model or cost_per_token)", perMillion/1e6)
	}
}

// statsRateSuffix renders the compact self-explaining cost-rate suffix for
// the by-tool/by-agent headers: the engaged per-model rate, the operator
// override, or a labeled flat assumption (never a bare number).
func statsRateSuffix() string {
	perMillion, model, kind := kernctx.CostRateInfo()
	switch kind {
	case "table":
		return fmt.Sprintf(" @ %.4g/1M (%s)", perMillion, model)
	case "override":
		return fmt.Sprintf(" @ $%.6f/token (operator override)", perMillion/1e6)
	default:
		return fmt.Sprintf(" @ $%.6f/token (assumed)", perMillion/1e6)
	}
}
