package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/app"
	kernctx "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func runGraph(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 && !f.html {
		fatalUsage("usage: kern graph <symbol> [root] [--mermaid] [--json] [--graphml] [--cypher] [--html] [--out FILE] [--max-tokens N] [--limit N]")
	}
	if len(args) < 1 {
		// Whole-repo explorer: kern graph --html [root] [--limit N]
		root := f.root
		if root == "" {
			root = "."
		}
		if f.limit == 0 {
			f.limit = 400
			fmt.Fprintf(os.Stderr, "kern: whole-repo graph limited to 400 symbols (--limit to raise)\n")
		}
		ix, err := loadOrBuild(root)
		if err != nil {
			fatal("Graph: %v", err)
		}
		g := ix.WholeGraph(f.limit)
		out := g.GraphHTML(ix)
		if f.out != "" {
			if err := os.WriteFile(f.out, []byte(out), 0o644); err != nil {
				fatal("Graph: %v", err)
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
		fatal("Graph: %v", err)
	}
	if f.mermaid {
		fmt.Println(ix.Mermaid(symbol))
		return
	}
	if f.json || f.graphml || f.cypher || f.html {
		g, gerr := svc.Graph.Neighborhood(context.Background(), root, symbol)
		if gerr != nil {
			fatalNoSymbol(symbol, ix)
		}
		var out string
		switch {
		case f.json:
			out = g.GraphJSON()
		case f.graphml:
			out = g.GraphGraphML()
		case f.cypher:
			out = g.GraphCypher()
		default:
			out = g.GraphHTML(ix)
		}
		if f.out != "" {
			if err := os.WriteFile(f.out, []byte(out), 0o644); err != nil {
				fatal("Graph: %v", err)
			}
			fmt.Printf("wrote %s (%d bytes)\n", f.out, len(out))
			return
		}
		fmt.Println(out)
		return
	}
	if f.maxTokens > 0 {
		out, err := intel.GraphCtxMin(ix, symbol, f.maxTokens, f.minConfidence)
		if err != nil {
			fatal("Graph: %v", err)
		}
		fmt.Println(out)
		return
	}
	out, gerr := svc.Graph.Graph(context.Background(), root, symbol)
	if gerr != nil || strings.Contains(out, "no symbol found") {
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
		fatal("Inherits: %v", err)
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
		fatal("Why: %v", err)
	}
	info, werr := svc.Graph.Why(context.Background(), root, symbol)
	if werr != nil {
		fatalNoSymbol(symbol, ix)
	}
	if f.json {
		printJSON(info)
		return
	}
	// --min-confidence prunes caller rows whose provenance ranks below the
	// threshold, so the answer lists only FACT/INFERENCE-grade dependents.
	if f.minConfidence != "" && info != nil {
		passes := intel.MinConfidenceFilter(f.minConfidence)
		kept := info.Callers[:0]
		for _, c := range info.Callers {
			if passes(intel.EdgeConfidenceLabel(ix, c.Name, info.Symbol.FullName())) {
				kept = append(kept, c)
			}
		}
		info.Callers = kept
		info.InEdges = len(kept)
	}
	fmt.Println(intel.FormatWhy(*info))

}

func runWiki(rest []string) {
	// --obsidian is wiki-only, so it is stripped here (bare bool, like the
	// sibling --json/--html flags) rather than added to the shared flag set.
	obsidian := false
	wikiArgs := make([]string, 0, len(rest))
	for _, a := range rest {
		if a == "--obsidian" {
			obsidian = true
			continue
		}
		wikiArgs = append(wikiArgs, a)
	}
	f, args, err := parseFlags(wikiArgs)
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
		fatal("Wiki: %v", err)
	}
	outDir := f.out
	if outDir == "" {
		outDir = filepath.Join(root, ".kern", "wiki")
	}
	written, err := intel.WikiExport(ix, outDir, obsidian)
	if err != nil {
		fatal("Wiki: %v", err)
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
		fatal("Hubs: %v", err)
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
		fatal("Bridges: %v", err)
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
		fatal("Testgaps: %v", err)
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
		fatal("Flows: %v", err)
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
		fatal("Entries: %v", err)
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
	pkgOf := map[string]string{} // file -> package name
	for _, p := range ix.Pkgs {
		for _, f := range p.Files {
			if _, ok := pkgOf[f]; !ok {
				pkgOf[f] = p.Name
			}
		}
	}
	for _, s := range ix.Symbols {
		framework := ""
		route := ""
		if s.Entry && s.Framework != "" {
			framework = s.Framework
			route = s.Route
		} else if fwID, ok := goNativeEntry(s, pkgOf); ok {
			// Language-native entry points (Go main/init) alongside the
			// framework-tagged ones (F-009).
			framework = fwID
			route = "-"
		}
		if framework == "" {
			continue
		}
		if f.json {
			entries = append(entries, entry{framework, s.FullName(), route, s.File, s.Line})
		} else {
			fmt.Fprintf(&b, "%s %s %s %s:%d\n", framework, s.FullName(), route, s.File, s.Line)
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
		fatal("Communities: %v", err)
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
	// F-033: --from/--to are flag aliases for the positional form
	// `kern path <from-symbol> <to-symbol> [root]`; the positional form
	// remains supported. The root is optional in both forms.
	from, to := f.from, f.to
	if from == "" && to == "" {
		// Positional form: kern path <from-symbol> <to-symbol> [root]
		if len(args) < 2 {
			fatalUsage("usage: kern path <from-symbol> <to-symbol> [root]  (or --from S --to S)")
		}
		from, to = args[0], args[1]
	} else if from == "" || to == "" {
		fatalUsage("usage: kern path --from <from-symbol> --to <to-symbol> [root]  (both flags required together)")
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
		fatal("Path: %v", err)
	}
	// Validation and path computation go through the service layer; the CLI
	// keeps only rendering (and resolved-name labels for JSON output). The
	// --min-confidence filter bypasses the service for the filtered search
	// (ShortestPathMin prunes AMBIGUOUS edges from the search graph).
	var path []string
	var perr error
	if f.minConfidence != "" {
		fromSym, _ := intel.Resolve(ix, from)
		toSym, _ := intel.Resolve(ix, to)
		path = intel.ShortestPathMin(ix, fromSym, toSym, f.minConfidence)
		if path == nil {
			perr = fmt.Errorf("no path found between %s and %s", from, to)
		}
	} else {
		path, perr = svc.Graph.Path(context.Background(), root, from, to)
	}
	if perr != nil {
		fatal("Path: %v", perr)
	}
	if f.json {
		fromSym, _ := intel.Resolve(ix, from)
		toSym, _ := intel.Resolve(ix, to)
		printJSON(map[string]any{
			"from": fromSym, "to": toSym,
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
		fatal("Dead: %v", err)
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
		fatal("Larges: %v", err)
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
		fatal("Arch: %v", err)
	}
	a := intel.AnalyzeArchitecture(ix)
	if f.json {
		printJSON(a)
		return
	}
	fmt.Println(intel.RenderArch(a))

}

// runTwin summarizes the merged knowledge graph's digital-twin dimensions:
// node counts per kind (api/data/messaging/infra/runtime plus code kinds)
// followed by the extracted API endpoint list. Deterministic ordering: kinds
// and endpoint names are sorted.
func runTwin(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if len(args) > 0 {
		root = args[0]
	}
	p, err := app.New(root)
	if err != nil {
		fatal("Twin: %v", err)
	}
	g := p.Graph()

	counts := map[string]int{}
	var apis []string
	for _, n := range g.Nodes {
		counts[n.Kind]++
		if n.Kind == "api" {
			name := n.Label
			if name == "" {
				name = n.ID
			}
			apis = append(apis, name)
		}
	}
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	sort.Strings(apis)

	fmt.Printf("twin knowledge graph: %s\n", g.Project.Root)
	for _, k := range kinds {
		fmt.Printf("  %-12s %d\n", k+":", counts[k])
	}
	if len(apis) > 0 {
		fmt.Printf("api endpoints (%d):\n", len(apis))
		for _, a := range apis {
			fmt.Printf("  %s\n", a)
		}
	}
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
		fatal("Churn: %v", err)
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
	report, err := intel.CoChangeContext(context.Background(), root, from, to)
	if err != nil {
		fatal("Cochange: %v", err)
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
		fatalUsage("usage: kern explore <symbol> [root] [--depth N] [--max N] [--explain]")
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
		fatal("Explore: %v", err)
	}
	// P2-8 promotion defaults: a bounded answer (2 hops, 30 nodes) unless
	// the caller asks otherwise. --depth 0 keeps the uncapped radius.
	depth, maxN := intel.DefaultExploreBounds(f.depth, f.max)
	rep, err := intel.ExploreBudgeted(ix, args[0], depth, maxN, f.minConfidence, f.maxTokens)
	if err != nil {
		// Unknown symbols get the same did-you-mean suggestions as the graph
		// path (fatalNoSymbol); other Explore failures (e.g. a resolved symbol
		// with no definition) keep their specific message.
		if strings.Contains(err.Error(), "unknown symbol") {
			fatalNoSymbol(args[0], ix)
		}
		fatal("Explore: %v", err)
	}
	if f.json {
		printJSON(rep)
		return
	}
	out := intel.RenderExplore(rep)
	if f.explain {
		if info, ok := intel.Why(ix, args[0]); ok {
			out = intel.RenderExploreExplain(rep, info)
		}
	}
	fmt.Println(out)
	if rep.Stats != nil {
		printSavingsFooter(os.Stderr, rep.Stats.FullContext, rep.Stats.CompactTokens, kernctx.CostPerToken())
	}
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
		fatal("Near: %v", err)
	}
	depth := f.depth
	if depth < 0 {
		depth = 2
	}
	maxN := f.max
	if maxN <= 0 {
		maxN = 100
	}
	if f.depth < 0 || f.max <= 0 {
		// Surface the defaults so a capped tree is never mistaken for the
		// full result (--json already reports depth/max_nodes).
		fmt.Fprintf(os.Stderr, "kern: walk defaults depth=%d max=%d (use --depth/--max to widen)\n", depth, maxN)
	}
	nodes, err := intel.Near(ix, args[0], depth, maxN)
	if err != nil {
		fatal("Near: %v", err)
	}
	if f.json {
		printJSON(map[string]any{"depth": depth, "max_nodes": maxN, "nodes": nodes})
		return
	}
	fmt.Println(intel.RenderNear(ix, nodes))

}

func runCycles(rest []string) {
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
		fatal("Cycles: %v", err)
	}
	cycles := intel.ImportCycles(ix)
	if f.json {
		printJSON(map[string]any{"cycles": cycles, "count": len(cycles)})
		return
	}
	fmt.Println(intel.RenderCycles(cycles))
}

func runSurprising(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if len(args) > 0 {
		root = args[0]
	}
	ix, err := intel.ReadIndex(root)
	if err != nil {
		fatal("Surprising: %v", err)
	}
	edges := intel.SurprisingConnections(ix, f.limit)
	if f.json {
		printJSON(map[string]any{"connections": edges, "count": len(edges)})
		return
	}
	fmt.Println(intel.RenderSurprising(edges))
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
		fatal("Probe: %v", err)
	}
	maxTokens := f.max
	if maxTokens <= 0 {
		maxTokens = 4000
	}
	report := intel.Probe(ix, args[0], maxTokens)
	// --min-confidence prunes AMBIGUOUS anchors' call rows so the probe
	// bundle never leads an agent to chase phantom references.
	if f.minConfidence != "" {
		passes := intel.MinConfidenceFilter(f.minConfidence)
		for i := range report.Anchors {
			a := &report.Anchors[i]
			keep := func(in []string, conf func(string) string) []string {
				out := in[:0]
				for _, c := range in {
					if passes(conf(c)) {
						out = append(out, c)
					}
				}
				return out
			}
			a.Callers = keep(a.Callers, func(c string) string {
				return intel.EdgeConfidenceLabel(ix, c, a.Resolved)
			})
			a.Callees = keep(a.Callees, func(c string) string {
				return intel.EdgeConfidenceLabel(ix, a.Resolved, c)
			})
		}
	}
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
			fatal("Trace: %v", err)
		}
		src = string(b)
	} else {
		b, err := os.ReadFile(sourceName)
		if err != nil {
			if os.IsNotExist(err) && intel.LooksLikeTrace(sourceName) {
				// Not a path — inline trace text such as
				// `kern trace "path/file.py:24 selectSlice"` (report A6).
				src = sourceName
				sourceName = "inline"
			} else {
				if os.IsNotExist(err) {
					fatal("trace: file not found: %s (pass a path, `-` for stdin, or inline trace text)", sourceName)
				}
				fatal("Trace: %v", err)
			}
		} else {
			src = string(b)
		}
	}
	root := "."
	if len(args) > 1 {
		root = args[1]
	}
	ix, err := intel.ReadIndex(root)
	if err != nil {
		fatal("Trace: %v", err)
	}
	report := intel.Trace(ix, src, sourceName, f.limit)
	if f.json {
		printJSON(report)
		return
	}
	fmt.Println(intel.RenderTrace(report))

}
