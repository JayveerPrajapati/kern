package main

import (
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"os"
	"path/filepath"
	"strings"
)

func runGraph(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 && !f.html {
		fatalUsage("usage: kern graph <symbol> [root] [--mermaid] [--json] [--graphml] [--html] [--out FILE] [--max-tokens N] [--limit N]")
	}
	if len(args) < 1 {
		// Whole-repo explorer: kern graph --html [root] [--limit N]
		root := f.root
		if root == "" {
			root = "."
		}
		if f.limit == 0 {
			f.limit = 400
		}
		ix, err := loadOrBuild(root)
		if err != nil {
			fatal("%v", err)
		}
		out := ix.WholeGraph(f.limit).GraphHTML()
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
	symbol := args[0]
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
	if f.mermaid {
		fmt.Println(ix.Mermaid(symbol))
		return
	}
	if f.json || f.graphml || f.html {
		g, ok := ix.Neighborhood(symbol)
		if !ok {
			fatalNoSymbol(symbol, ix)
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
	if f.maxTokens > 0 {
		out, err := intel.GraphCtx(ix, symbol, f.maxTokens)
		if err != nil {
			fatal("%v", err)
		}
		fmt.Println(out)
		return
	}
	out := ix.Graph(symbol)
	if strings.Contains(out, "no symbol found") {
		fatalNoSymbol(symbol, ix)
	}
	fmt.Println(out)

}

func runInherits(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("usage: kern inherits <symbol> [root] [--json]")
	}
	symbol := args[0]
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
	sym, ok := ix.FindSymbol(symbol)
	if !ok {
		fatalNoSymbol(symbol, ix)
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

}

func runWhy(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("usage: kern why <symbol> [root] [--json]")
	}
	symbol := args[0]
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
	info, ok := intel.Why(ix, symbol)
	if !ok {
		fatalNoSymbol(symbol, ix)
	}
	if f.json {
		printJSON(info)
		return
	}
	fmt.Println(intel.FormatWhy(info))

}

func runWiki(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 {
			root = args[0]
		}
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

}

func runHubs(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 {
			root = args[0]
		}
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

}

func runBridges(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 {
			root = args[0]
		}
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

}

func runTestgaps(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 {
			root = args[0]
		}
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

}

func runFlows(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 {
			root = args[0]
		}
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

}

func runEntries(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 {
			root = args[0]
		}
	}
	ix, err := intel.ReadIndex(root)
	if err != nil {
		fatal("%v", err)
	}
	limit := f.limit
	if limit <= 0 {
		limit = 50
	}
	type entry struct {
		Framework string `json:"framework"`
		Symbol    string `json:"symbol"`
		Route     string `json:"route"`
		File      string `json:"file"`
		Line      int    `json:"line"`
	}
	var entries []entry
	var b strings.Builder
	n := 0
	for _, s := range ix.Symbols {
		if !s.Entry || s.Framework == "" {
			continue
		}
		if f.json {
			entries = append(entries, entry{s.Framework, s.FullName(), s.Route, s.File, s.Line})
		} else {
			fmt.Fprintf(&b, "%s %s %s %s:%d\n", s.Framework, s.FullName(), s.Route, s.File, s.Line)
		}
		n++
		if n >= limit {
			break
		}
	}
	if f.json {
		printJSON(map[string]any{"entries": entries})
		return
	}
	if n == 0 {
		fmt.Println("no framework entry points in index (run kern index to populate)")
		return
	}
	fmt.Print(b.String())

}

func runCommunities(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 {
			root = args[0]
		}
	}
	ix, err := intel.ReadIndex(root)
	if err != nil {
		fatal("%v", err)
	}
	comms := intel.Communities(ix)
	if f.limit > 0 && len(comms) > f.limit {
		comms = comms[:f.limit]
	}
	if f.json {
		// Default JSON output is a compact summary (sample + size + hub +
		// packages); --full restores the legacy verbose symbol list.
		os.Stdout.Write(intel.MarshalCommunities(comms, f.full))
		fmt.Println()
		return
	}
	fmt.Println(intel.RenderCommunities(comms))

}

func runPath(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	// Require both symbols; the root is optional and passed last.
	if len(args) < 2 {
		fatalUsage("usage: kern path <from-symbol> <to-symbol> [root]")
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 2 {
			root = args[2]
		}
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

}

func runDead(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 {
			root = args[0]
		}
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

}

func runLarges(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 {
			root = args[0]
		}
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

}

func runArch(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 {
			root = args[0]
		}
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

}

func runChurn(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 {
			root = args[0]
		}
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

}

func runCochange(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 {
			root = args[0]
		}
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

}

func runExplore(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("usage: kern explore <symbol> [root] [--depth N] [--max N]")
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 1 {
			root = args[1]
		}
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

}

func runNear(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("usage: kern near <symbol> [root] [--depth N] [--max N]")
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 1 {
			root = args[1]
		}
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

}

func runProbe(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("usage: kern probe \"<task text>\" [root] [--max N]")
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

}

func runTrace(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("usage: kern trace <file|- for stdin> [root] [--limit N]")
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

}
