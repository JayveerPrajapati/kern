package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/app"
	kernctx "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/twin"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func runGraph(rest []string) {
	f, args := parseFlagsOrDie(rest)
	if len(args) < 1 && !f.html && !f.entities {
		fatalUsage("usage: kern graph <symbol> [root] [--mermaid] [--one-line] [--entities] [--json] [--graphml] [--cypher] [--html] [--out FILE] [--max-tokens N] [--limit N]")
	}
	if len(args) < 1 && f.entities {
		// No symbol + --entities: render the repo's entity inventory (all
		// twin entity nodes, grouped by kind) — text or JSON.
		root := projectRoot(f)
		ix, err := loadOrBuild(root)
		if err != nil {
			fatal("Graph: %v", err)
		}
		ents, _ := twin.Entities(twin.MergeIntoIndex(ix, root), "")
		if f.limit > 0 && len(ents) > f.limit {
			fmt.Fprintf(os.Stderr, "kern: entity inventory capped at --limit %d (%d omitted)\n", f.limit, len(ents)-f.limit)
			ents = ents[:f.limit]
		}
		if f.json {
			printJSON(map[string]any{"entities": ents})
			return
		}
		graphOut(f, twin.RenderEntities(ents))
		return
	}
	if len(args) < 1 {
		// Whole-repo explorer: kern graph --html [root] [--limit N]
		root := projectRoot(f)
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
	root := projectRoot(f)
	if f.root == "" && len(args) > 1 {
		root = args[1]
	}
	ix, err := loadOrBuild(root)
	if err != nil {
		fatal("Graph: %v", err)
	}
	if f.entities {
		// --entities renders the digital-twin entity nodes connected to the
		// queried symbol via twin edges (API endpoints, DB tables, topics,
		// services, deployments). The CLI graph path is code-graph only, so
		// the twin extractors are merged in the same way platform.go wires
		// them for the web/app graph (twin.MergeIntoIndex).
		ents, eerr := twin.Entities(twin.MergeIntoIndex(ix, root), symbol)
		if eerr != nil {
			fatalNoSymbol(symbol, ix)
		}
		if f.limit > 0 && len(ents) > f.limit {
			fmt.Fprintf(os.Stderr, "kern: entities capped at --limit %d (%d omitted)\n", f.limit, len(ents)-f.limit)
			ents = ents[:f.limit]
		}
		if f.json {
			printJSON(map[string]any{"entities": ents})
			return
		}
		graphOut(f, twin.RenderEntities(ents))
		return
	}
	if f.mermaid {
		out := ix.Mermaid(symbol)
		// Mermaid renders "" for an unresolvable symbol (no error return),
		// so an empty render is the no-symbol-found signal: fail with the
		// same did-you-mean message as the other graph paths instead of
		// printing nothing and exiting 0.
		if out == "" {
			fatalNoSymbol(symbol, ix)
		}
		if f.limit > 0 {
			out = capMermaid(out, f.limit)
		}
		graphOut(f, out)
		return
	}
	if f.oneLine {
		// --one-line renders the single-line call-graph neighbourhood
		// (definition, callers, callees) — the CLI mirror of the MCP
		// kern_graph format=one-line contract (surface consolidation T2b).
		out := ix.Graph(symbol)
		// ix.Graph renders "no symbol found: <symbol>" into the output
		// without an error return; treat it as a failed lookup (exit 1 with
		// did-you-mean) instead of printing the bare message with exit 0.
		if strings.Contains(out, "no symbol found") {
			fatalNoSymbol(symbol, ix)
		}
		graphOut(f, out)
		return
	}
	if f.json || f.graphml || f.cypher || f.html {
		g, gerr := intel.Neighborhood(ix, symbol)
		if gerr != nil {
			fatalNoSymbol(symbol, ix)
		}
		// F10: honor --limit in symbol mode too — cap the rendered
		// nodes/edges like the whole-repo path does (WholeGraph caps at
		// --limit), surfacing the cut on stderr so a capped graph is never
		// mistaken for the full neighbourhood.
		if f.limit > 0 {
			beforeN, beforeE := len(g.Nodes), len(g.Edges)
			if len(g.Nodes) > f.limit {
				g.Nodes = g.Nodes[:f.limit]
			}
			if len(g.Edges) > f.limit {
				g.Edges = g.Edges[:f.limit]
			}
			if beforeN > len(g.Nodes) || beforeE > len(g.Edges) {
				fmt.Fprintf(os.Stderr, "kern: graph capped at --limit %d (%d node(s), %d edge(s) omitted)\n",
					f.limit, beforeN-len(g.Nodes), beforeE-len(g.Edges))
			}
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
		graphOut(f, out)
		return
	}
	if f.maxTokens > 0 {
		out, err := intel.GraphCtxMin(ix, symbol, f.maxTokens, f.minConfidence)
		if err != nil {
			// Unknown symbols get the same did-you-mean suggestions as the
			// other graph paths (fatalNoSymbol); genuine context failures
			// keep their specific message.
			if strings.Contains(err.Error(), "unknown symbol") {
				fatalNoSymbol(symbol, ix)
			}
			fatal("Graph: %v", err)
		}
		graphOut(f, out)
		return
	}
	out := intel.GraphText(ix, symbol)
	if strings.Contains(out, "no symbol found") {
		fatalNoSymbol(symbol, ix)
	}
	if f.limit > 0 {
		out = capGraphText(out, f.limit)
	}
	graphOut(f, out)

}

// graphOut emits a rendered graph honoring --out with the whole-repo path's
// semantics: when --out is set the output is written to the file and a
// confirmation is printed (no stdout); otherwise it goes to stdout. F10:
// the symbol-subgraph paths previously ignored --out entirely.
func graphOut(f flags, out string) {
	if f.out != "" {
		if err := os.WriteFile(f.out, []byte(out), 0o644); err != nil {
			fatal("Graph: %v", err)
		}
		fmt.Printf("wrote %s (%d bytes)\n", f.out, len(out))
		return
	}
	fmt.Println(out)
}

// capGraphEdgeLines truncates the rendered edge lines of a symbol graph
// (text or mermaid) to at most limit entries, preserving headers and
// directives, and appends a "... (N more, capped at --limit)" note when
// anything is cut so a capped result is never mistaken for the full graph
// (F10). A limit <= 0 leaves the output untouched.
func capGraphEdgeLines(out string, limit int, isEdge func(string) bool, notePrefix string) string {
	if limit <= 0 || out == "" {
		return out
	}
	lines := strings.Split(out, "\n")
	var b strings.Builder
	kept, cut := 0, 0
	for _, ln := range lines {
		if isEdge(ln) {
			if kept >= limit {
				cut++
				continue
			}
			kept++
		}
		b.WriteString(ln)
		b.WriteString("\n")
	}
	if cut > 0 {
		fmt.Fprintf(&b, "%s... (%d more, capped at --limit %d)\n", notePrefix, cut, limit)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// capGraphText caps the def/callers/calls text render of a symbol subgraph:
// the root definition line(s) are always kept, the caller/callee edge lists
// are truncated to limit entries.
func capGraphText(out string, limit int) string {
	return capGraphEdgeLines(out, limit, func(ln string) bool {
		return strings.HasPrefix(ln, "  ") && !strings.HasPrefix(ln, "    ")
	}, "")
}

// capMermaid caps the edge lines of a Mermaid flowchart to limit entries,
// keeping the flowchart directive and any staleness banner.
func capMermaid(out string, limit int) string {
	return capGraphEdgeLines(out, limit, func(ln string) bool {
		return strings.Contains(ln, " --> ")
	}, "%% ")
}

// typeKindOf reports whether a symbol kind is a type (the target of
// inheritance queries). Mirrors the index's searchTypeKinds set.
func typeKindOf(kind string) bool {
	switch kind {
	case "type", "class", "interface", "struct", "enum", "record", "trait", "union":
		return true
	}
	return false
}

func runInherits(rest []string) {
	f, args := parseFlagsOrDie(rest)
	if len(args) < 1 {
		fatalUsage("usage: kern inherits <symbol> [root] [--json]")
	}
	symbol := args[0]
	root := projectRoot(f)
	if f.root == "" && len(args) > 1 {
		root = args[1]
	}
	ix, err := loadOrBuild(root)
	if err != nil {
		fatal("Inherits: %v", err)
	}
	sym, ok := ix.FindSymbol(symbol)
	if !ok {
		// Qualified forms ("index.Index") resolve through the same chain
		// Graph uses: dotted receiver match, then package-directory match.
		if r, ok2 := ix.ResolveName(symbol); ok2 {
			sym = r
			ok = true
		}
	}
	if ok && !typeKindOf(sym.Kind) {
		// Inheritance is a type-level concept. When the bare name is
		// ambiguous between a type and same-named methods/functions (e.g.
		// `kern inherits Index` must resolve the struct, not Platform.Index),
		// prefer the type symbol.
		if types := ix.Search("type "+symbol, 1); len(types) > 0 {
			sym = types[0]
		}
	}
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
	f, args := parseFlagsOrDie(rest)
	if len(args) < 1 {
		fatalUsage("usage: kern why <symbol> [root] [--json]")
	}
	symbol := args[0]
	root := projectRoot(f)
	if f.root == "" && len(args) > 1 {
		root = args[1]
	}
	ix, err := loadOrBuild(root)
	if err != nil {
		fatal("Why: %v", err)
	}
	info, werr := intel.WhySymbol(ix, symbol)
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
	f, args := parseFlagsOrDie(wikiArgs)
	root := projectRoot(f)
	if f.root == "" && len(args) > 0 {
		root = args[0]
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
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if f.root == "" && len(args) > 0 {
		root = args[0]
	}
	ix, err := intel.ReadIndex(root)
	if err != nil {
		fatal("Hubs: %v", err)
	}
	limit := f.limit
	if limit <= 0 {
		limit = 10
	}
	if f.bridgesOnly {
		// --bridges-only: render the coupling-point report alone instead of
		// the hubs+bridges bundle.
		bLimit := f.limit
		if bLimit <= 0 {
			bLimit = 15
		}
		if f.json {
			printJSON(map[string]any{"bridges": intel.Bridges(ix, bLimit)})
			return
		}
		fmt.Println(intel.RenderBridges(intel.Bridges(ix, bLimit)))
		return
	}
	if f.json {
		// Ordered struct (not a map) so JSON key order is stable: hubs
		// first, then bridges — a map renders keys in random order and
		// made the payload look like a bridges-only response.
		printJSON(struct {
			Hubs    any `json:"hubs"`
			Bridges any `json:"bridges"`
		}{intel.Hubs(ix, limit), intel.Bridges(ix, 15)})
		return
	}
	fmt.Println(intel.RenderHubs(intel.Hubs(ix, limit)))
	fmt.Println()
	fmt.Println(intel.RenderBridges(intel.Bridges(ix, 15)))

}

func runBridges(rest []string) {
	f, args := parseFlagsOrDie(rest)
	_, ix, err := resolveRoot(f, args)
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
	f, args := parseFlagsOrDie(rest)
	_, ix, err := resolveRoot(f, args)
	if err != nil {
		fatal("Testgaps: %v", err)
	}
	cov := intel.AnalyzeCoverage(ix)
	if f.json {
		gaps := cov.Gaps
		if f.limit > 0 && len(gaps) > f.limit {
			gaps = gaps[:f.limit]
		}
		printJSON(map[string]any{
			"coverage": cov,
			"gaps":     gaps,
		})
		return
	}
	fmt.Println(cov.Render())

}

func runFlows(rest []string) {
	f, args := parseFlagsOrDie(rest)
	_, ix, err := resolveRoot(f, args)
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

func runCommunities(rest []string) {
	f, args := parseFlagsOrDie(rest)
	_, ix, err := resolveRoot(f, args)
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
		_, _ = os.Stdout.Write(intel.MarshalCommunities(comms, f.full))
		fmt.Println()
		return
	}
	fmt.Println(intel.RenderCommunities(comms))

}

func runPath(rest []string) {
	f, args := parseFlagsOrDie(rest)
	// --from/--to are flag aliases for the positional form
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
	root := projectRoot(f)
	if f.root == "" && len(args) > 2 {
		root = args[2]
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
		path, perr = intel.PathBetween(ix, from, to)
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
	f, args := parseFlagsOrDie(rest)
	_, ix, err := resolveRoot(f, args)
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
	f, args := parseFlagsOrDie(rest)
	_, ix, err := resolveRoot(f, args)
	if err != nil {
		fatal("Larges: %v", err)
	}
	minLines := f.lines
	if minLines <= 0 {
		minLines = 60
	}
	large := intel.LargeFunctions(ix, minLines)
	if f.limit > 0 && len(large) > f.limit {
		large = large[:f.limit]
	}
	if f.json {
		printJSON(map[string]any{"min_lines": minLines, "large": large})
		return
	}
	fmt.Println(intel.RenderLarge(large))

}

func runArch(rest []string) {
	f, args := parseFlagsOrDie(rest)
	_, ix, err := resolveRoot(f, args)
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
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if len(args) > 0 {
		root = args[0]
	}
	// The twin is rooted at a project directory. A file path (or missing
	// path) would build a graph on a bogus root and make the governance
	// store log "approvals.json: not a directory" noise, so validate first.
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		fatalUsage("twin: %s is not a directory (usage: kern twin [root] [--root ROOT])", root)
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
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if f.root == "" && len(args) > 0 {
		root = args[0]
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
	fmt.Println(intel.RenderChurnLimit(report, f.limit))

}

func runCochange(rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if f.root == "" && len(args) > 0 {
		root = args[0]
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
	f, args := parseFlagsOrDie(rest)
	if len(args) < 1 {
		fatalUsage("usage: kern explore <symbol> [root] [--depth N] [--max N] [--explain]")
	}
	root := projectRoot(f)
	if f.root == "" && len(args) > 1 {
		root = args[1]
	}
	ix, err := intel.ReadIndex(root)
	if err != nil {
		fatal("Explore: %v", err)
	}
	// The verb is documented as "explore <symbol|file>": a file argument
	// (exact indexed path, root-relative path, an existing path on disk,
	// or a unique basename) renders that file's symbols instead of failing
	// symbol lookup.
	if rel, ok := indexedFile(ix, root, args[0]); ok {
		var syms []index.Symbol
		for _, s := range ix.Symbols {
			if s.File == rel {
				syms = append(syms, s)
			}
		}
		sort.Slice(syms, func(i, j int) bool { return syms[i].Line < syms[j].Line })
		if f.json {
			printJSON(map[string]any{"file": rel, "symbols": syms})
			return
		}
		var b strings.Builder
		fmt.Fprintf(&b, "file: %s (%d symbols)\n", rel, len(syms))
		for _, s := range syms {
			fmt.Fprintf(&b, "  %-10s %-7s %s:%d\n", s.Kind, s.Lang, s.Name, s.Line)
		}
		if len(syms) > 0 {
			b.WriteString("hint: run `kern explore <symbol>` for a symbol's callers, callees and blast radius\n")
		}
		fmt.Print(b.String())
		return
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
	f, args := parseFlagsOrDie(rest)
	if len(args) < 1 {
		fatalUsage("usage: kern near <symbol> [root] [--depth N] [--max N]")
	}
	root := projectRoot(f)
	if f.root == "" && len(args) > 1 {
		root = args[1]
	}
	ix, err := intel.ReadIndex(root)
	if err != nil {
		fatal("Near: %v", err)
	}
	// F-NR1 (the --precision F22 pattern): a negative flag value is a
	// usage error, not a silently-swapped default. parseFlags' unset
	// default is depth=-1 / max=0; an explicit --depth 0 stays 0.
	if f.depth < -1 {
		fatalUsage("near: invalid --depth %d (must be >= 0)", f.depth)
	}
	if f.max < 0 {
		fatalUsage("near: invalid --max %d (must be >= 0)", f.max)
	}
	depth := 2
	if f.depth >= 0 {
		depth = f.depth
	}
	maxN := 100
	if f.max > 0 {
		maxN = f.max
	}
	nodes, err := intel.Near(ix, args[0], depth, maxN)
	if err != nil {
		// F-NR2: ranked candidates + did-you-mean on the unknown symbol,
		// matching impact/context/graph (near is case-sensitive where search
		// is forgiving — exactly where the hint is most needed).
		fatalNoSymbol(args[0], ix)
	}
	if f.json {
		printJSON(map[string]any{"depth": depth, "max_nodes": maxN, "nodes": nodes})
		return
	}
	fmt.Println(intel.RenderNear(ix, nodes))

}

func runCycles(rest []string) {
	f, args := parseFlagsOrDie(rest)
	_, ix, err := resolveRoot(f, args)
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
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
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
	f, args := parseFlagsOrDie(rest)
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
	f, args := parseFlagsOrDie(rest)
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
			if errors.Is(err, fs.ErrNotExist) && intel.LooksLikeTrace(sourceName) {
				// Not a path — inline trace text such as
				// `kern trace "path/file.py:24 selectSlice"`.
				src = sourceName
				sourceName = "inline"
			} else {
				if errors.Is(err, fs.ErrNotExist) {
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

// indexedFile resolves a file argument against the index: an exact match of
// an indexed file path, a root-relative path, an existing file on disk, or a
// UNIQUE basename match. Returns the canonical (index-relative) path.
func indexedFile(ix *index.Index, root, arg string) (string, bool) {
	files := map[string]bool{}
	byBase := map[string][]string{}
	for _, s := range ix.Symbols {
		if s.File == "" {
			continue
		}
		files[s.File] = true
		b := filepath.Base(s.File)
		byBase[b] = append(byBase[b], s.File)
	}
	cand := filepath.Clean(arg)
	if files[cand] {
		return cand, true
	}
	if root != "" && root != "." {
		if rel, err := filepath.Rel(root, cand); err == nil && files[rel] {
			return rel, true
		}
		joined := filepath.Join(root, cand)
		if fi, err := os.Stat(joined); err == nil && !fi.IsDir() {
			if rel, err := filepath.Rel(root, joined); err == nil {
				return rel, true
			}
		}
	} else if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
		return cand, true
	}
	if hits := byBase[filepath.Base(cand)]; len(hits) == 1 {
		return hits[0], true
	}
	return "", false
}
